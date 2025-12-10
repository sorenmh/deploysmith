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

Example:
  forge publish --app my-app --version v1.0.0`,
	RunE: runPublish,
}

func init() {
	rootCmd.AddCommand(publishCmd)

	publishCmd.Flags().StringVar(&publishApp, "app", "", "Application name (required)")
	publishCmd.Flags().StringVar(&publishVersion, "version", "", "Version identifier (required)")
	publishCmd.Flags().BoolVar(&publishNoValidate, "no-validate", false, "Skip manifest validation")

	publishCmd.MarkFlagRequired("app")
	publishCmd.MarkFlagRequired("version")
}

func runPublish(cmd *cobra.Command, args []string) error {
	// Validate required config
	if err := ValidateConfig(); err != nil {
		return err
	}

	fmt.Printf("Publishing version %s...\n", publishVersion)

	// Call smithd API
	c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
	resp, err := c.PublishVersion(publishApp, publishVersion, publishNoValidate)
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

	fmt.Printf("\nVersion %s is now live\n", publishVersion)

	// Clean up .forge directory
	if err := os.RemoveAll(".forge"); err != nil {
		// Non-fatal, just warn
		fmt.Fprintf(os.Stderr, "Warning: failed to clean up .forge directory: %v\n", err)
	}

	return nil
}
