// Manager de salas: REST público + emissão do token de mídia. Participantes vêm do SFU.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

type config struct {
	secret, sfuPublicWS, sfuInternal string
	iceServers                       []map[string]any
	turn                             *turnSource // nil: entrega iceServers fixo
}

type Room struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	CreatedAt    time.Time     `json:"createdAt"`
	Participants []Participant `json:"participants"`
}

type Participant struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Mic    bool   `json:"mic"`
	Camera bool   `json:"camera"`
	Screen bool   `json:"screen"`
}

type server struct {
	cfg   config
	mu    sync.Mutex
	rooms map[string]Room // ponytail: só em memória; reiniciar o manager apaga as salas
	http  *http.Client
}

// seed: as salas que existem desde o arranque. Ids fixos, para o código de cada uma ser sempre o mesmo.
var seed = []struct{ id, name string }{
	{"geral", "Geral"}, {"reunioes", "Reuniões"}, {"dev", "Desenvolvimento"}, {"musica", "Música"},
}

func newServer(cfg config) http.Handler {
	s := &server{cfg: cfg, rooms: map[string]Room{}, http: &http.Client{Timeout: 2 * time.Second}}
	now := time.Now().UTC()
	for i, r := range seed { // no passado, na ordem do seed: a lista ordena por CreatedAt e as salas novas vêm depois
		at := now.Add(time.Duration(i-len(seed)) * time.Second)
		s.rooms[r.id] = Room{ID: r.id, Name: r.name, CreatedAt: at, Participants: []Participant{}}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/rooms", s.create)
	mux.HandleFunc("GET /v1/rooms", s.list)
	mux.HandleFunc("GET /v1/rooms/{id}", s.get)
	mux.HandleFunc("POST /v1/rooms/{id}/join", s.join)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, c, msg string) {
	writeJSON(w, code, map[string]string{"code": c, "message": msg})
}

// decode lê o corpo (máx. 1 MiB) e responde 422 se for inválido.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		apiErr(w, 422, "validation", "invalid JSON body")
		return false
	}
	return true
}

func validStr(s string, max int) bool { n := utf8.RuneCountInString(s); return n >= 1 && n <= max }

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// newID exige o mutex: garante ausência de colisão.
func (s *server) newID() string {
	for {
		b := make([]byte, 6)
		for i := range b {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			b[i] = alphabet[n.Int64()]
		}
		if _, ok := s.rooms[string(b)]; !ok {
			return string(b)
		}
	}
}

// participants consulta o SFU; se estiver fora devolve lista vazia.
func (s *server) participants(ctx context.Context, id string) []Participant {
	out := struct {
		Participants []Participant `json:"participants"`
	}{}
	req, _ := http.NewRequestWithContext(ctx, "GET", s.cfg.sfuInternal+"/internal/rooms/"+id, nil)
	resp, err := s.http.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			err = errors.New(resp.Status)
		} else {
			err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out)
		}
	}
	if err != nil {
		slog.Warn("sfu indisponível", "room", id, "err", err)
		return []Participant{}
	}
	if out.Participants == nil {
		return []Participant{}
	}
	return out.Participants
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	if !validStr(in.Name, 64) {
		apiErr(w, 422, "validation", "name must be 1..64 characters")
		return
	}
	s.mu.Lock()
	room := Room{ID: s.newID(), Name: in.Name, CreatedAt: time.Now().UTC(), Participants: []Participant{}}
	s.rooms[room.ID] = room
	s.mu.Unlock()
	writeJSON(w, 201, room)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rooms := make([]Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room)
	}
	s.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].CreatedAt.Before(rooms[j].CreatedAt) })
	// ponytail: 1 chamada ao SFU por sala, em série (2 s de teto cada); paralelizar ou cachear se houver muitas salas
	for i := range rooms {
		rooms[i].Participants = s.participants(r.Context(), rooms[i].ID)
	}
	writeJSON(w, 200, map[string]any{"rooms": rooms})
}

func (s *server) find(w http.ResponseWriter, r *http.Request) (Room, bool) {
	s.mu.Lock()
	room, ok := s.rooms[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		apiErr(w, 404, "not_found", "room not found")
		return room, false
	}
	room.Participants = s.participants(r.Context(), room.ID)
	return room, true
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	if room, ok := s.find(w, r); ok {
		writeJSON(w, 200, room)
	}
}

func (s *server) join(w http.ResponseWriter, r *http.Request) {
	var in struct{ UserID, Name string }
	if !decode(w, r, &in) {
		return
	}
	if !validStr(in.UserID, 64) || !validStr(in.Name, 64) {
		apiErr(w, 422, "validation", "userId and name must be 1..64 characters")
		return
	}
	room, ok := s.find(w, r)
	if !ok {
		return
	}
	jti := make([]byte, 16)
	rand.Read(jti)
	now := time.Now()
	// ponytail: userId sem autenticação; qualquer cliente pode se passar por outro
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "kurz-manager", "sub": in.UserID, "name": in.Name, "room": room.ID,
		"jti": hex.EncodeToString(jti), "exp": now.Add(60 * time.Second).Unix(),
	}).SignedString([]byte(s.cfg.secret))
	if err != nil {
		apiErr(w, 500, "internal", "could not sign token")
		return
	}
	parts := make([]map[string]any, 0, len(room.Participants))
	for _, p := range room.Participants {
		parts = append(parts, map[string]any{
			"user":  map[string]any{"id": p.UserID, "username": p.UserID, "name": p.Name, "status": "online", "role": "member"},
			"muted": !p.Mic, "camOn": p.Camera, "sharing": p.Screen,
		})
	}
	var ice any = s.cfg.iceServers
	if s.cfg.turn != nil {
		if t, err := s.cfg.turn.get(r.Context()); err == nil {
			ice = t
		} else {
			slog.Warn("turn indisponível; entregando ICE_SERVERS", "err", err)
		}
	}
	writeJSON(w, 200, map[string]any{
		"media":        map[string]string{"url": s.cfg.sfuPublicWS, "token": tok},
		"iceServers":   ice,
		"participants": parts,
	})
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := config{
		secret:      os.Getenv("KURZ_SECRET"),
		sfuPublicWS: os.Getenv("SFU_PUBLIC_WS_URL"),
		sfuInternal: getenv("SFU_INTERNAL_URL", "http://localhost:8082"),
		turn:        newTurnSource(os.Getenv("TURN_KEY_ID"), os.Getenv("TURN_KEY_API_TOKEN")),
	}
	if cfg.secret == "" || cfg.sfuPublicWS == "" {
		slog.Error("KURZ_SECRET e SFU_PUBLIC_WS_URL são obrigatórias")
		os.Exit(1)
	}
	ice := getenv("ICE_SERVERS", `[{"urls":["stun:stun.l.google.com:19302"]}]`)
	if err := json.Unmarshal([]byte(ice), &cfg.iceServers); err != nil {
		slog.Error("ICE_SERVERS inválido", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{Addr: ":" + getenv("PORT", "8080"), Handler: newServer(cfg), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	slog.Info("manager ouvindo", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("falha", "err", err)
		os.Exit(1)
	}
}
