package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/deploysmith/deploysmith/internal/smithd/api"
	"github.com/deploysmith/deploysmith/internal/smithd/config"
	"github.com/deploysmith/deploysmith/internal/smithd/db"
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

	// Ensure database directory exists
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	// Open database
	database, err := db.Open(cfg.DBType, cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	log.Printf("Database initialized: %s", cfg.DBPath)

	// Create HTTP server
	server := api.NewServer(cfg, database)

	// Start server
	log.Printf("Starting smithd on port %s", cfg.Port)
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
