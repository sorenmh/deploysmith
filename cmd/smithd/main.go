package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"
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
	// Generates the key pair for Web Push (VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY)
	if len(os.Args) > 1 && os.Args[1] == "vapid-keys" {
		private, public, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatalf("Failed to generate VAPID keys: %v", err)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", public, private)
		return
	}

	log.Printf("smithd %s (commit: %s, built: %s)\n", version, commit, date)
	api.Version = version

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
