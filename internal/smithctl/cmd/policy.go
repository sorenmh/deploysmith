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

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Manage auto-deployment policies",
	Long:  `Create, list, and delete auto-deployment policies.`,
}

var policyCreateCmd = &cobra.Command{
	Use:   "create [app-name]",
	Short: "Create an auto-deployment policy",
	Long: `Create an auto-deployment policy that automatically deploys versions
matching a branch pattern to a specified environment.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.

Example:
  smithctl policy create --name auto-deploy-main --branch main --env staging               # Uses app from binding
  smithctl policy create my-api-service --name auto-deploy-main --branch main --env staging
  smithctl policy create --app my-api-service --name auto-deploy-release --branch "release/*" --env production`,
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
		name, _ := cmd.Flags().GetString("name")
		branch, _ := cmd.Flags().GetString("branch")
		environment, _ := cmd.Flags().GetString("env")
		disabled, _ := cmd.Flags().GetBool("disabled")

		if name == "" {
			return fmt.Errorf("--name is required")
		}
		if branch == "" {
			return fmt.Errorf("--branch is required")
		}
		if environment == "" {
			return fmt.Errorf("--env is required")
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// Determine enabled state
		enabled := !disabled
		req := client.CreatePolicyRequest{
			Name:              name,
			GitBranchPattern:  branch,
			TargetEnvironment: environment,
			Enabled:           &enabled,
		}

		// Create policy
		policy, err := c.CreatePolicy(appID, req)
		if err != nil {
			return err
		}

		// Print success message
		output.Success("Auto-deploy policy created")
		fmt.Println()
		fmt.Printf("  Name:        %s\n", policy.Name)
		fmt.Printf("  Branch:      %s\n", policy.GitBranchPattern)
		fmt.Printf("  Environment: %s\n", policy.TargetEnvironment)
		status := "enabled"
		if !policy.Enabled {
			status = "disabled"
		}
		fmt.Printf("  Status:      %s\n", status)

		return nil
	},
}

var policyListCmd = &cobra.Command{
	Use:   "list [app-name]",
	Short: "List auto-deployment policies",
	Long: `List all auto-deployment policies for an application.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.

Example:
  smithctl policy list                    # Uses app from binding
  smithctl policy list my-api-service
  smithctl policy list --app my-api-service`,
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

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// List policies
		resp, err := c.ListPolicies(appID)
		if err != nil {
			return err
		}

		// Check if there are no policies
		if len(resp.Policies) == 0 {
			output.Info("No policies found")
			return nil
		}

		// Print output based on format
		format := output.Format(GetOutputFormat())
		return output.Print(format, resp, func() {
			headers := []string{"NAME", "BRANCH", "ENVIRONMENT", "STATUS"}
			rows := make([][]string, 0, len(resp.Policies))

			for _, policy := range resp.Policies {
				status := "enabled"
				if !policy.Enabled {
					status = "disabled"
				}
				rows = append(rows, []string{
					policy.Name,
					policy.GitBranchPattern,
					policy.TargetEnvironment,
					status,
				})
			}

			output.PrintTable(headers, rows)
		})
	},
}

var policyDeleteCmd = &cobra.Command{
	Use:   "delete [app-name] [policy-name]",
	Short: "Delete an auto-deployment policy",
	Long: `Delete an auto-deployment policy.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.

Example:
  smithctl policy delete my-policy-name                    # Uses app from binding
  smithctl policy delete my-api-service my-policy-name
  smithctl policy delete --app my-api-service my-policy-name`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Parse arguments - could be [policy-name] or [app-name, policy-name]
		var appIdentifier, policyName string
		if len(args) == 1 {
			// Only policy name provided, get app from flag or binding
			policyName = args[0]
			appIdentifier, _ = cmd.Flags().GetString("app")
		} else {
			// Both app and policy name provided
			appIdentifier = args[0]
			policyName = args[1]
		}

		// Resolve app ID
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		skipConfirm, _ := cmd.Flags().GetBool("confirm")

		// Show confirmation prompt unless --confirm is used
		if !skipConfirm {
			fmt.Printf("Are you sure you want to delete policy '%s'? (y/n): ", policyName)

			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))

			if response != "y" && response != "yes" {
				output.Info("Deletion cancelled")
				return nil
			}
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// List policies to find the policy ID
		resp, err := c.ListPolicies(appID)
		if err != nil {
			return err
		}

		var policyID string
		for _, p := range resp.Policies {
			if p.Name == policyName {
				policyID = p.ID
				break
			}
		}

		if policyID == "" {
			return fmt.Errorf("policy not found: %s", policyName)
		}

		// Delete policy
		if err := c.DeletePolicy(appID, policyID); err != nil {
			return err
		}

		// Print success message
		output.Success("Policy deleted")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(policyCmd)
	policyCmd.AddCommand(policyCreateCmd)
	policyCmd.AddCommand(policyListCmd)
	policyCmd.AddCommand(policyDeleteCmd)

	// Flags for policy create
	policyCreateCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
	policyCreateCmd.Flags().String("name", "", "Policy name (required)")
	policyCreateCmd.Flags().String("branch", "", "Git branch pattern (required)")
	policyCreateCmd.Flags().String("env", "", "Target environment (required)")
	policyCreateCmd.Flags().Bool("disabled", false, "Create policy in disabled state")

	// Flags for policy list
	policyListCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")

	// Flags for policy delete
	policyDeleteCmd.Flags().String("app", "", "Application name or ID (optional if app is bound)")
	policyDeleteCmd.Flags().Bool("confirm", false, "Skip confirmation prompt")
}
