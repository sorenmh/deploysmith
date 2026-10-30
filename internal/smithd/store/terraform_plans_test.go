package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// terraformDeployment creates a terraform deployment that is planning
func terraformDeployment(t *testing.T, deployments *DeploymentStore, appID, versionID string) *models.Deployment {
	t.Helper()
	deployment, _, err := deployments.CreateTerraform(appID, versionID, "production", "main", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := deployments.SetTerraformCommitted(deployment.ID, models.DeploymentPlanning, "cr-sha", "src-sha", "plan-main-src-sha"); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func TestTerraformPlans(t *testing.T) {
	database := dbtest.Open(t)
	if _, err := NewEnvironmentStore(database).Create(models.CreateEnvironmentRequest{Name: "production"}); err != nil {
		t.Fatal(err)
	}
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	version, err := NewVersionStore(database).Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	deployments := NewDeploymentStore(database)
	plans := NewTerraformPlanStore(database)
	deployment := terraformDeployment(t, deployments, app.ID, version.ID)

	if plan, err := plans.Get(deployment.ID); err != nil || plan != nil {
		t.Fatalf("a deployment without a plan has %+v, %v", plan, err)
	}

	plannedAt := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	want := models.TerraformPlan{
		PlanID: "plan-main-src-sha", PlanHash: "hash-1", PlannedAt: &plannedAt,
		ToAdd: 2, ToChange: 1, ToDestroy: 1, Destroys: true, OutputChanges: true,
		Resources: models.TerraformPlanResources{
			Create:  []string{"a.one", `b.two["x"]`},
			Update:  []string{"c.three"},
			Replace: []string{},
			Destroy: []string{"d.four"},
		},
		Text: "Plan: 2 to add, 1 to change, 1 to destroy.\n",
	}
	if err := plans.Save(deployment.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err := plans.Get(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PlannedAt.Equal(plannedAt) {
		t.Errorf("planned at %v, want %v", got.PlannedAt, plannedAt)
	}
	got.PlannedAt = want.PlannedAt
	if !reflect.DeepEqual(got, &want) {
		t.Errorf("got  %+v\nwant %+v", got, &want)
	}

	// Saving again replaces the plan; the applied hash is kept apart
	if err := plans.SetApplied(deployment.ID, "hash-2", true); err != nil {
		t.Fatal(err)
	}
	later := plannedAt.Add(time.Hour)
	if err := plans.SetPlannedAt(deployment.ID, later); err != nil {
		t.Fatal(err)
	}
	if got, _ = plans.Get(deployment.ID); !got.PlannedAt.Equal(later) || got.AppliedPlanHash != "hash-2" || !got.AppliedDiffered {
		t.Errorf("unexpected plan %+v", got)
	}
	replacement := models.TerraformPlan{PlanID: "plan-main-src-sha", PlanHash: "hash-3", Resources: models.TerraformPlanResources{}, Text: "No changes."}
	if err := plans.Save(deployment.ID, replacement); err != nil {
		t.Fatal(err)
	}
	got, _ = plans.Get(deployment.ID)
	if got.PlanHash != "hash-3" || got.ToAdd != 0 || got.Destroys || got.PlannedAt != nil || got.AppliedPlanHash != "hash-2" {
		t.Errorf("unexpected replaced plan %+v", got)
	}
	// Empty lists, not null, in the API
	if got.Resources.Create == nil || got.Resources.Destroy == nil {
		t.Errorf("nil resource lists %+v", got.Resources)
	}
}

func TestTransitionTerraform(t *testing.T) {
	database := dbtest.Open(t)
	if _, err := NewEnvironmentStore(database).Create(models.CreateEnvironmentRequest{Name: "production"}); err != nil {
		t.Fatal(err)
	}
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	version, err := NewVersionStore(database).Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	deployments := NewDeploymentStore(database)

	// Manifest deployments and terraform deployments that are being
	// committed are not followed
	if _, err := deployments.Create(app.ID, version.ID, "production", "alice", nil); err != nil {
		t.Fatal(err)
	}
	deployment := terraformDeployment(t, deployments, app.ID, version.ID)
	tracked, err := deployments.ListTrackedTerraform()
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0].ID != deployment.ID || tracked[0].Terraform.PlanID != "plan-main-src-sha" {
		t.Fatalf("tracked = %+v", tracked)
	}

	// Only the first of two callers that see the same state moves it
	for i, want := range []bool{true, false} {
		moved, err := deployments.TransitionTerraform(deployment.ID, models.DeploymentPlanning, models.DeploymentAwaitingApproval, "")
		if err != nil || moved != want {
			t.Fatalf("move %d = %v, %v; want %v", i, moved, err, want)
		}
	}
	stored, _ := deployments.GetByID(deployment.ID)
	if stored.Status != models.DeploymentAwaitingApproval || stored.CompletedAt != nil {
		t.Errorf("awaiting deployment is %+v", stored)
	}
	if tracked, _ = deployments.ListTrackedTerraform(); len(tracked) != 1 {
		t.Errorf("an awaiting deployment is not tracked: %+v", tracked)
	}

	// Finishing completes the deployment and ends the tracking
	if moved, err := deployments.TransitionTerraform(deployment.ID, models.DeploymentAwaitingApproval, models.DeploymentStale, "Stale: moved on"); err != nil || !moved {
		t.Fatalf("stale = %v, %v", moved, err)
	}
	stored, _ = deployments.GetByID(deployment.ID)
	if stored.Status != models.DeploymentStale || stored.ErrorMessage != "Stale: moved on" || stored.CompletedAt == nil {
		t.Errorf("stale deployment is %+v", stored)
	}
	if tracked, _ = deployments.ListTrackedTerraform(); len(tracked) != 0 {
		t.Errorf("a stale deployment is tracked: %+v", tracked)
	}
}
