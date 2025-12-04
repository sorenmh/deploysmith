package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/deploysmith/deploysmith/internal/smithd/models"
	"github.com/google/uuid"
)

// DeploymentStore handles deployment database operations
type DeploymentStore struct {
	db *sql.DB
}

// NewDeploymentStore creates a new deployment store
func NewDeploymentStore(db *sql.DB) *DeploymentStore {
	return &DeploymentStore{db: db}
}

// Create creates a new deployment record
func (s *DeploymentStore) Create(appID, versionID, environment, triggeredBy string, policyID *string) (*models.Deployment, error) {
	deployment := &models.Deployment{
		ID:          uuid.New().String(),
		AppID:       appID,
		VersionID:   versionID,
		Environment: environment,
		Status:      "pending",
		TriggeredBy: triggeredBy,
		PolicyID:    policyID,
		StartedAt:   time.Now().UTC(),
	}

	_, err := s.db.Exec(`
		INSERT INTO deployments (id, app_id, version_id, environment, status, triggered_by, policy_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, deployment.ID, deployment.AppID, deployment.VersionID, deployment.Environment, deployment.Status, deployment.TriggeredBy, deployment.PolicyID, deployment.StartedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	return deployment, nil
}

// GetByID gets a deployment by ID
func (s *DeploymentStore) GetByID(id string) (*models.Deployment, error) {
	var deployment models.Deployment
	var completedAt sql.NullTime
	var policyID sql.NullString

	err := s.db.QueryRow(`
		SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at
		FROM deployments
		WHERE id = ?
	`, id).Scan(&deployment.ID, &deployment.AppID, &deployment.VersionID, &deployment.Environment, &deployment.Status, &deployment.TriggeredBy, &policyID, &deployment.GitopsCommitSHA, &deployment.ErrorMessage, &deployment.StartedAt, &completedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	if completedAt.Valid {
		deployment.CompletedAt = &completedAt.Time
	}
	if policyID.Valid {
		deployment.PolicyID = &policyID.String
	}

	return &deployment, nil
}

// List lists deployments with optional filtering by app and environment
func (s *DeploymentStore) List(appID, environment string, limit, offset int) ([]models.Deployment, int, error) {
	// Build query with filters
	query := "SELECT COUNT(*) FROM deployments WHERE 1=1"
	args := []interface{}{}

	if appID != "" {
		query += " AND app_id = ?"
		args = append(args, appID)
	}
	if environment != "" {
		query += " AND environment = ?"
		args = append(args, environment)
	}

	// Get total count
	var total int
	err := s.db.QueryRow(query, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count deployments: %w", err)
	}

	// Get deployments
	query = `SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, gitops_commit_sha, error_message, started_at, completed_at
		FROM deployments WHERE 1=1`

	if appID != "" {
		query += " AND app_id = ?"
	}
	if environment != "" {
		query += " AND environment = ?"
	}

	query += " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list deployments: %w", err)
	}
	defer rows.Close()

	deployments := []models.Deployment{}
	for rows.Next() {
		var deployment models.Deployment
		var completedAt sql.NullTime
		var policyID sql.NullString

		err := rows.Scan(&deployment.ID, &deployment.AppID, &deployment.VersionID, &deployment.Environment, &deployment.Status, &deployment.TriggeredBy, &policyID, &deployment.GitopsCommitSHA, &deployment.ErrorMessage, &deployment.StartedAt, &completedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan deployment: %w", err)
		}

		if completedAt.Valid {
			deployment.CompletedAt = &completedAt.Time
		}
		if policyID.Valid {
			deployment.PolicyID = &policyID.String
		}

		deployments = append(deployments, deployment)
	}

	return deployments, total, nil
}

// UpdateStatus updates the deployment status
func (s *DeploymentStore) UpdateStatus(id, status, gitopsSHA, errorMsg string) error {
	now := time.Now().UTC()

	result, err := s.db.Exec(`
		UPDATE deployments
		SET status = ?, gitops_commit_sha = ?, error_message = ?, completed_at = ?
		WHERE id = ?
	`, status, gitopsSHA, errorMsg, now, id)

	if err != nil {
		return fmt.Errorf("failed to update deployment status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("deployment not found")
	}

	return nil
}
