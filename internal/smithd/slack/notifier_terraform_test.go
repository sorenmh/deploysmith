package slack

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func terraformDeploy() Deploy {
	return Deploy{
		App: "viino", Version: "tf1", Environment: "prod", Terraform: true, URL: "https://ui.example.com/ui/deployments/1",
		Plan: &PlanSummary{Add: 2, Change: 1, Destroy: 1, Destroyed: []string{"aws_s3_bucket.logs"}, Created: []string{"a.b", "c.d"}},
	}
}

func TestPlanReady_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) { n.PlanReady(context.Background(), terraformDeploy()) })
	assert.Contains(t, text, "Plan ready for approval")
	assert.Contains(t, text, "`viino` `tf1` → `prod`: 2 to add, 1 to change, 1 to destroy")
	assert.Contains(t, text, "This plan destroys resources")
	assert.Contains(t, text, "`aws_s3_bucket.logs`")
	assert.Contains(t, text, "<https://ui.example.com/ui/deployments/1|View deployment>")

	d := terraformDeploy()
	d.Plan = &PlanSummary{Add: 1, Created: []string{"a.b"}}
	text = withTestServer(t, func(n *Notifier) { n.PlanReady(context.Background(), d) })
	assert.NotContains(t, text, "destroys resources")

	// Replaced resources are destroyed too; long lists are cut
	d.Plan = &PlanSummary{Add: 7, Destroy: 7, Replaced: []string{"a", "b", "c", "d", "e", "f", "g"}}
	text = withTestServer(t, func(n *Notifier) { n.PlanReady(context.Background(), d) })
	assert.Contains(t, text, "`d`, `e` and 2 more")
	assert.NotContains(t, text, "`f`")
	assert.Contains(t, text, "and 2 more")

	// Nothing without a plan
	d.Plan = nil
	text = withTestServer(t, func(n *Notifier) { n.PlanReady(context.Background(), d) })
	assert.Empty(t, text)
}

func TestTerraformCommittedAppliedFailed_MessageContent(t *testing.T) {
	d := terraformDeploy()
	d.CommitSHA = "deadbeefcafe"

	text := withTestServer(t, func(n *Notifier) { n.Committed(context.Background(), d, true) })
	assert.Contains(t, text, "Waiting for the plan")
	assert.NotContains(t, text, "Flux")

	text = withTestServer(t, func(n *Notifier) { n.Applied(context.Background(), d, 0) })
	assert.Contains(t, text, "*Applied*")
	assert.Contains(t, text, "2 to add, 1 to change, 1 to destroy")
	assert.NotContains(t, text, "Flux")

	d.Plan = &PlanSummary{}
	text = withTestServer(t, func(n *Notifier) { n.Applied(context.Background(), d, 0) })
	assert.Contains(t, text, "no changes")

	// Every line of an error is quoted
	text = withTestServer(t, func(n *Notifier) {
		n.Failed(context.Background(), d, "plan", "TFExecPlanFailed: Error: boom\n\n  on main.tf line 3")
	})
	assert.Contains(t, text, "(plan)")
	assert.Contains(t, text, "> TFExecPlanFailed: Error: boom\n> \n>   on main.tf line 3")
}

func TestPlanSummary(t *testing.T) {
	assert.True(t, PlanSummary{}.NoChanges())
	assert.False(t, PlanSummary{}.Destroys())
	assert.False(t, PlanSummary{Change: 1}.NoChanges())
	assert.True(t, PlanSummary{Destroy: 1}.Destroys())
	assert.True(t, PlanSummary{Replaced: []string{"a"}}.Destroys())
	assert.Equal(t, "1 to add, 2 to change, 3 to destroy", PlanSummary{Add: 1, Change: 2, Destroy: 3}.Counts())
}

func TestNilNotifier_PlanReadyNoOps(t *testing.T) {
	var n *Notifier
	assert.NotPanics(t, func() { n.PlanReady(context.Background(), terraformDeploy()) })
}
