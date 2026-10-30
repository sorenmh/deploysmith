-- Temporary versions (DEP-25): the bundle of a local plan (forge plan
-- --terraform), uploaded by a signed-in user to see a plan of uncommitted
-- changes. They stay drafts, can never be published or deployed, are hidden
-- from version lists by default and expire: smithd deletes them with their
-- bundle once expires_at has passed.
ALTER TABLE versions ADD COLUMN temporary BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE versions ADD COLUMN expires_at TIMESTAMP;
ALTER TABLE versions ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_versions_expires_at ON versions(expires_at) WHERE temporary;

-- Who a plan-only run is for: the user of a local plan, empty for the plans
-- of CI and of published versions. The active run of a root is now per
-- requester, so a local plan only replaces the same user's earlier one and
-- never someone else's plan or CI's.
ALTER TABLE plan_runs ADD COLUMN requested_by TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_plan_runs_active_root;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_runs_active_root ON plan_runs(app_id, environment, terraform_root, requested_by)
    WHERE status IN ('pending', 'planning');
