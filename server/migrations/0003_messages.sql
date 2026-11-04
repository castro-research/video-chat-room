-- +goose Up
-- Exclusão é real (DELETE): reações somem em cascata e respostas ficam com reply_to_id nulo.
-- O nonce do cliente não é persistido: só volta no eco ao remetente.
CREATE TABLE messages (
    id          bigint PRIMARY KEY,
    channel_id  bigint NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    author_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    content     text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 4000),
    reply_to_id bigint REFERENCES messages (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    edited_at   timestamptz
);
CREATE INDEX messages_channel_idx ON messages (channel_id, id DESC);
CREATE INDEX messages_reply_idx ON messages (reply_to_id) WHERE reply_to_id IS NOT NULL;

CREATE TABLE reactions (
    message_id bigint NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    emoji      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);

-- last_read_message_id não tem FK: a mensagem lida pode ser apagada depois.
CREATE TABLE read_states (
    user_id              bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id           bigint NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    last_read_message_id bigint NOT NULL,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);

-- +goose Down
DROP TABLE read_states;
DROP TABLE reactions;
DROP TABLE messages;
