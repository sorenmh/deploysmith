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

// TestTerraformDeploymentsMigration migrates a database holding a deployment
// from before terraform deployments down and up again: the deployment is
// kept, terraform states are accepted, and only one run per app,
// environment and root can be active. It runs against SQLite, and also
// against PostgreSQL when TEST_POSTGRES_DSN is set.
func TestTerraformDeploymentsMigration(t *testing.T) {
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
				for _, table := range []string{"deployments", "versions", "environments", "applications"} {
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
			if err := m.Migrate(5); err != nil {
				t.Fatalf("migrating down: %v", err)
			}
			statements := []string{
				`INSERT INTO applications (id, name) VALUES ('app-1', 'hello')`,
				`INSERT INTO environments (id, name) VALUES ('env-1', 'production')`,
				`INSERT INTO versions (id, app_id, version_id, status) VALUES ('ver-1', 'app-1', 'v1', 'published')`,
				`INSERT INTO deployments (id, app_id, version_id, environment, status, gitops_commit_sha)
					VALUES ('dep-1', 'app-1', 'ver-1', 'production', 'success', 'sha-1')`,
			}
			for _, statement := range statements {
				if _, err := d.Exec(statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}

			if err := d.runMigrations(); err != nil {
				t.Fatalf("migrating up: %v", err)
			}

			var status, sha string
			if err := d.QueryRow(`SELECT status, gitops_commit_sha FROM deployments WHERE id = 'dep-1'`).Scan(&status, &sha); err != nil {
				t.Fatal(err)
			}
			if status != "success" || sha != "sha-1" {
				t.Errorf("existing deployment has status %q and commit %q", status, sha)
			}

			insert := func(id, status string) error {
				_, err := d.Exec(`INSERT INTO deployments (id, app_id, version_id, environment, status, terraform_root) VALUES (?, 'app-1', 'ver-1', 'production', ?, 'main')`, id, status)
				return err
			}
			if err := insert("dep-2", "stale"); err != nil {
				t.Errorf("stale deployment rejected: %v", err)
			}
			if err := insert("dep-3", "awaiting_approval"); err != nil {
				t.Errorf("awaiting_approval deployment rejected: %v", err)
			}
			if err := insert("dep-4", "planning"); err == nil {
				t.Error("second active run of the same root accepted")
			}
			if err := insert("dep-5", "deployed"); err == nil {
				t.Error("unknown status accepted")
			}
		})
	}
}
