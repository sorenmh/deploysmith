package cmd

import (
	"fmt"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var appTerraformCmd = &cobra.Command{
	Use:   "terraform",
	Short: "Manage an application's Terraform environments",
	Long: `Enable Terraform for an application in an environment, and provision
what its runner needs: the runner namespace tf-<app> in the gitops repository
and the OpenBao roles, policies and Transit key. smithd needs OpenBao for this.`,
}

var appTerraformEnableCmd = &cobra.Command{
	Use:   "enable [app-name]",
	Short: "Enable Terraform for an application in an environment",
	Long: `Enable Terraform for an application in an environment and record its
state backend. Without --backend-bucket the app uses smithd's shared backend,
with the state key <app>/<env>/main/terraform.tfstate. Existing apps pass
their own bucket, key, region and lock table to keep their state where it is.

The IAM role OpenBao assumes defaults to tf-<app>-<env> in smithd's AWS
account (created by the platform app's tf_app module); --aws-role-arn names
another one. Plan-only runs use the same ARN with a -readonly suffix.

You can specify the app by name or ID, or omit it if you've run 'forge app-bind' in this directory.

An existing app whose state is not encrypted yet passes --encryption-fallback,
so its first apply can read the plaintext state and write it back encrypted
(docs/terraform-migration.md). Turn it off afterwards with
'smithctl app terraform update --encryption-fallback=false'.

Example:
  smithctl app terraform enable viino --env prod
  smithctl app terraform enable legacy --env prod --backend-bucket legacy-tfstate \
    --backend-key prod/terraform.tfstate --backend-region eu-central-1 --backend-lock-table legacy-tflock \
    --encryption-fallback`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appIdentifier, _ := cmd.Flags().GetString("app")
		if len(args) > 0 {
			appIdentifier = args[0]
		}
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		environment, _ := cmd.Flags().GetString("env")
		if environment == "" {
			return fmt.Errorf("--env is required")
		}
		req := client.EnableTerraformRequest{Environment: environment}
		req.Backend.Bucket, _ = cmd.Flags().GetString("backend-bucket")
		req.Backend.Key, _ = cmd.Flags().GetString("backend-key")
		req.Backend.Region, _ = cmd.Flags().GetString("backend-region")
		req.Backend.LockTable, _ = cmd.Flags().GetString("backend-lock-table")
		req.AWSRoleARN, _ = cmd.Flags().GetString("aws-role-arn")
		req.EncryptionFallback, _ = cmd.Flags().GetBool("encryption-fallback")

		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		env, err := c.EnableTerraform(appID, req)
		if err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Terraform enabled for %s in %s", env.AppName, env.Environment))
		fmt.Println()
		printTerraformEnvironment(env)
		return provisionResult([]client.TerraformEnvironment{*env})
	},
}

var appTerraformUpdateCmd = &cobra.Command{
	Use:   "update [app-name]",
	Short: "Change the settings of an application's Terraform environment",
	Long: `Change the settings of an application's Terraform environment. They
apply from the next deploy on.

--encryption-fallback lets runs read plaintext state and write it back
encrypted, for the first apply of an existing app. Turn it off once that apply
is done, so plaintext state is refused again (docs/terraform-migration.md).

Example:
  smithctl app terraform update legacy --env prod --encryption-fallback=false`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appIdentifier, _ := cmd.Flags().GetString("app")
		if len(args) > 0 {
			appIdentifier = args[0]
		}
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		environment, _ := cmd.Flags().GetString("env")
		if environment == "" {
			return fmt.Errorf("--env is required")
		}
		var req client.UpdateTerraformRequest
		if cmd.Flags().Changed("encryption-fallback") {
			fallback, _ := cmd.Flags().GetBool("encryption-fallback")
			req.EncryptionFallback = &fallback
		}
		if req.EncryptionFallback == nil {
			return fmt.Errorf("nothing to change; pass --encryption-fallback")
		}

		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		env, err := c.UpdateTerraform(appID, environment, req)
		if err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Terraform updated for %s in %s", env.AppName, env.Environment))
		fmt.Println()
		printTerraformEnvironment(env)
		return nil
	},
}

var appTerraformListCmd = &cobra.Command{
	Use:   "list [app-name]",
	Short: "List the environments an application has Terraform enabled in",
	Long: `List the environments an application has Terraform enabled in, with
their state backends and the outcome of the last provisioning.

Example:
  smithctl app terraform list viino`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appIdentifier, _ := cmd.Flags().GetString("app")
		if len(args) > 0 {
			appIdentifier = args[0]
		}
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}

		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		resp, err := c.ListTerraformEnvironments(appID)
		if err != nil {
			return err
		}
		if len(resp.Environments) == 0 {
			output.Info("Terraform is not enabled in any environment")
			return nil
		}

		format := output.Format(GetOutputFormat())
		return output.Print(format, resp, func() { printTerraformTable(resp.Environments) })
	},
}

var appTerraformSyncCmd = &cobra.Command{
	Use:   "sync [app-name]",
	Short: "Provision an application's Terraform environments again",
	Long: `Provision an application's Terraform environments again: commit the
runner namespace to the gitops repository and write the OpenBao roles,
policies and Transit key. Safe to repeat; smithd also does it on start.

Example:
  smithctl app terraform sync viino             # all environments
  smithctl app terraform sync viino --env prod`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		appIdentifier, _ := cmd.Flags().GetString("app")
		if len(args) > 0 {
			appIdentifier = args[0]
		}
		appID, _, err := ResolveAppID(appIdentifier)
		if err != nil {
			return err
		}
		environment, _ := cmd.Flags().GetString("env")

		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
		resp, err := c.SyncTerraform(appID, environment)
		if err != nil {
			return err
		}

		format := output.Format(GetOutputFormat())
		if err := output.Print(format, resp, func() { printTerraformTable(resp.Environments) }); err != nil {
			return err
		}
		return provisionResult(resp.Environments)
	},
}

func printTerraformEnvironment(env *client.TerraformEnvironment) {
	fmt.Printf("  Namespace:  tf-%s\n", env.AppName)
	fmt.Printf("  Bucket:     %s\n", env.Backend.Bucket)
	fmt.Printf("  Key:        %s\n", env.Backend.Key)
	fmt.Printf("  Region:     %s\n", env.Backend.Region)
	if env.Backend.LockTable != "" {
		fmt.Printf("  Lock table: %s\n", env.Backend.LockTable)
	}
	if env.AWSRoleARN != "" {
		fmt.Printf("  AWS role:   %s\n", env.AWSRoleARN)
	}
	if env.EncryptionFallback {
		fmt.Printf("  Encryption: fallback to plaintext state on\n")
	}
}

func printTerraformTable(envs []client.TerraformEnvironment) {
	headers := []string{"ENVIRONMENT", "BUCKET", "KEY", "REGION", "PROVISIONED"}
	rows := make([][]string, 0, len(envs))
	for _, env := range envs {
		provisioned := "never"
		if env.ProvisionedAt != nil {
			provisioned = output.FormatTimeAgo(*env.ProvisionedAt)
		}
		if env.ProvisionError != nil {
			provisioned = "failed"
		}
		rows = append(rows, []string{env.Environment, env.Backend.Bucket, env.Backend.Key, env.Backend.Region, provisioned})
	}
	output.PrintTable(headers, rows)
}

// provisionResult reports environments whose provisioning failed, and
// returns an error if any did
func provisionResult(envs []client.TerraformEnvironment) error {
	failed := 0
	for _, env := range envs {
		if env.ProvisionError != nil {
			failed++
			fmt.Println()
			output.Error(fmt.Sprintf("Provisioning %s in %s failed: %s", env.AppName, env.Environment, *env.ProvisionError))
		}
	}
	if failed > 0 {
		return fmt.Errorf("provisioning failed for %d environments; fix the cause and run 'smithctl app terraform sync'", failed)
	}
	return nil
}

func init() {
	appCmd.AddCommand(appTerraformCmd)
	appTerraformCmd.AddCommand(appTerraformEnableCmd)
	appTerraformCmd.AddCommand(appTerraformUpdateCmd)
	appTerraformCmd.AddCommand(appTerraformListCmd)
	appTerraformCmd.AddCommand(appTerraformSyncCmd)

	for _, c := range []*cobra.Command{appTerraformEnableCmd, appTerraformUpdateCmd, appTerraformListCmd, appTerraformSyncCmd} {
		c.Flags().String("app", "", "Application name or ID")
	}

	appTerraformEnableCmd.Flags().String("env", "", "Environment (required)")
	appTerraformEnableCmd.Flags().String("backend-bucket", "", "S3 bucket of the state (default: smithd's shared bucket)")
	appTerraformEnableCmd.Flags().String("backend-key", "", "State object key (default: <app>/<env>/main/terraform.tfstate)")
	appTerraformEnableCmd.Flags().String("backend-region", "", "Region of the bucket (default: the shared backend's)")
	appTerraformEnableCmd.Flags().String("backend-lock-table", "", "DynamoDB lock table (default with the shared bucket: the shared table)")
	appTerraformEnableCmd.Flags().String("aws-role-arn", "", "IAM role OpenBao assumes (default: tf-<app>-<env> in smithd's AWS account)")
	appTerraformEnableCmd.Flags().Bool("encryption-fallback", false, "Read plaintext state until the first apply has encrypted it (existing apps)")

	appTerraformUpdateCmd.Flags().String("env", "", "Environment (required)")
	appTerraformUpdateCmd.Flags().Bool("encryption-fallback", false, "Read plaintext state until the first apply has encrypted it")

	appTerraformSyncCmd.Flags().String("env", "", "Only this environment")
}
