package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

const (
	// terraformPollInterval is how often smithd looks at the Terraform CRs
	// of the runs it follows
	terraformPollInterval = 10 * time.Second

	// terraformPlanTimeout is how long a deployment may wait for the
	// controller to plan it before it is failed. The controller needs Flux to
	// apply the CR first, then a runner pod that downloads providers.
	terraformPlanTimeout = time.Hour

	// maxRunError bounds the error kept in the deployment record; the
	// controller's condition messages are up to 20000 characters
	maxRunError = 20000
	// maxNoticeError bounds the error in Slack and Web Push messages
	maxNoticeError = 1500
	// maxRunEvents is how many events are added to the error of a run
	maxRunEvents = 5
)

// WatchTerraform follows the terraform deployments that are planning,
// waiting for approval or applying, until ctx is cancelled. Like the Flux
// polling of manifest deployments it reads the cluster, but the state is in
// the database, so it picks up where it was after a restart.
func (s *Service) WatchTerraform(ctx context.Context) {
	if s.TerraformReader == nil {
		logging.LogInfo("Not following Terraform runs: not running in a cluster")
		return
	}
	ticker := time.NewTicker(terraformPollInterval)
	defer ticker.Stop()

	for {
		s.pollTerraform(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pollTerraform looks at every terraform run once
func (s *Service) pollTerraform(ctx context.Context) {
	deployments, err := s.Deployments.ListTrackedTerraform()
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list terraform deployments: "+err.Error(), err)
		return
	}
	for i := range deployments {
		s.trackTerraformRun(ctx, &deployments[i])
	}
	s.resetApprovals(ctx)
}

// trackedRun is a terraform deployment being followed
type trackedRun struct {
	s          *Service
	ctx        context.Context
	deployment *models.Deployment
	app        *models.Application
	version    *models.Version
	// namespace and cr locate the root's Terraform CR
	namespace string
	cr        string
}

// trackTerraformRun compares a terraform deployment with the status of its
// Terraform CR and moves it on: see "Reading Status" in
// docs/specs/terraform-controller.md for the states. Several smithd replicas
// may do this at the same time; a state change only counts for the replica
// that made it in the database, which then also reports it.
func (s *Service) trackTerraformRun(ctx context.Context, deployment *models.Deployment) {
	if deployment.Terraform == nil || deployment.Terraform.SourceCommitSHA == "" || deployment.Terraform.PlanID == "" {
		return
	}
	app, err := s.Apps.GetByID(deployment.AppID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to get the application of a terraform deployment: "+err.Error(), err)
		return
	}
	version, err := s.Versions.GetByID(deployment.VersionID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to get the version of a terraform deployment: "+err.Error(), err)
		return
	}
	ctx = deployCtx(ctx, app.Name, version, deployment.Environment, "")
	ctx = logging.AppendLogAttrs(ctx, logging.Field("deployment_id", deployment.ID))

	r := &trackedRun{
		s:          s,
		ctx:        ctx,
		deployment: deployment,
		app:        app,
		version:    version,
		namespace:  terraform.Namespace(app.Name),
		cr:         terraform.CRName(deployment.Environment, deployment.Terraform.Root),
	}
	r.track()
}

func (r *trackedRun) track() {
	d := r.deployment
	run, err := r.s.TerraformReader.Run(r.ctx, r.namespace, r.cr)
	if err != nil {
		logging.LogWarnCtx(r.ctx, "Error polling Terraform CR: "+err.Error())
		return
	}

	switch {
	case run == nil:
		r.missing()
	case run.DeploymentID == d.ID:
		switch d.Status {
		case models.DeploymentPlanning:
			r.planning(run)
		case models.DeploymentAwaitingApproval:
			r.awaitingApproval(run)
		case models.DeploymentApplying:
			r.applying(run)
		}
	case d.Status != models.DeploymentApplying && r.replacedBy(run.DeploymentID):
		// Someone committed a newer deploy of the root, e.g. from another
		// smithd or by hand. A run that is applying is never replaced.
		r.stale("the root was deployed again (deployment " + run.DeploymentID + ")")
	default:
		r.waiting("Flux has not applied the Terraform CR of this deployment")
	}
}

// replacedBy reports whether the deployment with the given id is a newer
// deployment of the same root
func (r *trackedRun) replacedBy(id string) bool {
	if id == "" {
		return false
	}
	other, err := r.s.Deployments.GetByID(id)
	if err != nil {
		return false
	}
	d := r.deployment
	return other.AppID == d.AppID && other.Environment == d.Environment && other.StartedAt.After(d.StartedAt)
}

// missing handles a Terraform CR that does not exist. Before the plan that is
// normal while Flux has not applied it; afterwards somebody removed it.
func (r *trackedRun) missing() {
	switch r.deployment.Status {
	case models.DeploymentAwaitingApproval:
		r.stale("the Terraform CR no longer exists")
	case models.DeploymentApplying:
		r.fail("apply", "the Terraform CR no longer exists")
	default:
		r.waiting("Flux has not applied the Terraform CR of this deployment")
	}
}

// waiting leaves a planning deployment alone, until it has waited too long
func (r *trackedRun) waiting(reason string) {
	d := r.deployment
	if d.Status != models.DeploymentPlanning || time.Since(d.StartedAt) < terraformPlanTimeout {
		return
	}
	r.fail("plan", fmt.Sprintf("%s within %s", reason, terraformPlanTimeout))
}

// planning looks for the plan of the deployment's source
func (r *trackedRun) planning(run *terraform.Run) {
	d := r.deployment
	source := d.Terraform.SourceCommitSHA
	ready := run.Condition(terraform.ConditionReady)

	if run.AttemptedRevision == source && ready.Status == "False" {
		r.failRun("plan", run)
		return
	}
	if run.PlannedRevision == source {
		plan := run.Condition(terraform.ConditionPlan)
		switch {
		case plan.Status == "True" && plan.Reason == terraform.ReasonPlannedWithChanges && run.PendingPlan == d.Terraform.PlanID:
			r.planReady(run)
			return
		case ready.Status == "True" && ready.Reason == terraform.ReasonPlannedNoChanges:
			r.noChanges(run)
			return
		}
	}
	r.waiting("the controller has not planned this deployment")
}

// readPlan reads the plan the controller made for the deployment from the
// cluster. It returns nil, with the reason logged, while the plan is not
// there or is another plan, so that the next poll tries again.
func (r *trackedRun) readPlan(run *terraform.Run) *terraform.ReadablePlan {
	plan, err := r.s.TerraformReader.ReadablePlan(r.ctx, r.namespace, r.cr)
	if err != nil {
		logging.LogWarnCtx(r.ctx, "Failed to read the plan: "+err.Error())
		return nil
	}
	if plan == nil || plan.PlanID != r.deployment.Terraform.PlanID {
		logging.LogWarnCtx(r.ctx, "The readable plan of the CR is not the plan of this deployment yet",
			logging.Field("plan_id", r.deployment.Terraform.PlanID))
		return nil
	}
	return plan
}

// parsePlan builds the stored plan from the readable plan of the CR
func (r *trackedRun) parsePlan(run *terraform.Run, readable *terraform.ReadablePlan) models.TerraformPlan {
	plan := terraform.ParsePlan(readable.Text)
	plan.PlanID = r.deployment.Terraform.PlanID
	plan.PlanHash = readable.Hash
	if !run.PlannedAt.IsZero() {
		at := run.PlannedAt
		plan.PlannedAt = &at
	}
	return plan
}

// planReady stores the plan of a deployment and moves it to awaiting_approval.
// The plan is copied now: the cluster overwrites it with every plan,
// including the one made again right before the apply.
func (r *trackedRun) planReady(run *terraform.Run) {
	readable := r.readPlan(run)
	if readable == nil {
		r.waiting("the readable plan is not available")
		return
	}
	plan := r.parsePlan(run, readable)
	if err := r.s.TerraformPlans.Save(r.deployment.ID, plan); err != nil {
		logging.LogErrorCtx(r.ctx, "Failed to store the plan: "+err.Error(), err)
		return
	}
	if !r.move(models.DeploymentAwaitingApproval, "") {
		return
	}

	logging.LogInfoCtx(r.ctx, "Terraform plan ready for approval",
		logging.Field("plan_id", plan.PlanID),
		logging.Field("to_add", plan.ToAdd),
		logging.Field("to_change", plan.ToChange),
		logging.Field("to_destroy", plan.ToDestroy),
	)
	r.publish(events.EventDeploymentPlanReady, nil)
	r.s.notifyPlanReady(r.ctx, r.notice())
}

// noChanges finishes a deployment whose plan changes nothing: there is
// nothing to approve and the root already is what the version describes
func (r *trackedRun) noChanges(run *terraform.Run) {
	var plan models.TerraformPlan
	if readable := r.readPlan(run); readable != nil {
		plan = r.parsePlan(run, readable)
	} else {
		// The controller may keep no plan when there is nothing to show
		plan = r.parsePlan(run, &terraform.ReadablePlan{Text: "No changes. Your infrastructure matches the configuration.\n"})
	}
	if err := r.s.TerraformPlans.Save(r.deployment.ID, plan); err != nil {
		logging.LogErrorCtx(r.ctx, "Failed to store the plan: "+err.Error(), err)
		return
	}
	r.applied("Terraform plan has no changes")
}

// awaitingApproval checks that the plan is still the one the controller
// holds, and notices an approval made in git
func (r *trackedRun) awaitingApproval(run *terraform.Run) {
	d := r.deployment
	source, planID := d.Terraform.SourceCommitSHA, d.Terraform.PlanID

	if run.PlannedRevision != "" && run.PlannedRevision != source {
		r.stale(fmt.Sprintf("the Terraform CR moved on to revision %s", shortSHA(run.PlannedRevision)))
		return
	}
	if run.PendingPlan != "" && run.PendingPlan != planID {
		r.stale(fmt.Sprintf("the Terraform CR has another pending plan (%s)", run.PendingPlan))
		return
	}
	if run.ApprovePlan == planID {
		// Approved by a commit that did not come from smithd, or by smithd
		// before it has recorded that
		logging.LogInfoCtx(r.ctx, "Terraform plan approved in git", logging.Field("plan_id", planID))
		moved, err := r.s.Deployments.Approve(d.ID, "git", false)
		if err != nil {
			logging.LogErrorCtx(r.ctx, "Failed to record the deployment state: "+err.Error(), err)
			return
		}
		if moved {
			d.Status = models.DeploymentApplying
			r.s.notifyApplying(r.ctx, r.notice())
		}
		return
	}
	r.checkPlanUnchanged(run)
}

// checkPlanUnchanged compares the plan the controller holds with the stored
// one when it has planned again: the same plan again (the controller plans
// every interval) is fine, another plan means the reviewer did not see what
// would be applied, so the deployment is stale.
func (r *trackedRun) checkPlanUnchanged(run *terraform.Run) {
	d := r.deployment
	stored, err := r.s.TerraformPlans.Get(d.ID)
	if err != nil {
		logging.LogErrorCtx(r.ctx, "Failed to get the stored plan: "+err.Error(), err)
		return
	}
	if stored != nil && (run.PlannedAt.IsZero() || (stored.PlannedAt != nil && stored.PlannedAt.Truncate(time.Second).Equal(run.PlannedAt.Truncate(time.Second)))) {
		return
	}
	readable := r.readPlan(run)
	if readable == nil {
		return
	}
	switch {
	case stored == nil:
		// The record was lost; keep what the controller holds
		if err := r.s.TerraformPlans.Save(d.ID, r.parsePlan(run, readable)); err != nil {
			logging.LogErrorCtx(r.ctx, "Failed to store the plan: "+err.Error(), err)
		}
	case !samePlan(stored, readable):
		r.stale("the plan changed after it was made, and has to be reviewed again")
	case !run.PlannedAt.IsZero():
		if err := r.s.TerraformPlans.SetPlannedAt(d.ID, run.PlannedAt); err != nil {
			logging.LogErrorCtx(r.ctx, "Failed to update the plan: "+err.Error(), err)
		}
	}
}

// samePlan reports whether the readable plan is the stored one
func samePlan(stored *models.TerraformPlan, readable *terraform.ReadablePlan) bool {
	if stored.PlanHash != "" && readable.Hash != "" {
		return stored.PlanHash == readable.Hash
	}
	return stored.Text == readable.Text
}

// applying waits for the apply to succeed or fail
func (r *trackedRun) applying(run *terraform.Run) {
	d := r.deployment
	source, planID := d.Terraform.SourceCommitSHA, d.Terraform.PlanID
	ready := run.Condition(terraform.ConditionReady)

	switch {
	case ready.Status == "True" && ready.Reason == terraform.ReasonAppliedSucceed && run.AppliedRevision == source && run.LastAppliedPlan == planID:
		r.checkAppliedPlan(run)
		r.applied("Terraform plan applied")
	case ready.Status == "False" && run.AttemptedRevision == source:
		r.failRun("apply", run)
	}
}

// checkAppliedPlan compares the plan the controller made again right before
// applying with the reviewed one. The controller applies the plan of the
// approved id, which for a pinned source always is the same, but the
// content can differ, e.g. when the infrastructure drifted meanwhile.
func (r *trackedRun) checkAppliedPlan(run *terraform.Run) {
	d := r.deployment
	stored, err := r.s.TerraformPlans.Get(d.ID)
	if err != nil || stored == nil {
		return
	}
	readable := r.readPlan(run)
	if readable == nil || readable.Hash == "" || stored.PlanHash == "" {
		return
	}
	differed := readable.Hash != stored.PlanHash
	if differed {
		logging.LogWarnCtx(r.ctx, "The applied plan differed from the reviewed plan",
			logging.Field("reviewed_hash", stored.PlanHash),
			logging.Field("applied_hash", readable.Hash),
		)
	}
	if err := r.s.TerraformPlans.SetApplied(d.ID, readable.Hash, differed); err != nil {
		logging.LogErrorCtx(r.ctx, "Failed to record the applied plan: "+err.Error(), err)
	}
}

// applied finishes the deployment as applied
func (r *trackedRun) applied(message string) {
	if !r.move(models.DeploymentApplied, "") {
		return
	}
	logging.LogInfoCtx(r.ctx, message, logging.Field("plan_id", r.deployment.Terraform.PlanID))
	r.publish(events.EventDeploymentSucceeded, func(e *events.Event) { e.CommitSHA = r.deployment.GitopsCommitSHA })
	r.s.notifyApplied(r.ctx, r.notice(), 0)
}

// failRun fails the deployment with what the controller reports: the Ready
// condition and the events of the CR. The runner pod, and with it its log,
// is deleted after each run.
func (r *trackedRun) failRun(stage string, run *terraform.Run) {
	ready := run.Condition(terraform.ConditionReady)
	message := ready.Message
	if ready.Reason != "" {
		message = ready.Reason + ": " + ready.Message
	}

	runEvents, err := r.s.TerraformReader.Events(r.ctx, r.namespace, r.cr)
	if err != nil {
		logging.LogWarnCtx(r.ctx, "Failed to read the events of the CR: "+err.Error())
	}
	var lines []string
	for _, event := range runEvents {
		// Skip what the condition already says
		if event.Type == "Warning" && event.Message != "" && !strings.Contains(message, event.Message) {
			lines = append(lines, fmt.Sprintf("- %s: %s", event.Reason, event.Message))
		}
	}
	if len(lines) > maxRunEvents {
		lines = lines[len(lines)-maxRunEvents:]
	}
	if len(lines) > 0 {
		message += "\n\nEvents of the Terraform CR:\n" + strings.Join(lines, "\n")
	}
	r.fail(stage, message)
}

// fail finishes the deployment as failed and reports it
func (r *trackedRun) fail(stage, message string) {
	message = truncateMessage(strings.TrimSpace(message), maxRunError)
	if !r.move(models.DeploymentFailed, message) {
		return
	}
	logging.LogWarnCtx(r.ctx, "Terraform deployment failed ("+stage+"): "+message)
	r.publish(events.EventDeploymentFailed, func(e *events.Event) { e.Error = message })
	r.s.notifyFailed(r.ctx, r.notice(), stage, truncateMessage(message, maxNoticeError))
}

// stale finishes the deployment as stale: its plan can't be approved any
// more. A new deploy makes a new plan.
func (r *trackedRun) stale(reason string) {
	message := "Stale: " + reason
	if !r.move(models.DeploymentStale, message) {
		return
	}
	logging.LogInfoCtx(r.ctx, "Terraform deployment is stale: "+reason)
	r.s.notifyStale(r.ctx, r.notice(), reason)
}

// move records a state change in the database. It returns false if the
// deployment was not in its state any more, i.e. another replica made the
// change, which then reports it.
func (r *trackedRun) move(to, errorMsg string) bool {
	d := r.deployment
	moved, err := r.s.Deployments.TransitionTerraform(d.ID, d.Status, to, errorMsg)
	if err != nil {
		logging.LogErrorCtx(r.ctx, "Failed to record the deployment state: "+err.Error(), err)
		return false
	}
	if moved {
		d.Status = to
		d.ErrorMessage = errorMsg
	}
	return moved
}

func (r *trackedRun) publish(eventType events.EventType, mutate func(*events.Event)) {
	e := events.Event{
		Type:         eventType,
		AppName:      r.app.Name,
		AppID:        r.app.ID,
		VersionID:    r.version.VersionID,
		Environment:  r.deployment.Environment,
		DeploymentID: r.deployment.ID,
		Timestamp:    time.Now(),
	}
	if mutate != nil {
		mutate(&e)
	}
	_ = r.s.Publisher.Publish(e)
}

// notice describes the deployment, with its plan, for notifications
func (r *trackedRun) notice() slack.Deploy {
	return r.s.terraformNotice(r.deployment, r.app, r.version)
}

// terraformNotice describes a terraform deployment for notifications. Once
// there is a plan, it carries its summary.
func (s *Service) terraformNotice(deployment *models.Deployment, app *models.Application, version *models.Version) slack.Deploy {
	notice := s.slackDeploy(deployment, app, version, nil)
	if s.TerraformPlans == nil {
		return notice
	}
	if plan, err := s.TerraformPlans.Get(deployment.ID); err == nil && plan != nil {
		notice.Plan = planSummary(plan)
	}
	return notice
}

// planSummary is the plan as notifications show it
func planSummary(plan *models.TerraformPlan) *slack.PlanSummary {
	return &slack.PlanSummary{
		Add:       plan.ToAdd,
		Change:    plan.ToChange,
		Destroy:   plan.ToDestroy,
		Created:   plan.Resources.Create,
		Updated:   plan.Resources.Update,
		Replaced:  plan.Resources.Replace,
		Destroyed: plan.Resources.Destroy,
	}
}

// notifyReplaced reports a deployment whose pending plan a newer deploy of
// the root replaced
func (s *Service) notifyReplaced(ctx context.Context, app *models.Application, replaced, by *models.Deployment) {
	version, err := s.Versions.GetByID(replaced.VersionID)
	if err != nil {
		logging.LogWarnCtx(ctx, "Failed to get the version of a replaced deployment: "+err.Error())
		return
	}
	s.notifyStale(ctx, s.terraformNotice(replaced, app, version), "replaced by deployment "+by.ID)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// truncateMessage shortens a message to at most limit bytes, at a character
func truncateMessage(message string, limit int) string {
	if len(message) <= limit {
		return message
	}
	const note = "\n… (truncated)"
	cut := limit - len(note)
	for cut > 0 && message[cut]&0xC0 == 0x80 {
		cut--
	}
	return message[:cut] + note
}
