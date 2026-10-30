package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// TerraformPlanStore handles the readable plans of terraform deployments,
// one per deployment
type TerraformPlanStore struct {
	db *db.DB
}

// NewTerraformPlanStore creates a new terraform plan store
func NewTerraformPlanStore(db *db.DB) *TerraformPlanStore {
	return &TerraformPlanStore{db: db}
}

// Save stores the plan of a deployment, replacing the one it had. The
// applied hash is not touched; it is set by SetApplied.
func (s *TerraformPlanStore) Save(deploymentID string, plan models.TerraformPlan) error {
	resources, err := json.Marshal(plan.Resources)
	if err != nil {
		return fmt.Errorf("failed to encode plan resources: %w", err)
	}
	now := time.Now().UTC()
	var plannedAt *time.Time
	if plan.PlannedAt != nil {
		at := plan.PlannedAt.UTC()
		plannedAt = &at
	}
	_, err = s.db.Exec(`
		INSERT INTO terraform_plans (deployment_id, plan_id, plan_hash, planned_at, to_add, to_change, to_destroy, destroys, output_changes, resources, plan_text, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (deployment_id) DO UPDATE SET plan_id = excluded.plan_id, plan_hash = excluded.plan_hash, planned_at = excluded.planned_at,
			to_add = excluded.to_add, to_change = excluded.to_change, to_destroy = excluded.to_destroy, destroys = excluded.destroys,
			output_changes = excluded.output_changes, resources = excluded.resources, plan_text = excluded.plan_text, updated_at = excluded.updated_at
	`, deploymentID, plan.PlanID, plan.PlanHash, plannedAt, plan.ToAdd, plan.ToChange, plan.ToDestroy, plan.Destroys, plan.OutputChanges, string(resources), plan.Text, now, now)
	if err != nil {
		return fmt.Errorf("failed to save terraform plan: %w", err)
	}
	return nil
}

// Get returns the plan of a deployment with its text, or nil if it has none
func (s *TerraformPlanStore) Get(deploymentID string) (*models.TerraformPlan, error) {
	var plan models.TerraformPlan
	var plannedAt sql.NullTime
	var resources string
	err := s.db.QueryRow(`
		SELECT plan_id, plan_hash, planned_at, to_add, to_change, to_destroy, destroys, output_changes, resources, plan_text, applied_plan_hash, applied_differed
		FROM terraform_plans
		WHERE deployment_id = ?
	`, deploymentID).Scan(&plan.PlanID, &plan.PlanHash, &plannedAt, &plan.ToAdd, &plan.ToChange, &plan.ToDestroy, &plan.Destroys, &plan.OutputChanges,
		&resources, &plan.Text, &plan.AppliedPlanHash, &plan.AppliedDiffered)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get terraform plan: %w", err)
	}
	if plannedAt.Valid {
		plan.PlannedAt = &plannedAt.Time
	}
	if err := json.Unmarshal([]byte(resources), &plan.Resources); err != nil {
		return nil, fmt.Errorf("failed to decode plan resources: %w", err)
	}
	// Lists, not null, in the API
	for _, list := range []*[]string{&plan.Resources.Create, &plan.Resources.Update, &plan.Resources.Replace, &plan.Resources.Destroy} {
		if *list == nil {
			*list = []string{}
		}
	}
	return &plan, nil
}

// Summaries returns the plans of the given deployments by deployment id,
// with the counts only (no resources or text), for listings. Deployments
// without a plan are left out.
func (s *TerraformPlanStore) Summaries(deploymentIDs []string) (map[string]models.TerraformPlan, error) {
	summaries := map[string]models.TerraformPlan{}
	if len(deploymentIDs) == 0 {
		return summaries, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(deploymentIDs)), ", ")
	args := make([]any, len(deploymentIDs))
	for i, id := range deploymentIDs {
		args[i] = id
	}
	rows, err := s.db.Query(`
		SELECT deployment_id, plan_id, to_add, to_change, to_destroy, destroys
		FROM terraform_plans
		WHERE deployment_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list terraform plans: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var plan models.TerraformPlan
		if err := rows.Scan(&id, &plan.PlanID, &plan.ToAdd, &plan.ToChange, &plan.ToDestroy, &plan.Destroys); err != nil {
			return nil, fmt.Errorf("failed to scan terraform plan: %w", err)
		}
		summaries[id] = plan
	}
	return summaries, rows.Err()
}

// SetPlannedAt records that the controller made the same plan again
func (s *TerraformPlanStore) SetPlannedAt(deploymentID string, plannedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE terraform_plans SET planned_at = ?, updated_at = ? WHERE deployment_id = ?`,
		plannedAt.UTC(), time.Now().UTC(), deploymentID)
	if err != nil {
		return fmt.Errorf("failed to update terraform plan: %w", err)
	}
	return nil
}

// SetApplied records the hash of the plan the controller made again right
// before applying, and whether it differs from the reviewed plan
func (s *TerraformPlanStore) SetApplied(deploymentID, hash string, differed bool) error {
	_, err := s.db.Exec(`UPDATE terraform_plans SET applied_plan_hash = ?, applied_differed = ?, updated_at = ? WHERE deployment_id = ?`,
		hash, differed, time.Now().UTC(), deploymentID)
	if err != nil {
		return fmt.Errorf("failed to update terraform plan: %w", err)
	}
	return nil
}
