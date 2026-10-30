package config

import "testing"

func TestGitHubAppConfig(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		setRequired(t)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GitHubApp.Enabled() {
			t.Error("GitHub App reporting should be off")
		}
	})

	t.Run("id and key", func(t *testing.T) {
		setRequired(t)
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_APP_PRIVATE_KEY", `-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----`)
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
	})

	t.Run("id without key", func(t *testing.T) {
		setRequired(t)
		t.Setenv("GITHUB_APP_ID", "12345")
		if _, err := Load(); err == nil {
			t.Error("expected an error")
		}
	})
}
