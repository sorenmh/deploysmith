package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// planRunFixture is an app with a terraform version and a plan run store
func planRunFixture(t *testing.T) (*PlanRunStore, *models.Application, *models.Version, *DeploymentStore) {
	t.Helper()
	database := dbtest.Open(t)
	environments := NewEnvironmentStore(database)
	for _, name := range []string{"production", "staging"} {
		if _, err := environments.Create(models.CreateEnvironmentRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	version, err := NewVersionStore(database).Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	return NewPlanRunStore(database), app, version, NewDeploymentStore(database)
}

func TestPlanRunLifecycle(t *testing.T) {
	runs, app, version, _ := planRunFixture(t)

	run, superseded, err := runs.Create(app.ID, version, "production", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(superseded) != 0 || run.Status != models.PlanRunPending || run.VersionID != "tf1" {
		t.Fatalf("created %+v, superseded %+v", run, superseded)
	}
	if got, err := runs.ListPlanning(); err != nil || len(got) != 0 {
		t.Fatalf("a pending run is tracked: %+v, %v", got, err)
	}

	if ok, err := runs.SetCommitted(run.ID, "cr-sha", "src-sha", "plan-plans-src-sha"); err != nil || !ok {
		t.Fatalf("SetCommitted = %v, %v", ok, err)
	}
	if ok, _ := runs.SetCommitted(run.ID, "x", "x", "x"); ok {
		t.Error("a run was committed twice")
	}
	tracked, err := runs.ListPlanning()
	if err != nil || len(tracked) != 1 || tracked[0].ID != run.ID || tracked[0].SourceCommitSHA != "src-sha" || tracked[0].AppName != "viino" {
		t.Fatalf("ListPlanning = %+v, %v", tracked, err)
	}

	plannedAt := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	plan := models.TerraformPlan{
		PlanID: "plan-plans-src-sha", PlanHash: "hash-1", PlannedAt: &plannedAt,
		ToAdd: 2, ToChange: 1, ToDestroy: 1, Destroys: true, OutputChanges: true,
		Resources: models.TerraformPlanResources{
			Create:  []string{"a.one", `b.two["x"]`},
			Update:  []string{"c.three"},
			Replace: []string{},
			Destroy: []string{"d.four"},
		},
		Text: "Plan: 2 to add, 1 to change, 1 to destroy.\n",
	}
	if ok, err := runs.Finish(run.ID, models.PlanRunPlanning, models.PlanRunPlanned, "", &plan); err != nil || !ok {
		t.Fatalf("Finish = %v, %v", ok, err)
	}
	// Only the first replica to finish a run reports it
	if ok, err := runs.Finish(run.ID, models.PlanRunPlanning, models.PlanRunFailed, "late", nil); err != nil || ok {
		t.Fatalf("Finish of a finished run = %v, %v", ok, err)
	}

	got, err := runs.Get(run.ID)
	if err != nil || got == nil {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if got.Status != models.PlanRunPlanned || got.CompletedAt == nil || !got.Done() || got.GitopsCommitSHA != "cr-sha" {
		t.Errorf("run = %+v", got)
	}
	if !got.Plan.PlannedAt.Equal(plannedAt) {
		t.Errorf("planned at %v, want %v", got.Plan.PlannedAt, plannedAt)
	}
	got.Plan.PlannedAt = &plannedAt
	if !reflect.DeepEqual(got.Plan, &plan) {
		t.Errorf("got  %+v\nwant %+v", got.Plan, &plan)
	}

	if latest, err := runs.Latest(app.ID, "tf1", "production"); err != nil || latest == nil || latest.ID != run.ID || latest.Plan == nil {
		t.Errorf("Latest = %+v, %v", latest, err)
	}
	if latest, err := runs.Latest(app.ID, "tf1", "staging"); err != nil || latest != nil {
		t.Errorf("Latest of another environment = %+v, %v", latest, err)
	}
	if missing, err := runs.Get("nope"); err != nil || missing != nil {
		t.Errorf("Get of a missing run = %+v, %v", missing, err)
	}
}

func TestPlanRunFailed(t *testing.T) {
	runs, app, version, _ := planRunFixture(t)
	run, _, err := runs.Create(app.ID, version, "production", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := runs.Finish(run.ID, models.PlanRunPending, models.PlanRunFailed, "gitops: push rejected", nil); err != nil || !ok {
		t.Fatalf("Finish = %v, %v", ok, err)
	}
	got, err := runs.Get(run.ID)
	if err != nil || got.Status != models.PlanRunFailed || got.ErrorMessage != "gitops: push rejected" || got.Plan != nil {
		t.Errorf("run = %+v, %v", got, err)
	}
}

func TestPlanRunReplacesTheActiveRun(t *testing.T) {
	runs, app, version, deployments := planRunFixture(t)

	first, _, err := runs.Create(app.ID, version, "production", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.SetCommitted(first.ID, "cr", "src", "plan"); err != nil {
		t.Fatal(err)
	}
	// Another environment is another root
	other, superseded, err := runs.Create(app.ID, version, "staging", "main", "alice")
	if err != nil || len(superseded) != 0 {
		t.Fatalf("a run for another environment superseded %+v, %v", superseded, err)
	}

	second, superseded, err := runs.Create(app.ID, version, "production", "main", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(superseded) != 1 || superseded[0].ID != first.ID || superseded[0].Status != models.PlanRunSuperseded || superseded[0].ErrorMessage != "Replaced by plan run "+second.ID {
		t.Fatalf("superseded = %+v", superseded)
	}
	// The run that is being committed is replaced too, and finds out
	third, superseded, err := runs.Create(app.ID, version, "production", "main", "bob")
	if err != nil || len(superseded) != 1 || superseded[0].ID != second.ID || superseded[0].Status != models.PlanRunSuperseded {
		t.Fatalf("third run superseded %+v, %v", superseded, err)
	}
	if ok, err := runs.SetCommitted(second.ID, "cr", "src", "plan"); err != nil || ok {
		t.Errorf("a superseded run was committed: %v, %v", ok, err)
	}

	active, err := runs.ListActive()
	if err != nil || len(active) != 2 {
		t.Fatalf("ListActive = %+v, %v", active, err)
	}
	ids := map[string]bool{active[0].ID: true, active[1].ID: true}
	if !ids[third.ID] || !ids[other.ID] {
		t.Errorf("active runs %v, want %s and %s", ids, third.ID, other.ID)
	}

	// Plan-only runs don't compete with the real deployment of the root
	deployment, replaced, err := deployments.CreateTerraform(app.ID, version.ID, "production", "main", "alice")
	if err != nil || replaced != nil || deployment == nil {
		t.Errorf("deploying the root with a plan run active: %+v, %+v, %v", deployment, replaced, err)
	}
	if got, err := runs.Get(third.ID); err != nil || got.Status != models.PlanRunPending {
		t.Errorf("the deployment touched the plan run: %+v, %v", got, err)
	}
	if got, err := deployments.GetByID(deployment.ID); err != nil || got.Status != models.DeploymentPending {
		t.Errorf("the plan run touched the deployment: %+v, %v", got, err)
	}
}

func TestPlanRunCleanup(t *testing.T) {
	runs, app, version, _ := planRunFixture(t)
	active, _, err := runs.Create(app.ID, version, "staging", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	done, _, err := runs.Create(app.ID, version, "production", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := runs.Finish(done.ID, models.PlanRunPending, models.PlanRunFailed, "boom", nil); err != nil || !ok {
		t.Fatalf("Finish = %v, %v", ok, err)
	}

	if n, err := runs.CountUncleaned(); err != nil || n != 1 {
		t.Fatalf("CountUncleaned = %d, %v; want 1", n, err)
	}
	// A cutoff from before the run ended leaves it
	if err := runs.MarkCleaned(time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := runs.CountUncleaned(); n != 1 {
		t.Errorf("CountUncleaned = %d after an early cutoff, want 1", n)
	}
	if err := runs.MarkCleaned(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := runs.CountUncleaned(); n != 0 {
		t.Errorf("CountUncleaned = %d, want 0", n)
	}

	// Active runs are never marked or deleted
	if deleted, err := runs.DeleteEndedBefore(time.Now().Add(time.Hour)); err != nil || deleted != 1 {
		t.Fatalf("DeleteEndedBefore = %d, %v; want 1", deleted, err)
	}
	if got, _ := runs.Get(active.ID); got == nil {
		t.Error("an active run was deleted")
	}
	if got, _ := runs.Get(done.ID); got != nil {
		t.Error("an ended run was not deleted")
	}
}
