package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestPrintDeploymentDecisions(t *testing.T) {
	at := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)

	// Waiting: how to decide, with the destroy confirmation
	waiting := terraformDeployment()
	waiting.Terraform.Rollback = true
	out := captureStdout(t, func() { printDeployment(waiting, false) })
	for _, want := range []string{"Rollback:      yes", "smithctl deployment approve dep-1 --confirm-destroy", "smithctl deployment reject dep-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}

	approved := terraformDeployment()
	approved.Status = "applied"
	approved.Terraform.ApprovedBy, approved.Terraform.ApprovedAt, approved.Terraform.DestroyConfirmed = "alice", &at, true
	out = captureStdout(t, func() { printDeployment(approved, false) })
	for _, want := range []string{"Approved by: alice at ", "(confirmed the destroys)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "smithctl deployment approve") {
		t.Errorf("an applied deployment is offered for approval:\n%s", out)
	}

	rejected := terraformDeployment()
	rejected.Status = "rejected"
	rejected.Terraform.RejectedBy, rejected.Terraform.RejectedAt, rejected.Terraform.RejectReason = "bob", &at, "wrong env"
	out = captureStdout(t, func() { printDeployment(rejected, false) })
	for _, want := range []string{"Rejected by: bob at ", "Reason:      wrong env"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}
