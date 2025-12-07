package cmd

import (
	"fmt"
	"strings"

	"github.com/deploysmith/deploysmith/internal/smithctl/client"
	"github.com/deploysmith/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage versions",
	Long:  `List and view application versions.`,
}

var versionListCmd = &cobra.Command{
	Use:   "list [app-name]",
	Short: "List versions for an application",
	Long: `List all versions for an application.

Example:
  smithctl version list my-api-service
  smithctl version list my-api-service --status published
  smithctl version list my-api-service --limit 10`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appName := args[0]
		status, _ := cmd.Flags().GetString("status")
		limit, _ := cmd.Flags().GetInt("limit")

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// List versions
		resp, err := c.ListVersions(appName, status, limit, 0)
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
	Use:   "show [app-name] [version-id]",
	Short: "Show version details",
	Long:  `Show details for a specific version including manifest files and deployments.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appName := args[0]
		versionID := args[1]

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Get version
		ver, err := c.GetVersion(appName, versionID)
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
			fmt.Printf("  Build:    #%d\n", *ver.BuildNumber)
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
	versionListCmd.Flags().String("status", "", "Filter by status (draft, published)")
	versionListCmd.Flags().Int("limit", 20, "Maximum number of results")
}
