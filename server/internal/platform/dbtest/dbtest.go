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
