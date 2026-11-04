-- +goose Up
CREATE TABLE users (
    id            bigint PRIMARY KEY,
    username      text NOT NULL,
    name          text NOT NULL,
    subtitle      text NOT NULL DEFAULT '',
    hue           double precision,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_key ON users (username);

CREATE TABLE refresh_tokens (
    id         bigint PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  bigint NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);

-- +goose Down
DROP TABLE refresh_tokens;
DROP TABLE users;
