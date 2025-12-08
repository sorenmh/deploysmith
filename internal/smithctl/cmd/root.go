package cmd

import (
	"github.com/deploysmith/deploysmith/internal/shared/config"
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
    SMITHD_API_KEY      - smithd API authentication key (required)

  Config file (~/.deploysmith/config.yaml):
    url: https://smithd.example.com
    apiKey: sk_live_abc123

  CLI flags override environment variables and config file.

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

// ValidateConfig validates that required configuration is present
func ValidateConfig() error {
	return config.ValidateConfig()
}
