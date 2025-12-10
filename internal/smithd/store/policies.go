package store

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/google/uuid"
)

// PolicyStore handles policy database operations
type PolicyStore struct {
	db *sql.DB
}

// NewPolicyStore creates a new policy store
func NewPolicyStore(db *sql.DB) *PolicyStore {
	return &PolicyStore{db: db}
}

// Create creates a new policy
func (s *PolicyStore) Create(appID, name, branchPattern, targetEnv string, enabled bool) (*models.Policy, error) {
	policy := &models.Policy{
		ID:                uuid.New().String(),
		AppID:             appID,
		Name:              name,
		GitBranchPattern:  branchPattern,
		TargetEnvironment: targetEnv,
		Enabled:           enabled,
	}

	_, err := s.db.Exec(`
		INSERT INTO policies (id, app_id, name, git_branch_pattern, target_environment, enabled)
		VALUES (?, ?, ?, ?, ?, ?)
	`, policy.ID, policy.AppID, policy.Name, policy.GitBranchPattern, policy.TargetEnvironment, policy.Enabled)

	if err != nil {
		return nil, fmt.Errorf("failed to create policy: %w", err)
	}

	// Fetch the created policy to get timestamps
	return s.GetByID(policy.ID)
}

// GetByID gets a policy by ID
func (s *PolicyStore) GetByID(id string) (*models.Policy, error) {
	var policy models.Policy

	err := s.db.QueryRow(`
		SELECT id, app_id, name, git_branch_pattern, target_environment, enabled, created_at
		FROM policies
		WHERE id = ?
	`, id).Scan(&policy.ID, &policy.AppID, &policy.Name, &policy.GitBranchPattern, &policy.TargetEnvironment, &policy.Enabled, &policy.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("policy not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}

	return &policy, nil
}

// List lists all policies for an application
func (s *PolicyStore) List(appID string) ([]models.Policy, error) {
	rows, err := s.db.Query(`
		SELECT id, app_id, name, git_branch_pattern, target_environment, enabled, created_at
		FROM policies
		WHERE app_id = ?
		ORDER BY created_at DESC
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to list policies: %w", err)
	}
	defer rows.Close()

	policies := []models.Policy{}
	for rows.Next() {
		var policy models.Policy
		err := rows.Scan(&policy.ID, &policy.AppID, &policy.Name, &policy.GitBranchPattern, &policy.TargetEnvironment, &policy.Enabled, &policy.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}
		policies = append(policies, policy)
	}

	return policies, nil
}

// Delete deletes a policy
func (s *PolicyStore) Delete(id string) error {
	result, err := s.db.Exec("DELETE FROM policies WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete policy: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("policy not found")
	}

	return nil
}

// FindMatchingPolicies finds all enabled policies that match the given branch
func (s *PolicyStore) FindMatchingPolicies(appID, branch string) ([]models.Policy, error) {
	rows, err := s.db.Query(`
		SELECT id, app_id, name, git_branch_pattern, target_environment, enabled, created_at
		FROM policies
		WHERE app_id = ? AND enabled = 1
		ORDER BY created_at ASC
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to query policies: %w", err)
	}
	defer rows.Close()

	matchingPolicies := []models.Policy{}
	for rows.Next() {
		var policy models.Policy
		err := rows.Scan(&policy.ID, &policy.AppID, &policy.Name, &policy.GitBranchPattern, &policy.TargetEnvironment, &policy.Enabled, &policy.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}

		// Check if branch matches the pattern
		if matchesBranchPattern(branch, policy.GitBranchPattern) {
			matchingPolicies = append(matchingPolicies, policy)
		}
	}

	return matchingPolicies, nil
}

// matchesBranchPattern checks if a branch name matches a pattern
// Supports wildcards: "main", "release/*", "feature/xyz"
func matchesBranchPattern(branch, pattern string) bool {
	// Exact match
	if branch == pattern {
		return true
	}

	// Wildcard match using filepath.Match (supports * and ?)
	matched, err := filepath.Match(pattern, branch)
	if err != nil {
		return false
	}

	return matched
}
