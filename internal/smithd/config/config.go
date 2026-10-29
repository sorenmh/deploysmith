package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds the application configuration
type Config struct {
	// Server
	Port    string
	APIKeys []string

	// Database
	DBDriver string
	DBDSN    string

	// S3
	S3Bucket           string
	S3Region           string
	AWSEndpoint        string
	AWSAccessKeyID     string
	AWSSecretAccessKey string

	// Gitops
	GitopsRepo       string
	GitopsSSHKeyPath string
	GitopsUserName   string
	GitopsUserEmail  string

	// Events
	NATSUrl string

	// Flux
	FluxWebhookURL   string
	FluxWebhookToken string

	// Slack
	SlackWebhookURL string

	// Web Push (generate keys with `smithd vapid-keys`); push is off without them
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string

	// Flux Kustomization polling
	FluxNamespace         string
	FluxKustomizationName string

	// Web UI. The UI is only served when UIUserHeader is set or GitHub login
	// is configured. With UIUserHeader it must only be reachable through an
	// authenticating proxy (e.g. oauth2-proxy) that sets this header.
	UIUserHeader     string
	UIAllowedUsers   []string // emails, or "@domain" entries; empty allows all authenticated users
	UITrustedOrigins []string // extra origins allowed to submit UI forms
	// UIListenAddr serves the UI on its own listener instead of the API
	// port, e.g. 127.0.0.1:8081 behind an oauth2-proxy sidecar, so that
	// nothing but the proxy can reach it and set the user header
	UIListenAddr string
	// UIPublicURL is where users reach the UI, e.g. https://smithui.example.com,
	// used to link to deployments from Slack messages
	UIPublicURL string

	// Login with GitHub (a GitHub App or OAuth App), off without a client id.
	// People then sign in to smithctl (device flow) and the web UI (which
	// then requires it) and get short-lived smithd sessions; API keys keep
	// working for machines. Who may sign in: GitHubAllowedUsers, and the
	// members of GitHubAllowedOrg (narrowed to GitHubAllowedTeam if set).
	GitHubClientID     string
	GitHubClientSecret string
	GitHubURL          string // https://github.com, or a GitHub Enterprise Server URL
	GitHubAPIURL       string // defaults to the API of GitHubURL
	GitHubAllowedOrg   string
	GitHubAllowedTeam  string   // team slug in GitHubAllowedOrg
	GitHubAllowedUsers []string // GitHub logins
	SessionTTL         time.Duration
}

// GitHubLoginEnabled reports whether people can sign in with GitHub
func (c *Config) GitHubLoginEnabled() bool {
	return c.GitHubClientID != ""
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		Port:                  getEnv("PORT", "8080"),
		APIKeys:               strings.Split(getEnv("API_KEYS", ""), ","),
		DBDriver:              getEnv("DB_DRIVER", "sqlite"),
		DBDSN:                 getEnv("DB_DSN", "./data/smithd.db"),
		S3Bucket:              getEnv("S3_BUCKET", ""),
		S3Region:              getEnv("S3_REGION", "us-east-1"),
		AWSEndpoint:           getEnv("AWS_ENDPOINT", ""),
		AWSAccessKeyID:        getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey:    getEnv("AWS_SECRET_ACCESS_KEY", ""),
		GitopsRepo:            getEnv("GITOPS_REPO", ""),
		GitopsSSHKeyPath:      getEnv("GITOPS_SSH_KEY_PATH", ""),
		GitopsUserName:        getEnv("GITOPS_USER_NAME", "smithd"),
		GitopsUserEmail:       getEnv("GITOPS_USER_EMAIL", "smithd@deploysmith.io"),
		NATSUrl:               getEnv("NATS_URL", ""),
		FluxWebhookURL:        getEnv("FLUX_WEBHOOK_URL", ""),
		FluxWebhookToken:      getEnv("FLUX_WEBHOOK_TOKEN", ""),
		SlackWebhookURL:       getEnv("SLACK_WEBHOOK_URL", ""),
		VAPIDPublicKey:        getEnv("VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey:       getEnv("VAPID_PRIVATE_KEY", ""),
		VAPIDSubject:          getEnv("VAPID_SUBJECT", ""),
		FluxNamespace:         getEnv("FLUX_NAMESPACE", "flux-system"),
		FluxKustomizationName: getEnv("FLUX_KUSTOMIZATION_NAME", ""),
		UIUserHeader:          getEnv("UI_USER_HEADER", ""),
		UIAllowedUsers:        splitList(getEnv("UI_ALLOWED_USERS", "")),
		UITrustedOrigins:      splitList(getEnv("UI_TRUSTED_ORIGINS", "")),
		UIListenAddr:          getEnv("UI_LISTEN_ADDR", ""),
		UIPublicURL:           getEnv("UI_PUBLIC_URL", ""),
		GitHubClientID:        getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:    getEnv("GITHUB_CLIENT_SECRET", ""),
		GitHubURL:             strings.TrimRight(getEnv("GITHUB_URL", "https://github.com"), "/"),
		GitHubAPIURL:          strings.TrimRight(getEnv("GITHUB_API_URL", ""), "/"),
		GitHubAllowedOrg:      getEnv("GITHUB_ALLOWED_ORG", ""),
		GitHubAllowedTeam:     getEnv("GITHUB_ALLOWED_TEAM", ""),
		GitHubAllowedUsers:    splitList(getEnv("GITHUB_ALLOWED_USERS", "")),
	}

	ttl, err := time.ParseDuration(getEnv("SESSION_TTL", "12h"))
	if err != nil || ttl <= 0 {
		return nil, fmt.Errorf("SESSION_TTL must be a positive duration such as 12h")
	}
	cfg.SessionTTL = ttl

	if cfg.GitHubAPIURL == "" {
		if cfg.GitHubURL == "https://github.com" {
			cfg.GitHubAPIURL = "https://api.github.com"
		} else {
			cfg.GitHubAPIURL = cfg.GitHubURL + "/api/v3"
		}
	}

	if err := cfg.validateGitHubLogin(); err != nil {
		return nil, err
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

// validateGitHubLogin checks the GitHub login settings. Anyone has a GitHub
// account, so login without a restriction to an org or users is refused.
func (c *Config) validateGitHubLogin() error {
	if !c.GitHubLoginEnabled() {
		return nil
	}
	if c.GitHubClientSecret == "" {
		return fmt.Errorf("GITHUB_CLIENT_SECRET is required with GITHUB_CLIENT_ID")
	}
	if c.GitHubAllowedOrg == "" && len(c.GitHubAllowedUsers) == 0 {
		return fmt.Errorf("GITHUB_ALLOWED_ORG or GITHUB_ALLOWED_USERS is required with GITHUB_CLIENT_ID")
	}
	if c.GitHubAllowedTeam != "" && c.GitHubAllowedOrg == "" {
		return fmt.Errorf("GITHUB_ALLOWED_TEAM needs GITHUB_ALLOWED_ORG")
	}
	if c.UIUserHeader != "" {
		return fmt.Errorf("UI_USER_HEADER and GITHUB_CLIENT_ID can't both be set: the web UI either trusts a proxy or signs people in with GitHub")
	}
	return nil
}

// splitList splits a comma-separated list, dropping empty entries
func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
