package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

// DB wraps the database connection
type DB struct {
	*sql.DB
}

// Open opens a database connection and runs migrations
func Open(dbType, dbPath string) (*DB, error) {
	if dbType != "sqlite" {
		return nil, fmt.Errorf("unsupported database type: %s (only sqlite supported for MVP)", dbType)
	}

	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{sqlDB}

	// Run migrations
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return db, nil
}

// migrate runs database migrations
func (db *DB) migrate() error {
	// Check current schema version
	var currentVersion int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)
	if err != nil && err != sql.ErrNoRows {
		// Table might not exist yet, try to create it
		if _, err := db.Exec(schemaSQL); err != nil {
			return fmt.Errorf("failed to initialize schema: %w", err)
		}
		return nil
	}

	// If schema is already at latest version, we're done
	if currentVersion >= 1 {
		return nil
	}

	// Apply schema
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("failed to apply schema: %w", err)
	}

	return nil
}
