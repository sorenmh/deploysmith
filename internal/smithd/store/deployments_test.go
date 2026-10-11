package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestClaimStaleDeployment(t *testing.T) {
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	app, err := NewApplicationStore(database).Create("hello")
	if err != nil {
		t.Fatal(err)
	}
	version, err := NewVersionStore(database).Create(app.ID, "v1", models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}

	deployments := NewDeploymentStore(database)
	fresh, err := deployments.Create(app.ID, version.ID, "production", "auto-deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := deployments.Create(app.ID, version.ID, "production", "auto-deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deployments.UpdateStatus(finished.ID, "success", "abc", ""); err != nil {
		t.Fatal(err)
	}

	// Nothing is stale yet
	stale, err := deployments.ListStalePending(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected no stale deployments, got %d", len(stale))
	}

	// With a cutoff in the future, only the pending deployment is stale
	cutoff := time.Now().Add(time.Minute)
	stale, err = deployments.ListStalePending(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != fresh.ID {
		t.Fatalf("expected only the pending deployment to be stale, got %+v", stale)
	}

	// The first claim wins; a second instance using the same cutoff loses
	claimed, err := deployments.ClaimStale(fresh.ID, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claimed a deployment that is not stale")
	}
	claimed, err = deployments.ClaimStale(fresh.ID, cutoff)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true", claimed, err)
	}
	claimed, err = deployments.ClaimStale(fresh.ID, time.Now().Add(-time.Second))
	if err != nil || claimed {
		t.Fatalf("second claim = %v, %v; want false", claimed, err)
	}

	// Finished deployments are never claimed
	claimed, err = deployments.ClaimStale(finished.ID, cutoff)
	if err != nil || claimed {
		t.Fatalf("claim of finished deployment = %v, %v; want false", claimed, err)
	}
}
