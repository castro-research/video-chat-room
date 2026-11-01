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
