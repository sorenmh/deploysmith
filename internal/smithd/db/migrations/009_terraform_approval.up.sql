-- Who approved or rejected the plan of a terraform deployment and when
-- (DEP-20), whether the approval had to confirm destroys, and whether the
-- deployment puts an older version than the one applied (a rollback).
-- approval_reset_at is when smithd committed approvePlan: "" again after the
-- apply; an approved deployment that has finished without it is still to do.
ALTER TABLE deployments ADD COLUMN approved_by TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN approved_at TIMESTAMP;
ALTER TABLE deployments ADD COLUMN destroy_confirmed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE deployments ADD COLUMN rejected_by TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN rejected_at TIMESTAMP;
ALTER TABLE deployments ADD COLUMN reject_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN approval_reset_at TIMESTAMP;
ALTER TABLE deployments ADD COLUMN rollback BOOLEAN NOT NULL DEFAULT FALSE;
