package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var environmentCmd = &cobra.Command{
	Use:   "environment",
	Short: "Manage environments",
	Long:  `Create, list, update, and delete environments.`,
	Aliases: []string{"env"},
}

var createEnvironmentCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new environment",
	Long:  `Create a new environment with the specified name.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		name := args[0]
		description, _ := cmd.Flags().GetString("description")

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		req := client.CreateEnvironmentRequest{
			Name: name,
		}
		if description != "" {
			req.Description = &description
		}

		env, err := c.CreateEnvironment(req)
		if err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Environment '%s' created", env.Name))
		fmt.Printf("  ID:          %s\n", env.ID)
		fmt.Printf("  Name:        %s\n", env.Name)
		if env.Description != nil {
			fmt.Printf("  Description: %s\n", *env.Description)
		}
		fmt.Printf("  Created:     %s\n", output.FormatTime(env.CreatedAt))

		return nil
	},
}

var listEnvironmentsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all environments",
	Long:  `List all available environments.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		resp, err := c.ListEnvironments()
		if err != nil {
			return err
		}

		if len(resp.Environments) == 0 {
			output.Info("No environments found")
			return nil
		}

		// Create table writer
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION\tCREATED")

		for _, env := range resp.Environments {
			description := ""
			if env.Description != nil {
				description = *env.Description
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n",
				env.Name,
				description,
				output.FormatTimeAgo(env.CreatedAt),
			)
		}

		w.Flush()
		return nil
	},
}

var updateEnvironmentCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Update an environment",
	Long:  `Update an environment's description.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		name := args[0]
		description, _ := cmd.Flags().GetString("description")

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// First, get the environment by name to find its ID
		listResp, err := c.ListEnvironments()
		if err != nil {
			return err
		}

		var environmentID string
		for _, env := range listResp.Environments {
			if env.Name == name {
				environmentID = env.ID
				break
			}
		}

		if environmentID == "" {
			return fmt.Errorf("environment '%s' not found", name)
		}

		req := client.UpdateEnvironmentRequest{}
		if description != "" {
			req.Description = &description
		}

		env, err := c.UpdateEnvironment(environmentID, req)
		if err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Environment '%s' updated", env.Name))
		fmt.Printf("  ID:          %s\n", env.ID)
		fmt.Printf("  Name:        %s\n", env.Name)
		if env.Description != nil {
			fmt.Printf("  Description: %s\n", *env.Description)
		}
		fmt.Printf("  Updated:     %s\n", output.FormatTime(env.UpdatedAt))

		return nil
	},
}

var deleteEnvironmentCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete an environment",
	Long:  `Delete an environment. Warning: this will prevent future deployments to this environment.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		name := args[0]
		confirm, _ := cmd.Flags().GetBool("confirm")

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		// First, get the environment by name to find its ID
		listResp, err := c.ListEnvironments()
		if err != nil {
			return err
		}

		var environmentID string
		for _, env := range listResp.Environments {
			if env.Name == name {
				environmentID = env.ID
				break
			}
		}

		if environmentID == "" {
			return fmt.Errorf("environment '%s' not found", name)
		}

		// Show confirmation prompt unless --confirm is used
		if !confirm {
			fmt.Printf("You are about to delete environment '%s'.\n", name)
			fmt.Println("Warning: This will prevent future deployments to this environment.")
			fmt.Print("Continue? (y/n): ")

			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "yes" {
				output.Info("Deletion cancelled")
				return nil
			}
		}

		err = c.DeleteEnvironment(environmentID)
		if err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Environment '%s' deleted", name))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(environmentCmd)

	// Add subcommands
	environmentCmd.AddCommand(createEnvironmentCmd)
	environmentCmd.AddCommand(listEnvironmentsCmd)
	environmentCmd.AddCommand(updateEnvironmentCmd)
	environmentCmd.AddCommand(deleteEnvironmentCmd)

	// Flags for create
	createEnvironmentCmd.Flags().String("description", "", "Environment description")

	// Flags for update
	updateEnvironmentCmd.Flags().String("description", "", "Environment description")

	// Flags for delete
	deleteEnvironmentCmd.Flags().Bool("confirm", false, "Skip confirmation prompt")
}