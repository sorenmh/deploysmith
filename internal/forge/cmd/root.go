package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	smithdURL    string
	smithdAPIKey string
)

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "DeploySmith CI/CD tool for packaging and publishing versions",
	Long: `forge is a command-line tool for CI/CD pipelines that packages
applications and publishes them to smithd for deployment.

Configuration:
  SMITHD_URL          - smithd API endpoint (required)
  SMITHD_API_KEY      - smithd API authentication key (required)

Example usage:
  forge init --app my-app --version v1.0.0
  forge upload manifests/
  forge publish --app my-app --version v1.0.0`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&smithdURL, "smithd-url", os.Getenv("SMITHD_URL"), "smithd API endpoint")
	rootCmd.PersistentFlags().StringVar(&smithdAPIKey, "smithd-api-key", os.Getenv("SMITHD_API_KEY"), "smithd API key")
}
