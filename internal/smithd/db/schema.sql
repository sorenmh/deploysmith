-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Applications table
CREATE TABLE IF NOT EXISTS applications (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_applications_name ON applications(name);

-- Environments table
CREATE TABLE IF NOT EXISTS environments (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_environments_name ON environments(name);

-- Versions table
CREATE TABLE IF NOT EXISTS versions (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('draft', 'published')),
    -- What the version carries: Kubernetes manifests, or an OpenTofu bundle
    artifact_type TEXT NOT NULL DEFAULT 'manifests' CHECK(artifact_type IN ('manifests', 'terraform')),

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

CREATE INDEX IF NOT EXISTS idx_versions_app_id ON versions(app_id);
CREATE INDEX IF NOT EXISTS idx_versions_status ON versions(status);
CREATE INDEX IF NOT EXISTS idx_versions_created_at ON versions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_versions_git_branch ON versions(git_branch);

-- Policies table
CREATE TABLE IF NOT EXISTS policies (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    name TEXT NOT NULL,
    git_branch_pattern TEXT NOT NULL,
    target_environment TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (target_environment) REFERENCES environments(name) ON DELETE RESTRICT,
    UNIQUE(app_id, name)
);

CREATE INDEX IF NOT EXISTS idx_policies_app_id ON policies(app_id);
CREATE INDEX IF NOT EXISTS idx_policies_enabled ON policies(enabled);

-- Deployments table
CREATE TABLE IF NOT EXISTS deployments (
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

CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_environment ON deployments(environment);
CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments(started_at DESC);

-- Record schema version
INSERT OR IGNORE INTO schema_version (version) VALUES (2);

-- Web Push subscriptions: one row per browser/device that opted in
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id TEXT PRIMARY KEY,
    endpoint TEXT UNIQUE NOT NULL,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    user_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Terraform apps: one row per app and environment that Terraform is enabled
-- for, with the state backend smithd writes into the Terraform CR. Existing
-- apps keep their own backend; new ones default to the shared one.
CREATE TABLE IF NOT EXISTS terraform_environments (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    environment TEXT NOT NULL,

    -- S3 backend
    backend_bucket TEXT NOT NULL,
    backend_key TEXT NOT NULL,
    backend_region TEXT NOT NULL,
    backend_lock_table TEXT NOT NULL DEFAULT '',

    -- IAM role OpenBao assumes for apply runs; empty means the convention
    -- arn:aws:iam::<account>:role/tf-<app>-<env>
    aws_role_arn TEXT NOT NULL DEFAULT '',

    -- Outcome of the last provisioning (namespace in gitops, OpenBao roles)
    provisioned_at TIMESTAMP,
    provision_error TEXT,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (environment) REFERENCES environments(name) ON DELETE RESTRICT,
    UNIQUE(app_id, environment)
);

CREATE INDEX IF NOT EXISTS idx_terraform_environments_app_id ON terraform_environments(app_id);
