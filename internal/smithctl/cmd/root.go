package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile      string
	smithdURL    string
	smithdAPIKey string
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

  Config file (~/.smithctl/config.yaml):
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
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.smithctl/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&smithdURL, "url", "", "smithd API endpoint")
	rootCmd.PersistentFlags().StringVar(&smithdAPIKey, "api-key", "", "smithd API key")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "output format (table, json, yaml)")

	// Bind flags to viper
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("apiKey", rootCmd.PersistentFlags().Lookup("api-key"))
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// Search for config in ~/.smithctl directory
		configPath := filepath.Join(home, ".smithctl")
		viper.AddConfigPath(configPath)
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	// Read environment variables
	viper.SetEnvPrefix("SMITHD")
	viper.AutomaticEnv()

	// If a config file is found, read it
	if err := viper.ReadInConfig(); err == nil {
		// Config file found and successfully parsed
	}
}

// GetSmithdURL returns the configured smithd URL
func GetSmithdURL() string {
	if smithdURL != "" {
		return smithdURL
	}
	return viper.GetString("url")
}

// GetSmithdAPIKey returns the configured smithd API key
func GetSmithdAPIKey() string {
	if smithdAPIKey != "" {
		return smithdAPIKey
	}
	return viper.GetString("apiKey")
}

// GetOutputFormat returns the output format
func GetOutputFormat() string {
	return outputFormat
}

// ValidateConfig validates that required configuration is present
func ValidateConfig() error {
	if GetSmithdURL() == "" {
		return fmt.Errorf("smithd URL is required (set SMITHD_URL env var, --url flag, or url in config file)")
	}
	if GetSmithdAPIKey() == "" {
		return fmt.Errorf("smithd API key is required (set SMITHD_API_KEY env var, --api-key flag, or apiKey in config file)")
	}
	return nil
}
