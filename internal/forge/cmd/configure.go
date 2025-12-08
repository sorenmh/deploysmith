package cmd

import (
	"github.com/deploysmith/deploysmith/internal/shared/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure forge settings interactively",
	Long: `Configure forge settings interactively or via command line flags.

This command will prompt you for the required settings if they are not provided
as flags. The settings are saved to ~/.deploysmith/config.yaml by default.

This configuration is shared with smithctl for convenience.

Example:
  forge configure
  forge configure --url https://smithd.example.com --api-key sk_live_abc123`,
	RunE: runConfigure,
}

var (
	configureURL    string
	configureAPIKey string
)

func init() {
	rootCmd.AddCommand(configureCmd)

	configureCmd.Flags().StringVar(&configureURL, "url", "", "smithd API endpoint")
	configureCmd.Flags().StringVar(&configureAPIKey, "api-key", "", "smithd API key")
}

func runConfigure(cmd *cobra.Command, args []string) error {
	// Get current values from config
	currentURL := viper.GetString("url")
	currentAPIKey := viper.GetString("apiKey")

	var req *config.ConfigureRequest
	var err error

	// If flags provided, use them directly
	if configureURL != "" && configureAPIKey != "" {
		req = &config.ConfigureRequest{
			URL:    configureURL,
			APIKey: configureAPIKey,
		}
	} else {
		// Run interactive configuration
		req, err = config.ConfigureInteractive(currentURL, currentAPIKey)
		if err != nil {
			return err
		}

		// Override with any provided flags
		if configureURL != "" {
			req.URL = configureURL
		}
		if configureAPIKey != "" {
			req.APIKey = configureAPIKey
		}
	}

	return config.SaveConfig(*req)
}