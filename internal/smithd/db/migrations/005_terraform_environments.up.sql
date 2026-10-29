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
