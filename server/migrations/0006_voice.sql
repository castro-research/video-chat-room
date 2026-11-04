-- +goose Up
-- Estado de voz (RF17): quem está em qual canal de voz. Uma linha por usuário (PK em user_id): entrar em outro
-- canal move a linha. Fica no Postgres, e não em memória, para qualquer réplica responder o mesmo.
-- updated_at é o heartbeat (o gateway renova a cada heartbeat do cliente); um job varre as linhas vencidas.
-- Apagar o canal ou o usuário remove a linha por cascata (sem evento: o cliente já recebe channel.delete).
-- Sair da guild/kick/ban NÃO cascateia: o módulo de voz ouve member.leave e emite o voice.state de saída.
CREATE TABLE voice_states (
    user_id    bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    guild_id   bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    channel_id bigint NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    muted      boolean NOT NULL DEFAULT false,
    cam_on     boolean NOT NULL DEFAULT false,
    sharing    boolean NOT NULL DEFAULT false,
    joined_at  timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX voice_states_channel_idx ON voice_states (channel_id);
CREATE INDEX voice_states_guild_idx ON voice_states (guild_id);
CREATE INDEX voice_states_updated_idx ON voice_states (updated_at);

-- +goose Down
DROP TABLE voice_states;
