// Package bus leva eventos de domínio das escritas (REST) até os gateways, em qualquer réplica.
//
// O contrato é pequeno (Bus) para que o Postgres (outbox + LISTEN/NOTIFY) possa ser trocado por
// NATS JetStream sem tocar nos domínios. Os domínios só usam Enqueue (dentro da transação da escrita)
// e os tipos de evento deste pacote. Os tipos ficam aqui, num lugar neutro, para que nenhum domínio
// importe outro para conhecê-los.
//
// Garantias: entrega at-least-once e, no mesmo canal, ordem de id. Os consumidores precisam ser
// idempotentes: todo evento tem um id único.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Event é um evento de domínio. GuildID e ChannelID são 0 quando não se aplicam.
// Payload é o objeto JSON que o gateway entrega ao cliente em `d`.
type Event struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	GuildID   int64           `json:"guildId,omitempty"`
	ChannelID int64           `json:"channelId,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Handler recebe os eventos. Roda na goroutine de despacho do bus: não pode bloquear por muito tempo.
type Handler func(ctx context.Context, e Event)

// Bus publica e assina eventos.
type Bus interface {
	// Publish grava o evento no outbox numa transação própria. Para publicar junto de uma escrita, use Enqueue.
	Publish(ctx context.Context, e Event) error
	// Subscribe registra h para todos os eventos publicados por qualquer réplica. cancel remove o registro.
	Subscribe(h Handler) (cancel func())
}

// Execer é o que Enqueue precisa: pgx.Tx, *pgxpool.Pool e *pgx.Conn servem.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var errNoID = errors.New("bus: event id is required")
