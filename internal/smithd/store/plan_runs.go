package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// PlanRunStore handles plan-only runs of terraform versions and their plans
type PlanRunStore struct {
	db *db.DB
}

// NewPlanRunStore creates a new plan run store
func NewPlanRunStore(db *db.DB) *PlanRunStore {
	return &PlanRunStore{db: db}
}

// activePlanRunStatuses is the states of a run that is still being planned;
// only one per app, environment, root and requester can be in them
const activePlanRunStatuses = "('pending', 'planning')"

// planRunColumns are scanned by scanPlanRun, without the plan
const planRunColumns = `r.id, r.app_id, a.name, v.version_id, r.environment, r.terraform_root, r.status, COALESCE(r.triggered_by, ''), COALESCE(r.error_message, ''),
	r.started_at, r.completed_at, COALESCE(r.source_sha, ''), COALESCE(r.gitops_commit_sha, ''), COALESCE(r.plan_id, '')`

// planRunFrom is the FROM clause that goes with planRunColumns
const planRunFrom = `FROM plan_runs r JOIN applications a ON a.id = r.app_id JOIN versions v ON v.id = r.version_id`

func scanPlanRun(scanner interface{ Scan(...any) error }) (*models.PlanRun, error) {
	var run models.PlanRun
	var completedAt sql.NullTime
	err := scanner.Scan(&run.ID, &run.AppID, &run.AppName, &run.VersionID, &run.Environment, &run.Root, &run.Status, &run.TriggeredBy, &run.ErrorMessage,
		&run.StartedAt, &completedAt, &run.SourceCommitSHA, &run.GitopsCommitSHA, &run.PlanID)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return &run, nil
}

func scanPlanRuns(rows *sql.Rows) ([]models.PlanRun, error) {
	defer rows.Close()

	runs := []models.PlanRun{}
	for rows.Next() {
		run, err := scanPlanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan plan run: %w", err)
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read plan runs: %w", err)
	}
	return runs, nil
}

// Create creates the pending plan-only run of a version for a root and
// supersedes the root's active run, if any: a newer run replaces the older
// one, which is returned. requestedBy is the user of a local plan, or empty:
// a run only replaces the active run of the same requester, so a local plan
// never replaces anyone else's plan or the plan of CI, and the other way
// round. A run that is still being committed is replaced
// too; it finds out when it records its commits. Both happen in one
// transaction, and the unique index on active runs makes concurrent runs of
// the same root fail rather than both become active.
func (s *PlanRunStore) Create(appID string, version *models.Version, environment, root, triggeredBy, requestedBy string) (created *models.PlanRun, superseded []models.PlanRun, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	run := &models.PlanRun{
		ID:          uuid.New().String(),
		AppID:       appID,
		VersionID:   version.VersionID,
		Environment: environment,
		Root:        root,
		Status:      models.PlanRunPending,
		TriggeredBy: triggeredBy,
		StartedAt:   time.Now().UTC(),
	}

	rows, err := tx.Query(`
		SELECT `+planRunColumns+`
		`+planRunFrom+`
		WHERE r.app_id = ? AND r.environment = ? AND r.terraform_root = ? AND r.requested_by = ? AND r.status IN `+activePlanRunStatuses,
		appID, environment, root, requestedBy)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get active plan runs: %w", err)
	}
	active, err := scanPlanRuns(rows)
	if err != nil {
		return nil, nil, err
	}

	for i := range active {
		old := &active[i]
		now := time.Now().UTC()
		message := "Replaced by plan run " + run.ID
		// Only if it is still in the state read above
		result, err := tx.Exec(`
			UPDATE plan_runs
			SET status = ?, error_message = ?, completed_at = ?
			WHERE id = ? AND status = ?
		`, models.PlanRunSuperseded, message, now, old.ID, old.Status)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to supersede plan run: %w", err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return nil, nil, fmt.Errorf("plan run %s changed while it was being replaced", old.ID)
		}
		old.Status = models.PlanRunSuperseded
		old.ErrorMessage = message
		old.CompletedAt = &now
	}

	_, err = tx.Exec(`
		INSERT INTO plan_runs (id, app_id, version_id, environment, terraform_root, status, triggered_by, requested_by, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, appID, version.ID, environment, root, run.Status, triggeredBy, requestedBy, run.StartedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create plan run (is another plan of the root being started?): %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("failed to create plan run: %w", err)
	}
	return run, active, nil
}

// SetCommitted records that a pending run was committed to the plans
// branch: the commit of the CR, of the source and the plan id to expect.
// The run moves to planning. It returns false if the run is not pending any
// more, i.e. a newer run superseded it meanwhile.
func (s *PlanRunStore) SetCommitted(id, commitSHA, sourceSHA, planID string) (bool, error) {
	result, err := s.db.Exec(`
		UPDATE plan_runs
		SET status = ?, gitops_commit_sha = ?, source_sha = ?, plan_id = ?
		WHERE id = ? AND status = ?
	`, models.PlanRunPlanning, commitSHA, sourceSHA, planID, id, models.PlanRunPending)
	if err != nil {
		return false, fmt.Errorf("failed to update plan run: %w", err)
	}
	return rowsChanged(result)
}

// Finish ends a run that is in the from state: as planned with its plan, or
// as failed with an error message. It returns false if the run was not in
// the from state any more, e.g. because another smithd replica finished it
// first, so that only the caller that finished it reports the change.
func (s *PlanRunStore) Finish(id, from, to, errorMsg string, plan *models.TerraformPlan) (bool, error) {
	now := time.Now().UTC()
	if plan == nil {
		result, err := s.db.Exec(`
			UPDATE plan_runs
			SET status = ?, error_message = ?, completed_at = ?
			WHERE id = ? AND status = ?
		`, to, errorMsg, now, id, from)
		if err != nil {
			return false, fmt.Errorf("failed to update plan run: %w", err)
		}
		return rowsChanged(result)
	}

	resources, err := json.Marshal(plan.Resources)
	if err != nil {
		return false, fmt.Errorf("failed to encode plan resources: %w", err)
	}
	var plannedAt *time.Time
	if plan.PlannedAt != nil {
		at := plan.PlannedAt.UTC()
		plannedAt = &at
	}
	result, err := s.db.Exec(`
		UPDATE plan_runs
		SET status = ?, error_message = ?, completed_at = ?, plan_id = ?, plan_hash = ?, planned_at = ?,
			to_add = ?, to_change = ?, to_destroy = ?, destroys = ?, output_changes = ?, resources = ?, plan_text = ?
		WHERE id = ? AND status = ?
	`, to, errorMsg, now, plan.PlanID, plan.PlanHash, plannedAt,
		plan.ToAdd, plan.ToChange, plan.ToDestroy, plan.Destroys, plan.OutputChanges, string(resources), plan.Text, id, from)
	if err != nil {
		return false, fmt.Errorf("failed to update plan run: %w", err)
	}
	return rowsChanged(result)
}

func rowsChanged(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return rows == 1, nil
}

// Get returns a run with its plan (with the text), or nil if there is none
func (s *PlanRunStore) Get(id string) (*models.PlanRun, error) {
	run, err := scanPlanRun(s.db.QueryRow(`SELECT `+planRunColumns+` `+planRunFrom+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get plan run: %w", err)
	}
	if err := s.loadPlan(run); err != nil {
		return nil, err
	}
	return run, nil
}

// Latest returns the newest run of a version for an environment with its
// plan, or nil if there is none
func (s *PlanRunStore) Latest(appID, versionID, environment string) (*models.PlanRun, error) {
	run, err := scanPlanRun(s.db.QueryRow(`
		SELECT `+planRunColumns+` `+planRunFrom+`
		WHERE r.app_id = ? AND v.version_id = ? AND r.environment = ?
		ORDER BY r.started_at DESC, r.id DESC
		LIMIT 1
	`, appID, versionID, environment))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get plan run: %w", err)
	}
	if err := s.loadPlan(run); err != nil {
		return nil, err
	}
	return run, nil
}

// loadPlan fills in the plan of a planned run
func (s *PlanRunStore) loadPlan(run *models.PlanRun) error {
	if run.Status != models.PlanRunPlanned {
		return nil
	}
	plan := models.TerraformPlan{PlanID: run.PlanID}
	var plannedAt sql.NullTime
	var resources string
	err := s.db.QueryRow(`
		SELECT plan_hash, planned_at, to_add, to_change, to_destroy, destroys, output_changes, resources, plan_text
		FROM plan_runs WHERE id = ?
	`, run.ID).Scan(&plan.PlanHash, &plannedAt, &plan.ToAdd, &plan.ToChange, &plan.ToDestroy, &plan.Destroys, &plan.OutputChanges, &resources, &plan.Text)
	if err != nil {
		return fmt.Errorf("failed to get the plan of plan run: %w", err)
	}
	if plannedAt.Valid {
		plan.PlannedAt = &plannedAt.Time
	}
	if err := json.Unmarshal([]byte(resources), &plan.Resources); err != nil {
		return fmt.Errorf("failed to decode plan resources: %w", err)
	}
	// Lists, not null, in the API
	for _, list := range []*[]string{&plan.Resources.Create, &plan.Resources.Update, &plan.Resources.Replace, &plan.Resources.Destroy} {
		if *list == nil {
			*list = []string{}
		}
	}
	run.Plan = &plan
	return nil
}

// ListPlanning returns the runs that have been committed and wait for the
// controller's plan, oldest first
func (s *PlanRunStore) ListPlanning() ([]models.PlanRun, error) {
	rows, err := s.db.Query(`SELECT ` + planRunColumns + ` ` + planRunFrom + ` WHERE r.status = 'planning' ORDER BY r.started_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list plan runs: %w", err)
	}
	return scanPlanRuns(rows)
}

// ListActive returns the runs that are still being planned (pending or
// planning): their files must stay on the plans branch
func (s *PlanRunStore) ListActive() ([]models.PlanRun, error) {
	rows, err := s.db.Query(`SELECT ` + planRunColumns + ` ` + planRunFrom + ` WHERE r.status IN ` + activePlanRunStatuses)
	if err != nil {
		return nil, fmt.Errorf("failed to list plan runs: %w", err)
	}
	return scanPlanRuns(rows)
}

// CountUncleaned returns how many ended runs may still have files on the
// plans branch
func (s *PlanRunStore) CountUncleaned() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM plan_runs WHERE cleaned_at IS NULL AND status NOT IN ` + activePlanRunStatuses).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count plan runs: %w", err)
	}
	return n, nil
}

// MarkCleaned records that the files of the runs that ended at or before
// cutoff are removed from the plans branch. Runs that ended later are left
// alone: they were still active when the branch was cleaned.
func (s *PlanRunStore) MarkCleaned(cutoff time.Time) error {
	_, err := s.db.Exec(`
		UPDATE plan_runs SET cleaned_at = ?
		WHERE cleaned_at IS NULL AND status NOT IN `+activePlanRunStatuses+` AND completed_at <= ?
	`, time.Now().UTC(), cutoff.UTC())
	if err != nil {
		return fmt.Errorf("failed to update plan runs: %w", err)
	}
	return nil
}

// DeleteEndedBefore deletes the ended runs, with their plans, that ended
// before cutoff and are cleaned up. It returns how many.
func (s *PlanRunStore) DeleteEndedBefore(cutoff time.Time) (int64, error) {
	result, err := s.db.Exec(`
		DELETE FROM plan_runs
		WHERE cleaned_at IS NOT NULL AND status NOT IN `+activePlanRunStatuses+` AND completed_at < ?
	`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to delete plan runs: %w", err)
	}
	return result.RowsAffected()
}
