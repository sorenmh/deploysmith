package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// TerraformGitops writes a Terraform app's files to the gitops repository
type TerraformGitops interface {
	// WriteTerraformFiles writes files next to the app's roots and returns
	// the commit SHA
	WriteTerraformFiles(appName, environment, message string, files map[string][]byte) (string, error)
	// DeployTerraform commits a root's source and then its CR, pinned to
	// the source commit
	DeployTerraform(d gitops.TerraformDeploy) (gitops.TerraformCommits, error)
	// SetTerraformApproval sets approvePlan of the deployment's root to a
	// plan id (an approval) or to "" (the reset after the apply) in one
	// commit, and returns its SHA. gitops.ErrRootNotCurrent if the root's
	// file is not the deployment's.
	SetTerraformApproval(appName, environment, root, deploymentID, planID, message string) (string, error)
}

// defaultTerraformRoot is the root of the default state key; one root per
// app and environment for now, but the key has room for more
const defaultTerraformRoot = "main"

// EnableTerraform marks an app as a Terraform app in an environment, records
// its state backend and provisions its runner namespace and OpenBao roles.
// Backend fields left empty default to the shared backend. A provisioning
// failure does not undo the enabling: it is recorded on the returned
// environment and can be retried with SyncTerraform.
func (s *Service) EnableTerraform(ctx context.Context, appID string, req models.EnableTerraformRequest) (*models.TerraformEnvironment, error) {
	if err := s.requireTerraform(); err != nil {
		return nil, err
	}
	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}
	environment := strings.TrimSpace(req.Environment)
	if err := s.requireEnvironment(environment); err != nil {
		return nil, err
	}
	if err := terraform.ValidateNames(app.Name, environment); err != nil {
		return nil, invalid("invalid_request", err.Error())
	}

	existing, err := s.TerraformEnvironments.Get(app.ID, environment)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, &Error{Kind: KindConflict, Code: "terraform_enabled", Message: fmt.Sprintf(
			"Terraform is already enabled for %s in %s; sync it to provision it again", app.Name, environment)}
	}

	backend, err := s.terraformBackend(app.Name, environment, req.Backend)
	if err != nil {
		return nil, err
	}
	roleARN := strings.TrimSpace(req.AWSRoleARN)
	if _, err := s.Terraform.Opts.RoleARN(app.Name, environment, roleARN, false); err != nil {
		return nil, invalid("invalid_request", err.Error())
	}

	tfEnv, err := s.TerraformEnvironments.Create(app.ID, environment, backend, roleARN, req.EncryptionFallback)
	if err != nil {
		return nil, err
	}
	return s.provisionTerraform(ctx, tfEnv)
}

// UpdateTerraform changes the settings of an app's Terraform environment.
// They apply from the next deploy on.
func (s *Service) UpdateTerraform(appID, environment string, req models.UpdateTerraformRequest) (*models.TerraformEnvironment, error) {
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}
	tfEnv, err := s.TerraformEnvironments.Get(appID, environment)
	if err != nil {
		return nil, err
	}
	if tfEnv == nil {
		return nil, &Error{Kind: KindNotFound, Code: "terraform_not_enabled", Message: fmt.Sprintf("Terraform is not enabled in %s", environment)}
	}
	if req.EncryptionFallback != nil {
		if err := s.TerraformEnvironments.SetEncryptionFallback(tfEnv.ID, *req.EncryptionFallback); err != nil {
			return nil, err
		}
	}
	updated, err := s.TerraformEnvironments.Get(appID, environment)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, fmt.Errorf("terraform environment %s/%s disappeared", tfEnv.AppName, environment)
	}
	return updated, nil
}

// ListTerraformEnvironments lists the environments an app has Terraform
// enabled in
func (s *Service) ListTerraformEnvironments(appID string) ([]models.TerraformEnvironment, error) {
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}
	return s.TerraformEnvironments.List(appID)
}

// SyncTerraform provisions an app's Terraform environment again, or all of
// them if environment is empty, and returns them with the outcome
func (s *Service) SyncTerraform(ctx context.Context, appID, environment string) ([]models.TerraformEnvironment, error) {
	if err := s.requireTerraform(); err != nil {
		return nil, err
	}
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}

	var envs []models.TerraformEnvironment
	if environment = strings.TrimSpace(environment); environment != "" {
		tfEnv, err := s.TerraformEnvironments.Get(appID, environment)
		if err != nil {
			return nil, err
		}
		if tfEnv == nil {
			return nil, &Error{Kind: KindNotFound, Code: "terraform_not_enabled", Message: fmt.Sprintf("Terraform is not enabled in %s", environment)}
		}
		envs = []models.TerraformEnvironment{*tfEnv}
	} else {
		var err error
		if envs, err = s.TerraformEnvironments.List(appID); err != nil {
			return nil, err
		}
	}

	synced := make([]models.TerraformEnvironment, 0, len(envs))
	for i := range envs {
		tfEnv, err := s.provisionTerraform(ctx, &envs[i])
		if err != nil {
			return nil, err
		}
		synced = append(synced, *tfEnv)
	}
	return synced, nil
}

// SyncAllTerraform provisions every Terraform environment again, e.g. on
// start, so changed settings and anything removed by hand are restored.
// Failures are logged and recorded on the environments.
func (s *Service) SyncAllTerraform(ctx context.Context) {
	if s.Terraform == nil {
		return
	}
	envs, err := s.TerraformEnvironments.ListAll()
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list Terraform environments: "+err.Error(), err)
		return
	}
	for i := range envs {
		tfEnv, err := s.provisionTerraform(ctx, &envs[i])
		if err != nil {
			logging.LogErrorCtx(ctx, "Failed to provision Terraform: "+err.Error(), err)
		} else if tfEnv.ProvisionError != nil {
			logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to provision Terraform for %s in %s: %s", tfEnv.AppName, tfEnv.Environment, *tfEnv.ProvisionError))
		}
	}
}

// provisionTerraform commits the runner namespace of an environment to the
// gitops repo and writes its OpenBao roles, policies and Transit key. Both
// are idempotent, so this is safe to repeat. The outcome is recorded on the
// environment, which is returned reloaded; the error is only for failing to
// record it.
func (s *Service) provisionTerraform(ctx context.Context, tfEnv *models.TerraformEnvironment) (*models.TerraformEnvironment, error) {
	app, env := tfEnv.AppName, tfEnv.Environment

	var errs []error
	manifest, err := terraform.NamespaceManifest(app, env, s.TerraformManifests)
	if err == nil {
		message := fmt.Sprintf("Set up Terraform runner namespace for %s in %s", app, env)
		_, err = s.TerraformGitops.WriteTerraformFiles(app, env, message, map[string][]byte{terraform.NamespaceFile: manifest})
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("gitops: %w", err))
	}
	if err := s.Terraform.Provision(ctx, app, env, tfEnv.AWSRoleARN); err != nil {
		errs = append(errs, fmt.Errorf("openbao: %w", err))
	}

	if err := s.TerraformEnvironments.SetProvisioned(tfEnv.ID, errors.Join(errs...)); err != nil {
		return nil, err
	}
	updated, err := s.TerraformEnvironments.Get(tfEnv.AppID, env)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, fmt.Errorf("terraform environment %s/%s disappeared", app, env)
	}
	return updated, nil
}

// terraformBackend fills in the defaults of a backend: without a bucket the
// shared bucket, region and lock table; without a key
// <app>/<env>/main/terraform.tfstate
func (s *Service) terraformBackend(app, env string, backend models.TerraformBackend) (models.TerraformBackend, error) {
	backend = models.TerraformBackend{
		Bucket:    strings.TrimSpace(backend.Bucket),
		Key:       strings.TrimSpace(backend.Key),
		Region:    strings.TrimSpace(backend.Region),
		LockTable: strings.TrimSpace(backend.LockTable),
	}
	if backend.Bucket == "" {
		shared := s.TerraformSharedBackend
		if shared.Bucket == "" {
			return backend, invalid("invalid_request", "No state backend bucket given and smithd has no shared one (TERRAFORM_STATE_BUCKET)")
		}
		backend.Bucket = shared.Bucket
		if backend.Region == "" {
			backend.Region = shared.Region
		}
		if backend.LockTable == "" {
			backend.LockTable = shared.LockTable
		}
	}
	if backend.Region == "" {
		return backend, invalid("invalid_request", "The state backend's region is required")
	}
	if backend.Key == "" {
		backend.Key = fmt.Sprintf("%s/%s/%s/terraform.tfstate", app, env, defaultTerraformRoot)
	}
	return backend, nil
}

func (s *Service) requireTerraform() error {
	if s.Terraform == nil || s.TerraformGitops == nil {
		return invalid("terraform_disabled", "Terraform support is off: smithd has no OpenBao configured (OPENBAO_ADDR)")
	}
	return nil
}
