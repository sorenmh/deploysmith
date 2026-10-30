package cmd

import (
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var deploymentCmd = &cobra.Command{
	Use:   "deployment",
	Short: "Show, approve and reject deployments",
	Long: `Show the state of deployments. Terraform deployments also show their plan,
which has to be approved before it is applied.`,
}

var showDeploymentCmd = &cobra.Command{
	Use:   "show [deployment-id]",
	Short: "Show a deployment and, for Terraform, its plan",
	Long: `Show a deployment: its state, who deployed what where, and the error if it failed.

A Terraform deployment moves from planning to awaiting_approval when the
controller has made its plan. The plan summary shows how many resources are
added, changed and destroyed, and which; --plan prints the full plan as
OpenTofu shows it (sensitive values are masked by OpenTofu).

Example:
  smithctl deployment show 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11
  smithctl deployment show 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11 --plan
  smithctl deployment show 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		deployment, err := newClient().GetDeployment(args[0])
		if err != nil {
			return err
		}
		showText, _ := cmd.Flags().GetBool("plan")

		return output.Print(output.Format(GetOutputFormat()), deployment, func() {
			printDeployment(deployment, showText)
		})
	},
}

// printDeployment shows a deployment, with the summary of its plan
func printDeployment(d *client.Deployment, showText bool) {
	fmt.Printf("Deployment:    %s\n", d.ID)
	fmt.Printf("App:           %s\n", d.AppName)
	fmt.Printf("Version:       %s\n", d.VersionID)
	fmt.Printf("Environment:   %s\n", d.Environment)
	fmt.Printf("Status:        %s\n", d.Status)
	if d.PolicyName != "" {
		fmt.Printf("Triggered by:  policy %s\n", d.PolicyName)
	} else if d.TriggeredBy != "" {
		fmt.Printf("Triggered by:  %s\n", d.TriggeredBy)
	}
	fmt.Printf("Started:       %s\n", output.FormatTime(d.StartedAt))
	if d.CompletedAt != nil {
		fmt.Printf("Completed:     %s\n", output.FormatTime(*d.CompletedAt))
	}
	if d.GitopsCommitSHA != "" {
		fmt.Printf("GitOps Commit: %s\n", d.GitopsCommitSHA)
	}
	if d.Terraform != nil && d.Terraform.Rollback {
		fmt.Println("Rollback:      yes, an older version than the one applied")
	}
	if d.ErrorMessage != "" {
		fmt.Printf("Error:\n%s\n", indent(d.ErrorMessage, "  "))
	}

	if d.Terraform == nil {
		return
	}
	fmt.Println()
	fmt.Println("Terraform")
	fmt.Printf("  Root:        %s\n", d.Terraform.Root)
	fmt.Printf("  Plan ID:     %s\n", d.Terraform.PlanID)
	printDecision(d.Terraform)
	plan := d.Terraform.Plan
	if plan == nil {
		switch d.Status {
		case "planning":
			fmt.Println("  Plan:        waiting for the controller to plan")
		default:
			fmt.Println("  Plan:        none")
		}
		return
	}
	if plan.PlannedAt != nil {
		fmt.Printf("  Planned:     %s\n", output.FormatTime(*plan.PlannedAt))
	}
	switch {
	case plan.ToAdd == 0 && plan.ToChange == 0 && plan.ToDestroy == 0 && !plan.OutputChanges:
		fmt.Println("  Summary:     no changes")
	default:
		fmt.Printf("  Summary:     %d to add, %d to change, %d to destroy\n", plan.ToAdd, plan.ToChange, plan.ToDestroy)
	}
	if plan.Destroys {
		output.Warn("This plan destroys resources")
	}
	if plan.AppliedDiffered {
		output.Warn("The plan applied differed from the plan that was reviewed")
	}
	printResources("Destroy", plan.Resources.Destroy)
	printResources("Replace (destroy and create again)", plan.Resources.Replace)
	printResources("Create", plan.Resources.Create)
	printResources("Update in place", plan.Resources.Update)

	if showText {
		fmt.Println()
		fmt.Println(strings.TrimRight(plan.Text, "\n"))
	} else if plan.Text != "" {
		fmt.Println()
		output.Info("Use --plan to show the full plan")
	}
	if d.Status == "awaiting_approval" {
		hint := "smithctl deployment approve " + d.ID
		if plan.Destroys {
			hint += " --confirm-destroy"
		}
		output.Info("Approve with: " + hint + "; reject with: smithctl deployment reject " + d.ID)
	}
}

// printDecision shows who approved or rejected a plan, and when
func printDecision(run *client.TerraformRun) {
	switch {
	case run.ApprovedBy != "":
		approved := "  Approved by: " + run.ApprovedBy
		if run.ApprovedAt != nil {
			approved += " at " + output.FormatTime(*run.ApprovedAt)
		}
		if run.DestroyConfirmed {
			approved += " (confirmed the destroys)"
		}
		fmt.Println(approved)
		if run.ApprovalResetAt != nil {
			fmt.Printf("  Approval reset: %s\n", output.FormatTime(*run.ApprovalResetAt))
		}
	case run.RejectedBy != "":
		rejected := "  Rejected by: " + run.RejectedBy
		if run.RejectedAt != nil {
			rejected += " at " + output.FormatTime(*run.RejectedAt)
		}
		fmt.Println(rejected)
		if run.RejectReason != "" {
			fmt.Printf("  Reason:      %s\n", run.RejectReason)
		}
	}
}

// printResources lists the addresses of one action of a plan
func printResources(action string, addresses []string) {
	if len(addresses) == 0 {
		return
	}
	fmt.Printf("\n  %s (%d):\n", action, len(addresses))
	for _, address := range addresses {
		fmt.Printf("    %s\n", address)
	}
}

// indent prefixes every line of text
func indent(text, prefix string) string {
	return prefix + strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n"+prefix)
}

var approveDeploymentCmd = &cobra.Command{
	Use:   "approve [deployment-id]",
	Short: "Approve the plan of a Terraform deployment",
	Long: `Approve the plan of a Terraform deployment that is awaiting approval. smithd
commits the approval to the gitops repository and the controller applies the
plan. Approving a deployment that is being applied is not an error.

smithd refuses to approve a plan that is no longer the current one (the
controller planned again, or the root was deployed again): deploy again or wait
for the new plan. A plan that destroys or replaces resources has to be
confirmed with --confirm-destroy; without it the error lists what would be
destroyed.

The controller makes a new plan when it applies. If that plan differs from the
one you reviewed, the deployment shows it afterwards.

Example:
  smithctl deployment approve 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11
  smithctl deployment approve 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11 --confirm-destroy`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := ValidateConfig(); err != nil {
			return err
		}

		confirmDestroy, _ := cmd.Flags().GetBool("confirm-destroy")
		deployment, err := newClient().ApproveDeployment(args[0], confirmDestroy)
		if err != nil {
			return err
		}

		return output.Print(output.Format(GetOutputFormat()), deployment, func() {
			output.Success("Plan approved")
			fmt.Printf("  Deployment:  %s\n", deployment.ID)
			fmt.Printf("  Status:      %s\n", deployment.Status)
			if deployment.Terraform != nil && deployment.Terraform.ApprovedBy != "" {
				fmt.Printf("  Approved by: %s\n", deployment.Terraform.ApprovedBy)
			}
			output.Info("Follow it with: smithctl deployment show " + deployment.ID)
		})
	},
}

var rejectDeploymentCmd = &cobra.Command{
	Use:   "reject [deployment-id]",
	Short: "Reject the plan of a Terraform deployment",
	Long: `Reject the plan of a Terraform deployment that is awaiting approval. Nothing is
applied and nothing is committed; deploy a version again to get a new plan.

Example:
  smithctl deployment reject 5e6f0f5c-7f2e-4c7e-9a52-0c1d3a6a4b11 --reason "wrong environment"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := ValidateConfig(); err != nil {
			return err
		}

		reason, _ := cmd.Flags().GetString("reason")
		deployment, err := newClient().RejectDeployment(args[0], reason)
		if err != nil {
			return err
		}

		return output.Print(output.Format(GetOutputFormat()), deployment, func() {
			output.Success("Plan rejected")
			fmt.Printf("  Deployment:  %s\n", deployment.ID)
			fmt.Printf("  Status:      %s\n", deployment.Status)
		})
	},
}

func init() {
	rootCmd.AddCommand(deploymentCmd)
	deploymentCmd.AddCommand(showDeploymentCmd)
	deploymentCmd.AddCommand(approveDeploymentCmd)
	deploymentCmd.AddCommand(rejectDeploymentCmd)

	approveDeploymentCmd.Flags().Bool("confirm-destroy", false, "Confirm a plan that destroys or replaces resources")
	rejectDeploymentCmd.Flags().String("reason", "", "Why the plan is rejected")

	showDeploymentCmd.Flags().Bool("plan", false, "Print the full plan")
}
