package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

type planDeployCall struct {
	deploy   gitops.TerraformPlanDeploy
	manifest string
	commits  gitops.TerraformCommits
}

// fakePlansGitops is the plans branch: it renders the files of each run, and
// keeps the runs on the branch until they are cleaned up
type fakePlansGitops struct {
	mu      sync.Mutex
	deploys []planDeployCall
	// onBranch are the runs on the branch
	onBranch  []gitops.PlanEntry
	deployErr error
	cleanErr  error
	cleans    int
	squashes  int
	// squashed is the result of SquashTerraformPlans
	squashed bool
}

func (f *fakePlansGitops) DeployTerraformPlan(p gitops.TerraformPlanDeploy) (gitops.TerraformCommits, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deployErr != nil {
		return gitops.TerraformCommits{}, f.deployErr
	}
	n := len(f.deploys) + 1
	commits := gitops.TerraformCommits{
		Branch:   "plans",
		Source:   strings.Repeat(string(rune('a'+n)), 40),
		Manifest: strings.Repeat(string(rune('0'+n)), 40),
	}
	manifest, err := p.Manifest(commits.Branch, commits.Source)
	if err != nil {
		return gitops.TerraformCommits{}, err
	}
	f.deploys = append(f.deploys, planDeployCall{deploy: p, manifest: string(manifest), commits: commits})
	f.onBranch = append(f.onBranch, gitops.PlanEntry{Environment: p.Environment, App: p.App, Name: p.Name})
	return commits, nil
}

func (f *fakePlansGitops) CleanTerraformPlans(_ string, keep func(gitops.PlanEntry) bool) ([]gitops.PlanEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cleanErr != nil {
		return nil, f.cleanErr
	}
	f.cleans++
	var kept, removed []gitops.PlanEntry
	for _, e := range f.onBranch {
		if keep(e) {
			kept = append(kept, e)
		} else {
			removed = append(removed, e)
		}
	}
	f.onBranch = kept
	return removed, nil
}

func (f *fakePlansGitops) SquashTerraformPlans(int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.squashes++
	return f.squashed, nil
}

func (f *fakePlansGitops) Deploys() []planDeployCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]planDeployCall(nil), f.deploys...)
}

func (f *fakePlansGitops) OnBranch() []gitops.PlanEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitops.PlanEntry(nil), f.onBranch...)
}

type planFixture struct {
	*trackFixture
	plans *fakePlansGitops
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()
	f := &planFixture{trackFixture: newTrackFixture(t), plans: &fakePlansGitops{}}
	f.svc.PlanRuns = store.NewPlanRunStore(f.db)
	f.svc.TerraformPlansGitops = f.plans
	f.svc.TerraformAutoPlan = true
	return f
}

// draft creates a draft terraform version of viino built from acme/infra
func (f *planFixture) draft(t *testing.T, versionID string) *models.Version {
	t.Helper()
	version, err := f.svc.Versions.Create(f.viinoID, versionID, models.ArtifactTerraform, models.VersionMetadata{
		GitBranch: "feature",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Versions.SetSource(version.ID, models.VersionSource{Repository: "acme/infra", CommitSHA: sourceSHA, PullRequest: 3}); err != nil {
		t.Fatal(err)
	}
	return version
}

func (f *planFixture) plan(versionID string) (*models.PlanRun, error) {
	return f.svc.PlanVersion(context.Background(), f.viinoID, versionID, "production", "alice@example.com")
}

func (f *planFixture) pollPlans() { f.svc.pollTerraform(context.Background()) }

func (f *planFixture) get(t *testing.T, id string) *models.PlanRun {
	t.Helper()
	run, err := f.svc.PlanRuns.Get(id)
	if err != nil || run == nil {
		t.Fatalf("plan run %s = %+v, %v", id, run, err)
	}
	return run
}

func (f *planFixture) requireRun(t *testing.T, id, status string) *models.PlanRun {
	t.Helper()
	run := f.get(t, id)
	if run.Status != status {
		t.Fatalf("plan run is %s (%q), want %s", run.Status, run.ErrorMessage, status)
	}
	return run
}

// withChanges puts the plan of the run in the cluster: a CR with changes and
// the readable plan
func (f *planFixture) withChanges(run *models.PlanRun, at time.Time, hash, text string) {
	name := terraform.PlanName(run.ID)
	f.cluster.SetCR(trackNamespace, name, terraformtest.WithChanges("", run.SourceCommitSHA, run.PlanID, at).ForPlanRun(run.ID))
	f.cluster.SetPlan(trackNamespace, name, run.PlanID, hash, text, 0)
}

func TestPlanVersionOfADraft(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")

	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("b", 40)
	if run.Status != models.PlanRunPlanning || run.Environment != "production" || run.Root != "main" || run.VersionID != "tf1" || run.TriggeredBy != "alice@example.com" ||
		run.SourceCommitSHA != source || run.GitopsCommitSHA != strings.Repeat("1", 40) || run.PlanID != "plan-plans-bbbbbbbbbb" {
		t.Fatalf("unexpected run: %+v", run)
	}
	stored := f.requireRun(t, run.ID, models.PlanRunPlanning)
	if stored.CompletedAt != nil || stored.SourceCommitSHA != source || stored.PlanID != run.PlanID {
		t.Errorf("stored run: %+v", stored)
	}

	// The plans branch got a planOnly CR for the draft, and main got nothing
	if deploys := f.tfGitops.Deploys(); len(deploys) != 0 {
		t.Errorf("%d deploys to the main branch", len(deploys))
	}
	deploys := f.plans.Deploys()
	if len(deploys) != 1 {
		t.Fatalf("%d deploys to the plans branch, want 1", len(deploys))
	}
	d := deploys[0]
	name := terraform.PlanName(run.ID)
	if d.deploy.Name != name || d.deploy.App != "viino" || d.deploy.Environment != "production" || d.deploy.Root != "main" || string(d.deploy.Source["env/production.tfvars"]) != "name = \"prod\"\n" {
		t.Errorf("unexpected deploy: %+v", d.deploy)
	}
	sourcePath := "environments/production/terraform-plans/viino/" + name + "/src/main"
	for _, want := range []string{
		"    branch: plans\n    commit: " + source + "\n",
		"  name: " + name + "\n  namespace: tf-viino\n",
		"    deploysmith.io/plan-run-id: " + run.ID + "\n",
		"    deploysmith.io/version: tf1\n",
		"  planOnly: true\n",
		"  plan:\n    lock: false\n",
		"  path: ./" + sourcePath + "\n",
		"    - " + sourcePath + "/env/production.tfvars\n",
		"  serviceAccountName: tf-runner-production-readonly\n",
		"        key            = \"viino/production/main/terraform.tfstate\"\n",
		"          value: tf-viino-production-readonly\n",
		"          value: viino-production-readonly\n",
	} {
		if !strings.Contains(d.manifest, want) {
			t.Errorf("manifest does not contain %q:\n%s", want, d.manifest)
		}
	}
	if strings.Contains(d.manifest, "approvePlan") || strings.Contains(d.manifest, "deployment-id") {
		t.Errorf("a plan-only run can be approved:\n%s", d.manifest)
	}

	// Not a deployment: nothing is recorded as one, and the root is free
	if deployments := f.deployments(t); len(deployments) != 0 {
		t.Errorf("%d deployments after a plan-only run", len(deployments))
	}

	// The PR sees the plan check, and no deploy check
	check := f.check(t, "deploysmith/plan-production", run.ID)
	if check.Status != "in_progress" || check.HeadSHA != sourceSHA || check.Title != "Planning tf1 for production" {
		t.Errorf("plan check: %+v", check)
	}
	for _, c := range f.gh.CheckRuns() {
		if c.Name == "deploysmith/deploy-production" {
			t.Errorf("a plan-only run has a deploy check: %+v", c)
		}
	}
	// Quiet elsewhere
	f.slack.waitFor(t, 0)
}

func TestPlanVersionOfAPublishedVersion(t *testing.T) {
	f := newPlanFixture(t)
	f.version(t, "tf1")

	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	f.requireRun(t, run.ID, models.PlanRunPlanning)
	// Without a known source nothing is reported to GitHub
	if checks := f.gh.CheckRuns(); len(checks) != 0 {
		t.Errorf("check runs for a version without a source: %+v", checks)
	}
}

func TestTrackPlanRunWithChanges(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	name := terraform.PlanName(run.ID)

	// Not there yet, then another run's CR, then planning
	f.pollPlans()
	f.cluster.SetCR(trackNamespace, name, terraformtest.Reconciling("", run.SourceCommitSHA).ForPlanRun("someone-else"))
	f.pollPlans()
	f.cluster.SetCR(trackNamespace, name, terraformtest.Reconciling("", run.SourceCommitSHA).ForPlanRun(run.ID))
	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanning)

	// The plan is made
	at := plannedAt(1)
	f.withChanges(run, at, "hash-1", f.planText)
	f.pollPlans()
	done := f.requireRun(t, run.ID, models.PlanRunPlanned)
	if done.CompletedAt == nil {
		t.Error("a planned run is not complete")
	}
	got, err := f.svc.GetPlanRun(run.ID)
	if err != nil || got.Plan == nil {
		t.Fatalf("GetPlanRun = %+v, %v", got, err)
	}
	plan := got.Plan
	if plan.PlanID != run.PlanID || plan.PlanHash != "hash-1" || plan.PlannedAt == nil || !plan.PlannedAt.Equal(at) || plan.Text != f.planText {
		t.Errorf("unexpected plan %+v", plan)
	}
	if plan.ToAdd != 3 || plan.ToChange != 1 || plan.ToDestroy != 2 || !plan.Destroys || !plan.OutputChanges {
		t.Errorf("unexpected counts %+v", plan)
	}
	if got := strings.Join(plan.Resources.Replace, ",") + "|" + strings.Join(plan.Resources.Destroy, ","); got != "terraform_data.rep|terraform_data.with_secret" {
		t.Errorf("resources %+v", plan.Resources)
	}

	// GitHub: the plan check is complete, flagged because it destroys
	check := f.check(t, "deploysmith/plan-production", run.ID)
	if check.Status != "completed" || check.Conclusion != "neutral" || check.Title != "3 to add, 1 to change, 2 to destroy (destroys resources)" {
		t.Errorf("plan check: %+v", check)
	}
	requireMessage(t, check.Summary, "**Plan: 3 to add, 1 to change, 2 to destroy**", "This plan destroys resources",
		"- `terraform_data.with_secret`", "- `terraform_data.rep`", "Nothing is deployed")
	if strings.Contains(check.Summary+check.Text, "hunter2") || strings.Contains(check.Summary+check.Text, "resource \"terraform_data\"") {
		t.Error("the plan text was sent to GitHub")
	}

	// It is by id and by version
	latest, err := f.svc.GetVersionPlan(f.viinoID, "tf1", "production")
	if err != nil || latest.ID != run.ID || latest.Plan == nil {
		t.Errorf("GetVersionPlan = %+v, %v", latest, err)
	}
	if _, err := f.svc.GetVersionPlan(f.viinoID, "tf1", "staging"); err == nil {
		t.Error("GetVersionPlan of another environment succeeded")
	} else {
		requireKind(t, err, KindNotFound)
	}
	if _, err := f.svc.GetPlanRun("nope"); err == nil {
		t.Error("GetPlanRun of a missing run succeeded")
	} else {
		requireKind(t, err, KindNotFound)
	}

	// Polling again changes nothing
	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanned)
}

func TestTrackPlanRunWithoutChanges(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}

	f.cluster.SetCR(trackNamespace, terraform.PlanName(run.ID), terraformtest.WithoutChanges("", run.SourceCommitSHA, plannedAt(1)).ForPlanRun(run.ID))
	f.pollPlans()

	done := f.requireRun(t, run.ID, models.PlanRunPlanned)
	got := f.get(t, done.ID)
	if got.Plan == nil || got.Plan.ToAdd != 0 || got.Plan.Destroys || !strings.Contains(got.Plan.Text, "No changes") {
		t.Errorf("plan = %+v", got.Plan)
	}
	check := f.check(t, "deploysmith/plan-production", run.ID)
	if check.Conclusion != "success" || check.Title != "No changes" {
		t.Errorf("plan check: %+v", check)
	}
}

func TestTrackPlanRunChangesWithoutDestroysSucceed(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}

	f.withChanges(run, plannedAt(1), "hash-1", "Plan: 1 to add, 0 to change, 0 to destroy.\n\n  # terraform_data.new will be created\n")
	f.pollPlans()

	f.requireRun(t, run.ID, models.PlanRunPlanned)
	if check := f.check(t, "deploysmith/plan-production", run.ID); check.Conclusion != "success" || check.Title != "1 to add, 0 to change, 0 to destroy" {
		t.Errorf("plan check: %+v", check)
	}
}

func TestTrackPlanRunFailed(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	name := terraform.PlanName(run.ID)

	f.cluster.SetCR(trackNamespace, name, terraformtest.Reconciling("", run.SourceCommitSHA).ForPlanRun(run.ID).Failed("TerraformPlanFailed", "Error: Invalid provider configuration"))
	f.cluster.AddEvent(trackNamespace, name, "Warning", "TerraformPlanFailed", "exit status 1", time.Now())
	f.pollPlans()

	failed := f.requireRun(t, run.ID, models.PlanRunFailed)
	if failed.CompletedAt == nil || !strings.Contains(failed.ErrorMessage, "TerraformPlanFailed: Error: Invalid provider configuration") || !strings.Contains(failed.ErrorMessage, "exit status 1") {
		t.Errorf("failed run: %+v", failed)
	}
	check := f.check(t, "deploysmith/plan-production", run.ID)
	if check.Status != "completed" || check.Conclusion != "failure" || check.Title != "Plan failed" || !strings.Contains(check.Summary, "Invalid provider configuration") {
		t.Errorf("plan check: %+v", check)
	}
}

func TestTrackPlanRunTimesOut(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}

	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanning)

	if _, err := f.db.Exec(`UPDATE plan_runs SET started_at = ? WHERE id = ?`, time.Now().Add(-2*terraformPlanTimeout).UTC(), run.ID); err != nil {
		t.Fatal(err)
	}
	f.pollPlans()
	failed := f.requireRun(t, run.ID, models.PlanRunFailed)
	if !strings.Contains(failed.ErrorMessage, "Flux has not applied the Terraform CR of this plan-only run within 1h0m0s") {
		t.Errorf("failed run: %+v", failed)
	}
	if check := f.check(t, "deploysmith/plan-production", run.ID); check.Conclusion != "failure" {
		t.Errorf("plan check: %+v", check)
	}
}

func TestPlanVersionGitopsFailure(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	f.plans.deployErr = errors.New("push rejected")

	run, err := f.plan("tf1")
	if err == nil || run == nil {
		t.Fatalf("PlanVersion = %+v, %v", run, err)
	}
	failed := f.requireRun(t, run.ID, models.PlanRunFailed)
	if failed.ErrorMessage != "gitops: push rejected" {
		t.Errorf("failed run: %+v", failed)
	}
	if check := f.check(t, "deploysmith/plan-production", run.ID); check.Conclusion != "failure" || !strings.Contains(check.Summary, "push rejected") {
		t.Errorf("plan check: %+v", check)
	}

	// The root is free for the next one
	f.plans.deployErr = nil
	if _, err := f.plan("tf1"); err != nil {
		t.Errorf("plan after a failure: %v", err)
	}
}

func TestPlanVersionReplacesTheOlderRun(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	f.draft(t, "tf2")

	first, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.plan("tf2")
	if err != nil {
		t.Fatal(err)
	}

	old := f.requireRun(t, first.ID, models.PlanRunSuperseded)
	if old.ErrorMessage != "Replaced by plan run "+second.ID || old.CompletedAt == nil {
		t.Errorf("replaced run: %+v", old)
	}
	f.requireRun(t, second.ID, models.PlanRunPlanning)
	if check := f.check(t, "deploysmith/plan-production", first.ID); check.Status != "completed" || check.Conclusion != "neutral" || check.Title != "Plan replaced" {
		t.Errorf("replaced plan check: %+v", check)
	}
	if check := f.check(t, "deploysmith/plan-production", second.ID); check.Status != "in_progress" {
		t.Errorf("plan check: %+v", check)
	}

	// The replaced run is not followed, and its files are cleaned up while
	// the newer run's stay
	f.cluster.SetCR(trackNamespace, terraform.PlanName(first.ID), terraformtest.WithoutChanges("", first.SourceCommitSHA, plannedAt(1)).ForPlanRun(first.ID))
	f.pollPlans()
	f.requireRun(t, first.ID, models.PlanRunSuperseded)
	if branch := f.plans.OnBranch(); len(branch) != 1 || branch[0].Name != terraform.PlanName(second.ID) {
		t.Errorf("plans branch after the clean-up: %+v", branch)
	}
}

func TestPlanRunsDontInterfereWithDeployments(t *testing.T) {
	f := newPlanFixture(t)
	f.version(t, "tf1")
	f.version(t, "tf2")

	// A plan-only run of tf2 is active while tf1 is deployed
	run, err := f.plan("tf2")
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.deploy("tf1")
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Status != models.DeploymentPlanning {
		t.Errorf("deployment is %s", deployment.Status)
	}
	f.requireRun(t, run.ID, models.PlanRunPlanning)

	// And the other way round: a deployment waiting for approval or
	// applying doesn't stop a plan-only run, and a new run doesn't touch it
	for _, status := range []string{models.DeploymentAwaitingApproval, models.DeploymentApplying} {
		f.setStatus(t, deployment.ID, status)
		again, err := f.plan("tf2")
		if err != nil {
			t.Fatalf("plan-only run while the deployment is %s: %v", status, err)
		}
		if again.ID == run.ID {
			t.Fatal("no new run")
		}
		run = again
		stored, err := f.svc.Deployments.GetByID(deployment.ID)
		if err != nil || stored.Status != status {
			t.Errorf("the plan-only run changed the deployment: %+v, %v", stored, err)
		}
	}

	// The real deployment's CR is the root's own, not the plan's
	if deploys := f.tfGitops.Deploys(); len(deploys) != 1 || !strings.Contains(deploys[0].manifest, "approvePlan: \"\"") || strings.Contains(deploys[0].manifest, "planOnly") {
		t.Errorf("deployments to main: %+v", deploys)
	}
}

func TestPlanVersionRefusals(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")

	// A manifests version
	manifests := f.createVersionOf(t, f.viinoID, "m1")
	_, err := f.svc.PlanVersion(context.Background(), f.viinoID, manifests.VersionID, "production", "alice")
	requireCode(t, err, KindInvalid, "not_terraform")

	_, err = f.svc.PlanVersion(context.Background(), f.viinoID, "nope", "production", "alice")
	requireKind(t, err, KindNotFound)
	_, err = f.svc.PlanVersion(context.Background(), "nope", "tf1", "production", "alice")
	requireKind(t, err, KindNotFound)
	_, err = f.svc.PlanVersion(context.Background(), f.viinoID, "tf1", "nowhere", "alice")
	requireKind(t, err, KindInvalid)

	// Terraform not enabled in the environment
	_, err = f.svc.PlanVersion(context.Background(), f.viinoID, "tf1", "staging", "alice")
	requireCode(t, err, KindInvalid, "terraform_not_enabled")

	// A bundle for an OpenTofu version without runner image
	f.svc.TerraformRoots.RunnerImages = map[string]string{"1.12.9": "image"}
	_, err = f.plan("tf1")
	requireCode(t, err, KindInvalid, "unsupported_opentofu_version")
	f.svc.TerraformRoots.RunnerImages = map[string]string{"1.13.2": "image"}

	// No plans branch
	f.svc.TerraformPlansGitops = nil
	_, err = f.plan("tf1")
	requireCode(t, err, KindInvalid, "terraform_plans_disabled")

	// None of it left a record or touched the branch
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM plan_runs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d plan runs after refusals, %v", n, err)
	}
	if deploys := f.plans.Deploys(); len(deploys) != 0 {
		t.Errorf("%d deploys to the plans branch", len(deploys))
	}
}

// createVersionOf creates a published manifests version of an app
func (f *planFixture) createVersionOf(t *testing.T, appID, versionID string) *models.Version {
	t.Helper()
	version, err := f.svc.Versions.Create(appID, versionID, models.ArtifactManifests, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestPlanRunCleanup(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	name := terraform.PlanName(run.ID)

	// While the run is active its files stay; only the hourly sweep runs
	// on the first poll, and it keeps them
	f.pollPlans()
	if branch := f.plans.OnBranch(); len(branch) != 1 || branch[0].Name != name {
		t.Fatalf("plans branch of an active run: %+v", branch)
	}
	if f.plans.squashes != 1 {
		t.Errorf("%d squashes on the first poll, want 1", f.plans.squashes)
	}

	// Planned: the result is stored, then the files go, in one clean-up
	f.withChanges(run, plannedAt(1), "hash-1", f.planText)
	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanned)
	if branch := f.plans.OnBranch(); len(branch) != 0 {
		t.Errorf("plans branch after the plan: %+v", branch)
	}
	if n, err := f.svc.PlanRuns.CountUncleaned(); err != nil || n != 0 {
		t.Errorf("CountUncleaned = %d, %v", n, err)
	}
	// The result is still there, and nothing touches the branch again
	cleans := f.plans.cleans
	f.pollPlans()
	if f.plans.cleans != cleans {
		t.Errorf("the branch was cleaned %d times for nothing", f.plans.cleans-cleans)
	}
	if got := f.get(t, run.ID); got.Status != models.PlanRunPlanned {
		t.Errorf("run = %+v", got)
	}
}

func TestPlanRunSweepRemovesLeftovers(t *testing.T) {
	f := newPlanFixture(t)
	// A run smithd lost track of, e.g. recorded in a database that was restored
	f.plans.onBranch = []gitops.PlanEntry{{Environment: "production", App: "viino", Name: "plan-deadbeef0000"}}

	f.pollPlans()
	if branch := f.plans.OnBranch(); len(branch) != 0 {
		t.Errorf("leftovers on the plans branch: %+v", branch)
	}

	// The sweep is hourly, not on every poll
	f.plans.onBranch = []gitops.PlanEntry{{Environment: "production", App: "viino", Name: "plan-deadbeef0001"}}
	f.pollPlans()
	if len(f.plans.OnBranch()) != 1 {
		t.Error("swept again right away")
	}
	f.svc.planSweepAt = time.Now().Add(-2 * planSweepInterval)
	f.pollPlans()
	if len(f.plans.OnBranch()) != 0 {
		t.Error("the hourly sweep did not remove the leftover")
	}
}

func TestPlanRunSweepDeletesOldResults(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	f.cluster.SetCR(trackNamespace, terraform.PlanName(run.ID), terraformtest.WithoutChanges("", run.SourceCommitSHA, plannedAt(1)).ForPlanRun(run.ID))
	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanned)

	old := time.Now().Add(-planRunRetention - time.Hour).UTC()
	if _, err := f.db.Exec(`UPDATE plan_runs SET completed_at = ? WHERE id = ?`, old, run.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.planSweepAt = time.Time{}
	f.pollPlans()
	if got, err := f.svc.PlanRuns.Get(run.ID); err != nil || got != nil {
		t.Errorf("old result was kept: %+v, %v", got, err)
	}
}

func TestPlanRunCleanupFailureIsRetried(t *testing.T) {
	f := newPlanFixture(t)
	f.draft(t, "tf1")
	run, err := f.plan("tf1")
	if err != nil {
		t.Fatal(err)
	}
	f.cluster.SetCR(trackNamespace, terraform.PlanName(run.ID), terraformtest.WithoutChanges("", run.SourceCommitSHA, plannedAt(1)).ForPlanRun(run.ID))
	f.plans.cleanErr = errors.New("push rejected")
	f.pollPlans()
	f.requireRun(t, run.ID, models.PlanRunPlanned)
	if n, _ := f.svc.PlanRuns.CountUncleaned(); n != 1 {
		t.Fatalf("CountUncleaned = %d after a failed clean-up, want 1", n)
	}

	f.plans.cleanErr = nil
	f.pollPlans()
	if n, _ := f.svc.PlanRuns.CountUncleaned(); n != 0 {
		t.Errorf("CountUncleaned = %d after the retry, want 0", n)
	}
	if branch := f.plans.OnBranch(); len(branch) != 0 {
		t.Errorf("plans branch: %+v", branch)
	}
}

// waitForPlanRuns waits until n plan-only runs of the environment exist
func (f *planFixture) waitForPlanRuns(t *testing.T, versionID string, n int) []models.PlanRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var runs []models.PlanRun
		rows, err := f.db.Query(`SELECT r.id FROM plan_runs r JOIN versions v ON v.id = r.version_id WHERE v.version_id = ?`, versionID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			runs = append(runs, *f.get(t, id))
		}
		rows.Close()
		if len(runs) == n && (n == 0 || runs[0].Status != models.PlanRunPending) {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d plan runs of %s, want %d", len(runs), versionID, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoPlanOnPublish(t *testing.T) {
	f := newPlanFixture(t)
	version := f.draft(t, "tf1")
	app, err := f.svc.Apps.GetByID(f.viinoID)
	if err != nil {
		t.Fatal(err)
	}

	f.svc.TriggerAutoPlans(context.Background(), app, version)
	runs := f.waitForPlanRuns(t, "tf1", 1)
	if runs[0].Environment != "production" || runs[0].TriggeredBy != "auto-plan" || runs[0].Status != models.PlanRunPlanning {
		t.Errorf("auto-plan run: %+v", runs[0])
	}
	if check := f.check(t, "deploysmith/plan-production", runs[0].ID); check.HeadSHA != sourceSHA || check.Status != "in_progress" {
		t.Errorf("plan check: %+v", check)
	}
	// It is only a plan: nothing is deployed
	if deployments := f.deployments(t); len(deployments) != 0 {
		t.Errorf("%d deployments after an auto-plan", len(deployments))
	}
}

func TestAutoPlanSkips(t *testing.T) {
	f := newPlanFixture(t)
	app, err := f.svc.Apps.GetByID(f.viinoID)
	if err != nil {
		t.Fatal(err)
	}

	// Switched off
	version := f.draft(t, "tf1")
	f.svc.TerraformAutoPlan = false
	f.svc.TriggerAutoPlans(context.Background(), app, version)
	f.svc.TerraformAutoPlan = true

	// No repository and commit to report on
	unknown, err := f.svc.Versions.Create(f.viinoID, "tf2", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.TriggerAutoPlans(context.Background(), app, unknown)

	// Manifests versions have no plan
	manifests := f.createVersionOf(t, f.viinoID, "m1")
	if err := f.svc.Versions.SetSource(manifests.ID, models.VersionSource{Repository: "acme/infra", CommitSHA: sourceSHA}); err != nil {
		t.Fatal(err)
	}
	f.svc.TriggerAutoPlans(context.Background(), app, manifests)

	// Time for a wrongly started run to show up
	time.Sleep(100 * time.Millisecond)
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM plan_runs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d plan runs, want none (%v)", n, err)
	}
}

func TestAutoPlanForEveryProvisionedEnvironment(t *testing.T) {
	f := newPlanFixture(t)
	if _, err := f.svc.EnableTerraform(context.Background(), f.viinoID, models.EnableTerraformRequest{Environment: "staging"}); err != nil {
		t.Fatal(err)
	}
	version := f.draft(t, "tf1")
	app, err := f.svc.Apps.GetByID(f.viinoID)
	if err != nil {
		t.Fatal(err)
	}

	f.svc.TriggerAutoPlans(context.Background(), app, version)
	// One root per environment, so both are planned
	deadline := time.Now().Add(5 * time.Second)
	for len(f.plans.Deploys()) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d plan-only runs, want 2", len(f.plans.Deploys()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	environments := map[string]bool{}
	for _, d := range f.plans.Deploys() {
		environments[d.deploy.Environment] = true
	}
	if !environments["production"] || !environments["staging"] {
		t.Errorf("planned environments: %v", environments)
	}
}
