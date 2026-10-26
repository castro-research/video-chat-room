package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "s3cret"

func setup(t *testing.T, sfuURL string) *httptest.Server {
	t.Helper()
	m := httptest.NewServer(newServer(config{
		secret: secret, sfuPublicWS: "ws://x:30081/v1/media", sfuInternal: sfuURL,
		iceServers: []map[string]any{{"urls": []string{"stun:s"}}},
	}))
	t.Cleanup(m.Close)
	return m
}

func fakeSFU(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"participants":[{"userId":"bob","name":"Bob","mic":true,"camera":false,"screen":true}]}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func call(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, url, bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestRoomsCRUD(t *testing.T) {
	m := setup(t, fakeSFU(t).URL)
	var room Room
	if c := call(t, "POST", m.URL+"/v1/rooms", map[string]string{"name": "sala"}, &room); c != 201 {
		t.Fatal(c)
	}
	if len(room.ID) != 6 || strings.Trim(room.ID, alphabet) != "" {
		t.Fatalf("id %q", room.ID)
	}
	// a lista começa pelas salas do seed (na ordem do seed) e a nova vem por último
	var list struct{ Rooms []Room }
	call(t, "GET", m.URL+"/v1/rooms", nil, &list)
	if len(list.Rooms) != len(seed)+1 || list.Rooms[len(seed)].ID != room.ID || len(list.Rooms[len(seed)].Participants) != 1 {
		t.Fatalf("list %+v", list)
	}
	for i, s := range seed {
		if list.Rooms[i].ID != s.id || list.Rooms[i].Name != s.name {
			t.Fatalf("seed %d: %+v", i, list.Rooms[i])
		}
	}
	var got Room
	if c := call(t, "GET", m.URL+"/v1/rooms/"+room.ID, nil, &got); c != 200 || got.Name != "sala" || got.Participants[0].UserID != "bob" {
		t.Fatalf("get %d %+v", c, got)
	}
}

func TestValidationAndNotFound(t *testing.T) {
	m := setup(t, fakeSFU(t).URL)
	var e map[string]string
	for _, n := range []string{"", strings.Repeat("a", 65)} {
		if c := call(t, "POST", m.URL+"/v1/rooms", map[string]string{"name": n}, &e); c != 422 || e["code"] != "validation" {
			t.Fatalf("name %q: %d %v", n, c, e)
		}
	}
	if c := call(t, "GET", m.URL+"/v1/rooms/nope", nil, &e); c != 404 || e["code"] != "not_found" {
		t.Fatalf("%d %v", c, e)
	}
	if c := call(t, "POST", m.URL+"/v1/rooms/nope/join", map[string]string{"userId": "a", "name": "A"}, &e); c != 404 {
		t.Fatal(c)
	}
	var room Room
	call(t, "POST", m.URL+"/v1/rooms", map[string]string{"name": "r"}, &room)
	if c := call(t, "POST", m.URL+"/v1/rooms/"+room.ID+"/join", map[string]string{"userId": "", "name": "A"}, &e); c != 422 {
		t.Fatal(c)
	}
}

func TestJoin(t *testing.T) {
	m := setup(t, fakeSFU(t).URL)
	var room Room
	call(t, "POST", m.URL+"/v1/rooms", map[string]string{"name": "r"}, &room)
	type session struct {
		Media        struct{ URL, Token string }
		IceServers   []map[string]any
		Participants []struct {
			User                  map[string]string
			Muted, CamOn, Sharing bool
		}
	}
	join := func() session {
		var s session
		if c := call(t, "POST", m.URL+"/v1/rooms/"+room.ID+"/join", map[string]string{"userId": "alice", "name": "Alice"}, &s); c != 200 {
			t.Fatal(c)
		}
		return s
	}
	s1, s2 := join(), join()
	if s1.Media.URL != "ws://x:30081/v1/media" || len(s1.IceServers) != 1 {
		t.Fatalf("%+v", s1)
	}
	p := s1.Participants[0]
	if p.User["id"] != "bob" || p.User["username"] != "bob" || p.User["status"] != "online" || p.User["role"] != "member" || p.Muted || p.CamOn || !p.Sharing {
		t.Fatalf("%+v", p)
	}
	claims := func(tok string) jwt.MapClaims {
		c := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(tok, c, func(*jwt.Token) (any, error) { return []byte(secret), nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kurz-manager"), jwt.WithExpirationRequired())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1, c2 := claims(s1.Media.Token), claims(s2.Media.Token)
	if c1["sub"] != "alice" || c1["name"] != "Alice" || c1["room"] != room.ID || c1["jti"] == "" || c1["jti"] == c2["jti"] {
		t.Fatalf("%v %v", c1, c2)
	}
	if d := time.Until(time.Unix(int64(c1["exp"].(float64)), 0)); d > 60*time.Second || d < 50*time.Second {
		t.Fatal(d)
	}
}

func TestSFUDown(t *testing.T) {
	sfu := fakeSFU(t)
	m := setup(t, sfu.URL)
	sfu.Close()
	var room Room
	call(t, "POST", m.URL+"/v1/rooms", map[string]string{"name": "r"}, &room)
	var raw map[string]any
	if c := call(t, "GET", m.URL+"/v1/rooms/"+room.ID, nil, &raw); c != 200 {
		t.Fatal(c)
	}
	if p, ok := raw["participants"].([]any); !ok || len(p) != 0 {
		t.Fatalf("%v", raw["participants"])
	}
	var s struct{ Participants []any }
	if c := call(t, "POST", m.URL+"/v1/rooms/"+room.ID+"/join", map[string]string{"userId": "a", "name": "A"}, &s); c != 200 || s.Participants == nil {
		t.Fatalf("%d %v", c, s)
	}
}
