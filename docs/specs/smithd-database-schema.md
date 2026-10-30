# smithd Database Schema

SQLite database schema for smithd MVP.

## Tables

### `applications`

Stores registered applications.

```sql
CREATE TABLE applications (
    id TEXT PRIMARY KEY,                    -- UUID
    name TEXT UNIQUE NOT NULL,              -- Application name
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_applications_name ON applications(name);
```

**Example:**
```sql
INSERT INTO applications VALUES (
    'app-123',
    'my-api-service',
    '2025-01-15 10:30:00',
    '2025-01-15 10:30:00'
);
```

**Note:** Gitops repository is configured globally via `GITOPS_REPO` environment variable. The path for each app is derived as: `environments/{environment}/apps/{app_name}/`

---

### `versions`

Stores version metadata.

```sql
CREATE TABLE versions (
    id TEXT PRIMARY KEY,                    -- UUID
    app_id TEXT NOT NULL,                   -- FK to applications
    version_id TEXT NOT NULL,               -- User-provided version identifier (e.g., "42540c4-123")
    status TEXT NOT NULL,                   -- "draft" or "published"

    -- Metadata from version.yml
    git_sha TEXT,
    git_branch TEXT,
    git_committer TEXT,
    build_number TEXT,
    metadata_timestamp TIMESTAMP,

    -- Lifecycle timestamps
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    UNIQUE(app_id, version_id)
);

CREATE INDEX idx_versions_app_id ON versions(app_id);
CREATE INDEX idx_versions_status ON versions(status);
CREATE INDEX idx_versions_created_at ON versions(created_at DESC);
CREATE INDEX idx_versions_git_branch ON versions(git_branch);
```

**Example:**
```sql
INSERT INTO versions VALUES (
    'ver-456',
    'app-123',
    '42540c4-123',
    'published',
    '42540c4abc123',
    'main',
    'john@example.com',
    '123',
    '2025-01-15 10:30:00',
    '2025-01-15 10:30:00',
    '2025-01-15 10:35:00'
);
```

---

### `deployments`

Stores deployment history.

```sql
CREATE TABLE deployments (
    id TEXT PRIMARY KEY,                    -- UUID
    app_id TEXT NOT NULL,                   -- FK to applications
    version_id TEXT NOT NULL,               -- FK to versions (internal ID)
    environment TEXT NOT NULL,              -- Target environment (e.g., "staging")
    status TEXT NOT NULL,                   -- "pending", "success", "failed"

    -- Deployment details
    triggered_by TEXT,                      -- "auto" or "manual"
    policy_id TEXT,                         -- FK to policies if triggered by auto-deploy

    -- Git commit info
    gitops_commit_sha TEXT,                 -- Commit SHA in gitops repo

    -- Error details (if failed)
    error_message TEXT,

    -- Timestamps
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE,
    FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE SET NULL
);

CREATE INDEX idx_deployments_app_id ON deployments(app_id);
CREATE INDEX idx_deployments_environment ON deployments(environment);
CREATE INDEX idx_deployments_started_at ON deployments(started_at DESC);
```

**Example:**
```sql
INSERT INTO deployments VALUES (
    'deploy-789',
    'app-123',
    'ver-456',
    'staging',
    'success',
    'manual',
    NULL,
    'abc123def456',
    NULL,
    '2025-01-15 10:40:00',
    '2025-01-15 10:40:05'
);
```

---

### `policies`

Stores auto-deployment policies.

```sql
CREATE TABLE policies (
    id TEXT PRIMARY KEY,                    -- UUID
    app_id TEXT NOT NULL,                   -- FK to applications
    name TEXT NOT NULL,                     -- Policy name
    git_branch_pattern TEXT NOT NULL,       -- Branch pattern (supports wildcards)
    target_environment TEXT NOT NULL,       -- Target environment
    enabled BOOLEAN NOT NULL DEFAULT 1,     -- Is policy active
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    UNIQUE(app_id, name)
);

CREATE INDEX idx_policies_app_id ON policies(app_id);
CREATE INDEX idx_policies_enabled ON policies(enabled);
```

**Example:**
```sql
INSERT INTO policies VALUES (
    'policy-101',
    'app-123',
    'auto-deploy-main-to-staging',
    'main',
    'staging',
    1,
    '2025-01-15 11:00:00'
);
```

---

### Terraform and login tables

Added by migrations 003 to 012 (SQL in `internal/smithd/db/migrations/`). Columns are summarised; the migration files are the definition.

| Migration | Change |
|---|---|
| 003 | `versions.artifact_type` (`manifests` or `terraform`, default `manifests`) |
| 004 | `users` (GitHub id, login) and `sessions` (SHA-256 hash of the session token, `cli` or `web`, expiry) |
| 005 | `terraform_environments`: one row per app and environment with the S3 backend (`backend_bucket`, `backend_key`, `backend_region`, `backend_lock_table`), an optional `aws_role_arn` and the outcome of the last provisioning (`provisioned_at`, `provision_error`); unique per app and environment |
| 006 | `versions.source_repo`, `source_sha`, `source_pr`: where the version was built from, for GitHub check runs |
| 007 | `deployments` rebuilt with the Terraform states (`planning`, `awaiting_approval`, `applying`, `applied`, `stale`, `rejected`) and `terraform_root`, `terraform_source_sha`, `terraform_plan_id`; a partial unique index allows one active Terraform deployment per app, environment and root; `terraform_environments.encryption_fallback` |
| 008 | `terraform_plans`: the readable plan of a deployment (counts, `destroys`, resource addresses by action as JSON, the plan text, `plan_hash`, and `applied_plan_hash` / `applied_differed` after the apply) |
| 009 | `deployments.approved_by`, `approved_at`, `destroy_confirmed`, `rejected_by`, `rejected_at`, `reject_reason`, `approval_reset_at`, `rollback` |
| 010 | `plan_runs`: plan-only runs of a version for an environment (status `pending`, `planning`, `planned`, `failed`, `superseded`, the plan, `cleaned_at`); one active run per app, environment and root |
| 012 | `versions.temporary`, `expires_at`, `created_by` (local plans); `plan_runs.requested_by`, and the active-run index is per requester |

## Queries

### Get current deployed version per environment

```sql
SELECT
    d.environment,
    v.version_id,
    d.completed_at
FROM deployments d
JOIN versions v ON d.version_id = v.id
WHERE
    d.app_id = ?
    AND d.status = 'success'
    AND d.completed_at = (
        SELECT MAX(d2.completed_at)
        FROM deployments d2
        WHERE d2.app_id = d.app_id
        AND d2.environment = d.environment
        AND d2.status = 'success'
    )
ORDER BY d.environment;
```

### Check if version matches any auto-deploy policy

```sql
SELECT
    p.id,
    p.name,
    p.target_environment
FROM policies p
WHERE
    p.app_id = ?
    AND p.enabled = 1
    AND (
        p.git_branch_pattern = ?
        OR ? LIKE REPLACE(p.git_branch_pattern, '*', '%')
    );
```

### List versions with deployment status

```sql
SELECT
    v.*,
    GROUP_CONCAT(DISTINCT d.environment) as deployed_to
FROM versions v
LEFT JOIN deployments d ON v.id = d.version_id
    AND d.status = 'success'
    AND d.completed_at = (
        SELECT MAX(d2.completed_at)
        FROM deployments d2
        WHERE d2.version_id = v.id
        AND d2.environment = d.environment
        AND d2.status = 'success'
    )
WHERE v.app_id = ?
GROUP BY v.id
ORDER BY v.created_at DESC;
```

---

## Migration Strategy

For MVP, we'll use a simple migration approach:

1. Embed SQL files in the binary
2. Track schema version in a `schema_version` table
3. Apply migrations on startup if needed

```sql
CREATE TABLE schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Future: Consider using a migration tool like `golang-migrate` or `goose`.
