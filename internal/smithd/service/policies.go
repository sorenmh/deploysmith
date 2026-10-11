package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// ListPolicies lists an app's auto-deploy policies
func (s *Service) ListPolicies(appID string) ([]models.Policy, error) {
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}

	policies, err := s.Policies.List(appID)
	if err != nil {
		return nil, fmt.Errorf("failed to list policies: %w", err)
	}
	return policies, nil
}

// CreatePolicy creates an auto-deploy policy for an app. Policies are
// enabled unless req.Enabled says otherwise.
func (s *Service) CreatePolicy(appID string, req models.CreatePolicyRequest) (*models.Policy, error) {
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalid("invalid_request", "Policy name is required")
	}
	if err := validateBranchPattern(req.GitBranchPattern); err != nil {
		return nil, err
	}
	if err := s.requireEnvironment(strings.TrimSpace(req.TargetEnvironment)); err != nil {
		return nil, err
	}
	if err := s.requireUniquePolicyName(appID, name); err != nil {
		return nil, err
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	policy, err := s.Policies.Create(appID, name, strings.TrimSpace(req.GitBranchPattern), strings.TrimSpace(req.TargetEnvironment), enabled)
	if err != nil {
		return nil, fmt.Errorf("failed to create policy: %w", err)
	}
	return policy, nil
}

// UpdatePolicy updates the fields of a policy that are set in req
func (s *Service) UpdatePolicy(appID, policyID string, req models.UpdatePolicyRequest) (*models.Policy, error) {
	policy, err := s.getPolicy(appID, policyID)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, invalid("invalid_request", "Policy name cannot be empty")
		}
		if name != policy.Name {
			if err := s.requireUniquePolicyName(appID, name); err != nil {
				return nil, err
			}
		}
		policy.Name = name
	}

	if req.GitBranchPattern != nil {
		if err := validateBranchPattern(*req.GitBranchPattern); err != nil {
			return nil, err
		}
		policy.GitBranchPattern = strings.TrimSpace(*req.GitBranchPattern)
	}

	if req.TargetEnvironment != nil {
		env := strings.TrimSpace(*req.TargetEnvironment)
		if err := s.requireEnvironment(env); err != nil {
			return nil, err
		}
		policy.TargetEnvironment = env
	}

	if req.Enabled != nil {
		policy.Enabled = *req.Enabled
	}

	updated, err := s.Policies.Update(policy)
	if err != nil {
		return nil, fmt.Errorf("failed to update policy: %w", err)
	}
	return updated, nil
}

// DeletePolicy deletes a policy of an app
func (s *Service) DeletePolicy(appID, policyID string) error {
	if _, err := s.getApp(appID); err != nil {
		return err
	}
	if _, err := s.getPolicy(appID, policyID); err != nil {
		return err
	}

	if err := s.Policies.Delete(policyID); err != nil {
		return fmt.Errorf("failed to delete policy: %w", err)
	}
	return nil
}

func validateBranchPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return invalid("invalid_request", "Git branch pattern is required")
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return invalid("invalid_request", fmt.Sprintf("Invalid git branch pattern: %v", err))
	}
	return nil
}

func (s *Service) requireUniquePolicyName(appID, name string) error {
	policies, err := s.Policies.List(appID)
	if err != nil {
		return fmt.Errorf("failed to list policies: %w", err)
	}
	for _, p := range policies {
		if p.Name == name {
			return conflict(fmt.Sprintf("Policy '%s' already exists", name))
		}
	}
	return nil
}
