package service

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// CreateEnvironment creates an environment
func (s *Service) CreateEnvironment(req models.CreateEnvironmentRequest) (*models.Environment, error) {
	req.Name = strings.TrimSpace(req.Name)
	if err := validateName("Environment", req.Name); err != nil {
		return nil, err
	}

	exists, err := s.Environments.Exists(req.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to check environment: %w", err)
	}
	if exists {
		return nil, &Error{Kind: KindConflict, Code: "environment_exists", Message: fmt.Sprintf("Environment '%s' already exists", req.Name)}
	}

	env, err := s.Environments.Create(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create environment: %w", err)
	}
	return env, nil
}

// GetEnvironment returns an environment by ID
func (s *Service) GetEnvironment(id string) (*models.Environment, error) {
	env, err := s.Environments.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment: %w", err)
	}
	if env == nil {
		return nil, &Error{Kind: KindNotFound, Code: "environment_not_found", Message: "Environment not found"}
	}
	return env, nil
}

// UpdateEnvironment updates an environment's description
func (s *Service) UpdateEnvironment(id string, req models.UpdateEnvironmentRequest) (*models.Environment, error) {
	env, err := s.Environments.Update(id, req)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Kind: KindNotFound, Code: "environment_not_found", Message: "Environment not found"}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update environment: %w", err)
	}
	return env, nil
}

// DeleteEnvironment deletes an environment that no policy or deployment
// refers to
func (s *Service) DeleteEnvironment(id string) error {
	env, err := s.GetEnvironment(id)
	if err != nil {
		return err
	}

	policies, deployments, err := s.Environments.Usage(env.Name)
	if err != nil {
		return fmt.Errorf("failed to check environment usage: %w", err)
	}
	if policies > 0 || deployments > 0 {
		return &Error{Kind: KindConflict, Code: "environment_in_use", Message: fmt.Sprintf(
			"Environment '%s' is still used by %d auto-deploy policies and %d deployments, so it cannot be deleted", env.Name, policies, deployments)}
	}

	if err := s.Environments.Delete(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Kind: KindNotFound, Code: "environment_not_found", Message: "Environment not found"}
		}
		return fmt.Errorf("failed to delete environment: %w", err)
	}
	return nil
}

// RegisterApp registers a new application
func (s *Service) RegisterApp(name string) (*models.Application, error) {
	name = strings.TrimSpace(name)
	if err := validateName("Application", name); err != nil {
		return nil, err
	}

	app, err := s.Apps.Create(name)
	if err != nil {
		if err.Error() == fmt.Sprintf("application with name '%s' already exists", name) {
			return nil, conflict(err.Error())
		}
		return nil, fmt.Errorf("failed to create application: %w", err)
	}
	return app, nil
}
