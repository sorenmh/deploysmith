-- Local plans can't be kept: their runs would break the old unique index
DELETE FROM plan_runs WHERE requested_by <> '';
DELETE FROM versions WHERE temporary;

DROP INDEX IF EXISTS idx_plan_runs_active_root;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_runs_active_root ON plan_runs(app_id, environment, terraform_root)
    WHERE status IN ('pending', 'planning');
ALTER TABLE plan_runs DROP COLUMN requested_by;

DROP INDEX IF EXISTS idx_versions_expires_at;
ALTER TABLE versions DROP COLUMN created_by;
ALTER TABLE versions DROP COLUMN expires_at;
ALTER TABLE versions DROP COLUMN temporary;
