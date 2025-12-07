package main

import (
	"flag"
	"log"

	"github.com/sorenmh/infrastructure-shared/deployment-api/api"
	"github.com/sorenmh/infrastructure-shared/deployment-api/config"
	"github.com/sorenmh/infrastructure-shared/deployment-api/db"
	"github.com/sorenmh/infrastructure-shared/deployment-api/git"
)

func main() {
	configPath := flag.String("config", "/etc/deployment-api/config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Loaded configuration for %d services", len(cfg.Services))

	// Initialize database
	database, err := db.New(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	log.Printf("Database initialized at %s", cfg.Database.Path)

	// Initialize Git client
	gitClient, err := git.NewClient(
		cfg.Git.RepositoryURL,
		cfg.Git.Branch,
		cfg.Git.LocalPath,
		cfg.Git.Username,
		cfg.Git.Token,
		cfg.Git.AuthorName,
		cfg.Git.AuthorEmail,
	)
	if err != nil {
		log.Fatalf("Failed to initialize git client: %v", err)
	}

	log.Printf("Git client initialized (repo: %s, branch: %s)", cfg.Git.RepositoryURL, cfg.Git.Branch)

	// Create and start API server
	server := api.NewServer(cfg, database, gitClient)

	log.Printf("Starting Deployment API v%s", api.Version)

	if err := server.Run(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
