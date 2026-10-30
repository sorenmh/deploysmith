package github

import (
	"context"
	"fmt"

	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// How a plan-only run (DEP-22) is reported: its plan as the check run
// deploysmith/plan-<env> on the commit the version was built from, the same
// check a Terraform deployment reports its plan in. d.DeploymentID is the
// id of the plan run. A plan-only run has no deploy check: nothing is
// deployed. The full plan text is not sent to GitHub.

// maxPlanOnlyError bounds the error in the summary of a failed plan
const maxPlanOnlyError = 3000

// planOnlySummary describes a plan-only run in markdown
func planOnlySummary(d slack.Deploy, state string) string {
	return summary(d, state+" Nothing is deployed.")
}

// PlanRunStarted reports a plan-only run that waits for the controller's plan
func (r *Reporter) PlanRunStarted(ctx context.Context, d slack.Deploy) {
	r.reportPlan(ctx, d, StatusInProgress, "",
		fmt.Sprintf("Planning %s for %s", d.Version, d.Environment),
		planOnlySummary(d, "Waiting for the plan."))
}

// PlanRunPlanned reports a plan-only run whose plan is made. The check
// succeeds unless the plan destroys resources, which it flags as neutral.
func (r *Reporter) PlanRunPlanned(ctx context.Context, d slack.Deploy) {
	if d.Plan == nil {
		return
	}
	conclusion := ConclusionSuccess
	if d.Plan.Destroys() {
		conclusion = ConclusionNeutral
	}
	body := planBody(d.Plan, "Check the destroyed and replaced resources before you deploy this version.")
	r.reportPlan(ctx, d, StatusCompleted, conclusion, planTitle(d.Plan), body+planOnlySummary(d, "**Plan only.**"))
}

// PlanRunFailed reports a plan-only run that failed
func (r *Reporter) PlanRunFailed(ctx context.Context, d slack.Deploy, errMsg string) {
	if len(errMsg) > maxPlanOnlyError {
		errMsg = errMsg[:maxPlanOnlyError] + "\n… (truncated)"
	}
	r.reportPlan(ctx, d, StatusCompleted, ConclusionFailure, "Plan failed",
		planOnlySummary(d, "The plan failed.")+"\n~~~~\n"+errMsg+"\n~~~~\n")
}

// PlanRunSuperseded reports a plan-only run that a newer run replaced
func (r *Reporter) PlanRunSuperseded(ctx context.Context, d slack.Deploy, reason string) {
	r.reportPlan(ctx, d, StatusCompleted, ConclusionNeutral, "Plan replaced", planOnlySummary(d, "Not planned: "+reason+"."))
}
