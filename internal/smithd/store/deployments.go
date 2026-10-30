package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// deployedStatuses are the states of a deployment whose version runs in its
// environment: a committed manifest deployment or an applied terraform one
const deployedStatuses = "('success', 'applied')"

// activeTerraformStatuses is models.ActiveTerraformStatuses as an SQL list
var activeTerraformStatuses = "('" + strings.Join(models.ActiveTerraformStatuses, "', '") + "')"

// deploymentColumns are scanned by scanDeployment
const deploymentColumns = `id, app_id, version_id, environment, status, triggered_by, policy_id, COALESCE(gitops_commit_sha, ''), COALESCE(error_message, ''), started_at, completed_at,
	COALESCE(terraform_root, ''), COALESCE(terraform_source_sha, ''), COALESCE(terraform_plan_id, ''),
	approved_by, approved_at, destroy_confirmed, approval_reset_at, rejected_by, rejected_at, reject_reason, rollback`

// terraformDecisions are the scanned approval and rejection columns of a
// deployment, before they are set on its models.TerraformRun
type terraformDecisions struct {
	approvedBy, rejectedBy          string
	approvedAt, resetAt, rejectedAt sql.NullTime
}

func (d terraformDecisions) apply(run *models.TerraformRun) {
	run.ApprovedBy, run.RejectedBy = d.approvedBy, d.rejectedBy
	for _, t := range []struct {
		from sql.NullTime
		to   **time.Time
	}{{d.approvedAt, &run.ApprovedAt}, {d.resetAt, &run.ApprovalResetAt}, {d.rejectedAt, &run.RejectedAt}} {
		if t.from.Valid {
			at := t.from.Time
			*t.to = &at
		}
	}
}

func scanDeployment(scanner interface{ Scan(...any) error }) (*models.Deployment, error) {
	var deployment models.Deployment
	var completedAt sql.NullTime
	var policyID sql.NullString
	var run models.TerraformRun
	var decisions terraformDecisions

	err := scanner.Scan(&deployment.ID, &deployment.AppID, &deployment.VersionID, &deployment.Environment, &deployment.Status, &deployment.TriggeredBy, &policyID, &deployment.GitopsCommitSHA, &deployment.ErrorMessage, &deployment.StartedAt, &completedAt,
		&run.Root, &run.SourceCommitSHA, &run.PlanID, &decisions.approvedBy, &decisions.approvedAt, &run.DestroyConfirmed, &decisions.resetAt,
		&decisions.rejectedBy, &decisions.rejectedAt, &run.RejectReason, &run.Rollback)
	if err != nil {
		return nil, err
	}
	decisions.apply(&run)

	if completedAt.Valid {
		deployment.CompletedAt = &completedAt.Time
	}
	if policyID.Valid {
		deployment.PolicyID = &policyID.String
	}
	if run.Root != "" {
		deployment.Terraform = &run
	}
	return &deployment, nil
}

func scanDeployments(rows *sql.Rows) ([]models.Deployment, error) {
	defer rows.Close()

	deployments := []models.Deployment{}
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deployment: %w", err)
		}
		deployments = append(deployments, *deployment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read deployments: %w", err)
	}
	return deployments, nil
}

// Create creates a new deployment record
func (s *DeploymentStore) Create(appID, versionID, environment, triggeredBy string, policyID *string) (*models.Deployment, error) {
	deployment := newDeployment(appID, versionID, environment, triggeredBy, policyID)

	_, err := s.db.Exec(`
		INSERT INTO deployments (id, app_id, version_id, environment, status, triggered_by, policy_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, deployment.ID, deployment.AppID, deployment.VersionID, deployment.Environment, deployment.Status, deployment.TriggeredBy, deployment.PolicyID, deployment.StartedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	return deployment, nil
}

func newDeployment(appID, versionID, environment, triggeredBy string, policyID *string) *models.Deployment {
	return &models.Deployment{
		ID:          uuid.New().String(),
		AppID:       appID,
		VersionID:   versionID,
		Environment: environment,
		Status:      models.DeploymentPending,
		TriggeredBy: triggeredBy,
		PolicyID:    policyID,
		StartedAt:   time.Now().UTC(),
	}
}

// TerraformBusyError is returned by CreateTerraform when the root has a run
// that must not be replaced
type TerraformBusyError struct {
	// Active is the running deployment
	Active models.Deployment
}

func (e *TerraformBusyError) Error() string {
	return fmt.Sprintf("deployment %s of the root is %s", e.Active.ID, e.Active.Status)
}

// CreateTerraform creates the pending deployment of a terraform version to
// a root and replaces the root's active run, if any: a run that is planning
// or awaiting approval becomes stale, and is returned as replaced. A run
// that is pending (being committed) or applying is not replaced; that is a
// *TerraformBusyError. Both happen in one transaction, and the unique index
// on active runs makes concurrent deploys of the same root fail rather than
// both become active.
func (s *DeploymentStore) CreateTerraform(appID, versionID, environment, root, triggeredBy string) (created, replaced *models.Deployment, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	active, err := scanDeployment(tx.QueryRow(`
		SELECT `+deploymentColumns+`
		FROM deployments
		WHERE app_id = ? AND environment = ? AND terraform_root = ? AND status IN `+activeTerraformStatuses+`
	`, appID, environment, root))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		active = nil
	case err != nil:
		return nil, nil, fmt.Errorf("failed to get active terraform deployment: %w", err)
	}

	deployment := newDeployment(appID, versionID, environment, triggeredBy, nil)
	deployment.Terraform = &models.TerraformRun{Root: root}

	if active != nil {
		if active.Status != models.DeploymentPlanning && active.Status != models.DeploymentAwaitingApproval {
			return nil, nil, &TerraformBusyError{Active: *active}
		}
		// Only if it is still in the state read above
		now := time.Now().UTC()
		result, err := tx.Exec(`
			UPDATE deployments
			SET status = ?, error_message = ?, completed_at = ?
			WHERE id = ? AND status = ?
		`, models.DeploymentStale, "Replaced by deployment "+deployment.ID, now, active.ID, active.Status)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to mark deployment stale: %w", err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return nil, nil, fmt.Errorf("deployment %s changed while it was being replaced", active.ID)
		}
		active.Status = models.DeploymentStale
		active.ErrorMessage = "Replaced by deployment " + deployment.ID
		active.CompletedAt = &now
	}

	_, err = tx.Exec(`
		INSERT INTO deployments (id, app_id, version_id, environment, status, triggered_by, policy_id, terraform_root, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, deployment.ID, deployment.AppID, deployment.VersionID, deployment.Environment, deployment.Status, deployment.TriggeredBy, deployment.PolicyID, root, deployment.StartedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create deployment (is another deploy of the root running?): %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("failed to create deployment: %w", err)
	}
	return deployment, active, nil
}

// SetTerraformCommitted records that a pending terraform deployment was
// committed: the commit of the CR, of the source and the plan id to expect.
// The deployment moves to status (planning) and is not completed.
func (s *DeploymentStore) SetTerraformCommitted(id, status, commitSHA, sourceSHA, planID string) error {
	result, err := s.db.Exec(`
		UPDATE deployments
		SET status = ?, gitops_commit_sha = ?, terraform_source_sha = ?, terraform_plan_id = ?
		WHERE id = ? AND status = ?
	`, status, commitSHA, sourceSHA, planID, id, models.DeploymentPending)
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("deployment %s is no longer pending", id)
	}
	return nil
}

// trackedTerraformStatuses are the states of a terraform deployment smithd
// follows in the cluster: it has been committed and has not finished
var trackedTerraformStatuses = "('" + strings.Join([]string{models.DeploymentPlanning, models.DeploymentAwaitingApproval, models.DeploymentApplying}, "', '") + "')"

// ListTrackedTerraform returns the terraform deployments that are planning,
// waiting for approval or applying, oldest first
func (s *DeploymentStore) ListTrackedTerraform() ([]models.Deployment, error) {
	rows, err := s.db.Query(`
		SELECT ` + deploymentColumns + `
		FROM deployments
		WHERE terraform_root IS NOT NULL AND status IN ` + trackedTerraformStatuses + `
		ORDER BY started_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list terraform deployments: %w", err)
	}
	return scanDeployments(rows)
}

// TransitionTerraform moves a terraform deployment from one state to another
// and records errorMsg. It returns false if the deployment was not in the
// from state any more, e.g. because another smithd replica moved it first, so
// that only the caller that moved it reports the change. Finishing states
// (everything but planning, awaiting_approval and applying) complete the
// deployment.
func (s *DeploymentStore) TransitionTerraform(id, from, to, errorMsg string) (bool, error) {
	var completedAt *time.Time
	switch to {
	case models.DeploymentPlanning, models.DeploymentAwaitingApproval, models.DeploymentApplying:
	default:
		now := time.Now().UTC()
		completedAt = &now
	}
	result, err := s.db.Exec(`
		UPDATE deployments
		SET status = ?, error_message = ?, completed_at = ?
		WHERE id = ? AND status = ?
	`, to, errorMsg, completedAt, id, from)
	if err != nil {
		return false, fmt.Errorf("failed to update deployment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return rows == 1, nil
}

// Approve moves a terraform deployment from awaiting_approval to applying
// and records who approved it, and whether the approval confirmed a plan with
// destroys. It returns false if the deployment was not awaiting approval any
// more, e.g. because another request or replica approved it first.
func (s *DeploymentStore) Approve(id, by string, destroyConfirmed bool) (bool, error) {
	return s.decide(`
		UPDATE deployments
		SET status = ?, approved_by = ?, approved_at = ?, destroy_confirmed = ?, error_message = ''
		WHERE id = ? AND status = ?
	`, models.DeploymentApplying, by, time.Now().UTC(), destroyConfirmed, id, models.DeploymentAwaitingApproval)
}

// Reject moves a terraform deployment from awaiting_approval to rejected,
// completes it and records who rejected it and why. It returns false if the
// deployment was not awaiting approval any more.
func (s *DeploymentStore) Reject(id, by, reason string) (bool, error) {
	now := time.Now().UTC()
	message := "Rejected by " + by
	if reason != "" {
		message += ": " + reason
	}
	return s.decide(`
		UPDATE deployments
		SET status = ?, rejected_by = ?, rejected_at = ?, reject_reason = ?, error_message = ?, completed_at = ?
		WHERE id = ? AND status = ?
	`, models.DeploymentRejected, by, now, reason, message, now, id, models.DeploymentAwaitingApproval)
}

func (s *DeploymentStore) decide(query string, args ...any) (bool, error) {
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to update deployment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return rows == 1, nil
}

// SetRollback marks a terraform deployment as a rollback to an older version
func (s *DeploymentStore) SetRollback(id string) error {
	if _, err := s.db.Exec(`UPDATE deployments SET rollback = TRUE WHERE id = ?`, id); err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}
	return nil
}

// ListApprovalResets returns the approved terraform deployments that have
// finished (applied, or failed) and whose approvePlan has not been reset in
// git yet, oldest first
func (s *DeploymentStore) ListApprovalResets() ([]models.Deployment, error) {
	rows, err := s.db.Query(`
		SELECT ` + deploymentColumns + `
		FROM deployments
		WHERE terraform_root IS NOT NULL AND approved_at IS NOT NULL AND approval_reset_at IS NULL
			AND status IN ('` + models.DeploymentApplied + `', '` + models.DeploymentFailed + `')
		ORDER BY started_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list approvals to reset: %w", err)
	}
	return scanDeployments(rows)
}

// SetApprovalReset records that approvePlan was reset in git. It returns
// false if that was already recorded.
func (s *DeploymentStore) SetApprovalReset(id string) (bool, error) {
	return s.decide(`UPDATE deployments SET approval_reset_at = ? WHERE id = ? AND approval_reset_at IS NULL`, time.Now().UTC(), id)
}

// GetByID gets a deployment by ID
func (s *DeploymentStore) GetByID(id string) (*models.Deployment, error) {
	deployment, err := scanDeployment(s.db.QueryRow(`
		SELECT `+deploymentColumns+`
		FROM deployments
		WHERE id = ?
	`, id))

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	return deployment, nil
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
	query = `SELECT ` + deploymentColumns + `
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

	deployments, err := scanDeployments(rows)
	if err != nil {
		return nil, 0, err
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
		COALESCE(d.gitops_commit_sha, ''), COALESCE(d.error_message, ''), d.started_at, d.completed_at,
		COALESCE(d.terraform_root, ''), COALESCE(d.terraform_source_sha, ''), COALESCE(d.terraform_plan_id, ''),
		d.approved_by, d.approved_at, d.destroy_confirmed, d.approval_reset_at, d.rejected_by, d.rejected_at, d.reject_reason, d.rollback
	FROM deployments d
	LEFT JOIN applications a ON a.id = d.app_id
	LEFT JOIN versions v ON v.id = d.version_id
	LEFT JOIN policies p ON p.id = d.policy_id`

func scanDeploymentDetail(scanner interface{ Scan(...any) error }) (models.DeploymentDetail, error) {
	var detail models.DeploymentDetail
	var completedAt sql.NullTime
	var run models.TerraformRun
	var decisions terraformDecisions

	err := scanner.Scan(&detail.ID, &detail.AppID, &detail.AppName, &detail.VersionID, &detail.Environment, &detail.Status,
		&detail.TriggeredBy, &detail.PolicyID, &detail.PolicyName,
		&detail.GitopsCommitSHA, &detail.ErrorMessage, &detail.StartedAt, &completedAt,
		&run.Root, &run.SourceCommitSHA, &run.PlanID, &decisions.approvedBy, &decisions.approvedAt, &run.DestroyConfirmed, &decisions.resetAt,
		&decisions.rejectedBy, &decisions.rejectedAt, &run.RejectReason, &run.Rollback)
	if err != nil {
		return detail, err
	}
	decisions.apply(&run)

	if completedAt.Valid {
		detail.CompletedAt = &completedAt.Time
	}
	if run.Root != "" {
		detail.Terraform = &run
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

// LatestPerTarget returns the most recent deployment of every app to every
// environment it was deployed to
func (s *DeploymentStore) LatestPerTarget() ([]models.DeploymentDetail, error) {
	return s.queryDetails(deploymentDetailSelect + `
		WHERE d.started_at = (
			SELECT MAX(d2.started_at) FROM deployments d2
			WHERE d2.app_id = d.app_id AND d2.environment = d.environment
		)`)
}

// LatestSuccessPerTarget returns the most recent successful deployment of
// every app to every environment, i.e. what is currently running there
func (s *DeploymentStore) LatestSuccessPerTarget() ([]models.DeploymentDetail, error) {
	return s.queryDetails(deploymentDetailSelect + `
		WHERE d.status IN ` + deployedStatuses + ` AND d.completed_at = (
			SELECT MAX(d2.completed_at) FROM deployments d2
			WHERE d2.app_id = d.app_id AND d2.environment = d.environment AND d2.status IN ` + deployedStatuses + `
		)`)
}

func (s *DeploymentStore) queryDetails(query string, args ...any) ([]models.DeploymentDetail, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployments: %w", err)
	}
	defer rows.Close()

	deployments := []models.DeploymentDetail{}
	for rows.Next() {
		detail, err := scanDeploymentDetail(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deployment: %w", err)
		}
		deployments = append(deployments, detail)
	}
	return deployments, rows.Err()
}

// ListStalePending returns deployments that are still pending but were
// started before cutoff, i.e. deployments whose worker most likely died
func (s *DeploymentStore) ListStalePending(cutoff time.Time) ([]models.Deployment, error) {
	rows, err := s.db.Query(`
		SELECT `+deploymentColumns+`
		FROM deployments
		WHERE status = 'pending' AND started_at < ?
		ORDER BY started_at ASC
	`, cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to list stale deployments: %w", err)
	}
	return scanDeployments(rows)
}

// HasNewer reports whether the app was deployed to the same environment
// after the given deployment started, regardless of the outcome
func (s *DeploymentStore) HasNewer(deployment *models.Deployment) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM deployments
			WHERE app_id = ? AND environment = ? AND id != ? AND started_at > ?
		)
	`, deployment.AppID, deployment.Environment, deployment.ID, deployment.StartedAt.UTC()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check for newer deployments: %w", err)
	}
	return exists, nil
}

// PreviousSuccessVersion returns the ID of the version the most recent
// successful deployment before the given one put into the same environment,
// or "" if there is none
func (s *DeploymentStore) PreviousSuccessVersion(deployment *models.Deployment) (string, error) {
	var versionID string
	err := s.db.QueryRow(`
		SELECT version_id FROM deployments
		WHERE app_id = ? AND environment = ? AND status IN `+deployedStatuses+` AND id != ? AND started_at < ?
		ORDER BY started_at DESC LIMIT 1
	`, deployment.AppID, deployment.Environment, deployment.ID, deployment.StartedAt.UTC()).Scan(&versionID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get previous deployment: %w", err)
	}
	return versionID, nil
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

// FailStale marks a stale pending deployment as failed, leaving its start
// time alone so it still orders correctly against later deployments. It
// returns false if the deployment is no longer pending or no longer stale.
func (s *DeploymentStore) FailStale(id string, cutoff time.Time, errorMsg string) (bool, error) {
	result, err := s.db.Exec(`
		UPDATE deployments
		SET status = 'failed', error_message = ?, completed_at = ?
		WHERE id = ? AND status = 'pending' AND started_at < ?
	`, errorMsg, time.Now().UTC(), id, cutoff.UTC())
	if err != nil {
		return false, fmt.Errorf("failed to fail deployment: %w", err)
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
