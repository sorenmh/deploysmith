package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [app-name] [version-id]",
	Short: "Deploy a version to an environment",
	Long: `Deploy a specific version to an environment.

Example:
  smithctl deploy my-api-service v1.0.0 --env staging
  smithctl deploy my-api-service 42540c4-123 --env production --confirm`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appName := args[0]
		versionID := args[1]
		environment, _ := cmd.Flags().GetString("env")
		skipConfirm, _ := cmd.Flags().GetBool("confirm")

		if environment == "" {
			return fmt.Errorf("--env is required")
		}

		// Show confirmation prompt unless --confirm is used
		if !skipConfirm {
			fmt.Println("You are about to deploy:")
			fmt.Println()
			fmt.Printf("  App:         %s\n", appName)
			fmt.Printf("  Version:     %s\n", versionID)
			fmt.Printf("  Environment: %s\n", environment)
			fmt.Println()
			fmt.Println("This will update the gitops repository and Flux will apply the changes.")
			fmt.Println()
			fmt.Print("Continue? (y/n): ")

			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))

			if response != "y" && response != "yes" {
				output.Info("Deployment cancelled")
				os.Exit(2)
			}
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Deploy version
		resp, err := c.DeployVersion(appName, versionID, environment)
		if err != nil {
			return err
		}

		// Print success message
		output.Success("Deployment initiated")
		fmt.Printf("  Deployment ID: %s\n", resp.DeploymentID)

		return nil
	},
}

var rollbackCmd = &cobra.Command{
	Use:   "rollback [app-name]",
	Short: "Rollback to a previous version",
	Long: `Rollback to a previous version in an environment.

This command shows the current version and recent versions, allowing you to select
which version to rollback to.

Example:
  smithctl rollback my-api-service --env staging`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appName := args[0]
		environment, _ := cmd.Flags().GetString("env")

		if environment == "" {
			return fmt.Errorf("--env is required")
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Get application to find current version
		app, err := c.GetApplication(appName)
		if err != nil {
			return err
		}

		currentDeployment, exists := app.CurrentVersions[environment]
		if !exists {
			return fmt.Errorf("no deployment found for environment: %s", environment)
		}

		fmt.Printf("Current version in %s: %s\n\n", environment, currentDeployment.VersionID)

		// List recent versions
		resp, err := c.ListVersions(appName, "published", 10, 0)
		if err != nil {
			return err
		}

		if len(resp.Versions) == 0 {
			return fmt.Errorf("no published versions found")
		}

		// Filter out current version and show recent versions
		fmt.Println("Recent versions:")
		availableVersions := []client.Version{}
		for i, ver := range resp.Versions {
			if ver.Version != currentDeployment.VersionID {
				availableVersions = append(availableVersions, ver)
				deployInfo := ""
				if ver.PublishedAt != nil {
					deployInfo = fmt.Sprintf(" (published %s)", output.FormatTimeAgo(*ver.PublishedAt))
				}
				fmt.Printf("  %d. %s%s\n", len(availableVersions), ver.Version, deployInfo)
			}
			// Limit to showing 5 options
			if len(availableVersions) >= 5 || i >= 9 {
				break
			}
		}

		if len(availableVersions) == 0 {
			return fmt.Errorf("no other versions available for rollback")
		}

		// Prompt user to select version
		fmt.Println()
		fmt.Printf("Select version to rollback to (1-%d): ", len(availableVersions))

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		var selection int
		_, err = fmt.Sscanf(response, "%d", &selection)
		if err != nil || selection < 1 || selection > len(availableVersions) {
			return fmt.Errorf("invalid selection")
		}

		selectedVersion := availableVersions[selection-1]

		// Confirm rollback
		fmt.Println()
		fmt.Printf("✓ Rolling back to version %s...\n", selectedVersion.Version)

		// Deploy the selected version
		deployResp, err := c.DeployVersion(appName, selectedVersion.Version, environment)
		if err != nil {
			return err
		}

		output.Success("Deployment initiated")
		fmt.Printf("  Deployment ID: %s\n", deployResp.DeploymentID)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(rollbackCmd)

	// Flags for deploy
	deployCmd.Flags().String("env", "", "Target environment (required)")
	deployCmd.Flags().Bool("confirm", false, "Skip confirmation prompt")

	// Flags for rollback
	rollbackCmd.Flags().String("env", "", "Target environment (required)")
}
