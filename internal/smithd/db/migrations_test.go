package db

import (
	"os"
	"path/filepath"
	"testing"
)

// TestArtifactTypeMigration migrates a database holding a version from
// before artifact types down and up again: the version becomes manifests,
// and only known artifact types are accepted. It runs against SQLite, and
// also against PostgreSQL when TEST_POSTGRES_DSN is set.
func TestArtifactTypeMigration(t *testing.T) {
	drivers := map[string]string{"sqlite": filepath.Join(t.TempDir(), "smithd.db")}
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		drivers["postgres"] = dsn
	}

	for driver, dsn := range drivers {
		t.Run(driver, func(t *testing.T) {
			d, err := Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, table := range []string{"versions", "applications"} {
					if _, err := d.Exec("DELETE FROM " + table); err != nil {
						t.Errorf("failed to empty %s: %v", table, err)
					}
				}
				_ = d.Close()
			})

			m, err := d.migrator()
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Migrate(2); err != nil {
				t.Fatalf("migrating down: %v", err)
			}
			statements := []string{
				`INSERT INTO applications (id, name) VALUES ('app-1', 'hello')`,
				`INSERT INTO versions (id, app_id, version_id, status) VALUES ('ver-1', 'app-1', 'v1', 'published')`,
			}
			for _, statement := range statements {
				if _, err := d.Exec(statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}

			if err := d.runMigrations(); err != nil {
				t.Fatalf("migrating up: %v", err)
			}

			var artifactType string
			if err := d.QueryRow(`SELECT artifact_type FROM versions WHERE id = 'ver-1'`).Scan(&artifactType); err != nil {
				t.Fatal(err)
			}
			if artifactType != "manifests" {
				t.Errorf("existing version has artifact type %q, want manifests", artifactType)
			}

			if _, err := d.Exec(`INSERT INTO versions (id, app_id, version_id, status, artifact_type) VALUES ('ver-2', 'app-1', 'v2', 'draft', 'terraform')`); err != nil {
				t.Errorf("terraform version rejected: %v", err)
			}
			if _, err := d.Exec(`INSERT INTO versions (id, app_id, version_id, status, artifact_type) VALUES ('ver-3', 'app-1', 'v3', 'draft', 'helm')`); err == nil {
				t.Error("unknown artifact type accepted")
			}
		})
	}
}
