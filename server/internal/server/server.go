// Package server monta o http.Handler do monólito: cada módulo registra as suas rotas aqui.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/channels"
	"kurz/server/internal/guilds"
	"kurz/server/internal/identity"
	"kurz/server/internal/messages"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	// contractVersion é o major.minor de contract/openapi.yaml que este servidor implementa.
	contractVersion = "0.3"
)

func New(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	gen := ids.NewGenerator(cfg.NodeID)
	tokens := identity.NewTokenIssuer(cfg.JWTSecret, accessTTL)
	idSvc := identity.NewService(pool, gen, tokens, identity.Config{Registration: cfg.Registration, RefreshTTL: refreshTTL})

	// guilds precisa de channels (canal padrão na criação) e channels precisa de guilds (permissões):
	// o ciclo é fechado aqui por interfaces, sem import direto entre os pacotes.
	var chSvc *channels.Service
	guildSvc := guilds.NewService(pool, gen, lazyDefaults{&chSvc})
	chSvc = channels.NewService(pool, gen, guildSvc)

	msgSvc := messages.NewService(pool, gen, channelPerms{chSvc})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			httpx.WriteError(w, httpx.NewError(http.StatusServiceUnavailable, "unavailable", "database unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /.well-known/kurz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"contract":     contractVersion,
			"name":         cfg.ServerName,
			"registration": cfg.Registration,
			"capabilities": []string{"text"},
		})
	})
	idSvc.Mount(mux)
	auth := identity.Require(tokens)
	guildSvc.Mount(mux, auth)
	chSvc.Mount(mux, auth)
	msgSvc.Mount(mux, auth)

	return httpx.AccessLog(httpx.Recover(mux))
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
