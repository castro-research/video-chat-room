// Package gateway é o WebSocket de tempo real: sessões com seq e resume, fan-out filtrado por
// permissão e backpressure por conexão. Não importa os domínios: autenticação e permissões entram
// por interfaces pequenas definidas aqui, e os eventos chegam pelo bus.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"kurz/server/internal/bus"
)

// Authenticator valida o access token e devolve o usuário.
type Authenticator interface {
	Authenticate(token string) (userID int64, err error)
}

// AuthFunc adapta uma função (por exemplo identity.TokenIssuer.Parse) a Authenticator.
type AuthFunc func(token string) (int64, error)

func (f AuthFunc) Authenticate(token string) (int64, error) { return f(token) }

// Guild é o resumo de uma guild no ready.
type Guild struct {
	ID      int64
	Name    string
	OwnerID int64
}

// Directory responde o que o gateway precisa saber do resto do sistema.
type Directory interface {
	// Guilds são as guilds de que o usuário é membro.
	Guilds(ctx context.Context, userID int64) ([]Guild, error)
	// CanView diz se o usuário tem ViewChannel no canal. Canal inexistente ou invisível é (false, nil).
	CanView(ctx context.Context, userID, channelID int64) (bool, error)
}

// Voice é a parte de voz que o gateway usa (opcional, ligada com WithVoice): o estado atual no `ready`, o heartbeat
// que mantém o estado de voz vivo e a op `voice.state.set` do cliente.
type Voice interface {
	// Snapshot são os `d` de voice.state (connected=true) das guilds do usuário, só de canais que ele vê.
	Snapshot(ctx context.Context, userID int64) ([]json.RawMessage, error)
	// Touch renova o heartbeat do estado de voz do usuário.
	Touch(ctx context.Context, userID int64)
	// SetState aplica mute/câmera/tela do próprio usuário.
	SetState(ctx context.Context, userID int64, muted, camOn, sharing bool) error
}

// Config ajusta o gateway. Zero usa os padrões.
type Config struct {
	HeartbeatInterval time.Duration // o cliente deve mandar heartbeat a cada intervalo; silêncio de 2x derruba (30 s)
	IdentifyTimeout   time.Duration // tempo para o primeiro identify/resume (10 s)
	SendQueue         int           // frames na fila de saída de uma conexão (256)
	RingSize          int           // eventos guardados por sessão para resume (256)
	SessionTTL        time.Duration // quanto uma sessão sem conexão espera por resume (2 min)
	WriteTimeout      time.Duration // prazo de cada escrita no socket (5 s)
	CacheTTL          time.Duration // validade máxima de uma entrada do cache de permissões (30 s); a invalidação por evento é o caminho normal
	OriginPatterns    []string      // origens de navegador aceitas além do próprio host
}

func (c *Config) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&c.HeartbeatInterval, 30*time.Second)
	set(&c.IdentifyTimeout, 10*time.Second)
	set(&c.SessionTTL, 2*time.Minute)
	set(&c.WriteTimeout, 5*time.Second)
	set(&c.CacheTTL, 30*time.Second)
	if c.RingSize <= 0 {
		c.RingSize = 256
	}
	if c.SendQueue <= 0 {
		c.SendQueue = 256
	}
	// o resume enfileira o buffer inteiro de uma vez
	c.SendQueue = max(c.SendQueue, c.RingSize+8)
}

// Gateway mantém as sessões deste processo. Cada réplica tem o seu e escuta o bus sozinha.
type Gateway struct {
	cfg   Config
	auth  Authenticator
	dir   Directory
	bus   bus.Bus
	voice Voice

	mu       sync.RWMutex
	sessions map[string]*session
	conns    map[*conn]struct{}
	closing  bool

	events chan bus.Event
	cache  *permCache

	slowDisconnects atomic.Int64
	delivered       atomic.Int64
	dropped         atomic.Int64
}

func New(cfg Config, auth Authenticator, dir Directory, b bus.Bus) *Gateway {
	cfg.defaults()
	return &Gateway{
		cfg: cfg, auth: auth, dir: dir, bus: b,
		sessions: map[string]*session{}, conns: map[*conn]struct{}{},
		events: make(chan bus.Event, 4096),
		cache:  newPermCache(cfg.CacheTTL),
	}
}

// WithVoice liga a voz. Chame antes de Run/Mount. Sem ela, o ready não traz `voice` e `voice.state.set` é frame inválido.
func (g *Gateway) WithVoice(v Voice) *Gateway { g.voice = v; return g }

// Stats são contadores para testes e métricas.
type Stats struct {
	Sessions        int
	Connections     int
	SlowDisconnects int64
	Delivered       int64 // eventos enfileirados para sessões
	Dropped         int64 // eventos que estouraram a fila do cliente
}

func (g *Gateway) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Stats{Sessions: len(g.sessions), Connections: len(g.conns), SlowDisconnects: g.slowDisconnects.Load(),
		Delivered: g.delivered.Load(), Dropped: g.dropped.Load()}
}

// Mount registra GET /v1/gateway.
func (g *Gateway) Mount(mux *http.ServeMux) { mux.Handle("GET /v1/gateway", g) }

// Run assina o bus e roteia os eventos até ctx terminar; ao terminar fecha todas as conexões (1001).
func (g *Gateway) Run(ctx context.Context) {
	cancel := g.bus.Subscribe(func(ctx context.Context, e bus.Event) {
		select {
		case g.events <- e:
		case <-ctx.Done():
		}
	})
	defer cancel()

	janitor := time.NewTicker(max(g.cfg.SessionTTL/4, 20*time.Millisecond))
	defer janitor.Stop()
	for {
		select {
		case <-ctx.Done():
			g.shutdown()
			return
		case e := <-g.events:
			g.route(ctx, e)
		case <-janitor.C:
			g.reap()
		}
	}
}

func (g *Gateway) shutdown() {
	g.mu.Lock()
	g.closing = true
	cs := make([]*conn, 0, len(g.conns))
	for c := range g.conns {
		cs = append(cs, c)
	}
	g.mu.Unlock()
	for _, c := range cs {
		c.kill(1001, "server shutting down", true)
	}
}

// reap esquece as sessões que ficaram sem conexão além do TTL.
func (g *Gateway) reap() {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, s := range g.sessions {
		if s.expired(now, g.cfg.SessionTTL) {
			delete(g.sessions, id)
		}
	}
}

func (g *Gateway) snapshot() []*session {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		out = append(out, s)
	}
	return out
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
