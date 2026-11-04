-- +goose Up
-- A limpeza periódica de refresh tokens apaga o que expirou, foi usado ou foi revogado há mais de N dias
-- (identity.cleanup_refresh). Índices parciais para ela não varrer a tabela inteira.
CREATE INDEX refresh_tokens_expires_idx ON refresh_tokens (expires_at);
CREATE INDEX refresh_tokens_used_idx ON refresh_tokens (used_at) WHERE used_at IS NOT NULL;
CREATE INDEX refresh_tokens_revoked_idx ON refresh_tokens (revoked_at) WHERE revoked_at IS NOT NULL;
-- E a dos jobs que falharam de vez.
CREATE INDEX jobs_failed_idx ON jobs (failed_at) WHERE failed_at IS NOT NULL;

-- +goose Down
DROP INDEX jobs_failed_idx;
DROP INDEX refresh_tokens_revoked_idx;
DROP INDEX refresh_tokens_used_idx;
DROP INDEX refresh_tokens_expires_idx;
