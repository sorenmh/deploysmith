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
	Use:   "deploy [app-name-or-id] [version-id]",
	Short: "Deploy a version to an environment",
	Long: `Deploy a specific version to an environment.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.
If no version is specified, shows a list of the 10 newest versions to select from.

Examples:
  smithctl deploy --env staging                     # Shows version list for selection
  smithctl deploy v1.0.0 --env staging              # Uses app from binding
  smithctl deploy my-api-service v1.0.0 --env staging
  smithctl deploy --app my-api-service v1.0.0 --env production --confirm`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Parse arguments - could be [], [version], [app], or [app, version]
		var appIdentifier, versionID string
		if len(args) == 0 {
			// No arguments provided, get app from flag or binding
			appIdentifier, _ = cmd.Flags().GetString("app")
			// versionID will be selected interactively
		} else if len(args) == 1 {
			// Could be either version only or app only
			// Check if there's an app flag to disambiguate
			appFlag, _ := cmd.Flags().GetString("app")
			if appFlag != "" {
				// App specified via flag, so arg is version
				versionID = args[0]
				appIdentifier = appFlag
			} else {
				// Try to determine if arg looks like version or app
				// For now, assume if it starts with 'v' or has dots, it's a version
				arg := args[0]
				if strings.HasPrefix(arg, "v") || strings.Contains(arg, ".") {
					// Looks like a version
					versionID = arg
					// App will be resolved from binding
				} else {
					// Assume it's an app identifier
					appIdentifier = arg
					// versionID will be selected interactively
				}
			}
		} else {
			// Both app and version provided
			appIdentifier = args[0]
			versionID = args[1]
		}

		// Resolve app ID
		appID, appName, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		environment, _ := cmd.Flags().GetString("env")
		skipConfirm, _ := cmd.Flags().GetBool("confirm")

		if environment == "" {
			return fmt.Errorf("--env is required")
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// If no version provided, show interactive version selection
		if versionID == "" {
			// List recent versions
			resp, err := c.ListVersions(appID, "published", 10, 0)
			if err != nil {
				return err
			}

			if len(resp.Versions) == 0 {
				return fmt.Errorf("no published versions found for app: %s", appName)
			}

			// Show recent versions
			fmt.Printf("Recent versions for %s:\n", appName)
			for i, ver := range resp.Versions {
				deployInfo := ""
				if ver.PublishedAt != nil {
					deployInfo = fmt.Sprintf(" (published %s)", output.FormatTimeAgo(*ver.PublishedAt))
				}
				fmt.Printf("  %d. %s%s\n", i+1, ver.Version, deployInfo)
			}

			// Prompt user to select version
			fmt.Println()
			fmt.Printf("Select version to deploy (1-%d): ", len(resp.Versions))

			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(response)

			var selection int
			_, err = fmt.Sscanf(response, "%d", &selection)
			if err != nil || selection < 1 || selection > len(resp.Versions) {
				return fmt.Errorf("invalid selection")
			}

			selectedVersion := resp.Versions[selection-1]
			versionID = selectedVersion.Version
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

		// Deploy version
		resp, err := c.DeployVersion(appID, versionID, environment)
		if err != nil {
			return err
		}

		// Print success message
		output.Success("Deployment initiated")
		fmt.Printf("  Deployment ID: %s\n", resp.DeploymentID)
		fmt.Printf("  Version:       %s\n", resp.VersionID)
		fmt.Printf("  Environment:   %s\n", resp.Environment)
		if resp.GitopsCommitSHA != "" {
			fmt.Printf("  GitOps Commit: %s\n", resp.GitopsCommitSHA)
		}

		return nil
	},
}

var rollbackCmd = &cobra.Command{
	Use:   "rollback [app-name-or-id]",
	Short: "Rollback to a previous version",
	Long: `Rollback to a previous version in an environment.

This command shows the current version and recent versions, allowing you to select
which version to rollback to.

Examples:
  smithctl rollback --env staging                   # Uses app from binding
  smithctl rollback my-api-service --env staging
  smithctl rollback --app my-api-service --env staging`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Get app identifier from args or flag
		var appIdentifier string
		if len(args) > 0 {
			appIdentifier = args[0]
		} else {
			appIdentifier, _ = cmd.Flags().GetString("app")
		}

		// Resolve app ID
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		environment, _ := cmd.Flags().GetString("env")

		if environment == "" {
			return fmt.Errorf("--env is required")
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Get application to find current version
		app, err := c.GetApplication(appID)
		if err != nil {
			return err
		}

		currentDeployment, exists := app.CurrentVersions[environment]
		if !exists {
			return fmt.Errorf("no deployment found for environment: %s", environment)
		}

		fmt.Printf("Current version in %s: %s\n\n", environment, currentDeployment.VersionID)

		// List recent versions
		resp, err := c.ListVersions(appID, "published", 10, 0)
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
		deployResp, err := c.DeployVersion(appID, selectedVersion.Version, environment)
		if err != nil {
			return err
		}

		output.Success("Deployment initiated")
		fmt.Printf("  Deployment ID: %s\n", deployResp.DeploymentID)
		fmt.Printf("  Version:       %s\n", deployResp.VersionID)
		fmt.Printf("  Environment:   %s\n", deployResp.Environment)
		if deployResp.GitopsCommitSHA != "" {
			fmt.Printf("  GitOps Commit: %s\n", deployResp.GitopsCommitSHA)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(rollbackCmd)

	// Flags for deploy
	deployCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
	deployCmd.Flags().String("env", "", "Target environment (required)")
	deployCmd.Flags().Bool("confirm", false, "Skip confirmation prompt")

	// Flags for rollback
	rollbackCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
	rollbackCmd.Flags().String("env", "", "Target environment (required)")
}
