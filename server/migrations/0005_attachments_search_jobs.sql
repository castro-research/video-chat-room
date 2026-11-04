-- +goose Up
-- Anexos: o cliente envia o arquivo direto ao S3 por URL pré-assinada. A linha nasce 'pending' (upload pedido) e
-- vira 'ready' na mesma transação que cria a mensagem. Pendentes antigas são varridas por um job.
-- Sem FK do objeto: apagar a linha por cascata (canal, guild, usuário) deixa o objeto órfão no bucket (dívida conhecida).
CREATE TABLE attachments (
    id           bigint PRIMARY KEY,
    channel_id   bigint NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    uploader_id  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    message_id   bigint REFERENCES messages (id) ON DELETE CASCADE,
    filename     text NOT NULL,
    content_type text NOT NULL,
    size         bigint NOT NULL CHECK (size > 0),
    object_key   text NOT NULL UNIQUE,
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'ready') = (message_id IS NOT NULL))
);
CREATE INDEX attachments_message_idx ON attachments (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX attachments_pending_idx ON attachments (created_at) WHERE status = 'pending';

-- Fila de jobs no Postgres. O worker pega com FOR UPDATE SKIP LOCKED e segura um lease em locked_at;
-- se o processo morrer, o lease expira e outro worker repete (at-least-once: handlers idempotentes).
-- failed_at marca o job que esgotou as tentativas (fica na tabela para inspeção).
CREATE TABLE jobs (
    id         bigint PRIMARY KEY,
    kind       text NOT NULL,
    payload    jsonb NOT NULL DEFAULT '{}',
    run_at     timestamptz NOT NULL DEFAULT now(),
    attempts   int NOT NULL DEFAULT 0,
    last_error text,
    locked_at  timestamptz,
    done_at    timestamptz,
    failed_at  timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_ready_idx ON jobs (run_at, id) WHERE done_at IS NULL AND failed_at IS NULL;
CREATE INDEX jobs_finished_idx ON jobs (done_at) WHERE done_at IS NOT NULL;

-- Mensagem só com anexo tem texto vazio.
ALTER TABLE messages DROP CONSTRAINT messages_content_check;
ALTER TABLE messages ADD CONSTRAINT messages_content_len CHECK (char_length(content) <= 4000);

-- Busca (RF15). Coluna gerada: o Postgres calcula no INSERT/UPDATE e no ALTER (backfill das linhas existentes).
-- Config 'portuguese': stemming ("casa" casa "casas") e stopwords em PT; é o idioma do projeto. O stemmer PT do Postgres não unifica tudo (mensagem/mensagens ficam separadas). Tem o custo de
-- que termos em outros idiomas só sofrem o stemmer PT e de que consulta só de stopwords não casa nada.
ALTER TABLE messages ADD COLUMN search tsvector GENERATED ALWAYS AS (to_tsvector('portuguese'::regconfig, content)) STORED;
CREATE INDEX messages_search_idx ON messages USING GIN (search);

-- +goose Down
DROP INDEX messages_search_idx;
ALTER TABLE messages DROP COLUMN search;
-- a restrição antiga exige texto não vazio: mensagens só-anexo ganham um texto no lugar
UPDATE messages SET content = '(anexo)' WHERE content = '';
ALTER TABLE messages DROP CONSTRAINT messages_content_len;
ALTER TABLE messages ADD CONSTRAINT messages_content_check CHECK (char_length(content) BETWEEN 1 AND 4000);
DROP TABLE jobs;
DROP TABLE attachments;
