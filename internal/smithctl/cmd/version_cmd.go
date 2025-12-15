package cmd

import (
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage versions",
	Long:  `List and view application versions.`,
}

var versionListCmd = &cobra.Command{
	Use:   "list [app-name-or-id]",
	Short: "List versions for an application",
	Long: `List all versions for an application.

You can specify the app by name or ID as an argument, or omit it if you've run 'forge app-bind' in this directory.

Examples:
  smithctl version list                              # Uses app from binding
  smithctl version list my-api-service               # Uses app name
  smithctl version list --app my-api-service         # Uses --app flag
  smithctl version list --status published --limit 10`,
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

		// Resolve app ID using new resolver
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		status, _ := cmd.Flags().GetString("status")
		limit, _ := cmd.Flags().GetInt("limit")

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// List versions (use appID since client now resolves internally)
		resp, err := c.ListVersions(appID, status, limit, 0)
		if err != nil {
			return err
		}

		// Check if there are no versions
		if len(resp.Versions) == 0 {
			output.Info("No versions found")
			return nil
		}

		// Print output based on format
		format := output.Format(GetOutputFormat())
		return output.Print(format, resp, func() {
			headers := []string{"VERSION", "STATUS", "BRANCH", "DEPLOYED TO", "CREATED"}
			rows := make([][]string, 0, len(resp.Versions))

			for _, ver := range resp.Versions {
				branch := "-"
				if ver.GitBranch != nil {
					branch = *ver.GitBranch
				}

				deployedTo := "-"
				if len(ver.Deployments) > 0 {
					deployedTo = strings.Join(ver.Deployments, ", ")
				}

				rows = append(rows, []string{
					ver.Version,
					ver.Status,
					branch,
					deployedTo,
					output.FormatTime(ver.CreatedAt),
				})
			}

			output.PrintTable(headers, rows)
		})
	},
}

var versionShowCmd = &cobra.Command{
	Use:   "show [app-name-or-id] [version-id]",
	Short: "Show version details",
	Long: `Show details for a specific version including manifest files and deployments.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.

Examples:
  smithctl version show v1.0.0                      # Uses app from binding
  smithctl version show my-api-service v1.0.0       # Uses app name
  smithctl version show --app my-api-service v1.0.0 # Uses --app flag`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Parse arguments - could be [version] or [app, version]
		var appIdentifier, versionID string
		if len(args) == 1 {
			// Only version provided, get app from flag or binding
			versionID = args[0]
			appIdentifier, _ = cmd.Flags().GetString("app")
		} else {
			// Both app and version provided
			appIdentifier = args[0]
			versionID = args[1]
		}

		// Resolve app ID
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Get version
		ver, err := c.GetVersion(appID, versionID)
		if err != nil {
			return err
		}

		// Print output based on format
		format := output.Format(GetOutputFormat())
		if format == output.FormatJSON || format == output.FormatYAML {
			return output.Print(format, ver, nil)
		}

		// Table format
		fmt.Printf("Version: %s\n\n", ver.Version)
		fmt.Printf("  Status:   %s\n", ver.Status)

		if ver.GitSHA != nil {
			fmt.Printf("  Git SHA:  %s\n", *ver.GitSHA)
		}
		if ver.GitBranch != nil {
			fmt.Printf("  Branch:   %s\n", *ver.GitBranch)
		}
		if ver.GitCommitter != nil {
			fmt.Printf("  Committer: %s\n", *ver.GitCommitter)
		}
		if ver.BuildNumber != nil {
			fmt.Printf("  Build:    #%s\n", *ver.BuildNumber)
		}

		fmt.Printf("  Created:  %s\n", output.FormatTime(ver.CreatedAt))
		if ver.PublishedAt != nil {
			fmt.Printf("  Published: %s\n", output.FormatTime(*ver.PublishedAt))
		}

		if len(ver.Files) > 0 {
			fmt.Println("\nManifest Files:")
			for _, file := range ver.Files {
				fmt.Printf("  - %s\n", file)
			}
		}

		if len(ver.Deployments) > 0 {
			fmt.Println("\nDeployed To:")
			for _, env := range ver.Deployments {
				fmt.Printf("  %s\n", env)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.AddCommand(versionListCmd)
	versionCmd.AddCommand(versionShowCmd)

	// Flags for version list
	versionListCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
	versionListCmd.Flags().String("status", "", "Filter by status (draft, published)")
	versionListCmd.Flags().Int("limit", 20, "Maximum number of results")

	// Flags for version show
	versionShowCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
}
