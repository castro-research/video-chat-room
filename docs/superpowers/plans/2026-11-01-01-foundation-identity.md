# Fundação + Identidade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Um binário Go em `server/` que sobe HTTP, conecta no Postgres, migra o schema e permite registrar, logar, renovar e encerrar sessões, ler e editar o próprio perfil.

**Architecture:** Monólito modular (spec `docs/superpowers/specs/2026-11-01-kurz-server-design.md`). Pacotes `internal/platform/*` (ids, config, db, httpx) sem regra de negócio; `internal/identity` com senha, tokens, store e service; `internal/server` monta o `http.Handler`. Testes de integração usam Postgres real, um banco novo por teste.

**Tech Stack:** Go 1.24, `net/http` (ServeMux com métodos), `pgx/v5`, `goose/v3` (migrações embutidas), `golang-jwt/v5`, `x/crypto/argon2`, Postgres 18 via Docker Compose.

---

## Pré-requisitos

- Docker Desktop **rodando** (`docker info` não pode dar erro).
- Todos os comandos abaixo partem de `/Users/yuricosta/Documents/Programacao/video-chat-room/server`, salvo indicação.
- Testes que usam banco são **pulados** se `KURZ_TEST_DATABASE_URL` não estiver definida. Por isso os comandos de teste abaixo usam `make test`, que define a variável. Se a saída mostrar `SKIP`, o teste não rodou: suba o banco antes.

## Estrutura de arquivos

```
server/
  go.mod
  Makefile
  compose.yml
  cmd/kurz/main.go
  migrations/
    embed.go
    0001_identity.sql
  internal/
    platform/
      ids/ids.go, ids_test.go
      config/config.go, config_test.go
      httpx/errors.go, json.go, auth.go, middleware.go, httpx_test.go
      db/db.go
      dbtest/dbtest.go
    identity/
      password.go, password_test.go
      tokens.go, tokens_test.go
      store.go
      service.go, service_test.go
      http.go
    server/
      server.go, server_test.go
```

---

### Task 1: Scaffold do módulo, Postgres local e Makefile

**Files:**
- Create: `server/compose.yml`, `server/Makefile`
- Modify: `.gitignore`

- [ ] **Step 1: Criar o módulo**

```bash
mkdir -p server && cd server && go mod init kurz/server && go get github.com/jackc/pgx/v5 github.com/pressly/goose/v3 github.com/golang-jwt/jwt/v5 golang.org/x/crypto
```
Expected: `go.mod` criado com os quatro módulos em `require`.

- [ ] **Step 2: Criar `server/compose.yml`**

```yaml
services:
  db:
    image: postgres:18
    environment:
      POSTGRES_USER: kurz
      POSTGRES_PASSWORD: kurz
      POSTGRES_DB: kurz
    ports:
      - "5433:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U kurz"]
      interval: 2s
      timeout: 3s
      retries: 20
    volumes:
      - kurzdb:/var/lib/postgresql
volumes:
  kurzdb:
```

- [ ] **Step 3: Criar `server/Makefile`** (as linhas de receita começam com TAB)

```make
DB_URL    = postgres://kurz:kurz@localhost:5433/kurz?sslmode=disable
ADMIN_URL = postgres://kurz:kurz@localhost:5433/postgres?sslmode=disable

.PHONY: db-up db-down test run

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

test:
	KURZ_TEST_DATABASE_URL='$(ADMIN_URL)' go test ./... -count=1

run:
	KURZ_DATABASE_URL='$(DB_URL)' KURZ_JWT_SECRET='dev-secret-dev-secret-dev-secret-32' go run ./cmd/kurz
```

- [ ] **Step 4: Ignorar binários**

Acrescentar ao `.gitignore` da raiz a linha:
```
server/bin/
```

- [ ] **Step 5: Subir o banco**

Run: `make db-up && docker compose ps`
Expected: serviço `db` com status `healthy`.

- [ ] **Step 6: Commit**

```bash
cd .. && git add server/go.mod server/go.sum server/compose.yml server/Makefile .gitignore
git commit -m "chore(server): scaffold do módulo Go e Postgres local"
```

---

### Task 2: Gerador de snowflake IDs

**Files:**
- Create: `server/internal/platform/ids/ids.go`
- Test: `server/internal/platform/ids/ids_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package ids

import (
	"testing"
	"time"
)

func TestNextIsUniqueAndIncreasing(t *testing.T) {
	g := NewGenerator(1)
	prev := g.Next()
	seen := map[int64]bool{prev: true}
	for i := 0; i < 100000; i++ {
		id := g.Next()
		if id <= prev {
			t.Fatalf("id %d is not greater than previous %d", id, prev)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
		prev = id
	}
}

func TestNextSurvivesClockGoingBackwards(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	g := newGenerator(0, func() time.Time { return now })
	a := g.Next()
	now = now.Add(-time.Hour)
	b := g.Next()
	if b <= a {
		t.Fatalf("id went backwards: %d then %d", a, b)
	}
}

func TestNextBorrowsNextMillisecondOnSequenceOverflow(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	g := newGenerator(0, func() time.Time { return now })
	prev := g.Next()
	for i := 0; i < 5000; i++ { // mais que 4096 no mesmo milissegundo
		id := g.Next()
		if id <= prev {
			t.Fatalf("id %d is not greater than previous %d at i=%d", id, prev, i)
		}
		prev = id
	}
}

func TestNodeBitsAreEmbedded(t *testing.T) {
	g := NewGenerator(37)
	id := g.Next()
	if got := (id >> seqBits) & MaxNode; got != 37 {
		t.Fatalf("node bits = %d, want 37", got)
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	id := NewGenerator(3).Next()
	got, err := Parse(Format(id))
	if err != nil || got != id {
		t.Fatalf("Parse(Format(%d)) = %d, %v", id, got, err)
	}
	for _, bad := range []string{"", "abc", "-5", "0", "1.5"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/platform/ids/ -count=1`
Expected: FAIL com `undefined: NewGenerator` (e `Parse`, `Format`, `MaxNode`).

- [ ] **Step 3: Implementar `ids.go`**

```go
// Package ids gera IDs snowflake: 41 bits de milissegundos desde 2026-01-01, 10 bits de nó, 12 bits de sequência.
package ids

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

const (
	epochMs  = 1767225600000 // 2026-01-01T00:00:00Z
	nodeBits = 10
	seqBits  = 12
	seqMask  = 1<<seqBits - 1
	MaxNode  = 1<<nodeBits - 1
)

type Generator struct {
	mu   sync.Mutex
	node int64
	last int64
	seq  int64
	now  func() time.Time
}

func NewGenerator(node int64) *Generator { return newGenerator(node, time.Now) }

func newGenerator(node int64, now func() time.Time) *Generator {
	if node < 0 || node > MaxNode {
		panic("ids: node out of range")
	}
	return &Generator{node: node, now: now}
}

// Next devolve um ID único e estritamente crescente por gerador, mesmo se o relógio voltar.
func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := g.now().UnixMilli() - epochMs
	if ms < g.last {
		ms = g.last
	}
	if ms == g.last {
		g.seq = (g.seq + 1) & seqMask
		if g.seq == 0 { // estourou a sequência: pega emprestado o próximo milissegundo
			ms = g.last + 1
		}
	} else {
		g.seq = 0
	}
	g.last = ms
	return ms<<(nodeBits+seqBits) | g.node<<seqBits | g.seq
}

// Format escreve o ID como string, que é como ele aparece na API.
func Format(id int64) string { return strconv.FormatInt(id, 10) }

// Parse lê um ID da API. Só aceita inteiros positivos.
func Parse(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("ids: invalid id")
	}
	return id, nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/platform/ids/ -count=1 -v`
Expected: 5 testes `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/ids && git commit -m "feat(server): gerador de snowflake IDs"
```

---

### Task 3: Configuração por variáveis de ambiente

**Files:**
- Create: `server/internal/platform/config/config.go`
- Test: `server/internal/platform/config/config_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"KURZ_DATABASE_URL": "postgres://x",
		"KURZ_JWT_SECRET":   strings.Repeat("s", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.NodeID != 0 || c.ServerName != "Kurz" || c.Registration != "open" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if string(c.JWTSecret) != strings.Repeat("s", 32) || c.DatabaseURL != "postgres://x" {
		t.Fatalf("unexpected required fields: %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	m := valid()
	m["KURZ_ADDR"] = ":9000"
	m["KURZ_NODE_ID"] = "7"
	m["KURZ_SERVER_NAME"] = "Casa"
	m["KURZ_REGISTRATION"] = "closed"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9000" || c.NodeID != 7 || c.ServerName != "Casa" || c.Registration != "closed" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing database url": func(m map[string]string) { delete(m, "KURZ_DATABASE_URL") },
		"short secret":         func(m map[string]string) { m["KURZ_JWT_SECRET"] = "curto" },
		"node not a number":    func(m map[string]string) { m["KURZ_NODE_ID"] = "abc" },
		"node out of range":    func(m map[string]string) { m["KURZ_NODE_ID"] = "1024" },
		"unknown registration": func(m map[string]string) { m["KURZ_REGISTRATION"] = "weird" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid()
			mutate(m)
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/platform/config/ -count=1`
Expected: FAIL com `undefined: Load`.

- [ ] **Step 3: Implementar `config.go`**

```go
// Package config lê a configuração do processo a partir de variáveis de ambiente.
package config

import (
	"fmt"
	"strconv"

	"kurz/server/internal/platform/ids"
)

type Config struct {
	Addr         string
	DatabaseURL  string
	JWTSecret    []byte
	NodeID       int64
	ServerName   string
	Registration string // "open" ou "closed"
}

// Load recebe os.Getenv (ou um fake nos testes).
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:         or(getenv("KURZ_ADDR"), ":8080"),
		DatabaseURL:  getenv("KURZ_DATABASE_URL"),
		JWTSecret:    []byte(getenv("KURZ_JWT_SECRET")),
		ServerName:   or(getenv("KURZ_SERVER_NAME"), "Kurz"),
		Registration: or(getenv("KURZ_REGISTRATION"), "open"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("KURZ_DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("KURZ_JWT_SECRET must have at least 32 bytes")
	}
	if v := getenv("KURZ_NODE_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > ids.MaxNode {
			return Config{}, fmt.Errorf("KURZ_NODE_ID must be an integer in [0, %d]", ids.MaxNode)
		}
		c.NodeID = n
	}
	if c.Registration != "open" && c.Registration != "closed" {
		return Config{}, fmt.Errorf("KURZ_REGISTRATION must be open or closed")
	}
	return c, nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/platform/config/ -count=1 -v`
Expected: `PASS` em todos os subtestes.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/config && git commit -m "feat(server): configuração por variáveis de ambiente"
```

---

### Task 4: Pacote httpx (erros, JSON, auth no contexto, middlewares)

**Files:**
- Create: `server/internal/platform/httpx/errors.go`, `json.go`, `auth.go`, `middleware.go`
- Test: `server/internal/platform/httpx/httpx_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func body(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	return m
}

func TestWriteErrorUsesTypedError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, NewError(409, "username_taken", "taken"))
	if rec.Code != 409 {
		t.Fatalf("status = %d", rec.Code)
	}
	if m := body(t, rec); m["code"] != "username_taken" || m["message"] != "taken" {
		t.Fatalf("body = %v", m)
	}
}

func TestWriteErrorHidesUnknownErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errors.New("password=hunter2 leaked"))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("internal error message leaked to the client")
	}
	if body(t, rec)["code"] != "internal" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDecodeRejectsBadJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("{nope"))
	var v struct{ A string }
	err := Decode(r, &v)
	var he *Error
	if !errors.As(err, &he) || he.Status != 422 || he.Code != "validation" {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeOK(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"x"}`))
	var v struct{ A string }
	if err := Decode(r, &v); err != nil || v.A != "x" {
		t.Fatalf("v = %+v, err = %v", v, err)
	}
}

func TestWrapTurnsReturnedErrorIntoResponse(t *testing.T) {
	h := Wrap(func(w http.ResponseWriter, r *http.Request) error { return ErrForbidden })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 403 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	h := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || body(t, rec)["code"] != "internal" {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserIDContext(t *testing.T) {
	if _, ok := UserID(context.Background()); ok {
		t.Fatal("empty context must not have a user")
	}
	ctx := WithUserID(context.Background(), 42)
	if id, ok := UserID(ctx); !ok || id != 42 {
		t.Fatalf("id = %d, ok = %v", id, ok)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/platform/httpx/ -count=1`
Expected: FAIL com `undefined: WriteError` (e os outros símbolos).

- [ ] **Step 3: Implementar `errors.go`**

```go
// Package httpx reúne o que todo handler HTTP do servidor usa: erros tipados, JSON, usuário no contexto e middlewares.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"
)

// Error é o erro que vira resposta `{code, message}` (ApiError do contrato).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

var (
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "you are not allowed to do this")
	ErrNotFound     = NewError(http.StatusNotFound, "not_found", "not found")
	errInternal     = NewError(http.StatusInternalServerError, "internal", "internal error")
)

func Validation(message string) *Error {
	return NewError(http.StatusUnprocessableEntity, "validation", message)
}

// WriteError escreve err como ApiError. Erros que não são *Error viram 500 sem vazar a mensagem.
func WriteError(w http.ResponseWriter, err error) {
	var he *Error
	if !errors.As(err, &he) {
		slog.Error("unhandled error", "err", err)
		he = errInternal
	}
	WriteJSON(w, he.Status, map[string]string{"code": he.Code, "message": he.Message})
}

// Handler é um handler que devolve erro em vez de escrevê-lo.
type Handler func(http.ResponseWriter, *http.Request) error

func Wrap(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, err)
		}
	}
}
```

- [ ] **Step 4: Implementar `json.go`**

```go
package httpx

import (
	"encoding/json"
	"io"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Decode lê o corpo JSON (até 1 MiB). Corpo inválido vira 422 validation.
func Decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return Validation("invalid JSON body")
	}
	return nil
}
```

- [ ] **Step 5: Implementar `auth.go`**

```go
package httpx

import "context"

type ctxKey struct{}

func WithUserID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// UserID devolve o usuário autenticado da requisição, se houver.
func UserID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKey{}).(int64)
	return id, ok
}
```

- [ ] **Step 6: Implementar `middleware.go`**

```go
package httpx

import (
	"log/slog"
	"net/http"
	"time"
)

// Recover captura pânico, loga e responde 500.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "value", v, "method", r.Method, "path", r.URL.Path)
				WriteError(w, errInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AccessLog loga método, caminho, status e duração de cada requisição.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}
```

- [ ] **Step 7: Rodar e ver passar**

Run: `go test ./internal/platform/httpx/ -count=1 -v`
Expected: 7 testes `PASS`.

- [ ] **Step 8: Commit**

```bash
git add internal/platform/httpx && git commit -m "feat(server): httpx com erros tipados, JSON e middlewares"
```

---

### Task 5: Migrações, pool do Postgres e helper de teste

**Files:**
- Create: `server/migrations/embed.go`, `server/migrations/0001_identity.sql`
- Create: `server/internal/platform/db/db.go`
- Create: `server/internal/platform/dbtest/dbtest.go`
- Test: `server/internal/platform/db/db_test.go`

- [ ] **Step 1: Criar `migrations/0001_identity.sql`**

```sql
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
```

- [ ] **Step 2: Criar `migrations/embed.go`**

```go
// Package migrations embute os arquivos .sql para o binário aplicá-los sozinho.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 3: Escrever `internal/platform/db/db.go`**

```go
// Package db abre o pool do Postgres e aplica as migrações embutidas.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" para o goose
	"github.com/pressly/goose/v3"

	"kurz/server/migrations"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate aplica todas as migrações pendentes.
func Migrate(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open for migrate: %w", err)
	}
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Escrever `internal/platform/dbtest/dbtest.go`**

```go
// Package dbtest dá a cada teste um banco Postgres novo e migrado.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/db"
)

// New cria um banco `kurz_test_<rand>` no servidor de KURZ_TEST_DATABASE_URL, migra e devolve o pool.
// Sem a variável, o teste é pulado. O banco é apagado no Cleanup.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL := os.Getenv("KURZ_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("KURZ_TEST_DATABASE_URL not set (run `make db-up && make test`)")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "kurz_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	if err := db.Migrate(ctx, u.String()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		_ = admin.Close(ctx)
	})
	return pool
}
```

- [ ] **Step 5: Escrever o teste `internal/platform/db/db_test.go`**

Importa `dbtest`, que importa `db`: para evitar ciclo o teste fica em pacote externo `db_test`.

```go
package db_test

import (
	"context"
	"testing"

	"kurz/server/internal/platform/dbtest"
)

func TestMigrationsCreateIdentityTables(t *testing.T) {
	pool := dbtest.New(t)
	for _, table := range []string{"users", "refresh_tokens"} {
		var exists bool
		err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err = %v)", table, err)
		}
	}
}
```

- [ ] **Step 6: Rodar**

Run: `go mod tidy && make test 2>&1 | grep -E "db|FAIL|ok"`
Expected: `ok  kurz/server/internal/platform/db` e nenhuma linha `SKIP`/`FAIL`.

- [ ] **Step 7: Commit**

```bash
git add migrations internal/platform/db internal/platform/dbtest go.mod go.sum
git commit -m "feat(server): pool Postgres, migrações embutidas e helper de teste"
```

---

### Task 6: Hash de senha (Argon2id)

**Files:**
- Create: `server/internal/identity/password.go`
- Test: `server/internal/identity/password_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package identity

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected format: %s", h)
	}
	if !VerifyPassword("correct horse", h) {
		t.Fatal("right password rejected")
	}
	if VerifyPassword("wrong horse", h) {
		t.Fatal("wrong password accepted")
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,t=1,p=1$a", "$bcrypt$v=19$m=1,t=1,p=1$YQ$YQ", "$argon2id$v=19$m=x,t=1,p=1$YQ$YQ"} {
		if VerifyPassword("x", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/identity/ -run 'Hash|Verify' -count=1`
Expected: FAIL com `undefined: HashPassword`.

- [ ] **Step 3: Implementar `password.go`**

```go
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parâmetros recomendados pela OWASP para Argon2id: 19 MiB, 2 iterações, 1 thread.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

var b64 = base64.RawStdEncoding

// HashPassword devolve o hash no formato PHC: $argon2id$v=19$m=..,t=..,p=..$salt$hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword compara em tempo constante. Hash malformado nunca confere.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/identity/ -run 'Hash|Verify' -count=1 -v`
Expected: 3 testes `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/identity/password.go internal/identity/password_test.go
git commit -m "feat(identity): hash de senha com Argon2id"
```

---

### Task 7: Tokens (access JWT e refresh opaco)

**Files:**
- Create: `server/internal/identity/tokens.go`
- Test: `server/internal/identity/tokens_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte(strings.Repeat("k", 32))

func TestAccessTokenRoundTrip(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	raw, err := iss.Issue(12345)
	if err != nil {
		t.Fatal(err)
	}
	id, err := iss.Parse(raw)
	if err != nil || id != 12345 {
		t.Fatalf("id = %d, err = %v", id, err)
	}
}

func TestAccessTokenExpires(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	now := time.Now()
	iss.now = func() time.Time { return now }
	raw, _ := iss.Issue(1)
	iss.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := iss.Parse(raw); err != ErrTokenExpired {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestAccessTokenRejectsForgeries(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	other := NewTokenIssuer([]byte(strings.Repeat("z", 32)), time.Minute)
	foreign, _ := other.Issue(1)
	valid, _ := iss.Issue(1)

	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Issuer: "kurz", Subject: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))})
	noneRaw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	for name, raw := range map[string]string{
		"wrong secret": foreign,
		"tampered":     valid[:len(valid)-2] + "xx",
		"alg none":     noneRaw,
		"garbage":      "not.a.jwt",
		"empty":        "",
	} {
		if _, err := iss.Parse(raw); err != ErrTokenInvalid {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", name, err)
		}
	}
}

func TestRefreshTokensAreRandomAndHashStably(t *testing.T) {
	a, ha := newRefreshToken()
	b, hb := newRefreshToken()
	if a == b || len(a) < 40 {
		t.Fatalf("tokens not random enough: %q %q", a, b)
	}
	if string(ha) == string(hb) {
		t.Fatal("different tokens produced the same hash")
	}
	if string(hashRefreshToken(a)) != string(ha) {
		t.Fatal("hashRefreshToken does not match the hash returned with the token")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/identity/ -run 'Token' -count=1`
Expected: FAIL com `undefined: NewTokenIssuer`.

- [ ] **Step 3: Implementar `tokens.go`**

```go
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"kurz/server/internal/platform/ids"
)

var (
	ErrTokenExpired = errors.New("identity: token expired")
	ErrTokenInvalid = errors.New("identity: token invalid")
)

const tokenIssuer = "kurz"

// TokenIssuer emite e valida o access token (JWT HS256, curto).
type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokenIssuer(secret []byte, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, ttl: ttl, now: time.Now}
}

func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

func (t *TokenIssuer) Issue(userID int64) (string, error) {
	now := t.now()
	claims := jwt.RegisteredClaims{
		Issuer:    tokenIssuer,
		Subject:   ids.Format(userID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// Parse devolve o ID do usuário, ErrTokenExpired ou ErrTokenInvalid.
func (t *TokenIssuer) Parse(raw string) (int64, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return 0, ErrTokenExpired
	}
	if err != nil {
		return 0, ErrTokenInvalid
	}
	id, err := ids.Parse(claims.Subject)
	if err != nil {
		return 0, ErrTokenInvalid
	}
	return id, nil
}

// newRefreshToken devolve o token opaco (vai ao cliente) e o hash SHA-256 (vai ao banco).
func newRefreshToken() (raw string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sem entropia não dá para continuar
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashRefreshToken(raw)
}

func hashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/identity/ -run 'Token' -count=1 -v`
Expected: 4 testes `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/identity/tokens.go internal/identity/tokens_test.go
git commit -m "feat(identity): access token JWT e refresh token opaco"
```

---

### Task 8: Store (SQL do identity)

Sem teste próprio: é coberto pelos testes do service na Task 9, que usam Postgres real.

**Files:**
- Create: `server/internal/identity/store.go`

- [ ] **Step 1: Escrever `store.go`**

```go
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errUsernameTaken  = errors.New("identity: username taken")
	errNotFound       = errors.New("identity: not found")
	errInvalidRefresh = errors.New("identity: invalid refresh token")
	errRefreshReused  = errors.New("identity: refresh token reused")
)

type User struct {
	ID       int64
	Username string
	Name     string
	Subtitle string
	Hue      *float64
}

type newRefresh struct {
	ID        int64
	UserID    int64
	FamilyID  int64
	Hash      []byte
	ExpiresAt time.Time
}

type store struct{ db *pgxpool.Pool }

const userCols = `id, username, name, subtitle, hue`

func scanUser(row pgx.Row, extra ...any) (User, error) {
	var u User
	dest := append([]any{&u.ID, &u.Username, &u.Name, &u.Subtitle, &u.Hue}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errNotFound
		}
		return User{}, err
	}
	return u, nil
}

func (s *store) createUser(ctx context.Context, id int64, username, name, passwordHash string) (User, error) {
	_, err := s.db.Exec(ctx,
		`INSERT INTO users (id, username, name, password_hash) VALUES ($1, $2, $3, $4)`,
		id, username, name, passwordHash)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return User{}, errUsernameTaken
	}
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Name: name}, nil
}

func (s *store) userByUsername(ctx context.Context, username string) (User, string, error) {
	var hash string
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE username = $1`, username), &hash)
	return u, hash, err
}

func (s *store) userByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// updateProfile só altera os campos não nulos. Subtitle "" limpa o campo.
func (s *store) updateProfile(ctx context.Context, id int64, name, subtitle *string, hue *float64) (User, error) {
	return scanUser(s.db.QueryRow(ctx,
		`UPDATE users SET name = COALESCE($2, name), subtitle = COALESCE($3, subtitle), hue = COALESCE($4, hue)
		 WHERE id = $1 RETURNING `+userCols, id, name, subtitle, hue))
}

func (s *store) insertRefresh(ctx context.Context, r newRefresh) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.UserID, r.FamilyID, r.Hash, r.ExpiresAt)
	return err
}

// rotateRefresh gasta o token antigo e grava o novo, na mesma família, numa transação.
// Token já usado = reuso: revoga a família inteira (o commit acontece mesmo assim) e devolve errRefreshReused.
func (s *store) rotateRefresh(ctx context.Context, oldHash []byte, next newRefresh, now time.Time) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // depois do Commit devolve ErrTxClosed, que ignoramos

	var id, userID, familyID int64
	var expiresAt time.Time
	var usedAt, revokedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, family_id, expires_at, used_at, revoked_at FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		oldHash).Scan(&id, &userID, &familyID, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errInvalidRefresh
	}
	if err != nil {
		return 0, err
	}
	if revokedAt != nil || !now.Before(expiresAt) {
		return 0, errInvalidRefresh
	}
	if usedAt != nil {
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, now); err != nil {
			return 0, err
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}
		return 0, errRefreshReused
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = $2 WHERE id = $1`, id, now); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		next.ID, userID, familyID, next.Hash, next.ExpiresAt); err != nil {
		return 0, err
	}
	return userID, tx.Commit(ctx)
}

// revokeFamily revoga a família do token dado. Token desconhecido não é erro.
func (s *store) revokeFamily(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2
		 WHERE revoked_at IS NULL AND family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`,
		hash, now)
	return err
}
```

- [ ] **Step 2: Compilar**

Run: `go build ./...`
Expected: sem saída (sucesso).

- [ ] **Step 3: Commit**

```bash
git add internal/identity/store.go && git commit -m "feat(identity): store SQL de usuários e refresh tokens"
```

---

### Task 9: Service (regras de negócio)

**Files:**
- Create: `server/internal/identity/service.go`
- Test: `server/internal/identity/service_test.go`

- [ ] **Step 1: Escrever os testes**

```go
package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

func newTestService(t *testing.T, registration string) *Service {
	t.Helper()
	return NewService(dbtest.New(t), ids.NewGenerator(1), NewTokenIssuer(testSecret, 15*time.Minute),
		Config{Registration: registration, RefreshTTL: time.Hour})
}

func wantCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != status || he.Code != code {
		t.Fatalf("err = %v, want %d %s", err, status, code)
	}
}

func register(t *testing.T, s *Service, username string) Session {
	t.Helper()
	sess, err := s.Register(context.Background(), RegisterRequest{Username: username, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestRegisterCreatesAccountAndSession(t *testing.T) {
	s := newTestService(t, "open")
	sess, err := s.Register(context.Background(), RegisterRequest{Username: "  Yuri_09 ", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.Username != "yuri_09" || sess.User.Name != "yuri_09" {
		t.Fatalf("user = %+v", sess.User)
	}
	if sess.AccessToken == "" || sess.RefreshToken == "" || sess.ExpiresIn != 900 {
		t.Fatalf("session = %+v", sess)
	}
	if id, err := s.tokens.Parse(sess.AccessToken); err != nil || ids.Format(id) != sess.User.ID {
		t.Fatalf("access token does not belong to the user: id=%d err=%v", id, err)
	}
}

func TestRegisterUsesGivenDisplayName(t *testing.T) {
	s := newTestService(t, "open")
	sess, err := s.Register(context.Background(), RegisterRequest{Username: "yuri", Password: "password123", Name: "Yuri Costa"})
	if err != nil || sess.User.Name != "Yuri Costa" {
		t.Fatalf("sess = %+v, err = %v", sess, err)
	}
}

func TestRegisterRejectsDuplicateUsername(t *testing.T) {
	s := newTestService(t, "open")
	register(t, s, "yuri")
	_, err := s.Register(context.Background(), RegisterRequest{Username: "YURI", Password: "password123"})
	wantCode(t, err, 409, "username_taken")
}

func TestRegisterValidation(t *testing.T) {
	s := newTestService(t, "open")
	cases := map[string]RegisterRequest{
		"username too short": {Username: "ab", Password: "password123"},
		"username bad chars": {Username: "yuri costa", Password: "password123"},
		"username too long":  {Username: strings.Repeat("a", 33), Password: "password123"},
		"password too short": {Username: "yuri", Password: "short"},
		"password too long":  {Username: "yuri", Password: strings.Repeat("p", 129)},
		"name too long":      {Username: "yuri", Password: "password123", Name: strings.Repeat("n", 65)},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.Register(context.Background(), req)
			wantCode(t, err, 422, "validation")
		})
	}
}

func TestRegisterClosed(t *testing.T) {
	s := newTestService(t, "closed")
	_, err := s.Register(context.Background(), RegisterRequest{Username: "yuri", Password: "password123"})
	wantCode(t, err, 403, "registration_closed")
}

func TestLogin(t *testing.T) {
	s := newTestService(t, "open")
	register(t, s, "yuri")
	sess, err := s.Login(context.Background(), LoginRequest{Username: "Yuri", Password: "password123"})
	if err != nil || sess.User.Username != "yuri" {
		t.Fatalf("sess = %+v, err = %v", sess, err)
	}
	_, err = s.Login(context.Background(), LoginRequest{Username: "yuri", Password: "wrong-password"})
	wantCode(t, err, 401, "unauthorized")
	_, err = s.Login(context.Background(), LoginRequest{Username: "ghost", Password: "password123"})
	wantCode(t, err, 401, "unauthorized")
}

func TestRefreshRotatesToken(t *testing.T) {
	s := newTestService(t, "open")
	first := register(t, s, "yuri")
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.User.ID != first.User.ID {
		t.Fatalf("refresh did not rotate: %+v", second)
	}
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken}); err != nil {
		t.Fatalf("new refresh token should work: %v", err)
	}
}

func TestRefreshReuseRevokesTheWholeFamily(t *testing.T) {
	s := newTestService(t, "open")
	first := register(t, s, "yuri")
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	// reapresentar o token antigo é sinal de roubo
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	// e o token novo, da mesma família, também morre
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

func TestRefreshRejectsUnknownAndExpired(t *testing.T) {
	s := newTestService(t, "open")
	_, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: "nope"})
	wantCode(t, err, 401, "unauthorized")

	sess := register(t, s, "yuri")
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) } // além do RefreshTTL de 1h
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	s := newTestService(t, "open")
	sess := register(t, s, "yuri")
	if err := s.Logout(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	if err := s.Logout(context.Background(), RefreshRequest{RefreshToken: "unknown"}); err != nil {
		t.Fatalf("logout with unknown token must not fail: %v", err)
	}
}

func TestMeAndUpdateMe(t *testing.T) {
	s := newTestService(t, "open")
	sess := register(t, s, "yuri")
	uid, _ := ids.Parse(sess.User.ID)

	me, err := s.Me(context.Background(), uid)
	if err != nil || me.Username != "yuri" {
		t.Fatalf("me = %+v, err = %v", me, err)
	}

	name, sub, hue := "Yuri C.", "codando", 200.0
	got, err := s.UpdateMe(context.Background(), uid, UpdateMeRequest{Name: &name, Subtitle: &sub, Hue: &hue})
	if err != nil || got.Name != "Yuri C." || got.Subtitle != "codando" || got.Hue == nil || *got.Hue != 200 {
		t.Fatalf("got = %+v, err = %v", got, err)
	}

	empty := ""
	got, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Subtitle: &empty})
	if err != nil || got.Subtitle != "" || got.Name != "Yuri C." {
		t.Fatalf("clearing subtitle failed: %+v, err = %v", got, err)
	}

	bad := ""
	_, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Name: &bad})
	wantCode(t, err, 422, "validation")
	badHue := 400.0
	_, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Hue: &badHue})
	wantCode(t, err, 422, "validation")
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `make test 2>&1 | grep -E "identity|undefined"`
Expected: falha de compilação com `undefined: Service` / `NewService`.

- [ ] **Step 3: Implementar `service.go`**

```go
package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

type Config struct {
	Registration string // "open" ou "closed"
	RefreshTTL   time.Duration
}

type Service struct {
	store     *store
	ids       *ids.Generator
	tokens    *TokenIssuer
	cfg       Config
	now       func() time.Time
	dummyHash string // para gastar o mesmo tempo quando o usuário não existe
}

func NewService(db *pgxpool.Pool, gen *ids.Generator, tokens *TokenIssuer, cfg Config) *Service {
	dummy, err := HashPassword("kurz-dummy-password")
	if err != nil {
		panic(err)
	}
	return &Service{store: &store{db: db}, ids: gen, tokens: tokens, cfg: cfg, now: time.Now, dummyHash: dummy}
}

// Tipos de requisição e resposta (JSON do contrato).

type RegisterRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Name       string `json:"name"`
	InviteCode string `json:"inviteCode"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type UpdateMeRequest struct {
	Name     *string  `json:"name"`
	Subtitle *string  `json:"subtitle"`
	Hue      *float64 `json:"hue"`
}

// UserView é o User da API. `status` é sempre offline até o gateway existir (Plano 4).
type UserView struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Subtitle string   `json:"subtitle,omitempty"`
	Hue      *float64 `json:"hue,omitempty"`
}

type Session struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	ExpiresIn    int      `json:"expiresIn"`
	User         UserView `json:"user"`
}

func view(u User) UserView {
	return UserView{ID: ids.Format(u.ID), Username: u.Username, Name: u.Name, Status: "offline", Subtitle: u.Subtitle, Hue: u.Hue}
}

var errBadCredentials = httpx.NewError(401, "unauthorized", "invalid username or password")

func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (s *Service) Register(ctx context.Context, req RegisterRequest) (Session, error) {
	if s.cfg.Registration == "closed" {
		return Session{}, httpx.NewError(403, "registration_closed", "registration is closed")
	}
	username := normalizeUsername(req.Username)
	if !usernameRe.MatchString(username) {
		return Session{}, httpx.Validation("username must be 3-32 characters of a-z, 0-9 or _")
	}
	if n := utf8.RuneCountInString(req.Password); n < 8 || n > 128 {
		return Session{}, httpx.Validation("password must have 8-128 characters")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = username
	}
	if n := utf8.RuneCountInString(name); n > 64 {
		return Session{}, httpx.Validation("name must have 1-64 characters")
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.createUser(ctx, s.ids.Next(), username, name, hash)
	if errors.Is(err, errUsernameTaken) {
		return Session{}, httpx.NewError(409, "username_taken", "username is already taken")
	}
	if err != nil {
		return Session{}, err
	}
	return s.newSession(ctx, user, s.ids.Next())
}

func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, error) {
	user, hash, err := s.store.userByUsername(ctx, normalizeUsername(req.Username))
	if errors.Is(err, errNotFound) {
		VerifyPassword(req.Password, s.dummyHash)
		return Session{}, errBadCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if !VerifyPassword(req.Password, hash) {
		return Session{}, errBadCredentials
	}
	return s.newSession(ctx, user, s.ids.Next())
}

func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (Session, error) {
	raw, hash := newRefreshToken()
	now := s.now()
	userID, err := s.store.rotateRefresh(ctx, hashRefreshToken(req.RefreshToken),
		newRefresh{ID: s.ids.Next(), Hash: hash, ExpiresAt: now.Add(s.cfg.RefreshTTL)}, now)
	if errors.Is(err, errInvalidRefresh) || errors.Is(err, errRefreshReused) {
		return Session{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.userByID(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	return s.sessionFor(user, raw)
}

func (s *Service) Logout(ctx context.Context, req RefreshRequest) error {
	return s.store.revokeFamily(ctx, hashRefreshToken(req.RefreshToken), s.now())
}

func (s *Service) Me(ctx context.Context, id int64) (UserView, error) {
	u, err := s.store.userByID(ctx, id)
	if errors.Is(err, errNotFound) {
		return UserView{}, httpx.ErrUnauthorized // conta apagada com token ainda válido
	}
	if err != nil {
		return UserView{}, err
	}
	return view(u), nil
}

func (s *Service) UpdateMe(ctx context.Context, id int64, req UpdateMeRequest) (UserView, error) {
	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(*req.Name); n < 1 || n > 64 {
			return UserView{}, httpx.Validation("name must have 1-64 characters")
		}
	}
	if req.Subtitle != nil && utf8.RuneCountInString(*req.Subtitle) > 128 {
		return UserView{}, httpx.Validation("subtitle must have at most 128 characters")
	}
	if req.Hue != nil && (*req.Hue < 0 || *req.Hue > 360) {
		return UserView{}, httpx.Validation("hue must be between 0 and 360")
	}
	u, err := s.store.updateProfile(ctx, id, req.Name, req.Subtitle, req.Hue)
	if errors.Is(err, errNotFound) {
		return UserView{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return UserView{}, err
	}
	return view(u), nil
}

// newSession abre uma família de refresh tokens nova (login ou registro).
func (s *Service) newSession(ctx context.Context, user User, familyID int64) (Session, error) {
	raw, hash := newRefreshToken()
	err := s.store.insertRefresh(ctx, newRefresh{
		ID: s.ids.Next(), UserID: user.ID, FamilyID: familyID, Hash: hash, ExpiresAt: s.now().Add(s.cfg.RefreshTTL),
	})
	if err != nil {
		return Session{}, err
	}
	return s.sessionFor(user, raw)
}

func (s *Service) sessionFor(user User, refreshRaw string) (Session, error) {
	access, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: refreshRaw, ExpiresIn: int(s.tokens.TTL().Seconds()), User: view(user)}, nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `make test 2>&1 | grep -E "identity|FAIL|SKIP"`
Expected: `ok  kurz/server/internal/identity`, sem `FAIL` nem `SKIP`.

- [ ] **Step 5: Commit**

```bash
git add internal/identity/service.go internal/identity/service_test.go
git commit -m "feat(identity): service de registro, login, refresh, logout e perfil"
```

---

### Task 10: Handlers HTTP e middleware de autenticação

**Files:**
- Create: `server/internal/identity/http.go`

Os handlers são finos (decodificam, chamam o service, escrevem). Os testes ficam na Task 11, de ponta a ponta.

- [ ] **Step 1: Escrever `http.go`**

```go
package identity

import (
	"net/http"
	"strings"

	"kurz/server/internal/platform/httpx"
)

// Require exige `Authorization: Bearer <access token>` e põe o usuário no contexto.
func Require(tokens *TokenIssuer) func(httpx.Handler) httpx.Handler {
	return func(next httpx.Handler) httpx.Handler {
		return func(w http.ResponseWriter, r *http.Request) error {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				return httpx.ErrUnauthorized
			}
			id, err := tokens.Parse(raw)
			switch err {
			case nil:
			case ErrTokenExpired:
				return httpx.NewError(http.StatusUnauthorized, "token_expired", "access token expired")
			default:
				return httpx.ErrUnauthorized
			}
			return next(w, r.WithContext(httpx.WithUserID(r.Context(), id)))
		}
	}
}

// Mount registra as rotas de auth e de perfil.
func (s *Service) Mount(mux *http.ServeMux) {
	auth := Require(s.tokens)
	mux.HandleFunc("POST /v1/auth/register", httpx.Wrap(s.handleRegister))
	mux.HandleFunc("POST /v1/auth/login", httpx.Wrap(s.handleLogin))
	mux.HandleFunc("POST /v1/auth/refresh", httpx.Wrap(s.handleRefresh))
	mux.HandleFunc("POST /v1/auth/logout", httpx.Wrap(s.handleLogout))
	mux.HandleFunc("GET /v1/me", httpx.Wrap(auth(s.handleMe)))
	mux.HandleFunc("PATCH /v1/me", httpx.Wrap(auth(s.handleUpdateMe)))
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) error {
	var req RegisterRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Register(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, sess)
	return nil
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req LoginRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Login(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
	return nil
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) error {
	var req RefreshRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Refresh(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
	return nil
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) error {
	var req RefreshRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := s.Logout(r.Context(), req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := httpx.UserID(r.Context())
	u, err := s.Me(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}

func (s *Service) handleUpdateMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := httpx.UserID(r.Context())
	var req UpdateMeRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	u, err := s.UpdateMe(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}
```

- [ ] **Step 2: Compilar**

Run: `go build ./...`
Expected: sem saída.

- [ ] **Step 3: Commit**

```bash
git add internal/identity/http.go && git commit -m "feat(identity): handlers HTTP e middleware Bearer"
```

---

### Task 11: Montagem do servidor e teste ponta a ponta

**Files:**
- Create: `server/internal/server/server.go`
- Test: `server/internal/server/server_test.go`

- [ ] **Step 1: Escrever o teste**

```go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		ServerName:   "Teste",
		Registration: "open",
		JWTSecret:    []byte(strings.Repeat("k", 32)),
	}
	srv := httptest.NewServer(New(cfg, dbtest.New(t)))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestDiscoveryAndHealth(t *testing.T) {
	srv := newTestServer(t)
	status, body := call(t, "GET", srv.URL+"/.well-known/kurz", "", nil)
	if status != 200 || body["name"] != "Teste" || body["registration"] != "open" || body["contract"] != "0.2" {
		t.Fatalf("discovery = %d %v", status, body)
	}
	if caps, _ := body["capabilities"].([]any); len(caps) != 1 || caps[0] != "text" {
		t.Fatalf("capabilities = %v", body["capabilities"])
	}
	if status, _ := call(t, "GET", srv.URL+"/healthz", "", nil); status != 200 {
		t.Fatalf("healthz = %d", status)
	}
}

func TestAuthFlowEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	creds := map[string]string{"username": "yuri", "password": "password123"}

	status, sess := call(t, "POST", srv.URL+"/v1/auth/register", "", creds)
	if status != 201 {
		t.Fatalf("register = %d %v", status, sess)
	}
	if status, body := call(t, "POST", srv.URL+"/v1/auth/register", "", creds); status != 409 || body["code"] != "username_taken" {
		t.Fatalf("duplicate register = %d %v", status, body)
	}

	// /me exige token
	if status, body := call(t, "GET", srv.URL+"/v1/me", "", nil); status != 401 || body["code"] != "unauthorized" {
		t.Fatalf("me without token = %d %v", status, body)
	}
	access := sess["accessToken"].(string)
	status, me := call(t, "GET", srv.URL+"/v1/me", access, nil)
	if status != 200 || me["username"] != "yuri" || me["status"] != "offline" {
		t.Fatalf("me = %d %v", status, me)
	}

	status, upd := call(t, "PATCH", srv.URL+"/v1/me", access, map[string]any{"name": "Yuri C.", "subtitle": "codando"})
	if status != 200 || upd["name"] != "Yuri C." || upd["subtitle"] != "codando" {
		t.Fatalf("patch me = %d %v", status, upd)
	}

	// login
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/login", "", creds); status != 200 {
		t.Fatalf("login = %d", status)
	}
	if status, body := call(t, "POST", srv.URL+"/v1/auth/login", "", map[string]string{"username": "yuri", "password": "wrongwrong"}); status != 401 {
		t.Fatalf("bad login = %d %v", status, body)
	}

	// refresh: o token antigo para de funcionar e o reuso derruba a família
	old := sess["refreshToken"].(string)
	status, next := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": old})
	if status != 200 {
		t.Fatalf("refresh = %d %v", status, next)
	}
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": old}); status != 401 {
		t.Fatalf("reused refresh = %d", status)
	}
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": next["refreshToken"].(string)}); status != 401 {
		t.Fatalf("family should be revoked, got %d", status)
	}

	// logout sempre 204
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/logout", "", map[string]string{"refreshToken": "whatever"}); status != 204 {
		t.Fatalf("logout = %d", status)
	}
}

func TestInvalidBearerTokens(t *testing.T) {
	srv := newTestServer(t)
	for _, tok := range []string{"garbage", "a.b.c"} {
		if status, body := call(t, "GET", srv.URL+"/v1/me", tok, nil); status != 401 || body["code"] != "unauthorized" {
			t.Errorf("token %q: %d %v", tok, status, body)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `make test 2>&1 | grep -E "server|undefined"`
Expected: falha de compilação com `undefined: New`.

- [ ] **Step 3: Implementar `server.go`**

```go
// Package server monta o http.Handler do monólito: cada módulo registra as suas rotas aqui.
package server

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/identity"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	// contractVersion é o major.minor de contract/openapi.yaml que este servidor implementa.
	contractVersion = "0.2"
)

func New(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	gen := ids.NewGenerator(cfg.NodeID)
	tokens := identity.NewTokenIssuer(cfg.JWTSecret, accessTTL)
	idSvc := identity.NewService(pool, gen, tokens, identity.Config{Registration: cfg.Registration, RefreshTTL: refreshTTL})

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

	return httpx.AccessLog(httpx.Recover(mux))
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `make test 2>&1 | tail -15`
Expected: `ok` para ids, config, httpx, db, identity e server. Nenhum `FAIL` nem `SKIP`.

- [ ] **Step 5: Commit**

```bash
git add internal/server && git commit -m "feat(server): montagem do handler e teste ponta a ponta de auth"
```

---

### Task 12: `main`, execução real e README

**Files:**
- Create: `server/cmd/kurz/main.go`, `server/README.md`

- [ ] **Step 1: Escrever `cmd/kurz/main.go`**

```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/db"
	"kurz/server/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(cfg, pool),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.Addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Subir o servidor de verdade**

Run (em um terminal): `make run`
Expected: linha JSON `"msg":"listening"` com `"addr":":8080"`.

- [ ] **Step 3: Exercitar com curl (outro terminal)**

```bash
curl -s localhost:8080/.well-known/kurz
curl -s -X POST localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{"username":"yuri","password":"password123"}'
```
Expected: o primeiro devolve `{"capabilities":["text"],"contract":"0.2","name":"Kurz","registration":"open"}`; o segundo devolve `201` com `accessToken`, `refreshToken`, `expiresIn: 900` e `user`.

Depois, com o `accessToken` obtido:
```bash
curl -s localhost:8080/v1/me -H "Authorization: Bearer <accessToken>"
```
Expected: `{"id":"...","username":"yuri","name":"yuri","status":"offline"}`.

Pare o servidor com Ctrl+C. Expected: o processo encerra sem erro.

- [ ] **Step 4: Escrever `server/README.md`**

````markdown
# Kurz server

Servidor multi-guild em Go. Design: `docs/superpowers/specs/2026-11-01-kurz-server-design.md`. Planos: `docs/superpowers/plans/`.

## Rodar

```bash
cd server
make db-up   # Postgres em localhost:5433
make run     # servidor em :8080 (aplica as migrações sozinho)
make test    # testes, incluindo os de integração com Postgres
```

## Configuração

| Variável | Padrão | |
|---|---|---|
| `KURZ_DATABASE_URL` | obrigatória | URL do Postgres |
| `KURZ_JWT_SECRET` | obrigatória | 32 bytes ou mais |
| `KURZ_ADDR` | `:8080` | |
| `KURZ_NODE_ID` | `0` | 0 a 1023; deve ser único por réplica (bits do snowflake) |
| `KURZ_SERVER_NAME` | `Kurz` | |
| `KURZ_REGISTRATION` | `open` | `open` ou `closed` |
````

- [ ] **Step 5: Rodar toda a suíte uma última vez**

Run: `go vet ./... && make test 2>&1 | tail -10`
Expected: `go vet` sem saída; todos os pacotes `ok`; nenhum `SKIP`.

- [ ] **Step 6: Commit**

```bash
cd .. && git add server/cmd server/README.md
git commit -m "feat(server): main com shutdown gracioso e README"
```

---

## Self-review (spec x plano)

- **RF1** (registro, login, refresh rotativo, logout, Argon2id): Tasks 6, 7, 8, 9, 10, 11.
- **RF2** (perfil): `GET/PATCH /v1/me` nas Tasks 9, 10, 11. O avatar (`avatarUrl`) depende de uploads e fica para o Plano 5.
- **RNF11** (Argon2id, rotação com detecção de reuso): Tasks 6, 8, 9; teste de reuso na Task 9 e ponta a ponta na Task 11.
- **RNF15** (migrações versionadas e reversíveis): Task 5 (`Up` e `Down`). O "um comando sobe tudo" completo (kind) é do Plano 7; aqui há `make db-up` e `make run`.
- **RNF16** (testes): unitários e de integração com Postgres real. O gerador de carga é do Plano 7. Usamos Postgres via Docker Compose em vez de testcontainers, o que dá o mesmo resultado com menos dependências.
- **Divergência consciente do spec:** o spec cita testcontainers. O plano usa Compose + banco por teste (`dbtest`), porque o Docker já é pré-requisito do projeto e isso evita um módulo pesado.
- **Consistência de tipos:** `Session`, `UserView`, `RegisterRequest`, `LoginRequest`, `RefreshRequest`, `UpdateMeRequest`, `NewTokenIssuer`, `TokenIssuer.TTL`, `Service.now` e `testSecret` têm a mesma definição em todas as tasks que os usam. `httpx.Wrap`, `Decode`, `WriteJSON`, `WriteError`, `WithUserID`, `UserID`, `Validation`, `ErrUnauthorized` idem.
- **Sem placeholders.**
