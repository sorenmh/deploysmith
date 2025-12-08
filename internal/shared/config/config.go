package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	cfgFile      string
	smithdURL    string
	smithdAPIKey string
)

// InitConfig initializes the shared configuration system
func InitConfig() {
	cobra.OnInitialize(loadConfig)
}

// AddFlags adds common configuration flags to a cobra command
func AddFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.deploysmith/config.yaml)")
	cmd.PersistentFlags().StringVar(&smithdURL, "url", "", "smithd API endpoint")
	cmd.PersistentFlags().StringVar(&smithdAPIKey, "api-key", "", "smithd API key")

	// Bind flags to viper
	viper.BindPFlag("url", cmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("apiKey", cmd.PersistentFlags().Lookup("api-key"))
}

// loadConfig loads configuration from file and environment
func loadConfig() {
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

		// Search for config in ~/.deploysmith directory
		configPath := filepath.Join(home, ".deploysmith")
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

// ConfigureRequest represents configuration input
type ConfigureRequest struct {
	URL    string
	APIKey string
}

// ConfigureInteractive runs interactive configuration
func ConfigureInteractive(currentURL, currentAPIKey string) (*ConfigureRequest, error) {
	reader := bufio.NewReader(os.Stdin)

	// Get URL
	fmt.Printf("smithd URL")
	if currentURL != "" {
		fmt.Printf(" [%s]", currentURL)
	}
	fmt.Print(": ")

	urlInput, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	urlInput = strings.TrimSpace(urlInput)
	if urlInput == "" && currentURL != "" {
		urlInput = currentURL
	}

	// Get API Key
	fmt.Printf("smithd API Key")
	if currentAPIKey != "" {
		fmt.Printf(" [hidden]")
	}
	fmt.Print(": ")

	// Read password securely
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return nil, fmt.Errorf("failed to read API key: %w", err)
	}
	fmt.Println() // Add newline after password input

	apiKeyInput := strings.TrimSpace(string(bytePassword))
	if apiKeyInput == "" && currentAPIKey != "" {
		apiKeyInput = currentAPIKey
	}

	// Validate required fields
	if urlInput == "" {
		return nil, fmt.Errorf("URL is required")
	}
	if apiKeyInput == "" {
		return nil, fmt.Errorf("API key is required")
	}

	return &ConfigureRequest{
		URL:    urlInput,
		APIKey: apiKeyInput,
	}, nil
}

// SaveConfig saves configuration to the default config file
func SaveConfig(req ConfigureRequest) error {
	// Get config directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(home, ".deploysmith")
	configFile := filepath.Join(configDir, "config.yaml")

	// Create config directory
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Set values in viper
	viper.Set("url", req.URL)
	viper.Set("apiKey", req.APIKey)

	// Write config file
	if err := viper.WriteConfigAs(configFile); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Configuration saved to %s\n", configFile)
	fmt.Println("\nConfiguration:")
	fmt.Printf("  URL: %s\n", req.URL)
	fmt.Printf("  API Key: %s...%s\n", req.APIKey[:8], req.APIKey[len(req.APIKey)-4:])

	return nil
}