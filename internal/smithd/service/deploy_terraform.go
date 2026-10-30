package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// deployTerraform deploys a terraform version to the app's root in an
// environment: it commits the bundle source and then the root's Terraform
// CR, pinned to the source commit, to the gitops repo. tofu-controller then
// plans it and waits for approval; the deployment is left planning, and
// trackTerraformRun follows it from the CR's status.
//
// A deploy replaces the root's pending plan: a deployment that is planning
// or awaiting approval becomes stale, and the rewritten CR (approvePlan "")
// makes the controller plan the new source instead. A run that is applying
// is not interrupted, and a deploy that is still being committed is not
// replaced: both refuse the new deploy, which can be repeated once they are
// done.
//
// Everything that can be checked up front (Terraform enabled and
// provisioned, a valid bundle, a runner for its OpenTofu version) is, so a
// refused deploy leaves no deployment record and doesn't touch the
// pending plan.
func (s *Service) deployTerraform(ctx context.Context, app *models.Application, version *models.Version, environment, triggeredBy string) (*models.Deployment, error) {
	tfEnv, err := s.terraformTarget(app, environment)
	if err != nil {
		return nil, err
	}

	bundle, err := s.terraformBundle(app, version)
	if err != nil {
		return nil, err
	}
	image, err := s.TerraformRoots.RunnerImage(bundle.OpenTofuVersion)
	if err != nil {
		return nil, invalid("unsupported_opentofu_version", fmt.Sprintf("Version %s can't be deployed: %s", version.VersionID, err.Error()))
	}

	root := defaultTerraformRoot
	deployment, replaced, err := s.Deployments.CreateTerraform(app.ID, version.ID, environment, root, triggeredBy)
	var busy *store.TerraformBusyError
	if errors.As(err, &busy) {
		if busy.Active.Status == models.DeploymentApplying {
			return nil, &Error{Kind: KindConflict, Code: "terraform_applying", Message: fmt.Sprintf(
				"Terraform root %s of %s in %s is being applied (deployment %s); deploy again when the apply has finished", root, app.Name, environment, busy.Active.ID)}
		}
		return nil, &Error{Kind: KindConflict, Code: "terraform_deploying", Message: fmt.Sprintf(
			"Another deploy of %s to %s is being committed (deployment %s); try again in a moment", app.Name, environment, busy.Active.ID)}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	ctx = deployCtx(ctx, app.Name, version, environment, "")
	// An older version than the one applied is a normal deploy with a new
	// plan; it is only labelled
	if s.isRollback(deployment, version) {
		if err := s.Deployments.SetRollback(deployment.ID); err != nil {
			logging.LogErrorCtx(ctx, "Failed to record the rollback: "+err.Error(), err)
		}
		deployment.Terraform.Rollback = true
	}
	if replaced != nil {
		logging.LogInfoCtx(ctx, "Replacing the pending plan of an earlier deployment",
			logging.Field("deployment_id", deployment.ID),
			logging.Field("stale_deployment_id", replaced.ID),
		)
		// Closes the check runs of the replaced deployment
		s.notifyReplaced(ctx, app, replaced, deployment)
	}

	notice := s.slackDeploy(deployment, app, version, nil)
	s.notifyRequested(ctx, notice)

	event := events.Event{
		AppName:      app.Name,
		AppID:        app.ID,
		VersionID:    version.VersionID,
		Environment:  environment,
		DeploymentID: deployment.ID,
	}
	publish := func(eventType events.EventType, mutate func(*events.Event)) {
		e := event
		e.Type = eventType
		e.Timestamp = time.Now()
		if mutate != nil {
			mutate(&e)
		}
		_ = s.Publisher.Publish(e)
	}
	logging.LogInfoCtx(ctx, "Terraform deployment started", logging.Field("deployment_id", deployment.ID))
	publish(events.EventDeploymentStarted, nil)

	// The replaced plan stays stale even if this fails: it may already be
	// gone from the CR, and a new deploy plans again anyway
	fail := func(stage string, err error) (*models.Deployment, error) {
		logging.LogErrorCtx(ctx, "Deployment failed: "+err.Error(), err)
		if updateErr := s.Deployments.UpdateStatus(deployment.ID, models.DeploymentFailed, "", err.Error()); updateErr != nil {
			logging.LogErrorCtx(ctx, "Failed to record deployment failure: "+updateErr.Error(), updateErr)
		}
		deployment.Status = models.DeploymentFailed
		deployment.ErrorMessage = err.Error()
		s.notifyFailed(ctx, notice, stage, err.Error())
		publish(events.EventDeploymentFailed, func(e *events.Event) { e.Error = err.Error() })
		return deployment, err
	}

	tfVarsFile := ""
	sourcePath := gitops.TerraformSourcePath(environment, app.Name, root)
	if _, ok := bundle.Files[tfbundle.EnvDir+"/"+environment+".tfvars"]; ok {
		tfVarsFile = sourcePath + "/" + tfbundle.EnvDir + "/" + environment + ".tfvars"
	}
	commits, err := s.TerraformGitops.DeployTerraform(gitops.TerraformDeploy{
		App:         app.Name,
		Environment: environment,
		Root:        root,
		Source:      bundle.Files,
		Manifest: func(branch, sourceCommit string) ([]byte, error) {
			return terraform.RootManifest(terraform.Root{
				App:                app.Name,
				Environment:        environment,
				Name:               root,
				VersionID:          version.VersionID,
				DeploymentID:       deployment.ID,
				Branch:             branch,
				SourceCommit:       sourceCommit,
				SourcePath:         sourcePath,
				TFVarsFile:         tfVarsFile,
				RunnerImage:        image,
				Backend:            tfEnv.Backend,
				EncryptionFallback: tfEnv.EncryptionFallback,
			}, s.TerraformRoots)
		},
		SourceMessage:   fmt.Sprintf("Add terraform source of %s version %s for %s", app.Name, version.VersionID, environment),
		ManifestMessage: fmt.Sprintf("Deploy %s version %s to %s (terraform root %s, plan only until approved)", app.Name, version.VersionID, environment, root),
	})
	if err != nil {
		return fail("gitops", err)
	}

	planID := terraform.PlanID(commits.Branch, commits.Source)
	if err := s.Deployments.SetTerraformCommitted(deployment.ID, models.DeploymentPlanning, commits.Manifest, commits.Source, planID); err != nil {
		// The change is pushed and the controller plans it either way
		logging.LogErrorCtx(ctx, "Failed to record terraform deployment commit: "+err.Error(), err)
	}
	deployment.Status = models.DeploymentPlanning
	deployment.GitopsCommitSHA = commits.Manifest
	deployment.Terraform = &models.TerraformRun{Root: root, SourceCommitSHA: commits.Source, PlanID: planID, Rollback: deployment.Terraform.Rollback}

	s.FluxReconciler.Trigger(ctx)

	notice.CommitSHA = commits.Manifest
	// More follows: the plan, then the apply
	s.notifyCommitted(ctx, notice, true)
	logging.LogInfoCtx(ctx, "Terraform deployment committed, waiting for the plan",
		logging.Field("deployment_id", deployment.ID),
		logging.Field("commit_sha", commits.Manifest),
		logging.Field("plan_id", planID),
	)
	return deployment, nil
}

// terraformTarget returns the Terraform settings of an app in an environment
// and checks that a root can be deployed or planned there: Terraform is
// enabled and provisioned, and the names can be used for it.
func (s *Service) terraformTarget(app *models.Application, environment string) (*models.TerraformEnvironment, error) {
	if err := s.requireTerraform(); err != nil {
		return nil, err
	}
	tfEnv, err := s.TerraformEnvironments.Get(app.ID, environment)
	if err != nil {
		return nil, err
	}
	if tfEnv == nil {
		return nil, invalid("terraform_not_enabled", fmt.Sprintf(
			"Terraform is not enabled for %s in %s; enable it with 'smithctl app terraform enable %s --env %s'", app.Name, environment, app.Name, environment))
	}
	if tfEnv.ProvisionedAt == nil {
		reason := ""
		if tfEnv.ProvisionError != nil {
			reason = ": " + *tfEnv.ProvisionError
		}
		return nil, invalid("terraform_not_provisioned", fmt.Sprintf(
			"Terraform for %s in %s has not been provisioned yet%s; fix the cause and run 'smithctl app terraform sync %s'", app.Name, environment, reason, app.Name))
	}
	if err := terraform.ValidateNames(app.Name, environment); err != nil {
		return nil, invalid("invalid_request", err.Error())
	}
	return tfEnv, nil
}

// terraformBundle reads and validates a terraform version's bundle, from
// the published files or, for a draft, the draft's. It was validated on
// publish; this also gives the pinned OpenTofu version.
func (s *Service) terraformBundle(app *models.Application, version *models.Version) (*tfbundle.Bundle, error) {
	files, err := s.Storage.GetAllFiles(app.Name, version.VersionID, version.Status == "published")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch terraform bundle: %w", err)
	}
	archive, ok := files[tfbundle.ArchiveName]
	if !ok {
		return nil, invalid("no_bundle", fmt.Sprintf("Version %s has no %s; upload the bundle first", version.VersionID, tfbundle.ArchiveName))
	}
	bundleFiles, err := tfbundle.Read(bytes.NewReader(archive))
	if err == nil {
		var bundle *tfbundle.Bundle
		if bundle, err = tfbundle.Validate(bundleFiles); err == nil {
			return bundle, nil
		}
	}
	if validationErr, ok := tfbundle.AsValidationError(err); ok {
		return nil, invalid("validation_failed", fmt.Sprintf("Version %s can't be deployed: %s", version.VersionID, validationErr.Error()))
	}
	return nil, fmt.Errorf("failed to read terraform bundle: %w", err)
}
