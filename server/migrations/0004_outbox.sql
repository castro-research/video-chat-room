-- +goose Up
-- Outbox transacional: o evento é gravado na mesma transação da escrita e o relay o publica depois.
-- Sem FKs de propósito: o evento sobrevive à entidade (message.delete, guild.delete).
CREATE TABLE outbox (
    id           bigint PRIMARY KEY,
    type         text NOT NULL,
    guild_id     bigint,
    channel_id   bigint,
    payload      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
-- o relay só olha os pendentes, em ordem de id
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL;
-- catch-up de assinantes que reconectaram e limpeza dos publicados antigos
CREATE INDEX outbox_published_idx ON outbox (published_at) WHERE published_at IS NOT NULL;

-- +goose Down
DROP TABLE outbox;
