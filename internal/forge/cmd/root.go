package cmd

import (
	"github.com/deploysmith/deploysmith/internal/shared/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "DeploySmith CI/CD tool for packaging and publishing versions",
	Long: `forge is a command-line tool for CI/CD pipelines that packages
applications and publishes them to smithd for deployment.

Configuration:
  Environment variables:
    SMITHD_URL          - smithd API endpoint (required)
    SMITHD_API_KEY      - smithd API authentication key (required)

  Config file (~/.deploysmith/config.yaml):
    url: https://smithd.example.com
    apiKey: sk_live_abc123

  CLI flags override environment variables and config file.

Example usage:
  forge configure
  forge init --app my-app --version v1.0.0
  forge upload manifests/
  forge publish --app my-app --version v1.0.0`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	config.InitConfig()
	config.AddFlags(rootCmd)
}

// GetSmithdURL returns the configured smithd URL
func GetSmithdURL() string {
	return config.GetSmithdURL()
}

// GetSmithdAPIKey returns the configured smithd API key
func GetSmithdAPIKey() string {
	return config.GetSmithdAPIKey()
}

// ValidateConfig validates that required configuration is present
func ValidateConfig() error {
	return config.ValidateConfig()
}
