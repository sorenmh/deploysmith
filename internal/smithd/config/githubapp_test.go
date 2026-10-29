package config

import "testing"

func TestGitHubAppConfig(t *testing.T) {
	t.Setenv("API_KEYS", "key")
	t.Setenv("S3_BUCKET", "versions")
	t.Setenv("GITOPS_REPO", "git@example.com:gitops.git")

	t.Run("off by default", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GitHubApp.Enabled() {
			t.Error("GitHub App reporting should be off")
		}
		if cfg.GitHubApp.APIURL != "https://api.github.com" {
			t.Errorf("APIURL = %q", cfg.GitHubApp.APIURL)
		}
	})

	t.Run("id and key", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_APP_PRIVATE_KEY", `-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----`)
		t.Setenv("GITHUB_API_URL", "https://github.example.com/api/v3/")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.GitHubApp.Enabled() || cfg.GitHubApp.ID != "12345" {
			t.Errorf("unexpected config: %+v", cfg.GitHubApp)
		}
		if want := "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----"; cfg.GitHubApp.PrivateKey != want {
			t.Errorf("escaped newlines not restored: %q", cfg.GitHubApp.PrivateKey)
		}
		if cfg.GitHubApp.APIURL != "https://github.example.com/api/v3" {
			t.Errorf("APIURL = %q", cfg.GitHubApp.APIURL)
		}
	})

	t.Run("id without key", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		if _, err := Load(); err == nil {
			t.Error("expected an error")
		}
	})
}
