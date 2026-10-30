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
