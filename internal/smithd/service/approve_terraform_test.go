package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

// asUser is a request of a signed-in user
func asUser(login string) context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindUser, Login: login})
}

// awaiting deploys a version and polls until its plan waits for approval. The
// plan is the fixture's (with destroys) unless planText is set.
func (f *trackFixture) awaiting(t *testing.T, versionID string) (*models.Deployment, terraformtest.CR) {
	t.Helper()
	deployment := f.deployed(t, versionID)
	cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	return deployment, cr
}

func (f *trackFixture) approve(ctx context.Context, id string, confirmDestroy bool) (*models.DeploymentDetail, error) {
	return f.svc.ApproveTerraform(ctx, id, confirmDestroy)
}

func TestApproveTerraform(t *testing.T) {
	f := newTrackFixture(t)
	deployment, cr := f.awaiting(t, "tf1")
	planID := deployment.Terraform.PlanID
	f.slack.waitFor(t, 3)

	// The plan destroys resources: confirm them first, and be told which
	_, err := f.approve(asUser("alice"), deployment.ID, false)
	requireCode(t, err, KindInvalid, "destroy_confirmation_required")
	requireMessage(t, err.Error(), "2 to destroy", "destroy: terraform_data.with_secret", "replace (destroy and create again): terraform_data.rep", "--confirm-destroy")
	if len(f.tfGitops.Approvals()) != 0 {
		t.Fatal("an approval without confirmation was committed")
	}
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

	detail, err := f.approve(asUser("alice"), deployment.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	approvals := f.tfGitops.Approvals()
	if len(approvals) != 1 || approvals[0].deploymentID != deployment.ID || approvals[0].planID != planID || !strings.Contains(approvals[0].message, "approved by alice") {
		t.Fatalf("approvals = %+v, want one of %s", approvals, planID)
	}
	run := detail.Terraform
	if detail.Status != models.DeploymentApplying || detail.CompletedAt != nil || run.ApprovedBy != "alice" || run.ApprovedAt == nil || !run.DestroyConfirmed || run.Plan == nil || run.ApprovalResetAt != nil {
		t.Errorf("approved deployment: %+v %+v", detail, run)
	}
	if got := f.eventNamesPublished(); got[len(got)-1] != string(events.EventDeploymentApproved) {
		t.Errorf("events %v", got)
	}
	if check := f.check(t, "deploysmith/deploy-production", deployment.ID); check.Status != "in_progress" || check.Title != "Applying tf1 to production" {
		t.Errorf("deploy check: %+v", check)
	}

	// Approving again, also once the controller is on it, is fine and
	// commits nothing more
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved())
	f.poll()
	again, err := f.approve(asUser("bob"), deployment.ID, true)
	if err != nil || again.Terraform.ApprovedBy != "alice" || len(f.tfGitops.Approvals()) != 1 {
		t.Fatalf("approving again: %+v, %v (%d approvals)", again, err, len(f.tfGitops.Approvals()))
	}

	// Applied: the approval is reset, once, and recorded
	f.cluster.SetPlan(trackNamespace, trackCR, planID, "hash-1", f.planText, 0)
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	approvals = f.tfGitops.Approvals()
	if len(approvals) != 2 || approvals[1].planID != "" || approvals[1].deploymentID != deployment.ID || !strings.Contains(approvals[1].message, "Reset terraform approval") {
		t.Fatalf("approvals = %+v, want the approval and its reset", approvals)
	}
	if reset := f.get(t, deployment.ID).Terraform.ApprovalResetAt; reset == nil {
		t.Error("the reset was not recorded")
	}
	f.poll()
	f.poll()
	if n := len(f.tfGitops.Approvals()); n != 2 {
		t.Errorf("%d approval commits after polling again, want 2", n)
	}
	if again, err := f.approve(asUser("bob"), deployment.ID, true); err != nil || again.Status != models.DeploymentApplied || len(f.tfGitops.Approvals()) != 2 {
		t.Errorf("approving an applied deployment: %+v, %v", again, err)
	}
}

func TestApproveTerraformWithoutDestroys(t *testing.T) {
	f := newTrackFixture(t)
	text, err := os.ReadFile("../terraform/testdata/plans/add.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.planText = string(text)
	deployment, _ := f.awaiting(t, "tf1")

	// An API key client, and a confirmation that isn't needed
	ctx := auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindAPIKey})
	detail, err := f.approve(ctx, deployment.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Terraform.ApprovedBy != "api-key" || detail.Terraform.DestroyConfirmed || detail.Status != models.DeploymentApplying {
		t.Errorf("unexpected approval: %+v %+v", detail, detail.Terraform)
	}
}

func TestApproveTerraformRefusesStatesThatCantBeApproved(t *testing.T) {
	f := newTrackFixture(t)
	_, err := f.approve(context.Background(), "nope", false)
	requireKind(t, err, KindNotFound)

	deployment := f.deployed(t, "tf1")
	_, err = f.approve(context.Background(), deployment.ID, false)
	requireCode(t, err, KindConflict, "not_awaiting_approval")
	requireMessage(t, err.Error(), "is planning", "wait for the plan")

	for _, status := range []string{models.DeploymentStale, models.DeploymentFailed, models.DeploymentRejected} {
		f.setStatus(t, deployment.ID, status)
		_, err = f.approve(context.Background(), deployment.ID, true)
		requireCode(t, err, KindConflict, "not_awaiting_approval")
		requireMessage(t, err.Error(), "is "+status, "deploy the version again")
	}
	if len(f.tfGitops.Approvals()) != 0 {
		t.Error("something was approved")
	}

	// A manifest deployment has no plan
	manifest, err := f.svc.Deployments.Create(f.viinoID, deployment.VersionID, "production", "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.approve(context.Background(), manifest.ID, false)
	requireCode(t, err, KindInvalid, "not_terraform")
	_, err = f.svc.RejectTerraform(context.Background(), manifest.ID, "")
	requireCode(t, err, KindInvalid, "not_terraform")
}

func TestApproveTerraformRefusesAPlanThatIsNotCurrent(t *testing.T) {
	// bumpGeneration changes the CR after it was planned
	bumpGeneration := func(f *trackFixture) {
		client := f.cluster.Client.Resource(terraform.TerraformGVR).Namespace(trackNamespace)
		obj, err := client.Get(context.Background(), trackCR, metav1.GetOptions{})
		if err != nil {
			panic(err)
		}
		obj.Object["metadata"].(map[string]any)["generation"] = int64(2)
		if _, err := client.Update(context.Background(), obj, metav1.UpdateOptions{}); err != nil {
			panic(err)
		}
	}

	for _, tc := range []struct {
		name   string
		change func(f *trackFixture, d *models.Deployment, cr terraformtest.CR)
		reason string
	}{
		{"another pending plan", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			cr.PendingPlan = "plan-main-ffffffffff"
			f.cluster.SetCR(trackNamespace, trackCR, cr)
		}, `pending plan "plan-main-ffffffffff"`},
		{"another revision", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			cr.PlannedRevision = strings.Repeat("c", 40)
			f.cluster.SetCR(trackNamespace, trackCR, cr)
		}, "planned for revision cccccc"},
		{"changed after it was planned", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) { bumpGeneration(f) }, "generation 2"},
		{"another plan content", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			f.cluster.SetPlan(trackNamespace, trackCR, d.Terraform.PlanID, "hash-2", f.planText+"# drift\n", 0)
		}, "the plan changed after it was made"},
		{"another deployment", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			cr.DeploymentID = "someone-else"
			f.cluster.SetCR(trackNamespace, trackCR, cr)
		}, "belongs to deployment someone-else"},
		{"another approval", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			cr.ApprovePlan = "plan-main-ffffffffff"
			f.cluster.SetCR(trackNamespace, trackCR, cr)
		}, "approves another plan"},
		{"deleted", func(f *trackFixture, d *models.Deployment, cr terraformtest.CR) {
			f.cluster.DeleteCR(trackNamespace, trackCR)
		}, "no longer exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTrackFixture(t)
			deployment, cr := f.awaiting(t, "tf1")
			tc.change(f, deployment, cr)

			_, err := f.approve(asUser("alice"), deployment.ID, true)
			requireCode(t, err, KindConflict, "plan_not_current")
			requireMessage(t, err.Error(), tc.reason, "Deploy the version again")
			if len(f.tfGitops.Approvals()) != 0 {
				t.Error("a plan that is not current was approved")
			}
			f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
		})
	}

	t.Run("the file in git belongs to a newer deployment", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment, _ := f.awaiting(t, "tf1")
		f.tfGitops.roots["viino/production/main"].deploymentID = "newer"
		_, err := f.approve(asUser("alice"), deployment.ID, true)
		requireCode(t, err, KindConflict, "plan_not_current")
		f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	})

	t.Run("a failing push is an internal error and can be repeated", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment, _ := f.awaiting(t, "tf1")
		f.tfGitops.approvalErr = errors.New("push rejected")
		_, err := f.approve(asUser("alice"), deployment.ID, true)
		if _, ok := AsError(err); ok || err == nil {
			t.Fatalf("err = %v, want an internal error", err)
		}
		f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

		f.tfGitops.approvalErr = nil
		if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
			t.Fatal(err)
		}
		f.requireStatus(t, deployment.ID, models.DeploymentApplying)
	})
}

func TestApproveTerraformReplicasApproveOnce(t *testing.T) {
	f := newTrackFixture(t)
	deployment, _ := f.awaiting(t, "tf1")

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		replica := New(f.svc.Deps)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = replica.ApproveTerraform(asUser("alice"), deployment.ID, true)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := len(f.tfGitops.Approvals()); n != 1 {
		t.Errorf("%d approval commits, want 1", n)
	}
	f.requireStatus(t, deployment.ID, models.DeploymentApplying)
}

func TestRejectTerraform(t *testing.T) {
	f := newTrackFixture(t)
	deployment, _ := f.awaiting(t, "tf1")
	f.slack.waitFor(t, 3)

	detail, err := f.svc.RejectTerraform(asUser("alice"), deployment.ID, "  too many destroys ")
	if err != nil {
		t.Fatal(err)
	}
	run := detail.Terraform
	if detail.Status != models.DeploymentRejected || detail.CompletedAt == nil || run.RejectedBy != "alice" || run.RejectedAt == nil || run.RejectReason != "too many destroys" ||
		detail.ErrorMessage != "Rejected by alice: too many destroys" || run.ApprovedBy != "" || run.Plan == nil {
		t.Errorf("rejected deployment: %+v %+v", detail, run)
	}
	// Nothing is committed: the CR stays unapproved
	if len(f.tfGitops.Approvals()) != 0 || f.tfGitops.roots["viino/production/main"].approvePlan != "" {
		t.Error("rejecting changed the CR")
	}
	if got := f.eventNamesPublished(); got[len(got)-1] != string(events.EventDeploymentRejected) {
		t.Errorf("events %v", got)
	}

	// Slack, and the check runs are closed
	messages := f.slack.waitFor(t, 4)
	requireMessage(t, messages[3], "Plan rejected", "`viino` `tf1` → `production`", "by alice", "too many destroys")
	for _, name := range []string{"deploysmith/deploy-production", "deploysmith/plan-production"} {
		if check := f.check(t, name, deployment.ID); check.Status != "completed" || check.Conclusion != "neutral" || !strings.Contains(check.Title, "rejected") {
			t.Errorf("%s: %+v", name, check)
		}
	}

	// The same again is fine, anything else is not; it can't be approved
	again, err := f.svc.RejectTerraform(asUser("bob"), deployment.ID, "")
	if err != nil || again.Terraform.RejectedBy != "alice" {
		t.Errorf("rejecting again: %+v, %v", again, err)
	}
	f.slack.waitFor(t, 4)
	_, err = f.approve(asUser("bob"), deployment.ID, true)
	requireCode(t, err, KindConflict, "not_awaiting_approval")
	requireMessage(t, err.Error(), "Rejected by alice")

	// A new deploy goes on as usual
	next := f.deployed(t, "tf2")
	f.requireStatus(t, next.ID, models.DeploymentPlanning)
}

func TestRejectTerraformRefusals(t *testing.T) {
	f := newTrackFixture(t)
	deployment, cr := f.awaiting(t, "tf1")

	_, err := f.svc.RejectTerraform(asUser("alice"), deployment.ID, strings.Repeat("x", maxRejectReason+1))
	requireCode(t, err, KindInvalid, "invalid_request")

	// Approved by hand in git: it will be applied, so it can't be rejected
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved())
	_, err = f.svc.RejectTerraform(asUser("alice"), deployment.ID, "")
	requireCode(t, err, KindConflict, "conflict")
	requireMessage(t, err.Error(), "approved in git")
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

	// Approved through smithd
	f.cluster.SetCR(trackNamespace, trackCR, cr)
	if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.RejectTerraform(asUser("alice"), deployment.ID, "")
	requireCode(t, err, KindConflict, "not_awaiting_approval")
}

func TestResetApprovalIsRetriedAndSurvivesARestart(t *testing.T) {
	f := newTrackFixture(t)
	deployment, cr := f.awaiting(t, "tf1")
	if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
		t.Fatal(err)
	}
	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-1", f.planText, 0)
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))

	// The push of the reset fails: applied, but still to reset
	f.tfGitops.approvalErr = errors.New("push rejected")
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	if f.get(t, deployment.ID).Terraform.ApprovalResetAt != nil || len(f.tfGitops.Approvals()) != 1 {
		t.Fatal("the reset was recorded though its push failed")
	}

	// Another smithd process picks it up later, from the database alone
	f.tfGitops.approvalErr = nil
	New(f.svc.Deps).pollTerraform(context.Background())
	if approvals := f.tfGitops.Approvals(); len(approvals) != 2 || approvals[1].planID != "" {
		t.Fatalf("approvals = %+v", approvals)
	}
	if f.get(t, deployment.ID).Terraform.ApprovalResetAt == nil {
		t.Error("the reset was not recorded")
	}
}

func TestResetApprovalConcurrentReplicasCommitOnce(t *testing.T) {
	f := newTrackFixture(t)
	deployment, cr := f.awaiting(t, "tf1")
	if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
		t.Fatal(err)
	}
	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-1", f.planText, 0)
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))

	var wg sync.WaitGroup
	for range 3 {
		replica := New(f.svc.Deps)
		wg.Add(1)
		go func() {
			defer wg.Done()
			replica.pollTerraform(context.Background())
		}()
	}
	wg.Wait()
	if n := len(f.tfGitops.Approvals()); n != 2 {
		t.Errorf("%d approval commits, want the approval and one reset", n)
	}
}

func TestResetApprovalAfterAFailedApplyAndAfterANewerDeploy(t *testing.T) {
	t.Run("failed apply", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment, cr := f.awaiting(t, "tf1")
		if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
			t.Fatal(err)
		}
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Failed("TerraformApplyFailed", "boom"))
		f.poll()
		f.requireStatus(t, deployment.ID, models.DeploymentFailed)
		if approvals := f.tfGitops.Approvals(); len(approvals) != 2 || approvals[1].planID != "" {
			t.Errorf("approvals = %+v, want the approval reset", approvals)
		}
	})

	t.Run("the root was deployed again", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment, cr := f.awaiting(t, "tf1")
		if _, err := f.approve(asUser("alice"), deployment.ID, true); err != nil {
			t.Fatal(err)
		}
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))
		// A deploy that rewrote the file (approvePlan "") before smithd could reset
		f.tfGitops.roots["viino/production/main"].deploymentID = "newer"
		f.poll()
		f.requireStatus(t, deployment.ID, models.DeploymentApplied)
		if n := len(f.tfGitops.Approvals()); n != 1 {
			t.Errorf("%d approval commits, want only the approval", n)
		}
		if f.get(t, deployment.ID).Terraform.ApprovalResetAt == nil {
			t.Error("there was nothing to reset, which should be recorded")
		}
	})

	t.Run("approved in git", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment, cr := f.awaiting(t, "tf1")
		f.tfGitops.roots["viino/production/main"].approvePlan = deployment.Terraform.PlanID
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved())
		f.poll()
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))
		f.poll()
		applied := f.requireStatus(t, deployment.ID, models.DeploymentApplied)
		if applied.Terraform.ApprovedBy != "git" {
			t.Errorf("approved by %q", applied.Terraform.ApprovedBy)
		}
		if approvals := f.tfGitops.Approvals(); len(approvals) != 1 || approvals[0].planID != "" {
			t.Errorf("approvals = %+v, want only the reset", approvals)
		}
	})

	t.Run("never approved", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment := f.deployed(t, "tf1")
		cr := terraformtest.WithoutChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, plannedAt(1))
		f.cluster.SetCR(trackNamespace, trackCR, cr)
		f.poll()
		f.requireStatus(t, deployment.ID, models.DeploymentApplied)
		if n := len(f.tfGitops.Approvals()); n != 0 {
			t.Errorf("%d approval commits for a plan without changes", n)
		}
	})
}

func TestTerraformRollbackLabel(t *testing.T) {
	f := newTrackFixture(t)
	older := f.version(t, "tf1")
	newer := f.version(t, "tf2")
	// Order the versions, whatever the clock says
	for id, at := range map[string]time.Time{older.ID: time.Now().Add(-2 * time.Hour), newer.ID: time.Now().Add(-time.Hour)} {
		if _, err := f.db.Exec(`UPDATE versions SET created_at = ? WHERE id = ?`, at.UTC(), id); err != nil {
			t.Fatal(err)
		}
	}

	// The first deploy, of the newer version, is no rollback; once applied
	// it is what runs
	first, err := f.deploy("tf2")
	if err != nil {
		t.Fatal(err)
	}
	if first.Terraform.Rollback || f.get(t, first.ID).Terraform.Rollback {
		t.Error("the first deploy is labelled a rollback")
	}
	f.setStatus(t, first.ID, models.DeploymentApplied)

	// Deploying the older version is a normal deploy with a new plan, labelled
	second, err := f.deploy("tf1")
	if err != nil {
		t.Fatal(err)
	}
	stored := f.get(t, second.ID)
	if !second.Terraform.Rollback || !stored.Terraform.Rollback || stored.Status != models.DeploymentPlanning {
		t.Errorf("rollback deploy: %+v %+v", stored, stored.Terraform)
	}
	detail, err := f.svc.GetDeployment(second.ID)
	if err != nil || !detail.Terraform.Rollback {
		t.Errorf("detail = %+v, %v", detail, err)
	}
	f.setStatus(t, second.ID, models.DeploymentApplied)

	// Back to the newer version is not a rollback
	third, err := f.deploy("tf2")
	if err != nil || third.Terraform.Rollback || f.get(t, third.ID).Terraform.Rollback {
		t.Errorf("deploying forward: %+v, %v", third, err)
	}
}
