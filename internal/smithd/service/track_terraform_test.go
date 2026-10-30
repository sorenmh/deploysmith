package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/github"
	"github.com/sorenmh/deploysmith/internal/smithd/github/githubtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

const (
	trackNamespace = "tf-viino"
	trackCR        = "production-main"
)

// trackFixture is a terraform deployment fixture with a fake cluster, Slack
// and GitHub, to follow runs
type trackFixture struct {
	*terraformDeployFixture
	cluster *terraformtest.Cluster
	slack   *slackRecorder
	gh      *githubtest.Server
	// planText is the readable plan of the fake controller: a plan that
	// adds, changes, replaces and destroys (3 to add, 1 to change, 2 to destroy)
	planText string
}

func newTrackFixture(t *testing.T) *trackFixture {
	t.Helper()
	f := &trackFixture{terraformDeployFixture: newTerraformDeployFixture(t), cluster: terraformtest.New(t)}
	f.svc.TerraformPlans = store.NewTerraformPlanStore(f.db)
	f.svc.TerraformReader = f.cluster.Reader()
	f.svc.UIBaseURL = "https://ui.example.com"

	f.slack, f.svc.Slack = newSlackRecorder(t)

	f.gh = githubtest.New(t)
	f.gh.Install("acme/infra")
	reporter, err := github.NewReporter(githubtest.AppID, f.gh.PrivateKey, f.gh.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.GitHub = reporter

	text, err := os.ReadFile("../terraform/testdata/plans/mixed.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.planText = string(text)
	return f
}

// deployed deploys a terraform version built from acme/infra and returns the
// planning deployment
func (f *trackFixture) deployed(t *testing.T, versionID string) *models.Deployment {
	t.Helper()
	version := f.version(t, versionID)
	if err := f.svc.Versions.SetSource(version.ID, models.VersionSource{Repository: "acme/infra", CommitSHA: sourceSHA, PullRequest: 3}); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.deploy(versionID)
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}

func (f *trackFixture) poll() { f.svc.pollTerraform(context.Background()) }

func (f *trackFixture) get(t *testing.T, id string) *models.Deployment {
	t.Helper()
	deployment, err := f.svc.Deployments.GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}

func (f *trackFixture) requireStatus(t *testing.T, id, status string) *models.Deployment {
	t.Helper()
	deployment := f.get(t, id)
	if deployment.Status != status {
		t.Fatalf("deployment is %s (%q), want %s", deployment.Status, deployment.ErrorMessage, status)
	}
	return deployment
}

// plannedAt makes the controller's lastPlanAt of the n-th plan
func plannedAt(n int) time.Time {
	return time.Date(2026, 10, 29, 12, n, 0, 0, time.UTC)
}

// withChanges puts a plan with changes for the deployment in the cluster
func (f *trackFixture) withChanges(d *models.Deployment, at time.Time, hash, text string) terraformtest.CR {
	cr := terraformtest.WithChanges(d.ID, d.Terraform.SourceCommitSHA, d.Terraform.PlanID, at)
	f.cluster.SetCR(trackNamespace, trackCR, cr)
	f.cluster.SetPlan(trackNamespace, trackCR, d.Terraform.PlanID, hash, text, 0)
	return cr
}

// check returns the check run of a deployment with a name
func (f *trackFixture) check(t *testing.T, name, deploymentID string) githubtest.CheckRun {
	t.Helper()
	for _, run := range f.gh.CheckRuns() {
		if run.Name == name && run.ExternalID == deploymentID {
			return run
		}
	}
	t.Fatalf("no check run %s for %s in %+v", name, deploymentID, f.gh.CheckRuns())
	return githubtest.CheckRun{}
}

func (f *trackFixture) eventTypes() []events.EventType {
	var types []events.EventType
	for _, e := range f.svc.Publisher.(*events.MemoryPublisher).Events() {
		types = append(types, e.Type)
	}
	return types
}

func TestTrackTerraformPlanAndApply(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	planID := deployment.Terraform.PlanID

	// Requested, and committed with the plan to come
	messages := f.slack.waitFor(t, 2)
	requireMessage(t, messages[1], "Committed to gitops", "Waiting for the plan")
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Status != "in_progress" || !strings.HasPrefix(run.Title, "Planning tf1") {
		t.Errorf("deploy check after the commit: %+v", run)
	}
	if run := f.check(t, "deploysmith/plan-production", deployment.ID); run.Status != "in_progress" {
		t.Errorf("plan check after the commit: %+v", run)
	}

	// Nothing there yet, then planning: still planning, nothing reported
	f.poll()
	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.Reconciling("someone-else", strings.Repeat("a", 40)))
	f.poll()
	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.Reconciling(deployment.ID, deployment.Terraform.SourceCommitSHA))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)
	f.slack.waitFor(t, 2)

	// The plan is ready
	at := plannedAt(1)
	cr := f.withChanges(deployment, at, "hash-1", f.planText)
	f.poll()
	stored := f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	if stored.CompletedAt != nil {
		t.Error("a deployment waiting for approval is complete")
	}

	plan, err := f.svc.TerraformPlans.Get(deployment.ID)
	if err != nil || plan == nil {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if plan.PlanID != planID || plan.PlanHash != "hash-1" || plan.PlannedAt == nil || !plan.PlannedAt.Equal(at) || plan.Text != f.planText {
		t.Errorf("unexpected plan %+v", plan)
	}
	if plan.ToAdd != 3 || plan.ToChange != 1 || plan.ToDestroy != 2 || !plan.Destroys || !plan.OutputChanges {
		t.Errorf("unexpected counts %+v", plan)
	}
	if got := strings.Join(plan.Resources.Replace, ",") + "|" + strings.Join(plan.Resources.Destroy, ","); got != "terraform_data.rep|terraform_data.with_secret" {
		t.Errorf("resources %+v", plan.Resources)
	}

	// Slack, and GitHub: the plan check is complete, the deploy check goes on
	messages = f.slack.waitFor(t, 3)
	requireMessage(t, messages[2], "Plan ready for approval", "`viino` `tf1` → `production`", "3 to add, 1 to change, 2 to destroy",
		"destroys resources", "terraform_data.with_secret", "terraform_data.rep", "<https://ui.example.com/ui/deployments/"+deployment.ID+"|View deployment>")
	planCheck := f.check(t, "deploysmith/plan-production", deployment.ID)
	if planCheck.Status != "completed" || planCheck.Conclusion != "neutral" || planCheck.HeadSHA != sourceSHA ||
		planCheck.Title != "3 to add, 1 to change, 2 to destroy (destroys resources)" ||
		planCheck.DetailsURL != "https://ui.example.com/ui/deployments/"+deployment.ID {
		t.Errorf("plan check: %+v", planCheck)
	}
	requireMessage(t, planCheck.Summary, "**Plan: 3 to add, 1 to change, 2 to destroy**", "This plan destroys resources",
		"- `terraform_data.with_secret`", "- `terraform_data.rep`", "- `terraform_data.new`", "- `terraform_data.web`")
	if strings.Contains(planCheck.Summary+planCheck.Text, "hunter2") || strings.Contains(planCheck.Summary+planCheck.Text, "resource \"terraform_data\"") {
		t.Error("the plan text was sent to GitHub")
	}
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Status != "in_progress" || run.Title != "Plan for production waits for approval" {
		t.Errorf("deploy check: %+v", run)
	}

	// Polling again changes nothing and reports nothing again
	f.poll()
	f.slack.waitFor(t, 3)
	if types := f.eventTypes(); strings.Count(string(types[len(types)-1]), "plan_ready") != 1 {
		t.Errorf("events %v", types)
	}

	// Approved (DEP-20 sets applying): applying until the controller is done
	if moved, err := f.svc.Deployments.TransitionTerraform(deployment.ID, models.DeploymentAwaitingApproval, models.DeploymentApplying, ""); err != nil || !moved {
		t.Fatal(moved, err)
	}
	approved := cr.Approved()
	f.cluster.SetCR(trackNamespace, trackCR, approved)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplying)

	// The plan the controller makes again before applying has the same content
	f.cluster.SetPlan(trackNamespace, trackCR, planID, "hash-1", f.planText, 0)
	f.cluster.SetCR(trackNamespace, trackCR, approved.Applied(deployment.Terraform.SourceCommitSHA))
	f.poll()
	applied := f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	if applied.CompletedAt == nil || applied.ErrorMessage != "" {
		t.Errorf("applied deployment: %+v", applied)
	}
	plan, _ = f.svc.TerraformPlans.Get(deployment.ID)
	if plan.AppliedDiffered || plan.AppliedPlanHash != "hash-1" {
		t.Errorf("applied plan: %+v", plan)
	}

	messages = f.slack.waitFor(t, 4)
	requireMessage(t, messages[3], "Applied", "`viino` `tf1` → `production`", "3 to add, 1 to change, 2 to destroy")
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Status != "completed" || run.Conclusion != "success" || run.Title != "Applied tf1 to production" {
		t.Errorf("deploy check: %+v", run)
	}
	if run := f.check(t, "deploysmith/plan-production", deployment.ID); run.Conclusion != "success" || !strings.HasPrefix(run.Title, "Applied: 3 to add") {
		t.Errorf("plan check: %+v", run)
	}

	// It is what runs in the environment now
	current, err := f.svc.Deployments.LatestSuccessPerTarget()
	if err != nil || len(current) != 1 || current[0].ID != deployment.ID {
		t.Errorf("current = %+v, %v", current, err)
	}
	want := []events.EventType{events.EventDeploymentStarted, events.EventDeploymentPlanReady, events.EventDeploymentSucceeded}
	if got := f.eventTypes(); strings.Join(eventNames(got), ",") != strings.Join(eventNames(want), ",") {
		t.Errorf("events %v, want %v", got, want)
	}

	// Finished deployments are left alone
	f.poll()
	f.slack.waitFor(t, 4)
}

func eventNames(types []events.EventType) []string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return names
}

func TestTrackTerraformWaitsForThePlanOfTheDeployment(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	at := plannedAt(1)

	// The CR of this deployment, but the readable plan is of an older plan
	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.WithChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, deployment.Terraform.PlanID, at))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)
	f.cluster.SetPlan(trackNamespace, trackCR, "plan-main-aaaaaaaaaa", "old", "Plan: 9 to add, 0 to change, 0 to destroy.\n", 0)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)
	if plan, _ := f.svc.TerraformPlans.Get(deployment.ID); plan != nil {
		t.Errorf("stored the plan of another run: %+v", plan)
	}

	// A plan with another pending id is not this deployment's plan
	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-1", f.planText, 0)
	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.WithChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, "plan-main-bbbbbbbbbb-other", at))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)

	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.WithChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, deployment.Terraform.PlanID, at))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
}

func TestTrackTerraformNoChanges(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")

	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.WithoutChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, plannedAt(1)))
	f.poll()

	applied := f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	if applied.CompletedAt == nil {
		t.Error("not completed")
	}
	// Even without a readable plan there is a record
	plan, err := f.svc.TerraformPlans.Get(deployment.ID)
	if err != nil || plan == nil || plan.ToAdd != 0 || plan.Destroys || !strings.Contains(plan.Text, "No changes") {
		t.Errorf("plan = %+v, %v", plan, err)
	}

	messages := f.slack.waitFor(t, 3)
	requireMessage(t, messages[2], "Applied", "no changes")
	run := f.check(t, "deploysmith/plan-production", deployment.ID)
	if run.Conclusion != "success" || run.Title != "No changes" || !strings.Contains(run.Summary, "No changes") {
		t.Errorf("plan check: %+v", run)
	}
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Conclusion != "success" {
		t.Errorf("deploy check: %+v", run)
	}
}

func TestTrackTerraformNoChangesWithReadablePlan(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")

	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-1", "\nNo changes. Your infrastructure matches the configuration.\n", 0)
	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.WithoutChanges(deployment.ID, deployment.Terraform.SourceCommitSHA, plannedAt(1)))
	f.poll()

	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	plan, _ := f.svc.TerraformPlans.Get(deployment.ID)
	if plan.PlanHash != "hash-1" || !strings.Contains(plan.Text, "No changes") {
		t.Errorf("plan = %+v", plan)
	}
}

func TestTrackTerraformFailedPlan(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	at := plannedAt(1)

	f.cluster.SetCR(trackNamespace, trackCR, terraformtest.Reconciling(deployment.ID, deployment.Terraform.SourceCommitSHA).
		Failed("TFExecPlanFailed", "Error: Invalid provider configuration\n\n  on main.tf line 3"))
	f.cluster.AddEvent(trackNamespace, trackCR, "Normal", "TerraformInit", "Initialised", at)
	f.cluster.AddEvent(trackNamespace, trackCR, "Warning", "TFExecPlanFailed", "tofu plan failed: exit status 1", at.Add(time.Second))
	f.cluster.AddEvent(trackNamespace, trackCR, "Warning", "TFExecPlanFailed", "Error: Invalid provider configuration", at.Add(2*time.Second))
	f.poll()

	failed := f.requireStatus(t, deployment.ID, models.DeploymentFailed)
	if failed.CompletedAt == nil {
		t.Error("not completed")
	}
	// The condition, and the events it doesn't already say
	requireMessage(t, failed.ErrorMessage, "TFExecPlanFailed: Error: Invalid provider configuration", "on main.tf line 3",
		"Events of the Terraform CR:", "- TFExecPlanFailed: tofu plan failed: exit status 1")
	if strings.Contains(failed.ErrorMessage, "Initialised") || strings.Count(failed.ErrorMessage, "Invalid provider configuration") != 1 {
		t.Errorf("unexpected error message %q", failed.ErrorMessage)
	}

	messages := f.slack.waitFor(t, 3)
	requireMessage(t, messages[2], "*Deployment failed* (plan)", "`viino` `tf1` → `production`", "> TFExecPlanFailed: Error: Invalid provider configuration", "\n> \n>   on main.tf line 3")
	deploy := f.check(t, "deploysmith/deploy-production", deployment.ID)
	if deploy.Conclusion != "failure" || deploy.Title != "Deployment to production failed (plan)" || !strings.Contains(deploy.Text, "Invalid provider configuration") {
		t.Errorf("deploy check: %+v", deploy)
	}
	if run := f.check(t, "deploysmith/plan-production", deployment.ID); run.Conclusion != "failure" || run.Title != "Plan failed" {
		t.Errorf("plan check: %+v", run)
	}
	if got := f.eventTypes(); got[len(got)-1] != events.EventDeploymentFailed {
		t.Errorf("events %v", got)
	}

	// The next deploy of the root is allowed again
	f.version(t, "tf2")
	if _, err := f.deploy("tf2"); err != nil {
		t.Fatal(err)
	}
}

func TestTrackTerraformFailedApply(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	if _, err := f.svc.Deployments.TransitionTerraform(deployment.ID, models.DeploymentAwaitingApproval, models.DeploymentApplying, ""); err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("x", 30000)
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Failed("TFExecApplyFailed", "Error: AccessDenied\n"+long))
	f.poll()

	failed := f.requireStatus(t, deployment.ID, models.DeploymentFailed)
	if !strings.HasPrefix(failed.ErrorMessage, "TFExecApplyFailed: Error: AccessDenied") || len(failed.ErrorMessage) > maxRunError || !strings.HasSuffix(failed.ErrorMessage, "(truncated)") {
		t.Errorf("error message of %d bytes: %.80q", len(failed.ErrorMessage), failed.ErrorMessage)
	}
	messages := f.slack.waitFor(t, 4)
	requireMessage(t, messages[3], "*Deployment failed* (apply)")
	if len(messages[3]) > maxNoticeError+500 {
		t.Errorf("the Slack message has %d bytes", len(messages[3]))
	}
	// The plan check keeps what the plan was; only the deployment failed
	if run := f.check(t, "deploysmith/plan-production", deployment.ID); run.Conclusion != "neutral" {
		t.Errorf("plan check: %+v", run)
	}
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Conclusion != "failure" || run.Title != "Deployment to production failed (apply)" {
		t.Errorf("deploy check: %+v", run)
	}
}

func TestTrackTerraformApprovedInGit(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

	// Someone commits approvePlan by hand
	approved := cr.Approved()
	f.cluster.SetCR(trackNamespace, trackCR, approved)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplying)
	if run := f.check(t, "deploysmith/deploy-production", deployment.ID); run.Status != "in_progress" || run.Title != "Applying tf1 to production" {
		t.Errorf("deploy check: %+v", run)
	}

	// The plan made again right before applying differs from the reviewed one
	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-2", f.planText+"# drift\n", 0)
	f.cluster.SetCR(trackNamespace, trackCR, approved.Applied(deployment.Terraform.SourceCommitSHA))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	plan, _ := f.svc.TerraformPlans.Get(deployment.ID)
	if !plan.AppliedDiffered || plan.AppliedPlanHash != "hash-2" || plan.PlanHash != "hash-1" || plan.Text != f.planText {
		t.Errorf("plan = %+v", plan)
	}
}

func TestTrackTerraformStale(t *testing.T) {
	t.Run("replaced by a newer deploy", func(t *testing.T) {
		f := newTrackFixture(t)
		first := f.deployed(t, "tf1")
		f.withChanges(first, plannedAt(1), "hash-1", f.planText)
		f.poll()
		f.requireStatus(t, first.ID, models.DeploymentAwaitingApproval)
		f.slack.waitFor(t, 3)

		f.version(t, "tf2")
		second, err := f.deploy("tf2")
		if err != nil {
			t.Fatal(err)
		}
		stale := f.requireStatus(t, first.ID, models.DeploymentStale)
		if stale.ErrorMessage != "Replaced by deployment "+second.ID {
			t.Errorf("error message %q", stale.ErrorMessage)
		}

		// The checks of the replaced deployment are closed, with its plan
		deploy := f.check(t, "deploysmith/deploy-production", first.ID)
		plan := f.check(t, "deploysmith/plan-production", first.ID)
		if deploy.Status != "completed" || deploy.Conclusion != "neutral" || !strings.Contains(deploy.Summary, "replaced by deployment "+second.ID) ||
			plan.Conclusion != "neutral" || plan.Title != "Plan is stale" {
			t.Errorf("deploy check %+v, plan check %+v", deploy, plan)
		}

		// Only the new deployment is followed, whatever the CR says
		f.poll()
		f.requireStatus(t, first.ID, models.DeploymentStale)
		f.requireStatus(t, second.ID, models.DeploymentPlanning)
	})

	t.Run("the CR belongs to a newer deploy", func(t *testing.T) {
		f := newTrackFixture(t)
		first := f.deployed(t, "tf1")
		f.withChanges(first, plannedAt(1), "hash-1", f.planText)
		f.poll()
		f.requireStatus(t, first.ID, models.DeploymentAwaitingApproval)

		// Another deployment, made by another smithd, took over the CR. Its
		// record replaced ours; put ours back as that smithd would not have.
		newer, _, err := f.svc.Deployments.CreateTerraform(f.viinoID, f.version(t, "tf3").ID, "production", "main", "bob")
		if err != nil {
			t.Fatal(err)
		}
		f.setStatus(t, newer.ID, models.DeploymentFailed)
		f.setStatus(t, first.ID, models.DeploymentAwaitingApproval)
		f.cluster.SetCR(trackNamespace, trackCR, terraformtest.Reconciling(newer.ID, strings.Repeat("d", 40)))
		f.poll()
		stale := f.requireStatus(t, first.ID, models.DeploymentStale)
		requireMessage(t, stale.ErrorMessage, "Stale:", newer.ID)
	})

	t.Run("an applying run is never replaced", func(t *testing.T) {
		f := newTrackFixture(t)
		first := f.deployed(t, "tf1")
		cr := f.withChanges(first, plannedAt(1), "hash-1", f.planText)
		f.poll()
		f.setStatus(t, first.ID, models.DeploymentApplying)
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved())
		f.poll()
		f.requireStatus(t, first.ID, models.DeploymentApplying)
	})

	t.Run("the CR moved on to another revision", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment := f.deployed(t, "tf1")
		cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
		f.poll()
		f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

		cr.PlannedRevision = strings.Repeat("9", 40)
		f.cluster.SetCR(trackNamespace, trackCR, cr)
		f.poll()
		stale := f.requireStatus(t, deployment.ID, models.DeploymentStale)
		requireMessage(t, stale.ErrorMessage, "Stale: the Terraform CR moved on to revision 9999999")
		if stale.CompletedAt == nil {
			t.Error("not completed")
		}
	})

	t.Run("another pending plan", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment := f.deployed(t, "tf1")
		cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
		f.poll()

		cr.PendingPlan = "plan-main-0000000000"
		f.cluster.SetCR(trackNamespace, trackCR, cr)
		f.poll()
		f.requireStatus(t, deployment.ID, models.DeploymentStale)
	})

	t.Run("the CR is gone", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment := f.deployed(t, "tf1")
		f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
		f.poll()

		f.cluster.DeleteCR(trackNamespace, trackCR)
		f.poll()
		stale := f.requireStatus(t, deployment.ID, models.DeploymentStale)
		requireMessage(t, stale.ErrorMessage, "no longer exists")
	})

	t.Run("the CR is gone while applying", func(t *testing.T) {
		f := newTrackFixture(t)
		deployment := f.deployed(t, "tf1")
		cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
		f.poll()
		f.setStatus(t, deployment.ID, models.DeploymentApplying)
		f.cluster.SetCR(trackNamespace, trackCR, cr.Approved())
		f.cluster.DeleteCR(trackNamespace, trackCR)
		f.poll()
		failed := f.requireStatus(t, deployment.ID, models.DeploymentFailed)
		requireMessage(t, failed.ErrorMessage, "no longer exists")
	})
}

func TestTrackTerraformPlannedAgain(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	messages := f.slack.waitFor(t, 3)

	// The controller's interval run made the same plan again: still current
	f.withChanges(deployment, plannedAt(30), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	plan, _ := f.svc.TerraformPlans.Get(deployment.ID)
	if plan.PlannedAt == nil || !plan.PlannedAt.Equal(plannedAt(30)) {
		t.Errorf("planned at %v", plan.PlannedAt)
	}
	// ... and is not read again while it is unchanged
	f.cluster.SetPlan(trackNamespace, trackCR, deployment.Terraform.PlanID, "hash-9", "changed without a new plan", 0)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

	// A plan with other content (drift) can't be approved as reviewed
	f.withChanges(deployment, plannedAt(59), "hash-2", f.planText+"# drift\n")
	f.poll()
	stale := f.requireStatus(t, deployment.ID, models.DeploymentStale)
	requireMessage(t, stale.ErrorMessage, "the plan changed after it was made")
	// The record keeps the plan that was reviewed
	if plan, _ = f.svc.TerraformPlans.Get(deployment.ID); plan.PlanHash != "hash-1" || plan.Text != f.planText {
		t.Errorf("plan = %+v", plan)
	}
	f.slack.waitFor(t, len(messages))
}

func TestTrackTerraformPlanTimeout(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")

	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)
	if _, err := f.db.Exec(`UPDATE deployments SET started_at = ? WHERE id = ?`, time.Now().UTC().Add(-2*terraformPlanTimeout), deployment.ID); err != nil {
		t.Fatal(err)
	}
	f.poll()
	failed := f.requireStatus(t, deployment.ID, models.DeploymentFailed)
	requireMessage(t, failed.ErrorMessage, "Flux has not applied the Terraform CR of this deployment within 1h0m0s")
	f.slack.waitFor(t, 3)
}

func TestTrackTerraformResumesAfterRestart(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	cr := f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)

	// The deployment was approved and applied while smithd was down. A new
	// process has nothing in memory, only the database and the cluster.
	f.setStatus(t, deployment.ID, models.DeploymentApplying)
	f.cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(deployment.Terraform.SourceCommitSHA))
	restarted := New(f.svc.Deps)
	restarted.pollTerraform(context.Background())

	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
	f.slack.waitFor(t, 4)
}

func TestTrackTerraformReplicasReportOnce(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)

	// Three smithd replicas see the plan at the same time
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

	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	// Requested, committed and one plan ready
	f.slack.waitFor(t, 3)
	if n := strings.Count(strings.Join(f.eventNamesPublished(), ","), "plan_ready"); n != 1 {
		t.Errorf("%d plan_ready events", n)
	}
}

func (f *trackFixture) eventNamesPublished() []string { return eventNames(f.eventTypes()) }

func TestGetDeploymentWithPlan(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")

	detail, err := f.svc.GetDeployment(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Terraform == nil || detail.Terraform.Plan != nil || detail.Status != models.DeploymentPlanning {
		t.Errorf("before the plan: %+v %+v", detail, detail.Terraform)
	}

	f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)
	f.poll()
	detail, err = f.svc.GetDeployment(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := detail.Terraform.Plan
	if plan == nil || plan.Text != f.planText || plan.ToAdd != 3 || !plan.Destroys || detail.Status != models.DeploymentAwaitingApproval ||
		detail.Terraform.PlanID != deployment.Terraform.PlanID {
		t.Errorf("after the plan: %+v %+v", detail, plan)
	}

	// Listings carry the state, not the plan
	list, _, err := f.svc.ListDeployments(store.DeploymentFilter{AppID: f.viinoID, Status: models.DeploymentAwaitingApproval}, 10, 0)
	if err != nil || len(list) != 1 || list[0].Terraform.Plan != nil {
		t.Errorf("list = %+v, %v", list, err)
	}
	if _, _, err := f.svc.ListDeployments(store.DeploymentFilter{Status: models.DeploymentApplying}, 10, 0); err != nil {
		t.Error(err)
	}
}

func TestWatchTerraformWithoutCluster(t *testing.T) {
	f := newTrackFixture(t)
	f.svc.TerraformReader = nil

	done := make(chan struct{})
	go func() {
		f.svc.WatchTerraform(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchTerraform did not return without a cluster")
	}
}

func TestWatchTerraformStopsWithTheContext(t *testing.T) {
	f := newTrackFixture(t)
	deployment := f.deployed(t, "tf1")
	f.withChanges(deployment, plannedAt(1), "hash-1", f.planText)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.svc.WatchTerraform(ctx)
		close(done)
	}()
	// The first pass runs at once
	deadline := time.Now().Add(5 * time.Second)
	for f.get(t, deployment.ID).Status != models.DeploymentAwaitingApproval {
		if time.Now().After(deadline) {
			t.Fatal("the watcher did not follow the run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchTerraform did not stop")
	}
}

func TestTruncateMessage(t *testing.T) {
	if got := truncateMessage("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
	got := truncateMessage(strings.Repeat("é", 100), 51)
	if len(got) > 51 || !strings.HasSuffix(got, "(truncated)") || strings.ContainsRune(got, '�') {
		t.Errorf("got %q (%d bytes)", got, len(got))
	}
}
