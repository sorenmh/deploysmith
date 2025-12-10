package cmd

import (
	"fmt"
	"os"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/spf13/cobra"
)

var (
	publishApp        string
	publishVersion    string
	publishNoValidate bool
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a version to make it deployable",
	Long: `Publish a draft version to make it available for deployment.

This moves the manifests from draft to published state and triggers
any matching auto-deploy policies.

Examples:
  forge publish                                      # Uses app and version from init
  forge publish --version v1.0.0                    # Uses app from binding or init
  forge publish --app my-app --version v1.0.0       # Explicit app and version`,
	RunE: runPublish,
}

func init() {
	rootCmd.AddCommand(publishCmd)

	publishCmd.Flags().StringVar(&publishApp, "app", "", "Application name (optional if app is bound)")
	publishCmd.Flags().StringVar(&publishVersion, "version", "", "Version identifier (optional if init was run)")
	publishCmd.Flags().BoolVar(&publishNoValidate, "no-validate", false, "Skip manifest validation")
}

func runPublish(cmd *cobra.Command, args []string) error {
	// Validate required config
	if err := ValidateConfig(); err != nil {
		return err
	}

	// Resolve app ID and version from flags or files
	appID, appName, version, err := ResolveVersion(publishVersion)
	if err != nil {
		return err
	}

	fmt.Printf("Publishing version %s for app %s (ID: %s)...\n", version, appName, appID)

	// Call smithd API
	c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
	resp, err := c.PublishVersion(appID, version, publishNoValidate)
	if err != nil {
		return fmt.Errorf("failed to publish version: %w", err)
	}

	fmt.Println("  ✓ Version published")

	// Show auto-deployment status
	if len(resp.AutoDeployments) > 0 {
		for _, env := range resp.AutoDeployments {
			fmt.Printf("  ✓ Auto-deployment triggered for %s\n", env)
		}
	}

	fmt.Printf("\nVersion %s is now live\n", version)

	// Clean up .forge directory
	if err := os.RemoveAll(".forge"); err != nil {
		// Non-fatal, just warn
		fmt.Fprintf(os.Stderr, "Warning: failed to clean up .forge directory: %v\n", err)
	}

	return nil
}
