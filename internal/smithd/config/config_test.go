package config

import (
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Setenv("API_KEYS", "key")
	t.Setenv("S3_BUCKET", "bucket")
	t.Setenv("GITOPS_REPO", "git@example.com:gitops.git")
	// GitHub Actions sets this one
	t.Setenv("GITHUB_API_URL", "")
}

func TestLoadGitHubLogin(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		setRequired(t)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GitHubLoginEnabled() || cfg.SessionTTL != 12*time.Hour || cfg.GitHubAPIURL != "https://api.github.com" {
			t.Errorf("unexpected defaults: %+v", cfg)
		}
	})

	t.Run("configured", func(t *testing.T) {
		setRequired(t)
		t.Setenv("GITHUB_CLIENT_ID", "Iv1.abc")
		t.Setenv("GITHUB_CLIENT_SECRET", "secret")
		t.Setenv("GITHUB_URL", "https://github.example.com/")
		t.Setenv("GITHUB_ALLOWED_ORG", "acme")
		t.Setenv("GITHUB_ALLOWED_TEAM", "deployers")
		t.Setenv("GITHUB_ALLOWED_USERS", "octocat, hubot")
		t.Setenv("SESSION_TTL", "2h")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.GitHubLoginEnabled() || cfg.SessionTTL != 2*time.Hour || len(cfg.GitHubAllowedUsers) != 2 {
			t.Errorf("unexpected config: %+v", cfg)
		}
		if cfg.GitHubAPIURL != "https://github.example.com/api/v3" {
			t.Errorf("API URL = %q, want the GitHub Enterprise Server API", cfg.GitHubAPIURL)
		}
	})

	errors := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no secret", map[string]string{"GITHUB_CLIENT_ID": "id", "GITHUB_ALLOWED_ORG": "acme"}, "GITHUB_CLIENT_SECRET"},
		{"nobody allowed", map[string]string{"GITHUB_CLIENT_ID": "id", "GITHUB_CLIENT_SECRET": "s"}, "GITHUB_ALLOWED_ORG or GITHUB_ALLOWED_USERS"},
		{"team without org", map[string]string{"GITHUB_CLIENT_ID": "id", "GITHUB_CLIENT_SECRET": "s", "GITHUB_ALLOWED_USERS": "a", "GITHUB_ALLOWED_TEAM": "t"}, "GITHUB_ALLOWED_TEAM"},
		{"with proxy header", map[string]string{"GITHUB_CLIENT_ID": "id", "GITHUB_CLIENT_SECRET": "s", "GITHUB_ALLOWED_ORG": "acme", "UI_USER_HEADER": "X-User"}, "UI_USER_HEADER"},
		{"bad session TTL", map[string]string{"SESSION_TTL": "forever"}, "SESSION_TTL"},
	}
	for _, tt := range errors {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one about %s", err, tt.want)
			}
		})
	}
}
