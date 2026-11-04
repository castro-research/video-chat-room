// Package server monta o http.Handler do monólito: cada módulo registra as suas rotas aqui.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/channels"
	"kurz/server/internal/gateway"
	"kurz/server/internal/guilds"
	"kurz/server/internal/identity"
	"kurz/server/internal/jobs"
	"kurz/server/internal/messages"
	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
	"kurz/server/internal/platform/objstore"
	"kurz/server/internal/platform/obs"
	"kurz/server/internal/platform/ratelimit"
	"kurz/server/internal/search"
	"kurz/server/internal/voice"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	// contractVersion é o major.minor de contract/openapi.yaml que este servidor implementa.
	contractVersion = "0.6"
)

// App é o monólito montado: o handler HTTP (inclui o WebSocket do gateway) e os workers de fundo
// (relay do outbox e gateway), que rodam no mesmo processo.
type App struct {
	Handler http.Handler
	bus     *bus.Postgres
	gateway *gateway.Gateway
	jobs    *jobs.Worker
	voice   *voice.Service
	limits  []*ratelimit.Limiter
}

// Option ajusta a montagem do App.
type Option func(*options)

type options struct {
	store objstore.Store
	jobs  jobs.Options
	voice func(*voice.Config) // ajuste da config de voz (testes)
}

// WithVoiceConfig ajusta a configuração de voz depois de lida do config (testes: TTL, relógio, ICE).
func WithVoiceConfig(fn func(*voice.Config)) Option { return func(o *options) { o.voice = fn } }

// WithObjectStore liga os anexos. Sem ele, os endpoints de anexo respondem 501 e a capability `attachments` não é anunciada.
func WithObjectStore(s objstore.Store) Option { return func(o *options) { o.store = s } }

// WithJobOptions ajusta o worker de jobs (testes usam polling rápido).
func WithJobOptions(j jobs.Options) Option { return func(o *options) { o.jobs = j } }

// New devolve só o handler HTTP, sem os workers: serve para testes de REST. O binário usa NewApp.
func New(cfg config.Config, pool *pgxpool.Pool, opts ...Option) http.Handler {
	return NewApp(cfg, pool, opts...).Handler
}

// RunJobsOnce roda os jobs prontos agora, na goroutine de quem chamou. Só para testes.
func (a *App) RunJobsOnce(ctx context.Context) (int, error) { return a.jobs.RunOnce(ctx) }

// Run roda o relay do outbox, o gateway e o worker de jobs até ctx terminar. Ao terminar, o gateway fecha as
// conexões (1001) e o worker espera os jobs em curso.
func (a *App) Run(ctx context.Context) {
	done := make(chan struct{}, 4)
	for _, l := range a.limits {
		go l.Run(ctx, time.Minute) // varre buckets ociosos; termina com ctx, sem entrar no `done`
	}
	go func() { a.bus.Run(ctx); done <- struct{}{} }()
	go func() { a.gateway.Run(ctx); done <- struct{}{} }()
	go func() { a.jobs.Run(ctx); done <- struct{}{} }()
	go func() {
		if a.voice.Enabled() {
			a.voice.Run(ctx, a.bus)
		}
		done <- struct{}{}
	}()
	<-done
	<-done
	<-done
	<-done
}

func NewApp(cfg config.Config, pool *pgxpool.Pool, opts ...Option) *App {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	gen := ids.NewGenerator(cfg.NodeID)
	tokens := identity.NewTokenIssuer(cfg.JWTSecret, accessTTL)
	idSvc := identity.NewService(pool, gen, tokens, identity.Config{Registration: cfg.Registration, RefreshTTL: refreshTTL})

	// guilds precisa de channels (canal padrão na criação) e channels precisa de guilds (permissões):
	// o ciclo é fechado aqui por interfaces, sem import direto entre os pacotes.
	var chSvc *channels.Service
	guildSvc := guilds.NewService(pool, gen, lazyDefaults{&chSvc})
	chSvc = channels.NewService(pool, gen, guildSvc)

	msgSvc := messages.NewService(pool, gen, channelPerms{chSvc})
	if o.store != nil {
		msgSvc.WithStorage(o.store)
	}
	searchSvc := search.NewService(pool, channelPerms{chSvc})
	worker := jobs.NewWorker(pool, gen, o.jobs)
	msgSvc.RegisterJobs(worker)

	vcfg := voice.Config{Secret: cfg.Voice.Secret, SFUURL: cfg.Voice.SFUURL}
	for _, s := range cfg.Voice.ICEServers {
		vcfg.ICEServers = append(vcfg.ICEServers, voice.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	if o.voice != nil {
		o.voice(&vcfg)
	}
	voiceSvc := voice.NewService(pool, gen, voiceChannels{chSvc}, vcfg)
	voiceSvc.RegisterJobs(worker)

	evBus := bus.NewPostgres(pool, gen, bus.Options{})
	gw := gateway.New(gateway.Config{}, gateway.AuthFunc(tokens.Parse), gatewayDirectory{guilds: guildSvc, channels: chSvc}, evBus)
	if voiceSvc.Enabled() {
		gw.WithVoice(gatewayVoice{voiceSvc})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, "unavailable", "database unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// `search` é sempre anunciada (só depende do Postgres); `attachments` só com o storage configurado.
	caps := []string{"text", "realtime", "search"}
	if msgSvc.HasStorage() {
		caps = append(caps, "attachments")
	}
	if voiceSvc.Enabled() {
		caps = append(caps, "voice")
	}
	mux.HandleFunc("GET /.well-known/kurz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"contract":     contractVersion,
			"name":         cfg.ServerName,
			"registration": cfg.Registration,
			"capabilities": caps,
		})
	})
	idSvc.Mount(mux)
	auth := identity.Require(tokens)
	guildSvc.Mount(mux, auth)
	chSvc.Mount(mux, auth)
	msgSvc.Mount(mux, auth)
	searchSvc.Mount(mux, auth)
	voiceSvc.Mount(mux, auth)
	gw.Mount(mux)

	metrics := obs.New(pool, func() obs.GatewayStats {
		st := gw.Stats()
		return obs.GatewayStats{Connections: st.Connections, Sessions: st.Sessions, Delivered: st.Delivered, Dropped: st.Dropped, SlowKills: st.SlowDisconnects}
	})
	// Interno: sem auth e fora do rate limit (o limitador só olha escritas, e a ordem abaixo o deixa de fora de qualquer forma).
	mux.Handle("GET /metrics", metrics.Handler())

	var h http.Handler = mux
	app := &App{bus: evBus, gateway: gw, jobs: worker, voice: voiceSvc}
	if rl := cfg.RateLimit; rl.Enabled {
		m := &ratelimit.Middleware{
			Auth:       ratelimit.New(rl.AuthPerMin/60, rl.AuthBurst, nil),
			Write:      ratelimit.New(rl.WritePerSec, rl.WriteBurst, nil),
			TrustProxy: cfg.TrustedProxy,
			User: func(r *http.Request) (int64, bool) {
				tok, ok := identity.BearerToken(r.Header.Get("Authorization"))
				if !ok {
					return 0, false
				}
				uid, err := tokens.Parse(tok)
				return uid, err == nil
			},
		}
		app.limits = []*ratelimit.Limiter{m.Auth, m.Write}
		h = m.Handler(h)
	}
	app.Handler = httpx.WithRequestID(metrics.Middleware(httpx.AccessLog(httpx.Recover(h))))
	return app
}

// gatewayDirectory adapta guilds e channels à interface Directory do gateway.
type gatewayDirectory struct {
	guilds   *guilds.Service
	channels *channels.Service
}

func (d gatewayDirectory) Guilds(ctx context.Context, userID int64) ([]gateway.Guild, error) {
	gs, err := d.guilds.Mine(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.Guild, len(gs))
	for i, g := range gs {
		out[i] = gateway.Guild{ID: g.ID, Name: g.Name, OwnerID: g.OwnerID}
	}
	return out, nil
}

func (d gatewayDirectory) CanView(ctx context.Context, userID, channelID int64) (bool, error) {
	p, err := d.channels.Permissions(ctx, userID, channelID)
	var he *httpx.Error
	if errors.As(err, &he) && he.Status == http.StatusNotFound { // não é membro, ou não vê, ou o canal sumiu
		return false, nil
	}
	return p.Has(permissions.ViewChannel), err
}

// lazyDefaults adia a resolução do serviço de canais, que só existe depois do de guilds.
type lazyDefaults struct{ ch **channels.Service }

func (l lazyDefaults) CreateDefaults(ctx context.Context, tx pgx.Tx, guildID int64) error {
	return (*l.ch).CreateDefaults(ctx, tx, guildID)
}

// channelPerms adapta channels.Service à interface Perms do módulo de mensagens.
type channelPerms struct{ ch *channels.Service }

func (c channelPerms) ChannelAccess(ctx context.Context, userID, channelID int64) (messages.Access, error) {
	ch, p, err := c.ch.Access(ctx, userID, channelID)
	if err != nil {
		return messages.Access{}, err
	}
	return messages.Access{Perms: p, Text: ch.Kind == channels.KindText}, nil
}

func (c channelPerms) VisibleTextChannels(ctx context.Context, userID, guildID int64) ([]int64, error) {
	cs, err := c.ch.List(ctx, userID, guildID)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, ch := range cs {
		if ch.Kind == channels.KindText {
			out = append(out, ch.ID)
		}
	}
	return out, nil
}

// voiceChannels adapta channels.Service à interface Channels do módulo de voz.
type voiceChannels struct{ ch *channels.Service }

func (v voiceChannels) VoiceAccess(ctx context.Context, userID, channelID int64) (voice.Channel, error) {
	ch, p, err := v.ch.Access(ctx, userID, channelID)
	if err != nil {
		return voice.Channel{}, err
	}
	return voice.Channel{ID: ch.ID, GuildID: ch.GuildID, Voice: ch.Kind == channels.KindVoice,
		Connect: p.Has(permissions.Connect), Speak: p.Has(permissions.Speak)}, nil
}

// gatewayVoice adapta voice.Service à interface Voice do gateway.
type gatewayVoice struct{ v *voice.Service }

func (g gatewayVoice) Snapshot(ctx context.Context, userID int64) ([]json.RawMessage, error) {
	return g.v.Snapshot(ctx, userID)
}
func (g gatewayVoice) Touch(ctx context.Context, userID int64) { g.v.Touch(ctx, userID) }
func (g gatewayVoice) SetState(ctx context.Context, userID int64, muted, camOn, sharing bool) error {
	return g.v.SetState(ctx, userID, muted, camOn, sharing)
}
