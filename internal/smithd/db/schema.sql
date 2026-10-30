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

    -- Where it was built from, for reporting to GitHub (sent by forge)
    source_repo TEXT NOT NULL DEFAULT '',   -- owner/name
    source_sha TEXT NOT NULL DEFAULT '',    -- the commit (a PR's head, not its merge commit)
    source_pr INTEGER NOT NULL DEFAULT 0,   -- pull request number, 0 if none

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

CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_environment ON deployments(environment);
CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments(started_at DESC);

-- One active run per app, environment and root
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployments_active_terraform_root ON deployments(app_id, environment, terraform_root)
    WHERE terraform_root IS NOT NULL AND status IN ('pending', 'planning', 'awaiting_approval', 'applying');

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

    -- Existing plaintext state is read through an unencrypted fallback until
    -- its first apply has written it back encrypted
    encryption_fallback BOOLEAN NOT NULL DEFAULT FALSE,

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

-- The readable plan of a terraform deployment, copied from the cluster when
-- the controller has made it. The binary plan is never stored.
CREATE TABLE IF NOT EXISTS terraform_plans (
    deployment_id TEXT PRIMARY KEY,

    -- Identify the plan together with planned_at: the controller reuses the
    -- plan id for every plan of a revision
    plan_id TEXT NOT NULL,
    plan_hash TEXT NOT NULL DEFAULT '',
    planned_at TIMESTAMP,

    -- The "Plan: X to add, Y to change, Z to destroy." line; destroys is set
    -- if the plan destroys or replaces resources
    to_add INTEGER NOT NULL DEFAULT 0,
    to_change INTEGER NOT NULL DEFAULT 0,
    to_destroy INTEGER NOT NULL DEFAULT 0,
    destroys BOOLEAN NOT NULL DEFAULT FALSE,
    output_changes BOOLEAN NOT NULL DEFAULT FALSE,

    -- JSON: the resource addresses by action (create, update, replace, destroy)
    resources TEXT NOT NULL DEFAULT '{}',
    plan_text TEXT NOT NULL DEFAULT '',

    -- Hash of the plan made again right before the apply, and whether it
    -- differs from the reviewed one
    applied_plan_hash TEXT NOT NULL DEFAULT '',
    applied_differed BOOLEAN NOT NULL DEFAULT FALSE,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Plan-only runs of terraform versions (DEP-22): a plan of a draft or
-- published version for an environment, made without deploying it. They are
-- not deployments: they can never be approved or applied, don't count for
-- what runs in an environment, and don't compete with the one active run of
-- a root (DEP-18). The plan is kept here once the controller has made it,
-- like terraform_plans does for deployments.
CREATE TABLE IF NOT EXISTS plan_runs (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    terraform_root TEXT NOT NULL,

    -- pending (being committed to the plans branch), planning, then planned
    -- (the plan is stored), failed or superseded (a newer run replaced it)
    status TEXT NOT NULL CHECK(status IN ('pending', 'planning', 'planned', 'failed', 'superseded')),
    triggered_by TEXT,
    error_message TEXT,

    -- The commit of the Terraform CR on the plans branch, the one holding
    -- the source that the CR's GitRepository is pinned to, and the id of the
    -- plan the controller makes for it
    gitops_commit_sha TEXT,
    source_sha TEXT,
    plan_id TEXT,

    -- The plan, once planned: the same as terraform_plans
    plan_hash TEXT NOT NULL DEFAULT '',
    planned_at TIMESTAMP,
    to_add INTEGER NOT NULL DEFAULT 0,
    to_change INTEGER NOT NULL DEFAULT 0,
    to_destroy INTEGER NOT NULL DEFAULT 0,
    destroys BOOLEAN NOT NULL DEFAULT FALSE,
    output_changes BOOLEAN NOT NULL DEFAULT FALSE,
    resources TEXT NOT NULL DEFAULT '{}',
    plan_text TEXT NOT NULL DEFAULT '',

    -- When the run's files were removed from the plans branch
    cleaned_at TIMESTAMP,

    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,

    FOREIGN KEY (app_id) REFERENCES applications(id) ON DELETE CASCADE,
    FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE,
    FOREIGN KEY (environment) REFERENCES environments(name) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_plan_runs_version ON plan_runs(version_id, environment, started_at DESC);

-- One active run per app, environment and root: a newer run replaces it
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_runs_active_root ON plan_runs(app_id, environment, terraform_root)
    WHERE status IN ('pending', 'planning');

-- Finished runs whose files are still on the plans branch
CREATE INDEX IF NOT EXISTS idx_plan_runs_uncleaned ON plan_runs(status)
    WHERE cleaned_at IS NULL;

-- People who signed in with GitHub, keyed by their GitHub user id (logins
-- can be renamed)
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    github_id BIGINT UNIQUE NOT NULL,
    login TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- smithd sessions of signed-in users, for smithctl ('cli') and the web UI
-- ('web'). Only a SHA-256 hash of the session token is stored.
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    token_hash TEXT UNIQUE NOT NULL,
    user_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('cli', 'web')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,

    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
