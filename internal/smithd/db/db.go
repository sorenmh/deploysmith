package db

import (
	"database/sql"
	"embed"
	"fmt"
	"regexp"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	migratePostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	migrateSQLite "github.com/golang-migrate/migrate/v4/database/sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var questionMark = regexp.MustCompile(`\?`)

// DB wraps sql.DB and provides automatic placeholder rebinding for PostgreSQL.
type DB struct {
	*sql.DB
	driver string // "sqlite3" or "postgres"
}

// Open opens a database connection and runs migrations.
// dsn is the SQLite file path (when driver=="sqlite") or a PostgreSQL DSN.
func Open(driver, dsn string) (*DB, error) {
	var sqlDriver string
	switch driver {
	case "sqlite", "sqlite3", "":
		sqlDriver = "sqlite3"
		if dsn == "" {
			dsn = "./data/smithd.db"
		}
	case "postgres", "postgresql":
		sqlDriver = "postgres"
	default:
		return nil, fmt.Errorf("unsupported database driver: %s (supported: sqlite, postgres)", driver)
	}

	sqlDB, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{DB: sqlDB, driver: sqlDriver}
	if err := db.runMigrations(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	return db, nil
}

func (db *DB) runMigrations() error {
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to load migration source: %w", err)
	}

	var m *migrate.Migrate
	switch db.driver {
	case "sqlite3":
		dbDriver, err := migrateSQLite.WithInstance(db.DB, &migrateSQLite.Config{})
		if err != nil {
			return fmt.Errorf("failed to create sqlite migrate driver: %w", err)
		}
		m, err = migrate.NewWithInstance("iofs", sourceDriver, "sqlite3", dbDriver)
		if err != nil {
			return fmt.Errorf("failed to create migrator: %w", err)
		}
	case "postgres":
		dbDriver, err := migratePostgres.WithInstance(db.DB, &migratePostgres.Config{})
		if err != nil {
			return fmt.Errorf("failed to create postgres migrate driver: %w", err)
		}
		m, err = migrate.NewWithInstance("iofs", sourceDriver, "postgres", dbDriver)
		if err != nil {
			return fmt.Errorf("failed to create migrator: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration failed: %w", err)
	}
	return nil
}

// rebind converts SQLite-style `?` placeholders to PostgreSQL `$N` placeholders.
// It is a no-op for SQLite.
func (db *DB) rebind(query string) string {
	if db.driver != "postgres" {
		return query
	}
	// Replace each `?` with $1, $2, ...
	n := 0
	return questionMark.ReplaceAllStringFunc(query, func(_ string) string {
		n++
		return fmt.Sprintf("$%d", n)
	})
}

// Exec overrides sql.DB.Exec to rebind placeholders.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(db.rebind(query), args...)
}

// Query overrides sql.DB.Query to rebind placeholders.
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(db.rebind(query), args...)
}

// QueryRow overrides sql.DB.QueryRow to rebind placeholders.
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(db.rebind(query), args...)
}
