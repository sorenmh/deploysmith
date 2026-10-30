package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// How a Terraform deployment is reported: its plan as the check run
// deploysmith/plan-<env> (see ReportPlan) and the deployment as the usual
// check run deploysmith/deploy-<env>, which stays open until the plan has
// been applied or the deployment has ended otherwise. The plan check is
// complete once the plan is made, with a neutral conclusion while it waits
// for approval. The full plan text is not sent to GitHub; it is in the web
// UI, behind login.

// maxListed is how many resources of an action are listed in a check run
const maxListed = 30

// reportPlan reports the plan of a Terraform deployment
func (r *Reporter) reportPlan(ctx context.Context, d slack.Deploy, status, conclusion, title, summary string) {
	r.ReportPlan(ctx, Plan{
		Repo:        d.SourceRepo,
		SHA:         d.SourceSHA,
		Environment: d.Environment,
		RunID:       d.DeploymentID,
		Status:      status,
		Conclusion:  conclusion,
		Title:       title,
		Summary:     summary,
		URL:         d.URL,
	})
}

// Planning reports a Terraform deployment whose files are committed and
// that waits for the controller's plan
func (r *Reporter) Planning(ctx context.Context, d slack.Deploy) {
	r.sendDeploy(ctx, d, StatusInProgress, "",
		fmt.Sprintf("Planning %s for %s", d.Version, d.Environment),
		summary(d, "Committed to gitops, waiting for the plan."), "")
	r.reportPlan(ctx, d, StatusInProgress, "",
		fmt.Sprintf("Planning %s for %s", d.Version, d.Environment),
		summary(d, "Waiting for the plan."))
}

// PlanReady reports a Terraform plan that waits for approval
func (r *Reporter) PlanReady(ctx context.Context, d slack.Deploy) {
	if d.Plan == nil {
		return
	}
	r.sendDeploy(ctx, d, StatusInProgress, "",
		fmt.Sprintf("Plan for %s waits for approval", d.Environment),
		summary(d, "The plan waits for approval: "+d.Plan.Counts()+"."), "")
	r.reportPlan(ctx, d, StatusCompleted, ConclusionNeutral, planTitle(d.Plan), planSummary(d))
}

// Applying reports a Terraform deployment whose plan was approved
func (r *Reporter) Applying(ctx context.Context, d slack.Deploy) {
	r.sendDeploy(ctx, d, StatusInProgress, "",
		fmt.Sprintf("Applying %s to %s", d.Version, d.Environment),
		summary(d, "The plan was approved and is being applied."), "")
}

// Stale reports a Terraform deployment whose plan can no longer be
// approved, e.g. because a newer deploy replaced it
func (r *Reporter) Stale(ctx context.Context, d slack.Deploy, reason string) {
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionNeutral,
		fmt.Sprintf("Deployment to %s is stale", d.Environment),
		summary(d, "Not applied: "+reason), "")
	r.reportPlan(ctx, d, StatusCompleted, ConclusionNeutral, "Plan is stale", summary(d, "Not applied: "+reason))
}

// terraformApplied reports a Terraform deployment whose plan was applied
func (r *Reporter) terraformApplied(ctx context.Context, d slack.Deploy) {
	result := "No changes."
	title := "No changes"
	if d.Plan != nil && !d.Plan.NoChanges() {
		result = "Applied: " + d.Plan.Counts() + "."
		title = "Applied: " + d.Plan.Counts()
	}
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionSuccess,
		fmt.Sprintf("Applied %s to %s", d.Version, d.Environment), summary(d, result), "")
	r.reportPlan(ctx, d, StatusCompleted, ConclusionSuccess, title, planSummary(d))
}

// terraformFailed reports a failed Terraform deployment. A failure to plan
// fails the plan check too; after that the plan is what it was.
func (r *Reporter) terraformFailed(ctx context.Context, d slack.Deploy, stage, errMsg string) {
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionFailure,
		fmt.Sprintf("Deployment to %s failed (%s)", d.Environment, stage),
		summary(d, fmt.Sprintf("Failed at %s.", stage)),
		"~~~~\n"+errMsg+"\n~~~~")
	if stage == "plan" {
		r.reportPlan(ctx, d, StatusCompleted, ConclusionFailure, "Plan failed", summary(d, "The plan failed. See the deployment check for the error."))
	}
}

// planTitle is the one-line result of a plan
func planTitle(p *slack.PlanSummary) string {
	if p.NoChanges() {
		return "No changes"
	}
	title := p.Counts()
	if p.Destroys() {
		title += " (destroys resources)"
	}
	return title
}

// planSummary describes a Terraform plan in markdown: the counts, destroys
// highlighted, and the resources by action
func planSummary(d slack.Deploy) string {
	if d.Plan == nil {
		return summary(d, "No plan.")
	}
	return planBody(d.Plan, "Check the destroyed and replaced resources before approving.") + summary(d, "**Deployment**")
}

// planBody is the plan of planSummary without the deployment. advice follows
// the warning that the plan destroys resources.
func planBody(p *slack.PlanSummary, advice string) string {
	var b strings.Builder
	if p.NoChanges() {
		b.WriteString("**No changes.** The infrastructure matches the configuration.\n\n")
	} else {
		fmt.Fprintf(&b, "**Plan: %s**\n\n", p.Counts())
	}
	if p.Destroys() {
		fmt.Fprintf(&b, "> [!WARNING]\n> **This plan destroys resources.** %s\n\n", advice)
	}
	list(&b, "Destroyed", p.Destroyed)
	list(&b, "Replaced (destroyed and created again)", p.Replaced)
	list(&b, "Created", p.Created)
	list(&b, "Updated in place", p.Updated)
	return b.String()
}

// list writes the addresses of one action
func list(b *strings.Builder, heading string, addresses []string) {
	if len(addresses) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s** (%d)\n", heading, len(addresses))
	for i, address := range addresses {
		if i == maxListed {
			fmt.Fprintf(b, "- … and %d more\n", len(addresses)-maxListed)
			break
		}
		fmt.Fprintf(b, "- `%s`\n", address)
	}
	b.WriteString("\n")
}
