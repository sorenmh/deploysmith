package cmd

import (
	"fmt"

	"github.com/deploysmith/deploysmith/internal/smithctl/client"
	"github.com/deploysmith/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage applications",
	Long:  `Register, list, and view applications.`,
}

var appRegisterCmd = &cobra.Command{
	Use:   "register [name]",
	Short: "Register a new application",
	Long: `Register a new application with DeploySmith.

The application name can be provided as a positional argument or via the --name flag.

Example:
  smithctl app register my-api-service
  smithctl app register --name my-api-service --gitops-repo https://github.com/org/gitops --gitops-path apps/my-api-service`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Get app name from args or flag
		name, _ := cmd.Flags().GetString("name")
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("application name is required")
		}

		// Get other required flags
		gitopsRepo, _ := cmd.Flags().GetString("gitops-repo")
		gitopsPath, _ := cmd.Flags().GetString("gitops-path")

		if gitopsRepo == "" {
			return fmt.Errorf("--gitops-repo is required")
		}
		if gitopsPath == "" {
			// Default to environments/{environment}/apps/{name}
			gitopsPath = fmt.Sprintf("environments/{environment}/apps/%s", name)
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Register application
		app, err := c.RegisterApplication(client.RegisterApplicationRequest{
			Name:       name,
			GitopsRepo: gitopsRepo,
			GitopsPath: gitopsPath,
		})
		if err != nil {
			return err
		}

		// Print success message
		output.Success("Application registered successfully")
		fmt.Println()
		fmt.Printf("  Name: %s\n", app.Name)
		fmt.Printf("  ID:   %s\n", app.ID)
		fmt.Printf("  Path: %s\n", app.GitopsPath)

		return nil
	},
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all applications",
	Long:  `List all registered applications.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// List applications
		resp, err := c.ListApplications(100, 0)
		if err != nil {
			return err
		}

		// Check if there are no applications
		if len(resp.Apps) == 0 {
			output.Info("No applications found")
			return nil
		}

		// Print output based on format
		format := output.Format(GetOutputFormat())
		return output.Print(format, resp, func() {
			headers := []string{"NAME", "ID", "CREATED"}
			rows := make([][]string, 0, len(resp.Apps))

			for _, app := range resp.Apps {
				rows = append(rows, []string{
					app.Name,
					app.ID,
					output.FormatTime(app.CreatedAt),
				})
			}

			output.PrintTable(headers, rows)
		})
	},
}

var appShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show application details",
	Long:  `Show details for a specific application including current deployments.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appName := args[0]

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Get application
		app, err := c.GetApplication(appName)
		if err != nil {
			return err
		}

		// Print output based on format
		format := output.Format(GetOutputFormat())
		if format == output.FormatJSON || format == output.FormatYAML {
			return output.Print(format, app, nil)
		}

		// Table format
		fmt.Printf("Application: %s\n\n", app.Name)
		fmt.Printf("  ID:      %s\n", app.ID)
		fmt.Printf("  Path:    %s\n", app.GitopsPath)
		fmt.Printf("  Created: %s\n", output.FormatTime(app.CreatedAt))

		if len(app.CurrentVersions) > 0 {
			fmt.Println("\nCurrent Deployments:")
			for env, deployment := range app.CurrentVersions {
				fmt.Printf("  %s: %s (deployed %s)\n",
					env,
					deployment.VersionID,
					output.FormatTimeAgo(deployment.DeployedAt),
				)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appRegisterCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appShowCmd)

	// Flags for app register
	appRegisterCmd.Flags().String("name", "", "Application name")
	appRegisterCmd.Flags().String("gitops-repo", "", "GitOps repository URL (required)")
	appRegisterCmd.Flags().String("gitops-path", "", "Path in GitOps repository (default: environments/{environment}/apps/{name})")
}
