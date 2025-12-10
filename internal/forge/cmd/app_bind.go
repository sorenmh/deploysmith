package cmd

import (
	"fmt"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/spf13/cobra"
)

var appBindName string

var appBindCmd = &cobra.Command{
	Use:   "app-bind",
	Short: "Bind this repository to an application",
	Long: `Bind this repository to an application by creating a .deploysmith/app.yaml config file.
This allows other forge commands to work without specifying --app.

Example:
  forge app-bind --app viino-api`,
	RunE: runAppBind,
}

func init() {
	rootCmd.AddCommand(appBindCmd)
	appBindCmd.Flags().StringVar(&appBindName, "app", "", "Application name (required)")
	appBindCmd.MarkFlagRequired("app")
}

func runAppBind(cmd *cobra.Command, args []string) error {
	// Validate required config
	if err := ValidateConfig(); err != nil {
		return err
	}

	// Look up app ID by name
	c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
	appID, err := c.GetAppIDByName(appBindName)
	if err != nil {
		return fmt.Errorf("failed to find app '%s': %w", appBindName, err)
	}

	// Save config file
	if err := SaveAppConfig(appID, appBindName); err != nil {
		return fmt.Errorf("failed to save app config: %w", err)
	}

	fmt.Printf("Repository bound to application '%s' (ID: %s)\n", appBindName, appID)
	fmt.Println("Config saved to .deploysmith/app.yaml")
	fmt.Println("You can now run forge commands without specifying --app")

	return nil
}