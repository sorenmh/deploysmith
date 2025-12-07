package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the application configuration
type Config struct {
	// Server
	Port    string
	APIKeys []string

	// Database
	DBType string
	DBPath string

	// S3
	S3Bucket           string
	S3Region           string
	AWSEndpoint        string
	AWSAccessKeyID     string
	AWSSecretAccessKey string

	// Gitops
	GitopsRepo        string
	GitopsSSHKeyPath  string
	GitopsUserName    string
	GitopsUserEmail   string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		Port:              getEnv("PORT", "8080"),
		APIKeys:           strings.Split(getEnv("API_KEYS", ""), ","),
		DBType:            getEnv("DB_TYPE", "sqlite"),
		DBPath:            getEnv("DB_PATH", "./data/smithd.db"),
		S3Bucket:           getEnv("S3_BUCKET", ""),
		S3Region:           getEnv("S3_REGION", "us-east-1"),
		AWSEndpoint:        getEnv("AWS_ENDPOINT", ""),
		AWSAccessKeyID:     getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: getEnv("AWS_SECRET_ACCESS_KEY", ""),
		GitopsRepo:        getEnv("GITOPS_REPO", ""),
		GitopsSSHKeyPath:  getEnv("GITOPS_SSH_KEY_PATH", ""),
		GitopsUserName:    getEnv("GITOPS_USER_NAME", "smithd"),
		GitopsUserEmail:   getEnv("GITOPS_USER_EMAIL", "smithd@deploysmith.io"),
	}

	// Validate required fields
	if len(cfg.APIKeys) == 0 || cfg.APIKeys[0] == "" {
		return nil, fmt.Errorf("API_KEYS is required")
	}

	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}

	if cfg.GitopsRepo == "" {
		return nil, fmt.Errorf("GITOPS_REPO is required")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
