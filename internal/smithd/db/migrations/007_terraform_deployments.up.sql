-- Terraform deployments (DEP-18) have their own states and know the root and
-- the plan they are for. SQLite can't change a CHECK constraint, so the table
-- is rebuilt; this works the same in PostgreSQL.
CREATE TABLE deployments_new (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    -- pending, success and failed for manifests; terraform deployments go
    -- pending -> planning -> awaiting_approval -> applying -> applied, or end
    -- as failed, stale (replaced by a newer deploy) or rejected
    status TEXT NOT NULL CHECK(status IN ('pending', 'success', 'failed', 'planning', 'awaiting_approval', 'applying', 'applied', 'stale', 'rejected')),

    -- Deployment details
    triggered_by TEXT,
    policy_id TEXT,

    -- Git commit info
    gitops_commit_sha TEXT,

    -- Error details (if failed)
    error_message TEXT,

    -- Terraform deployments only: the root, the gitops commit holding its
    -- source and the id of the plan the controller makes for that commit
    terraform_root TEXT,
    terraform_source_sha TEXT,
    terraform_plan_id TEXT,

    -- Timestamps
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE,
    FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE SET NULL,
    FOREIGN KEY (environment) REFERENCES environments(name) ON DELETE RESTRICT
);

INSERT INTO deployments_new (id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at)
    SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at FROM deployments;

DROP TABLE deployments;
ALTER TABLE deployments_new RENAME TO deployments;

CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_environment ON deployments(environment);
CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments(started_at DESC);

-- One active run per app, environment and root
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployments_active_terraform_root ON deployments(app_id, environment, terraform_root)
    WHERE terraform_root IS NOT NULL AND status IN ('pending', 'planning', 'awaiting_approval', 'applying');

-- Existing plaintext state is read through an unencrypted fallback until its
-- first apply has written it back encrypted (docs/terraform-migration.md)
ALTER TABLE terraform_environments ADD COLUMN encryption_fallback BOOLEAN NOT NULL DEFAULT FALSE;
