package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seed inserts one row of every dumped table
func seed(t *testing.T, d *DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO applications (id, name) VALUES ('app-1', 'hello')`,
		`INSERT INTO environments (id, name, description) VALUES ('env-1', 'production', 'Prod')`,
		`INSERT INTO versions (id, app_id, version_id, status, git_sha, git_branch, build_number, metadata_timestamp, published_at)
			VALUES ('ver-1', 'app-1', 'v1', 'published', 'abc123', 'master', '7', '2026-10-19 10:00:00', '2026-10-19 10:05:00')`,
		`INSERT INTO policies (id, app_id, name, git_branch_pattern, target_environment, enabled)
			VALUES ('pol-1', 'app-1', 'master-prod', 'master', 'production', TRUE)`,
		`INSERT INTO policies (id, app_id, name, git_branch_pattern, target_environment, enabled)
			VALUES ('pol-2', 'app-1', 'off', 'dev', 'production', FALSE)`,
		`INSERT INTO deployments (id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, completed_at)
			VALUES ('dep-1', 'app-1', 'ver-1', 'production', 'success', 'auto-deploy', 'pol-1', 'sha-1', '2026-10-19 10:06:00')`,
		`INSERT INTO terraform_environments (id, app_id, environment, backend_bucket, backend_key, backend_region, provisioned_at)
			VALUES ('tf-1', 'app-1', 'production', 'tfstate', 'hello/production/main/terraform.tfstate', 'eu-north-1', '2026-10-19 10:07:00')`,
	}
	for _, statement := range statements {
		if _, err := d.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func count(t *testing.T, d *DB, query string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// TestDumpLoadSQLiteToPostgres moves a database the way the migration does:
// dump, JSON round trip (as over the API), load into an empty database. It
// needs a PostgreSQL server, given as TEST_POSTGRES_DSN.
func TestDumpLoadSQLiteToPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}

	source, err := Open("sqlite", filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { source.Close() })
	seed(t, source)

	target, err := Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"terraform_environments", "deployments", "policies", "versions", "environments", "applications"} {
			target.Exec("DELETE FROM " + table)
		}
		target.Close()
	})

	dumps, err := source.Dump()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(dumps)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []TableDump
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if err := target.EnsureEmpty(); err != nil {
		t.Fatal(err)
	}
	if err := target.Load(decoded); err != nil {
		t.Fatal(err)
	}

	if n := count(t, target, `SELECT COUNT(*) FROM policies WHERE enabled`); n != 1 {
		t.Errorf("%d enabled policies, want 1", n)
	}
	if n := count(t, target, `SELECT COUNT(*) FROM deployments WHERE completed_at IS NOT NULL AND policy_id = 'pol-1'`); n != 1 {
		t.Errorf("%d completed deployments, want 1", n)
	}
	if n := count(t, target, `SELECT COUNT(*) FROM versions WHERE published_at > metadata_timestamp`); n != 1 {
		t.Errorf("%d versions with intact timestamps, want 1", n)
	}
	if n := count(t, target, `SELECT COUNT(*) FROM terraform_environments WHERE provisioned_at IS NOT NULL`); n != 1 {
		t.Errorf("%d terraform environments, want 1", n)
	}
}

func TestLoadRollsBackOnError(t *testing.T) {
	target, err := Open("sqlite", filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close() })

	err = target.Load([]TableDump{
		{Table: "applications", Rows: []map[string]any{
			{"id": "app-1", "name": "hello"},
			// Same primary key as the first row
			{"id": "app-1", "name": "other"},
		}},
	})
	if err == nil {
		t.Fatal("expected the import to fail")
	}
	if n := count(t, target, `SELECT COUNT(*) FROM applications`); n != 0 {
		t.Errorf("%d applications left behind by a failed import", n)
	}
}
