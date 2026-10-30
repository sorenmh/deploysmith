package models

import "time"

// TerraformBackend is the S3 state backend of a Terraform app in an
// environment. smithd writes it into the Terraform CR; bundles never set
// their own backend.
type TerraformBackend struct {
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	Region    string `json:"region"`
	LockTable string `json:"lockTable,omitempty"`
}

// TerraformEnvironment marks an app as a Terraform app in an environment
type TerraformEnvironment struct {
	ID          string           `json:"id"`
	AppID       string           `json:"appId"`
	AppName     string           `json:"appName"`
	Environment string           `json:"environment"`
	Backend     TerraformBackend `json:"backend"`
	// AWSRoleARN is the IAM role OpenBao assumes for apply runs; plan-only
	// runs use the same ARN with a "-readonly" suffix. Empty means the
	// convention arn:aws:iam::<account>:role/tf-<app>-<env>.
	AWSRoleARN string `json:"awsRoleArn,omitempty"`
	// EncryptionFallback lets runs read plaintext state, for the first
	// apply of an app whose existing state is not encrypted yet; that
	// apply writes it back encrypted (docs/terraform-migration.md)
	EncryptionFallback bool `json:"encryptionFallback"`
	// ProvisionedAt is when the namespace and OpenBao roles were last set
	// up without errors; ProvisionError is the error of the last attempt
	ProvisionedAt  *time.Time `json:"provisionedAt,omitempty"`
	ProvisionError *string    `json:"provisionError,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// EnableTerraformRequest is the request to enable Terraform for an app in an
// environment. Backend fields left empty default to the shared backend.
type EnableTerraformRequest struct {
	Environment string           `json:"environment"`
	Backend     TerraformBackend `json:"backend"`
	AWSRoleARN  string           `json:"awsRoleArn,omitempty"`
	// EncryptionFallback is for existing apps with plaintext state
	EncryptionFallback bool `json:"encryptionFallback,omitempty"`
}

// UpdateTerraformRequest changes the settings of a Terraform environment;
// fields left out are kept
type UpdateTerraformRequest struct {
	EncryptionFallback *bool `json:"encryptionFallback,omitempty"`
}

// SyncTerraformRequest is the request to provision an app's Terraform
// environments again; an empty Environment means all of them
type SyncTerraformRequest struct {
	Environment string `json:"environment,omitempty"`
}

// ListTerraformEnvironmentsResponse is the response for listing an app's
// Terraform environments
type ListTerraformEnvironmentsResponse struct {
	Environments []TerraformEnvironment `json:"environments"`
}
