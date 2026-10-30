-- The readable plan of a terraform deployment (DEP-19), copied from the
-- cluster when the controller has made it: the summary and the full text of
-- `tofu show`, which the cluster overwrites with the next plan. The binary
-- plan is never stored.
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
