// Package dbtest opens the database for tests: a temporary SQLite file, or
// the PostgreSQL server in TEST_POSTGRES_DSN, so the same tests can run
// against both. Tests share that server's database, so run them one package
// at a time (go test -p 1 ./...).
package dbtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/db"
)

// tables are emptied children first, so foreign keys allow it
var tables = []string{"sessions", "users", "push_subscriptions", "terraform_environments", "deployments", "policies", "versions", "environments", "applications"}

// Open returns an empty, migrated database that is closed (and for
// PostgreSQL emptied) when the test ends
func Open(t testing.TB) *db.DB {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		return database
	}

	database, err := db.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	empty := func() {
		for _, table := range tables {
			if _, err := database.Exec("DELETE FROM " + table); err != nil {
				t.Errorf("failed to empty %s: %v", table, err)
			}
		}
	}
	empty()
	t.Cleanup(func() {
		empty()
		database.Close()
	})
	return database
}
