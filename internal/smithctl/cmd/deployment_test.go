package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
)

// captureStdout returns what fn prints
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = original }()

	fn()
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func terraformDeployment() *client.Deployment {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	d := &client.Deployment{
		ID: "dep-1", AppName: "viino", VersionID: "tf1", Environment: "production", Status: "awaiting_approval",
		TriggeredBy: "alice", StartedAt: at,
		Terraform: &client.TerraformRun{Root: "main", PlanID: "plan-main-bbbbbbbbbb"},
	}
	plan := &client.TerraformPlan{PlanID: "plan-main-bbbbbbbbbb", PlannedAt: &at, ToAdd: 1, ToDestroy: 1, Destroys: true, Text: "Plan: 1 to add, 0 to change, 1 to destroy.\n"}
	plan.Resources.Create = []string{"a.new"}
	plan.Resources.Destroy = []string{"b.old"}
	d.Terraform.Plan = plan
	return d
}

func TestPrintDeploymentWithPlan(t *testing.T) {
	out := captureStdout(t, func() { printDeployment(terraformDeployment(), false) })
	for _, want := range []string{
		"Status:        awaiting_approval", "Triggered by:  alice", "Root:        main", "Plan ID:     plan-main-bbbbbbbbbb",
		"Summary:     1 to add, 0 to change, 1 to destroy", "Warning: This plan destroys resources",
		"Destroy (1):\n    b.old", "Create (1):\n    a.new", "Use --plan to show the full plan",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Plan: 1 to add") {
		t.Errorf("the plan text was printed without --plan:\n%s", out)
	}

	out = captureStdout(t, func() { printDeployment(terraformDeployment(), true) })
	if !strings.Contains(out, "Plan: 1 to add, 0 to change, 1 to destroy.") {
		t.Errorf("the plan text was not printed:\n%s", out)
	}
}

func TestPrintDeploymentStates(t *testing.T) {
	d := terraformDeployment()
	d.Status = "planning"
	d.Terraform.Plan = nil
	if out := captureStdout(t, func() { printDeployment(d, false) }); !strings.Contains(out, "waiting for the controller to plan") {
		t.Errorf("planning:\n%s", out)
	}

	d = terraformDeployment()
	d.Status = "failed"
	d.Terraform.Plan = nil
	d.ErrorMessage = "TFExecPlanFailed: Error: boom\n\n  on main.tf line 3"
	out := captureStdout(t, func() { printDeployment(d, false) })
	if !strings.Contains(out, "Error:\n  TFExecPlanFailed: Error: boom\n  \n    on main.tf line 3") || !strings.Contains(out, "Plan:        none") {
		t.Errorf("failed:\n%s", out)
	}

	d = terraformDeployment()
	d.Terraform.Plan.ToAdd, d.Terraform.Plan.ToDestroy, d.Terraform.Plan.Destroys = 0, 0, false
	if out := captureStdout(t, func() { printDeployment(d, false) }); !strings.Contains(out, "Summary:     no changes") {
		t.Errorf("no changes:\n%s", out)
	}

	// A manifest deployment has no terraform section
	manifest := &client.Deployment{ID: "dep-2", AppName: "hello", VersionID: "v1", Environment: "staging", Status: "success", StartedAt: time.Now()}
	if out := captureStdout(t, func() { printDeployment(manifest, false) }); strings.Contains(out, "Terraform") {
		t.Errorf("manifest deployment:\n%s", out)
	}
}
