// Package server monta o http.Handler do monólito: cada módulo registra as suas rotas aqui.
package server

import (
	"context"
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
	"kurz/server/internal/search"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	// contractVersion é o major.minor de contract/openapi.yaml que este servidor implementa.
	contractVersion = "0.5"
)

// App é o monólito montado: o handler HTTP (inclui o WebSocket do gateway) e os workers de fundo
// (relay do outbox e gateway), que rodam no mesmo processo.
type App struct {
	Handler http.Handler
	bus     *bus.Postgres
	gateway *gateway.Gateway
	jobs    *jobs.Worker
}

// Option ajusta a montagem do App.
type Option func(*options)

type options struct {
	store objstore.Store
	jobs  jobs.Options
}

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
	done := make(chan struct{}, 3)
	go func() { a.bus.Run(ctx); done <- struct{}{} }()
	go func() { a.gateway.Run(ctx); done <- struct{}{} }()
	go func() { a.jobs.Run(ctx); done <- struct{}{} }()
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

	evBus := bus.NewPostgres(pool, gen, bus.Options{})
	gw := gateway.New(gateway.Config{}, gateway.AuthFunc(tokens.Parse), gatewayDirectory{guilds: guildSvc, channels: chSvc}, evBus)

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
	gw.Mount(mux)

	return &App{Handler: httpx.AccessLog(httpx.Recover(mux)), bus: evBus, gateway: gw, jobs: worker}
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
