package service

import (
	"context"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// pendingDeployment creates a deployment to production that has been pending
// since the given time, as if smithd died while deploying it
func (f *fixture) pendingDeployment(t *testing.T, policyID *string, startedAgo time.Duration) *models.Deployment {
	t.Helper()
	triggeredBy := "alice@example.com"
	if policyID != nil {
		triggeredBy = "auto-deploy"
	}
	deployment, err := f.svc.Deployments.Create(f.app.ID, f.version.ID, "production", triggeredBy, policyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE deployments SET started_at = ? WHERE id = ?`, time.Now().UTC().Add(-startedAgo), deployment.ID); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func (f *fixture) policy(t *testing.T) *string {
	t.Helper()
	policy, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "main-to-prod", GitBranchPattern: "main", TargetEnvironment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	return &policy.ID
}

// waitForStatus waits for a deployment to leave pending, since resumed
// deployments run in the background
func (f *fixture) waitForStatus(t *testing.T, id string) *models.DeploymentDetail {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		deployment, err := f.svc.Deployments.GetDetailed(id)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status != "pending" || time.Now().After(deadline) {
			return deployment
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRecoverStaleDeployments(t *testing.T) {
	t.Run("resumes interrupted auto-deploy", func(t *testing.T) {
		f := newFixture(t)
		deployment := f.pendingDeployment(t, f.policy(t), 20*time.Minute)

		f.svc.recoverStaleDeployments(context.Background())

		if got := f.waitForStatus(t, deployment.ID); got.Status != "success" {
			t.Errorf("status = %s (%s), want success", got.Status, got.ErrorMessage)
		}
		if calls := f.gitops.Calls(); len(calls) != 1 {
			t.Errorf("expected one gitops deploy, got %+v", calls)
		}
	})

	t.Run("fails manual deploy", func(t *testing.T) {
		f := newFixture(t)
		deployment := f.pendingDeployment(t, nil, 20*time.Minute)

		f.svc.recoverStaleDeployments(context.Background())

		if got := f.waitForStatus(t, deployment.ID); got.Status != "failed" {
			t.Errorf("status = %s, want failed", got.Status)
		}
		if calls := f.gitops.Calls(); len(calls) != 0 {
			t.Errorf("manual deploy was resumed: %+v", calls)
		}
	})

	t.Run("fails auto-deploy interrupted too long ago", func(t *testing.T) {
		f := newFixture(t)
		deployment := f.pendingDeployment(t, f.policy(t), 2*time.Hour)

		f.svc.recoverStaleDeployments(context.Background())

		if got := f.waitForStatus(t, deployment.ID); got.Status != "failed" {
			t.Errorf("status = %s, want failed", got.Status)
		}
		if calls := f.gitops.Calls(); len(calls) != 0 {
			t.Errorf("old auto-deploy was resumed: %+v", calls)
		}
	})

	t.Run("fails superseded auto-deploy and resumes the newer one", func(t *testing.T) {
		f := newFixture(t)
		policyID := f.policy(t)
		older := f.pendingDeployment(t, policyID, 30*time.Minute)
		newer := f.pendingDeployment(t, policyID, 20*time.Minute)

		f.svc.recoverStaleDeployments(context.Background())

		if got := f.waitForStatus(t, older.ID); got.Status != "failed" {
			t.Errorf("older status = %s, want failed", got.Status)
		}
		if got := f.waitForStatus(t, newer.ID); got.Status != "success" {
			t.Errorf("newer status = %s (%s), want success", got.Status, got.ErrorMessage)
		}
		if calls := f.gitops.Calls(); len(calls) != 1 {
			t.Errorf("expected only the newer deployment to deploy, got %+v", calls)
		}
	})

	t.Run("fails auto-deploy superseded by a completed deployment", func(t *testing.T) {
		f := newFixture(t)
		deployment := f.pendingDeployment(t, f.policy(t), 30*time.Minute)
		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "alice@example.com"); err != nil {
			t.Fatal(err)
		}

		f.svc.recoverStaleDeployments(context.Background())

		if got := f.waitForStatus(t, deployment.ID); got.Status != "failed" {
			t.Errorf("status = %s, want failed", got.Status)
		}
		if calls := f.gitops.Calls(); len(calls) != 1 {
			t.Errorf("superseded deployment was resumed: %+v", calls)
		}
	})
}
