package terraform_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

const (
	runNamespace = "tf-viino"
	runCR        = "dev-main"
	sourceCommit = "8fa3d36e98d0d2f5d1a8f1c8c1c4b0a1e4b2f3d4"
	runPlanID    = "plan-main-8fa3d36e98"
)

func TestReaderRun(t *testing.T) {
	cluster := terraformtest.New(t)
	reader := cluster.Reader()
	ctx := context.Background()

	run, err := reader.Run(ctx, runNamespace, runCR)
	if err != nil || run != nil {
		t.Fatalf("a CR that doesn't exist is %+v, %v; want nil", run, err)
	}

	plannedAt := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	cluster.SetCR(runNamespace, runCR, terraformtest.WithChanges("dep-1", sourceCommit, runPlanID, plannedAt))
	run, err = reader.Run(ctx, runNamespace, runCR)
	if err != nil || run == nil {
		t.Fatalf("Run = %+v, %v", run, err)
	}
	if run.DeploymentID != "dep-1" || run.PendingPlan != runPlanID || run.PlannedRevision != sourceCommit || run.AttemptedRevision != sourceCommit || run.AppliedRevision != "" {
		t.Errorf("unexpected run %+v", run)
	}
	if !run.PlannedAt.Equal(plannedAt) || run.Generation != 1 || run.ObservedGeneration != 1 || run.PlannedGeneration != 1 {
		t.Errorf("unexpected times or generations %+v", run)
	}
	if plan := run.Condition(terraform.ConditionPlan); plan.Status != "True" || plan.Reason != terraform.ReasonPlannedWithChanges {
		t.Errorf("plan condition = %+v", plan)
	}
	if ready := run.Condition(terraform.ConditionReady); ready.Status != "Unknown" || !strings.Contains(ready.Message, runPlanID) {
		t.Errorf("ready condition = %+v", ready)
	}
	if apply := run.Condition(terraform.ConditionApply); apply.Status != "" {
		t.Errorf("a condition the CR doesn't have is %+v", apply)
	}

	approved := terraformtest.WithChanges("dep-1", sourceCommit, runPlanID, plannedAt).Approved().Applied(sourceCommit)
	cluster.SetCR(runNamespace, runCR, approved)
	run, err = reader.Run(ctx, runNamespace, runCR)
	if err != nil {
		t.Fatal(err)
	}
	if run.ApprovePlan != runPlanID || run.LastAppliedPlan != runPlanID || run.AppliedRevision != sourceCommit || run.Condition(terraform.ConditionReady).Reason != terraform.ReasonAppliedSucceed {
		t.Errorf("unexpected applied run %+v", run)
	}
}

func TestReaderReadablePlan(t *testing.T) {
	cluster := terraformtest.New(t)
	reader := cluster.Reader()
	ctx := context.Background()
	text := "Plan: 1 to add, 0 to change, 0 to destroy.\n" + strings.Repeat("  + line\n", 50)

	plan, err := reader.ReadablePlan(ctx, runNamespace, runCR)
	if err != nil || plan != nil {
		t.Fatalf("a plan that doesn't exist is %+v, %v; want nil", plan, err)
	}

	t.Run("one ConfigMap", func(t *testing.T) {
		cluster.SetPlan(runNamespace, runCR, runPlanID, "hash-1", text, 0)
		plan, err := reader.ReadablePlan(ctx, runNamespace, runCR)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Text != text || plan.PlanID != runPlanID || plan.Hash != "hash-1" {
			t.Errorf("unexpected plan %+v", plan)
		}
	})

	t.Run("chunks are put together in order", func(t *testing.T) {
		// More than 10 chunks, so that "-10" would sort before "-2" as text
		cluster.SetPlan(runNamespace, runCR, runPlanID, "hash-2", text, 30)
		plan, err := reader.ReadablePlan(ctx, runNamespace, runCR)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Text != text || plan.Hash != "hash-2" {
			t.Errorf("unexpected plan %q (%s)", plan.Text, plan.Hash)
		}
	})

	t.Run("another CR's plan is not read", func(t *testing.T) {
		if plan, err := reader.ReadablePlan(ctx, runNamespace, "dev-other"); err != nil || plan != nil {
			t.Errorf("got %+v, %v", plan, err)
		}
	})

	t.Run("a plan that changed while it was read", func(t *testing.T) {
		cluster.SetPlan(runNamespace, runCR, runPlanID, "hash-3", text, 30)
		// The next plan had reached only the first chunk
		cluster.SetPlanChunk(runNamespace, runCR, 0, "plan-main-other", "hash-4", "x")
		if plan, err := reader.ReadablePlan(ctx, runNamespace, runCR); err == nil {
			t.Errorf("got %+v, want an error", plan)
		}
	})
}

func TestReaderEvents(t *testing.T) {
	cluster := terraformtest.New(t)
	reader := cluster.Reader()
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)

	cluster.AddEvent(runNamespace, runCR, "Normal", "TerraformPlanSucceed", "Plan generated", at)
	cluster.AddEvent(runNamespace, runCR, "Warning", "TFExecApplyFailed", "Error: AccessDenied", at.Add(time.Minute))
	cluster.AddEvent(runNamespace, "dev-other", "Warning", "TFExecPlanFailed", "not ours", at)

	events, err := reader.Events(ctx, runNamespace, runCR)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Reason != "TerraformPlanSucceed" || events[1].Type != "Warning" || events[1].Message != "Error: AccessDenied" || !events[1].Time.Equal(at.Add(time.Minute)) {
		t.Errorf("unexpected events %+v", events)
	}
}
