package config

import (
	"fmt"
	"strings"
)

// GitHubAppConfig is the GitHub App smithd reports deployments (and later
// Terraform plans) with, as check runs on the commit a version was built
// from. It is the same app as for login with GitHub (GITHUB_CLIENT_ID), which
// only needs its client id and secret; reporting needs the app id and a
// private key. Reporting is off unless both are set, and it talks to the API
// at GitHubAPIURL.
type GitHubAppConfig struct {
	ID         string // the App ID (or its client id)
	PrivateKey string // PEM, as downloaded from the app's settings
}

// Enabled reports whether smithd reports to GitHub
func (c GitHubAppConfig) Enabled() bool {
	return c.ID != "" && c.PrivateKey != ""
}

func loadGitHubApp() (GitHubAppConfig, error) {
	cfg := GitHubAppConfig{
		ID: getEnv("GITHUB_APP_ID", ""),
		// An env var can't always hold newlines; accept them escaped
		PrivateKey: strings.ReplaceAll(getEnv("GITHUB_APP_PRIVATE_KEY", ""), `\n`, "\n"),
	}
	if (cfg.ID == "") != (cfg.PrivateKey == "") {
		return cfg, fmt.Errorf("GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY must be set together")
	}
	return cfg, nil
}
