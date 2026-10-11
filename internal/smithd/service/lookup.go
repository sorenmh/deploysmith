package service

import (
	"fmt"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// The stores signal missing rows with plain errors; these helpers turn them
// into *Error values.

func (s *Service) getApp(appID string) (*models.Application, error) {
	app, err := s.Apps.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			return nil, notFound("Application not found")
		}
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	return app, nil
}

func (s *Service) getPolicy(appID, policyID string) (*models.Policy, error) {
	policy, err := s.Policies.GetByID(policyID)
	if err != nil {
		if err.Error() == "policy not found" {
			return nil, notFound("Policy not found")
		}
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}
	if policy.AppID != appID {
		return nil, notFound("Policy not found")
	}
	return policy, nil
}

func (s *Service) requireEnvironment(name string) error {
	if name == "" {
		return invalid("invalid_request", "Environment is required")
	}
	exists, err := s.Environments.Exists(name)
	if err != nil {
		return fmt.Errorf("failed to check environment: %w", err)
	}
	if !exists {
		return invalid("invalid_environment", fmt.Sprintf("Environment '%s' does not exist", name))
	}
	return nil
}
