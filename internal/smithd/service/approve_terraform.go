package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

const (
	// maxRejectReason bounds the reason given for a rejection
	maxRejectReason = 1000
	// maxListedDestroys is how many resources an error about a plan with
	// destroys lists
	maxListedDestroys = 20
	// unknownActor is recorded when a request has no identity, which only
	// happens when smithd runs without authentication
	unknownActor = "unknown"
)

// actor names who made a request in the records of an approval or rejection
func actor(ctx context.Context) string {
	if who := auth.Actor(ctx); who != "" {
		return who
	}
	return unknownActor
}

// ApproveTerraform approves the plan of a terraform deployment that is
// awaiting approval: it commits approvePlan with the plan's id to the root's
// Terraform CR, which makes the controller apply, and moves the deployment to
// applying. Approving a deployment that is applying already is not an error.
//
// The plan must still be the one the reviewer saw, as far as smithd can tell:
// the CR must hold it as pending, for the source revision and the generation
// it was planned for, and the readable plan must have the stored hash. Plans
// that destroy or replace resources need confirmDestroy. Note that the
// controller makes a new plan when it applies, which only has the same id: if
// the applied plan differs from the reviewed one the watcher records that
// (TerraformPlan.AppliedDiffered).
func (s *Service) ApproveTerraform(ctx context.Context, deploymentID string, confirmDestroy bool) (*models.DeploymentDetail, error) {
	deployment, app, version, err := s.terraformDeployment(deploymentID)
	if err != nil {
		return nil, err
	}
	if deployment.Status == models.DeploymentApplying || (deployment.Status == models.DeploymentApplied && deployment.Terraform.ApprovedAt != nil) {
		return s.GetDeployment(deploymentID)
	}
	if deployment.Status != models.DeploymentAwaitingApproval {
		return nil, notAwaitingApproval(deployment, "approved")
	}
	ctx = deployCtx(ctx, app.Name, version, deployment.Environment, "")
	ctx = logging.AppendLogAttrs(ctx, logging.Field("deployment_id", deployment.ID))

	run := deployment.Terraform
	plan, err := s.TerraformPlans.Get(deployment.ID)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.PlanID != run.PlanID {
		return nil, conflict(fmt.Sprintf("Deployment %s has no stored plan for %s; wait for the plan or deploy again", deployment.ID, run.PlanID))
	}
	if err := s.checkPlanCurrent(ctx, deployment, app, plan); err != nil {
		return nil, err
	}
	if plan.Destroys && !confirmDestroy {
		return nil, invalid("destroy_confirmation_required", destroyMessage(plan))
	}

	who := actor(ctx)
	message := fmt.Sprintf("Approve terraform plan %s of %s version %s for %s (approved by %s)", run.PlanID, app.Name, version.VersionID, deployment.Environment, who)
	commit, err := s.TerraformGitops.SetTerraformApproval(app.Name, deployment.Environment, run.Root, deployment.ID, run.PlanID, message)
	if errors.Is(err, gitops.ErrRootNotCurrent) {
		return nil, &Error{Kind: KindConflict, Code: "plan_not_current", Message: fmt.Sprintf(
			"The Terraform CR of %s in %s is not the one of deployment %s any more (%s); deploy again to get a new plan", app.Name, deployment.Environment, deployment.ID, err.Error())}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to commit the approval: %w", err)
	}

	confirmed := plan.Destroys && confirmDestroy
	moved, err := s.Deployments.Approve(deployment.ID, who, confirmed)
	if err != nil {
		return nil, err
	}
	if moved {
		logging.LogInfoCtx(ctx, "Terraform plan approved",
			logging.Field("plan_id", run.PlanID),
			logging.Field("approved_by", who),
			logging.Field("commit_sha", commit),
			logging.Field("destroy_confirmed", confirmed),
		)
		s.FluxReconciler.Trigger(ctx)
		s.publishDecision(deployment, app, version, events.EventDeploymentApproved, "")
		deployment.Status = models.DeploymentApplying
		s.notifyApplying(ctx, s.terraformNotice(deployment, app, version))
	} else if current, err := s.Deployments.GetByID(deployment.ID); err != nil || (current.Status != models.DeploymentApplying && current.Status != models.DeploymentApplied) {
		// Anything else than another request or replica approving it first
		return nil, conflict(fmt.Sprintf("Deployment %s is no longer awaiting approval; its approval was committed but it is not applying", deployment.ID))
	}
	return s.GetDeployment(deploymentID)
}

// RejectTerraform rejects the plan of a terraform deployment that is
// awaiting approval. Nothing is committed: the root's CR stays unapproved
// (approvePlan ""), and the next deploy replaces the pending plan.
// Rejecting a deployment that is rejected already is not an error.
func (s *Service) RejectTerraform(ctx context.Context, deploymentID, reason string) (*models.DeploymentDetail, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > maxRejectReason {
		return nil, invalid("invalid_request", fmt.Sprintf("The reason may be at most %d characters", maxRejectReason))
	}
	deployment, app, version, err := s.terraformDeployment(deploymentID)
	if err != nil {
		return nil, err
	}
	if deployment.Status == models.DeploymentRejected {
		return s.GetDeployment(deploymentID)
	}
	if deployment.Status != models.DeploymentAwaitingApproval {
		return nil, notAwaitingApproval(deployment, "rejected")
	}
	ctx = deployCtx(ctx, app.Name, version, deployment.Environment, "")
	ctx = logging.AppendLogAttrs(ctx, logging.Field("deployment_id", deployment.ID))

	// An approval committed by hand would be applied whatever is recorded here
	cr, err := s.TerraformReader.Run(ctx, terraform.Namespace(app.Name), terraform.CRName(deployment.Environment, deployment.Terraform.Root))
	if err != nil {
		return nil, fmt.Errorf("failed to read the Terraform CR: %w", err)
	}
	if cr != nil && cr.DeploymentID == deployment.ID && cr.ApprovePlan != "" {
		return nil, conflict(fmt.Sprintf("The plan of deployment %s is approved in git (%s) and is being applied", deployment.ID, cr.ApprovePlan))
	}

	who := actor(ctx)
	moved, err := s.Deployments.Reject(deployment.ID, who, reason)
	if err != nil {
		return nil, err
	}
	if !moved {
		current, err := s.Deployments.GetByID(deployment.ID)
		if err != nil || current.Status != models.DeploymentRejected {
			return nil, conflict(fmt.Sprintf("Deployment %s is no longer awaiting approval", deployment.ID))
		}
		return s.GetDeployment(deploymentID)
	}

	logging.LogInfoCtx(ctx, "Terraform plan rejected", logging.Field("plan_id", deployment.Terraform.PlanID), logging.Field("rejected_by", who))
	deployment.Status = models.DeploymentRejected
	s.publishDecision(deployment, app, version, events.EventDeploymentRejected, reason)
	s.notifyRejected(ctx, s.terraformNotice(deployment, app, version), who, reason)
	return s.GetDeployment(deploymentID)
}

// terraformDeployment gets a terraform deployment with its app and version
func (s *Service) terraformDeployment(id string) (*models.Deployment, *models.Application, *models.Version, error) {
	deployment, err := s.Deployments.GetByID(id)
	if err != nil {
		if err.Error() == "deployment not found" {
			return nil, nil, nil, notFound("Deployment not found")
		}
		return nil, nil, nil, err
	}
	if deployment.Terraform == nil {
		return nil, nil, nil, invalid("not_terraform", fmt.Sprintf("Deployment %s is not a terraform deployment; there is no plan to approve or reject", id))
	}
	if err := s.requireTerraform(); err != nil {
		return nil, nil, nil, err
	}
	if s.TerraformPlans == nil || s.TerraformReader == nil {
		return nil, nil, nil, invalid("terraform_unavailable", "smithd is not running in a cluster, so it can't check the plan against the Terraform CR")
	}
	app, err := s.Apps.GetByID(deployment.AppID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get application: %w", err)
	}
	version, err := s.Versions.GetByID(deployment.VersionID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get version: %w", err)
	}
	return deployment, app, version, nil
}

func notAwaitingApproval(deployment *models.Deployment, verb string) error {
	message := fmt.Sprintf("Deployment %s is %s; only a plan that is awaiting_approval can be %s", deployment.ID, deployment.Status, verb)
	switch deployment.Status {
	case models.DeploymentPlanning, models.DeploymentPending:
		message += "; wait for the plan"
	case models.DeploymentStale, models.DeploymentFailed, models.DeploymentRejected, models.DeploymentApplied:
		message += "; deploy the version again to get a new plan"
		if deployment.ErrorMessage != "" {
			message += " (" + deployment.ErrorMessage + ")"
		}
	}
	return &Error{Kind: KindConflict, Code: "not_awaiting_approval", Message: message}
}

// destroyMessage is the error for an approval of a plan with destroys that
// does not confirm them: it lists what would be destroyed
func destroyMessage(plan *models.TerraformPlan) string {
	var lines []string
	for _, group := range []struct {
		action    string
		addresses []string
	}{{"destroy", plan.Resources.Destroy}, {"replace (destroy and create again)", plan.Resources.Replace}} {
		for _, address := range group.addresses {
			lines = append(lines, fmt.Sprintf("  %s: %s", group.action, address))
		}
	}
	if len(lines) > maxListedDestroys {
		more := len(lines) - maxListedDestroys
		lines = append(lines[:maxListedDestroys], fmt.Sprintf("  and %d more", more))
	}
	return fmt.Sprintf("The plan destroys resources (%d to destroy) and needs an explicit confirmation: approve again with confirmDestroy (smithctl deployment approve --confirm-destroy).\n%s", plan.ToDestroy, strings.Join(lines, "\n"))
}

// checkPlanCurrent checks that the plan in the record is the plan the
// controller holds for the deployment, so that approving it approves what
// the reviewer saw. It refuses with a conflict that says what changed; the
// watcher marks such a deployment stale.
func (s *Service) checkPlanCurrent(ctx context.Context, deployment *models.Deployment, app *models.Application, plan *models.TerraformPlan) error {
	namespace, name := terraform.Namespace(app.Name), terraform.CRName(deployment.Environment, deployment.Terraform.Root)
	run, err := s.TerraformReader.Run(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to read the Terraform CR: %w", err)
	}

	source, planID := deployment.Terraform.SourceCommitSHA, deployment.Terraform.PlanID
	var reason string
	switch {
	case run == nil:
		reason = "the Terraform CR no longer exists"
	case run.DeploymentID != deployment.ID:
		reason = fmt.Sprintf("the Terraform CR belongs to deployment %s", run.DeploymentID)
	case run.PendingPlan != planID:
		reason = fmt.Sprintf("the CR has pending plan %q, not %s", run.PendingPlan, planID)
	case run.PlannedRevision != source:
		reason = fmt.Sprintf("the CR was planned for revision %s, not %s", shortSHA(run.PlannedRevision), shortSHA(source))
	case run.PlannedGeneration != run.Generation:
		reason = fmt.Sprintf("the CR changed (generation %d) after it was planned (generation %d)", run.Generation, run.PlannedGeneration)
	case run.ApprovePlan != "" && run.ApprovePlan != planID:
		reason = fmt.Sprintf("the CR approves another plan (%s)", run.ApprovePlan)
	}
	if reason == "" {
		readable, err := s.TerraformReader.ReadablePlan(ctx, namespace, name)
		if err != nil {
			return fmt.Errorf("failed to read the plan: %w", err)
		}
		switch {
		case readable == nil || readable.PlanID != planID:
			reason = "the controller's plan is not the reviewed one"
		case !samePlan(plan, readable):
			reason = "the plan changed after it was made"
		}
	}
	if reason != "" {
		return &Error{Kind: KindConflict, Code: "plan_not_current", Message: fmt.Sprintf(
			"The plan of deployment %s can't be approved: %s. Deploy the version again, or wait for the new plan and review that", deployment.ID, reason)}
	}
	return nil
}

func (s *Service) publishDecision(deployment *models.Deployment, app *models.Application, version *models.Version, eventType events.EventType, reason string) {
	_ = s.Publisher.Publish(events.Event{
		Type:         eventType,
		AppName:      app.Name,
		AppID:        app.ID,
		VersionID:    version.VersionID,
		Environment:  deployment.Environment,
		DeploymentID: deployment.ID,
		Error:        reason,
		Timestamp:    time.Now(),
	})
}

// resetApprovals commits approvePlan: "" for every approved terraform
// deployment that has finished, so that the next plan of the same revision
// (a drift plan, a spec change, a retry) waits for approval again instead of
// being applied under the old approval. What is left to do is in the
// database, so a restart loses nothing, and a push that failed is tried
// again at the next poll. Several replicas may do this at once: the commit
// is only made if the file still has the approval, and the first to record
// the reset wins.
func (s *Service) resetApprovals(ctx context.Context) {
	if s.TerraformGitops == nil {
		return
	}
	deployments, err := s.Deployments.ListApprovalResets()
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list approvals to reset: "+err.Error(), err)
		return
	}
	for i := range deployments {
		s.resetApproval(ctx, &deployments[i])
	}
}

func (s *Service) resetApproval(ctx context.Context, deployment *models.Deployment) {
	app, err := s.Apps.GetByID(deployment.AppID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to get the application of a terraform deployment: "+err.Error(), err)
		return
	}
	ctx = logging.AppendLogAttrs(ctx, logging.Field("deployment_id", deployment.ID), logging.Field("app", app.Name))

	run := deployment.Terraform
	message := fmt.Sprintf("Reset terraform approval of %s in %s after deployment %s %s", app.Name, deployment.Environment, deployment.ID, deployment.Status)
	commit, err := s.TerraformGitops.SetTerraformApproval(app.Name, deployment.Environment, run.Root, deployment.ID, "", message)
	switch {
	case errors.Is(err, gitops.ErrRootNotCurrent):
		// A newer deploy rewrote the file with approvePlan "" already
		logging.LogInfoCtx(ctx, "No terraform approval to reset: the root was deployed again")
	case err != nil:
		logging.LogWarnCtx(ctx, "Failed to reset the terraform approval, trying again: "+err.Error())
		return
	default:
		logging.LogInfoCtx(ctx, "Terraform approval reset", logging.Field("plan_id", run.PlanID), logging.Field("commit_sha", commit))
	}
	if _, err := s.Deployments.SetApprovalReset(deployment.ID); err != nil {
		logging.LogErrorCtx(ctx, "Failed to record the reset of the approval: "+err.Error(), err)
	}
}
