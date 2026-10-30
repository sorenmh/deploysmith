package store

import (
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestTemporaryVersions(t *testing.T) {
	database := dbtest.Open(t)
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	versions := NewVersionStore(database)
	published, err := versions.Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Now().Add(time.Hour)
	local, err := versions.CreateTemporary(app.ID, "local-alice-0123456789ab", "alice", expires)
	if err != nil {
		t.Fatal(err)
	}
	got, err := versions.GetByVersionID(app.ID, local.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Temporary || got.CreatedBy != "alice" || got.Status != "draft" || got.ArtifactType != models.ArtifactTerraform || got.ExpiresAt == nil || got.ExpiresAt.Sub(expires).Abs() > time.Second {
		t.Errorf("temporary version = %+v", got)
	}
	if again, err := versions.GetByID(local.ID); err != nil || !again.Temporary {
		t.Errorf("GetByID = %+v, %v", again, err)
	}
	if published, err := versions.GetByID(published.ID); err != nil || published.Temporary || published.ExpiresAt != nil || published.CreatedBy != "" {
		t.Errorf("an ordinary version = %+v, %v", published, err)
	}
	if _, err := versions.CreateTemporary(app.ID, local.VersionID, "alice", expires); err == nil {
		t.Error("the same temporary version was created twice")
	}

	// Hidden from the list, and from its count, unless asked for
	list, total, err := versions.List(app.ID, 10, 0, false)
	if err != nil || total != 1 || len(list) != 1 || list[0].VersionID != "tf1" {
		t.Errorf("List = %+v, %d, %v; want only tf1", list, total, err)
	}
	list, total, err = versions.List(app.ID, 10, 0, true)
	if err != nil || total != 2 || len(list) != 2 {
		t.Errorf("List with temporary = %+v, %d, %v", list, total, err)
	}

	// Planning it again moves its expiry
	later := time.Now().Add(48 * time.Hour)
	if ok, err := versions.ExtendTemporary(local.ID, later); err != nil || !ok {
		t.Fatalf("ExtendTemporary = %v, %v", ok, err)
	}
	if got, _ := versions.GetByID(local.ID); got.ExpiresAt.Sub(later).Abs() > time.Second {
		t.Errorf("expires at %v, want %v", got.ExpiresAt, later)
	}
	if ok, err := versions.ExtendTemporary(published.ID, later); err != nil || ok {
		t.Errorf("ExtendTemporary of an ordinary version = %v, %v", ok, err)
	}
}

func TestExpiredTemporaryVersions(t *testing.T) {
	database := dbtest.Open(t)
	environments := NewEnvironmentStore(database)
	if _, err := environments.Create(models.CreateEnvironmentRequest{Name: "production"}); err != nil {
		t.Fatal(err)
	}
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	versions := NewVersionStore(database)
	runs := NewPlanRunStore(database)

	ordinary, err := versions.Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	expired, err := versions.CreateTemporary(app.ID, "local-alice-000000000001", "alice", past)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := versions.CreateTemporary(app.ID, "local-alice-000000000002", "alice", future)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := versions.CreateTemporary(app.ID, "local-carol-000000000003", "carol", past)
	if err != nil {
		t.Fatal(err)
	}
	// One that is still being planned is kept; one that is done goes with
	// its version
	if _, _, err := runs.Create(app.ID, busy, "production", "main", "carol", "carol"); err != nil {
		t.Fatal(err)
	}
	finished, _, err := runs.Create(app.ID, expired, "production", "main", "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := runs.Finish(finished.ID, models.PlanRunPending, models.PlanRunFailed, "boom", nil); err != nil || !ok {
		t.Fatalf("Finish = %v, %v", ok, err)
	}

	list, err := versions.ListExpiredTemporary(time.Now())
	if err != nil || len(list) != 1 || list[0].ID != expired.ID {
		t.Fatalf("ListExpiredTemporary = %+v, %v; want only the expired one", list, err)
	}

	if err := versions.DeleteTemporary(expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := versions.GetByID(expired.ID); err == nil {
		t.Error("the expired version is still there")
	}
	if run, err := runs.Get(finished.ID); err != nil || run != nil {
		t.Errorf("the plan run of the deleted version = %+v, %v; want it gone", run, err)
	}

	// Only temporary versions are ever deleted, and others are left alone
	if err := versions.DeleteTemporary(ordinary.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ordinary.ID, fresh.ID, busy.ID} {
		if _, err := versions.GetByID(id); err != nil {
			t.Errorf("version %s was deleted: %v", id, err)
		}
	}
}

func TestLocalPlanRunsAreSeparate(t *testing.T) {
	database := dbtest.Open(t)
	environments := NewEnvironmentStore(database)
	if _, err := environments.Create(models.CreateEnvironmentRequest{Name: "production"}); err != nil {
		t.Fatal(err)
	}
	app, err := NewApplicationStore(database).Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	versions := NewVersionStore(database)
	runs := NewPlanRunStore(database)
	ordinary, err := versions.Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	aliceV1, _ := versions.CreateTemporary(app.ID, "local-alice-000000000001", "alice", time.Now().Add(time.Hour))
	aliceV2, _ := versions.CreateTemporary(app.ID, "local-alice-000000000002", "alice", time.Now().Add(time.Hour))
	bobV1, _ := versions.CreateTemporary(app.ID, "local-bob-000000000001", "bob", time.Now().Add(time.Hour))

	ci, _, err := runs.Create(app.ID, ordinary, "production", "main", "ci", "")
	if err != nil {
		t.Fatal(err)
	}
	first, superseded, err := runs.Create(app.ID, aliceV1, "production", "main", "alice", "alice")
	if err != nil || len(superseded) != 0 {
		t.Fatalf("alice's local plan superseded %+v, %v", superseded, err)
	}
	bob, superseded, err := runs.Create(app.ID, bobV1, "production", "main", "bob", "bob")
	if err != nil || len(superseded) != 0 {
		t.Fatalf("bob's local plan superseded %+v, %v", superseded, err)
	}

	// Alice's newer local plan replaces her earlier one, and only that
	second, superseded, err := runs.Create(app.ID, aliceV2, "production", "main", "alice", "alice")
	if err != nil || len(superseded) != 1 || superseded[0].ID != first.ID {
		t.Fatalf("alice's second local plan superseded %+v, %v", superseded, err)
	}

	// CI's newer plan replaces CI's, not the local plans
	_, superseded, err = runs.Create(app.ID, ordinary, "production", "main", "ci", "")
	if err != nil || len(superseded) != 1 || superseded[0].ID != ci.ID {
		t.Fatalf("the second plan of CI superseded %+v, %v", superseded, err)
	}
	for _, id := range []string{second.ID, bob.ID} {
		if run, err := runs.Get(id); err != nil || run.Status != models.PlanRunPending {
			t.Errorf("run %s = %+v, %v; want it left alone", id, run, err)
		}
	}
}
