package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// DeployVersion deploys a published version to an environment and waits
// until the change is pushed to the gitops repository. triggeredBy records
// who requested the deployment.
//
// If the deployment fails after its record was created, the failed
// deployment is returned along with the error.
func (s *Service) DeployVersion(ctx context.Context, appID, versionID, environment, triggeredBy string) (*models.Deployment, error) {
	if err := s.requireEnvironment(environment); err != nil {
		return nil, err
	}

	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}

	version, err := s.Versions.GetByVersionID(appID, versionID)
	if err != nil {
		if err.Error() == "version not found" {
			return nil, notFound("Version not found")
		}
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	if version.Status != "published" {
		return nil, invalid("invalid_status", "Version must be published before deployment")
	}

	deployment, err := s.Deployments.Create(appID, version.ID, environment, triggeredBy, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	ctx = deployCtx(ctx, app.Name, version, environment, "")
	err = s.execute(ctx, deployment, app, version, nil)
	return deployment, err
}

// execute carries out a pending deployment: it pushes the version's
// manifests to the gitops repo, records the outcome and sends
// notifications. policy is nil for manual deployments. It is safe to call
// again for a deployment that was interrupted.
func (s *Service) execute(ctx context.Context, deployment *models.Deployment, app *models.Application, version *models.Version, policy *models.Policy) error {
	environment := deployment.Environment

	trigger := "manual"
	policyName := ""
	commitMsg := fmt.Sprintf("Deploy %s version %s to %s", app.Name, version.VersionID, environment)
	if policy != nil {
		trigger = policy.Name
		policyName = policy.Name
		commitMsg = fmt.Sprintf("Auto-deploy %s version %s to %s (policy: %s)", app.Name, version.VersionID, environment, policy.Name)
	}

	event := events.Event{
		AppName:      app.Name,
		AppID:        app.ID,
		VersionID:    version.VersionID,
		Environment:  environment,
		PolicyName:   policyName,
		DeploymentID: deployment.ID,
	}
	publish := func(eventType events.EventType, mutate func(*events.Event)) {
		e := event
		e.Type = eventType
		e.Timestamp = time.Now()
		if mutate != nil {
			mutate(&e)
		}
		s.Publisher.Publish(e)
	}

	fail := func(err error) error {
		logging.LogErrorCtx(ctx, "Deployment failed: "+err.Error(), err)
		if updateErr := s.Deployments.UpdateStatus(deployment.ID, "failed", "", err.Error()); updateErr != nil {
			logging.LogErrorCtx(ctx, "Failed to record deployment failure: "+updateErr.Error(), updateErr)
		}
		deployment.Status = "failed"
		deployment.ErrorMessage = err.Error()
		s.Slack.DeployFailed(ctx, app.Name, version.VersionID, environment, err.Error())
		publish(events.EventDeploymentFailed, func(e *events.Event) { e.Error = err.Error() })
		return err
	}

	logging.LogInfoCtx(ctx, "Deployment started", logging.Field("deployment_id", deployment.ID))
	s.Slack.DeployStarted(ctx, app.Name, version.VersionID, environment, trigger)
	publish(events.EventDeploymentStarted, nil)

	logging.LogDebugCtx(ctx, "Fetching manifests from storage")
	manifests, err := s.Storage.GetAllFiles(app.Name, version.VersionID, true)
	if err != nil {
		return fail(fmt.Errorf("failed to fetch manifests: %w", err))
	}

	logging.LogDebugCtx(ctx, "Deploying to gitops repo")
	commitSHA, err := s.Gitops.Deploy(app.Name, environment, commitMsg, manifests)
	if err != nil {
		return fail(err)
	}

	s.FluxReconciler.Trigger(ctx)

	if err := s.Deployments.UpdateStatus(deployment.ID, "success", commitSHA, ""); err != nil {
		// The change is pushed, so the deployment itself succeeded
		logging.LogErrorCtx(ctx, "Failed to record deployment success: "+err.Error(), err)
	}
	deployment.Status = "success"
	deployment.GitopsCommitSHA = commitSHA

	publish(events.EventDeploymentSucceeded, func(e *events.Event) { e.CommitSHA = commitSHA })
	s.Slack.DeploySucceeded(ctx, app.Name, version.VersionID, environment, commitSHA)
	logging.LogInfoCtx(ctx, "Deployment succeeded",
		logging.Field("deployment_id", deployment.ID),
		logging.Field("commit_sha", commitSHA),
	)

	// Watch Flux in the background so callers don't wait for reconciliation
	if s.FluxPoller != nil && s.FluxKustomizationName != "" {
		go s.watchFlux(context.WithoutCancel(ctx), app.Name, environment)
	}

	return nil
}

// watchFlux alerts if Flux fails to reconcile after a deployment
func (s *Service) watchFlux(ctx context.Context, appName, environment string) {
	result := s.FluxPoller.Poll(ctx, s.FluxKustomizationName, 2*time.Minute, 10*time.Second)
	if !result.Ready {
		s.Slack.FluxError(ctx, appName, environment, s.FluxKustomizationName, result.Message)
	}
}

// deployCtx adds a deployment's identifying fields to the log context
func deployCtx(ctx context.Context, appName string, version *models.Version, environment, policyName string) context.Context {
	fields := []logging.AttrProvider{
		logging.Field("app", appName),
		logging.Field("version", version.VersionID),
		logging.Field("environment", environment),
	}
	if policyName != "" {
		fields = append(fields, logging.Field("policy", policyName))
	}
	return logging.AppendLogAttrs(ctx, fields...)
}
