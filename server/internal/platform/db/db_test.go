package db_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"

	"kurz/server/internal/platform/db"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/migrations"
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

// Várias réplicas subindo juntas não podem quebrar umas às outras na migração.
func TestConcurrentMigrate(t *testing.T) {
	dbURL := dbtest.EmptyDatabase(t)
	ctx := context.Background()
	const n = 4
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, n)
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = db.Migrate(ctx, dbURL)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("migrate #%d: %v", i, err)
		}
	}

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, table := range []string{"users", "refresh_tokens"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err = %v)", table, err)
		}
	}
}

// A 0002 é reversível: desce até a 0001, sobe de novo e as tabelas voltam.
func TestGuildsMigrationDownAndUp(t *testing.T) {
	dbURL := dbtest.EmptyDatabase(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	has := func(table string) bool {
		var ok bool
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	tables := []string{"guilds", "roles", "members", "member_roles", "channels", "channel_overrides", "invites", "bans"}
	for _, tb := range tables {
		if !has(tb) {
			t.Fatalf("%s missing after up", tb)
		}
	}
	if _, err := provider.DownTo(ctx, 1); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tb := range tables {
		if has(tb) {
			t.Fatalf("%s still exists after down", tb)
		}
	}
	if !has("users") {
		t.Fatal("down must keep 0001")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !has("guilds") {
		t.Fatal("guilds missing after re-up")
	}
}

// A 0003 é reversível: desce até a 0002, sobe de novo e as tabelas voltam.
func TestMessagesMigrationDownAndUp(t *testing.T) {
	dbURL := dbtest.EmptyDatabase(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	has := func(table string) bool {
		var ok bool
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	tables := []string{"messages", "reactions", "read_states"}
	for _, tb := range tables {
		if !has(tb) {
			t.Fatalf("%s missing after up", tb)
		}
	}
	if _, err := provider.DownTo(ctx, 2); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tb := range tables {
		if has(tb) {
			t.Fatalf("%s still exists after down", tb)
		}
	}
	if !has("channels") {
		t.Fatal("down must keep 0002")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !has("messages") {
		t.Fatal("messages missing after re-up")
	}
}

// A 0004 é reversível: desce até a 0003, sobe de novo e o outbox volta.
func TestOutboxMigrationDownAndUp(t *testing.T) {
	dbURL := dbtest.EmptyDatabase(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	has := func(table string) bool {
		var ok bool
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !has("outbox") {
		t.Fatal("outbox missing after up")
	}
	if _, err := provider.DownTo(ctx, 3); err != nil {
		t.Fatalf("down: %v", err)
	}
	if has("outbox") {
		t.Fatal("outbox still exists after down")
	}
	if !has("messages") {
		t.Fatal("down must keep 0003")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !has("outbox") {
		t.Fatal("outbox missing after re-up")
	}
}

// A 0005 é reversível: desce até a 0004 (anexos, jobs e busca somem; mensagens vazias ganham texto) e sobe de novo.
func TestAttachmentsSearchJobsMigrationDownAndUp(t *testing.T) {
	dbURL := dbtest.EmptyDatabase(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := sqlDB.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := sqlDB.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	tableCount := func() int {
		return count(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('attachments', 'jobs')`)
	}
	exec(`INSERT INTO users (id, username, name, password_hash) VALUES (1, 'u', 'u', 'x')`)
	exec(`INSERT INTO guilds (id, name, owner_id) VALUES (2, 'g', 1)`)
	exec(`INSERT INTO channels (id, guild_id, kind, name, position) VALUES (3, 2, 'text', 'c', 0)`)
	exec(`INSERT INTO messages (id, channel_id, author_id, content) VALUES (4, 3, 1, ''), (5, 3, 1, 'as casas de planejamento')`)
	if n := count(`SELECT count(*) FROM messages WHERE search @@ websearch_to_tsquery('portuguese', 'casa')`); n != 1 {
		t.Fatalf("stemmed search matches = %d", n)
	}
	if tableCount() != 2 {
		t.Fatal("attachments/jobs missing after up")
	}
	if _, err := provider.DownTo(ctx, 4); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableCount() != 0 || count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'messages' AND column_name = 'search'`) != 0 {
		t.Fatal("0005 objects still exist after down")
	}
	if count(`SELECT count(*) FROM messages`) != 2 || count(`SELECT count(*) FROM messages WHERE content = ''`) != 0 {
		t.Fatal("down must keep messages and fill empty text")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if tableCount() != 2 || count(`SELECT count(*) FROM messages WHERE search @@ websearch_to_tsquery('portuguese', 'planejamento')`) != 1 {
		t.Fatal("re-up must restore tables and backfill the search column")
	}
}
