package cmd

import (
	"fmt"

	"github.com/sorenmh/deploysmith/internal/shared/config"
	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
)

var rootCmd = &cobra.Command{
	Use:   "smithctl",
	Short: "DeploySmith CLI for managing deployments",
	Long: `smithctl is a command-line tool for developers to interact with DeploySmith.

It allows you to:
  - Register and manage applications
  - List and view versions
  - Deploy specific versions to environments
  - Manage auto-deployment policies
  - View deployment history

Configuration:
  Environment variables:
    SMITHD_URL          - smithd API endpoint (required)
    SMITHD_API_KEY      - smithd API authentication key (unless logged in)

  Config file (~/.deploysmith/config.yaml):
    url: https://smithd.example.com
    apiKey: sk_live_abc123

  CLI flags override environment variables and config file.

  Or log in with GitHub, if smithd has it configured: smithctl login. The
  session (~/.deploysmith/sessions.json) is then used instead of the API key.

Example usage:
  smithctl app register my-api-service
  smithctl version list my-api-service
  smithctl deploy my-api-service v1.0.0 --env staging`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	config.InitConfig()
	config.AddFlags(rootCmd)

	// Add smithctl-specific flags
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "output format (table, json, yaml)")
}

// GetSmithdURL returns the configured smithd URL
func GetSmithdURL() string {
	return config.GetSmithdURL()
}

// GetSmithdAPIKey returns the configured smithd API key
func GetSmithdAPIKey() string {
	return config.GetSmithdAPIKey()
}

// GetOutputFormat returns the output format
func GetOutputFormat() string {
	return outputFormat
}

// ValidateConfig validates that required configuration is present: the
// URL, and a session from `smithctl login` or an API key
func ValidateConfig() error {
	if err := config.ValidateURL(); err != nil {
		return err
	}
	if config.GetSessionToken() != "" {
		return nil
	}
	if err := config.ValidateConfig(); err != nil {
		return fmt.Errorf("%w, or log in with `smithctl login`", err)
	}
	return nil
}

// newClient returns a smithd client that authenticates as the logged-in
// user if there is a session, else with the API key
func newClient() *client.Client {
	return client.NewClient(GetSmithdURL(), GetSmithdAPIKey()).WithSessionToken(config.GetSessionToken())
}
