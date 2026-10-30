package push

import (
	"context"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

func TestTerraformMessages(t *testing.T) {
	plan := &slack.PlanSummary{Add: 2, Change: 1, Destroy: 1, Destroyed: []string{"aws_s3_bucket.logs"}}
	d := slack.Deploy{App: "viino", Version: "tf1", Environment: "prod", Terraform: true, Plan: plan, URL: "https://ui.example.com/ui/deployments/1"}

	msg := planReadyMessage(d)
	if msg.Title != "Plan ready for approval, destroys resources" || msg.Body != "viino tf1 → prod: 2 to add, 1 to change, 1 to destroy" || msg.URL != d.URL || msg.Tag != "deploy:viino:prod" {
		t.Errorf("unexpected message %+v", msg)
	}
	// Without a public URL the notification still opens the deployment in the UI
	if msg := planReadyMessage(slack.Deploy{App: "viino", Version: "tf1", Environment: "prod", Terraform: true, Plan: plan, DeploymentID: "d1"}); msg.URL != "/ui/deployments/d1" {
		t.Errorf("link without a public URL is %q", msg.URL)
	}
	d.Plan = &slack.PlanSummary{Add: 1}
	if msg := planReadyMessage(d); msg.Title != "Plan ready for approval" {
		t.Errorf("title of a plan without destroys is %q", msg.Title)
	}

	d.Plan = plan
	if msg := terraformAppliedMessage(d); msg.Title != "Applied" || msg.Body != "viino tf1 → prod, 2 to add, 1 to change, 1 to destroy" {
		t.Errorf("unexpected message %+v", msg)
	}
	d.Plan = &slack.PlanSummary{}
	if msg := terraformAppliedMessage(d); msg.Body != "viino tf1 → prod, no changes" {
		t.Errorf("unexpected message %+v", msg)
	}
}

func TestNilNotifier_TerraformNoOps(t *testing.T) {
	var n *Notifier
	n.PlanReady(context.Background(), slack.Deploy{})
	n.PlanReady(context.Background(), slack.Deploy{Plan: &slack.PlanSummary{}})
	n.Applied(context.Background(), slack.Deploy{Terraform: true}, 0)
}
