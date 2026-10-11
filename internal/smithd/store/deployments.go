package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// DeploymentStore handles deployment database operations
type DeploymentStore struct {
	db *db.DB
}

// NewDeploymentStore creates a new deployment store
func NewDeploymentStore(db *db.DB) *DeploymentStore {
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
		SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, COALESCE(gitops_commit_sha, ''), COALESCE(error_message, ''), started_at, completed_at
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
	query = `SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, COALESCE(gitops_commit_sha, ''), COALESCE(error_message, ''), started_at, completed_at
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

// DeploymentFilter narrows down a deployment listing; empty fields match all
type DeploymentFilter struct {
	AppID       string
	Environment string
	Status      string
}

const deploymentDetailSelect = `
	SELECT d.id, d.app_id, COALESCE(a.name, ''), COALESCE(v.version_id, ''), d.environment, d.status,
		COALESCE(d.triggered_by, ''), COALESCE(d.policy_id, ''), COALESCE(p.name, ''),
		COALESCE(d.gitops_commit_sha, ''), COALESCE(d.error_message, ''), d.started_at, d.completed_at
	FROM deployments d
	LEFT JOIN applications a ON a.id = d.app_id
	LEFT JOIN versions v ON v.id = d.version_id
	LEFT JOIN policies p ON p.id = d.policy_id`

func scanDeploymentDetail(scanner interface{ Scan(...any) error }) (models.DeploymentDetail, error) {
	var detail models.DeploymentDetail
	var completedAt sql.NullTime

	err := scanner.Scan(&detail.ID, &detail.AppID, &detail.AppName, &detail.VersionID, &detail.Environment, &detail.Status,
		&detail.TriggeredBy, &detail.PolicyID, &detail.PolicyName,
		&detail.GitopsCommitSHA, &detail.ErrorMessage, &detail.StartedAt, &completedAt)
	if err != nil {
		return detail, err
	}

	if completedAt.Valid {
		detail.CompletedAt = &completedAt.Time
	}
	return detail, nil
}

// ListDetailed lists deployments, newest first, with app, version and policy names
func (s *DeploymentStore) ListDetailed(filter DeploymentFilter, limit, offset int) ([]models.DeploymentDetail, int, error) {
	where := " WHERE 1=1"
	args := []interface{}{}

	if filter.AppID != "" {
		where += " AND d.app_id = ?"
		args = append(args, filter.AppID)
	}
	if filter.Environment != "" {
		where += " AND d.environment = ?"
		args = append(args, filter.Environment)
	}
	if filter.Status != "" {
		where += " AND d.status = ?"
		args = append(args, filter.Status)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM deployments d"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count deployments: %w", err)
	}

	rows, err := s.db.Query(deploymentDetailSelect+where+" ORDER BY d.started_at DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list deployments: %w", err)
	}
	defer rows.Close()

	deployments := []models.DeploymentDetail{}
	for rows.Next() {
		detail, err := scanDeploymentDetail(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan deployment: %w", err)
		}
		deployments = append(deployments, detail)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to list deployments: %w", err)
	}

	return deployments, total, nil
}

// GetDetailed gets a deployment with app, version and policy names
func (s *DeploymentStore) GetDetailed(id string) (*models.DeploymentDetail, error) {
	detail, err := scanDeploymentDetail(s.db.QueryRow(deploymentDetailSelect+" WHERE d.id = ?", id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	return &detail, nil
}

// ListStalePending returns deployments that are still pending but were
// started before cutoff, i.e. deployments whose worker most likely died
func (s *DeploymentStore) ListStalePending(cutoff time.Time) ([]models.Deployment, error) {
	rows, err := s.db.Query(`
		SELECT id, app_id, version_id, environment, status, triggered_by, policy_id, COALESCE(gitops_commit_sha, ''), COALESCE(error_message, ''), started_at, completed_at
		FROM deployments
		WHERE status = 'pending' AND started_at < ?
		ORDER BY started_at ASC
	`, cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to list stale deployments: %w", err)
	}
	defer rows.Close()

	deployments := []models.Deployment{}
	for rows.Next() {
		var deployment models.Deployment
		var completedAt sql.NullTime
		var policyID sql.NullString

		err := rows.Scan(&deployment.ID, &deployment.AppID, &deployment.VersionID, &deployment.Environment, &deployment.Status, &deployment.TriggeredBy, &policyID, &deployment.GitopsCommitSHA, &deployment.ErrorMessage, &deployment.StartedAt, &completedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deployment: %w", err)
		}

		if completedAt.Valid {
			deployment.CompletedAt = &completedAt.Time
		}
		if policyID.Valid {
			deployment.PolicyID = &policyID.String
		}

		deployments = append(deployments, deployment)
	}

	return deployments, nil
}

// ClaimStale atomically takes over a stale pending deployment by resetting
// its start time. It returns false if the deployment is no longer pending or
// another instance claimed it first.
func (s *DeploymentStore) ClaimStale(id string, cutoff time.Time) (bool, error) {
	result, err := s.db.Exec(`
		UPDATE deployments
		SET started_at = ?
		WHERE id = ? AND status = 'pending' AND started_at < ?
	`, time.Now().UTC(), id, cutoff.UTC())
	if err != nil {
		return false, fmt.Errorf("failed to claim deployment: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}

	return rows == 1, nil
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
