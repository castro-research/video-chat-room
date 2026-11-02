-- +goose Up
CREATE TABLE guilds (
    id         bigint PRIMARY KEY,
    name       text NOT NULL,
    owner_id   bigint NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX guilds_owner_idx ON guilds (owner_id);

-- O papel @everyone de uma guild tem id = id da guild e position 0.
CREATE TABLE roles (
    id          bigint PRIMARY KEY,
    guild_id    bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    name        text NOT NULL,
    permissions bigint NOT NULL DEFAULT 0,
    position    integer NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX roles_guild_idx ON roles (guild_id, position);

CREATE TABLE members (
    guild_id  bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    user_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, user_id)
);
CREATE INDEX members_user_idx ON members (user_id);

CREATE TABLE member_roles (
    guild_id bigint NOT NULL,
    user_id  bigint NOT NULL,
    role_id  bigint NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    PRIMARY KEY (guild_id, user_id, role_id),
    FOREIGN KEY (guild_id, user_id) REFERENCES members (guild_id, user_id) ON DELETE CASCADE
);
CREATE INDEX member_roles_role_idx ON member_roles (role_id);

CREATE TABLE channels (
    id         bigint PRIMARY KEY,
    guild_id   bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    parent_id  bigint REFERENCES channels (id) ON DELETE SET NULL, -- categoria (kind = 'category')
    kind       text NOT NULL CHECK (kind IN ('text', 'voice', 'category')),
    name       text NOT NULL,
    position   integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX channels_guild_idx ON channels (guild_id, parent_id, position);

CREATE TABLE channel_overrides (
    channel_id  bigint NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    target_kind text NOT NULL CHECK (target_kind IN ('role', 'member')),
    target_id   bigint NOT NULL,
    allow       bigint NOT NULL DEFAULT 0,
    deny        bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, target_kind, target_id)
);

CREATE TABLE invites (
    code       text PRIMARY KEY,
    guild_id   bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    creator_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    max_uses   integer,
    uses       integer NOT NULL DEFAULT 0,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invites_guild_idx ON invites (guild_id);

CREATE TABLE bans (
    guild_id   bigint NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    banned_by  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, user_id)
);

-- +goose Down
DROP TABLE bans;
DROP TABLE invites;
DROP TABLE channel_overrides;
DROP TABLE channels;
DROP TABLE member_roles;
DROP TABLE members;
DROP TABLE roles;
DROP TABLE guilds;
