ALTER TABLE terraform_environments DROP COLUMN encryption_fallback;

-- Terraform deployments don't fit the old status check
DELETE FROM deployments WHERE terraform_root IS NOT NULL;

CREATE TABLE deployments_old (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending', 'success', 'failed')),

    -- Deployment details
    triggered_by TEXT,
    policy_id TEXT,

    -- Git commit info
    gitops_commit_sha TEXT,

    -- Error details (if failed)
    error_message TEXT,

    -- Timestamps
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE,
    FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE SET NULL,
    FOREIGN KEY (environment) REFERENCES environments(name) ON DELETE RESTRICT
);

INSERT INTO deployments_old (id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at)
    SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at FROM deployments;

DROP TABLE deployments;
ALTER TABLE deployments_old RENAME TO deployments;

CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_environment ON deployments(environment);
CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments(started_at DESC);
