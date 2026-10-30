ALTER TABLE deployments DROP COLUMN rollback;
ALTER TABLE deployments DROP COLUMN approval_reset_at;
ALTER TABLE deployments DROP COLUMN reject_reason;
ALTER TABLE deployments DROP COLUMN rejected_at;
ALTER TABLE deployments DROP COLUMN rejected_by;
ALTER TABLE deployments DROP COLUMN destroy_confirmed;
ALTER TABLE deployments DROP COLUMN approved_at;
ALTER TABLE deployments DROP COLUMN approved_by;
