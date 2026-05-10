package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/sorenmh/deploysmith/internal/smithd/api"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	log.Printf("smithd %s (commit: %s, built: %s)\n", version, commit, date)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Ensure database directory exists (for SQLite)
	if cfg.DBDriver == "sqlite" || cfg.DBDriver == "sqlite3" || cfg.DBDriver == "" {
		dbDir := filepath.Dir(cfg.DBDSN)
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			log.Fatalf("Failed to create database directory: %v", err)
		}
	}

	// Open database
	database, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	log.Printf("Database initialized: %s", cfg.DBDSN)

	// Create HTTP server
	server := api.NewServer(cfg, database)

	// Start server
	log.Printf("Starting smithd on port %s", cfg.Port)
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
