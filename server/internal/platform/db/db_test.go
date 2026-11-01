package db_test

import (
	"context"
	"sync"
	"testing"

	"kurz/server/internal/platform/db"
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
