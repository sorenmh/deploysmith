package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// TerraformStore handles the Terraform environments of apps
type TerraformStore struct {
	db *db.DB
}

// NewTerraformStore creates a new Terraform store
func NewTerraformStore(db *db.DB) *TerraformStore {
	return &TerraformStore{db: db}
}

const terraformColumns = `
	t.id, t.app_id, a.name, t.environment,
	t.backend_bucket, t.backend_key, t.backend_region, t.backend_lock_table,
	t.aws_role_arn, t.encryption_fallback, t.provisioned_at, t.provision_error, t.created_at, t.updated_at`

// Create enables Terraform for an app in an environment
func (s *TerraformStore) Create(appID, environment string, backend models.TerraformBackend, awsRoleARN string, encryptionFallback bool) (*models.TerraformEnvironment, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO terraform_environments (id, app_id, environment, backend_bucket, backend_key, backend_region, backend_lock_table, aws_role_arn, encryption_fallback, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, appID, environment, backend.Bucket, backend.Key, backend.Region, backend.LockTable, awsRoleARN, encryptionFallback, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to enable terraform: %w", err)
	}
	return s.Get(appID, environment)
}

// Get returns an app's Terraform environment, or nil if Terraform is not
// enabled for the app in that environment
func (s *TerraformStore) Get(appID, environment string) (*models.TerraformEnvironment, error) {
	rows, err := s.db.Query(`
		SELECT `+terraformColumns+`
		FROM terraform_environments t
		JOIN applications a ON a.id = t.app_id
		WHERE t.app_id = ? AND t.environment = ?
	`, appID, environment)
	if err != nil {
		return nil, fmt.Errorf("failed to get terraform environment: %w", err)
	}
	list, err := scanTerraformEnvironments(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// List returns an app's Terraform environments, by environment name
func (s *TerraformStore) List(appID string) ([]models.TerraformEnvironment, error) {
	rows, err := s.db.Query(`
		SELECT `+terraformColumns+`
		FROM terraform_environments t
		JOIN applications a ON a.id = t.app_id
		WHERE t.app_id = ?
		ORDER BY t.environment
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to list terraform environments: %w", err)
	}
	return scanTerraformEnvironments(rows)
}

// ListAll returns the Terraform environments of all apps
func (s *TerraformStore) ListAll() ([]models.TerraformEnvironment, error) {
	rows, err := s.db.Query(`
		SELECT ` + terraformColumns + `
		FROM terraform_environments t
		JOIN applications a ON a.id = t.app_id
		ORDER BY a.name, t.environment
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list terraform environments: %w", err)
	}
	return scanTerraformEnvironments(rows)
}

// SetEncryptionFallback turns the unencrypted state fallback of an
// environment on or off
func (s *TerraformStore) SetEncryptionFallback(id string, fallback bool) error {
	_, err := s.db.Exec(`
		UPDATE terraform_environments SET encryption_fallback = ?, updated_at = ? WHERE id = ?
	`, fallback, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("failed to update terraform environment: %w", err)
	}
	return nil
}

// SetProvisioned records the outcome of provisioning an environment: on
// success (provisionErr nil) the time, otherwise the error. A failure keeps
// the time of the last success.
func (s *TerraformStore) SetProvisioned(id string, provisionErr error) error {
	now := time.Now().UTC()
	var err error
	if provisionErr == nil {
		_, err = s.db.Exec(`
			UPDATE terraform_environments SET provisioned_at = ?, provision_error = NULL, updated_at = ? WHERE id = ?
		`, now, now, id)
	} else {
		_, err = s.db.Exec(`
			UPDATE terraform_environments SET provision_error = ?, updated_at = ? WHERE id = ?
		`, provisionErr.Error(), now, id)
	}
	if err != nil {
		return fmt.Errorf("failed to record provisioning: %w", err)
	}
	return nil
}

func scanTerraformEnvironments(rows *sql.Rows) ([]models.TerraformEnvironment, error) {
	defer rows.Close()

	list := []models.TerraformEnvironment{}
	for rows.Next() {
		var env models.TerraformEnvironment
		var provisionedAt sql.NullTime
		var provisionError sql.NullString
		err := rows.Scan(&env.ID, &env.AppID, &env.AppName, &env.Environment,
			&env.Backend.Bucket, &env.Backend.Key, &env.Backend.Region, &env.Backend.LockTable,
			&env.AWSRoleARN, &env.EncryptionFallback, &provisionedAt, &provisionError, &env.CreatedAt, &env.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan terraform environment: %w", err)
		}
		if provisionedAt.Valid {
			env.ProvisionedAt = &provisionedAt.Time
		}
		if provisionError.Valid {
			env.ProvisionError = &provisionError.String
		}
		list = append(list, env)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read terraform environments: %w", err)
	}
	return list, nil
}
