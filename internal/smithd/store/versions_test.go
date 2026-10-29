package store

import (
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestVersionSource(t *testing.T) {
	database := dbtest.Open(t)
	app, err := NewApplicationStore(database).Create("hello")
	if err != nil {
		t.Fatal(err)
	}
	versions := NewVersionStore(database)
	version, err := versions.Create(app.ID, "v1", "", models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}

	// Versions without a source have an empty one
	source, err := versions.GetSource(version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source != (models.VersionSource{}) {
		t.Errorf("expected an empty source, got %+v", source)
	}

	want := models.VersionSource{Repository: "acme/web", CommitSHA: "0123456789abcdef0123456789abcdef01234567", PullRequest: 42}
	if err := versions.SetSource(version.ID, want); err != nil {
		t.Fatal(err)
	}
	if source, err = versions.GetSource(version.ID); err != nil {
		t.Fatal(err)
	}
	if source != want {
		t.Errorf("source = %+v, want %+v", source, want)
	}

	if err := versions.SetSource("missing", want); err == nil {
		t.Error("expected an error for a missing version")
	}
	if _, err := versions.GetSource("missing"); err == nil {
		t.Error("expected an error for a missing version")
	}
}
