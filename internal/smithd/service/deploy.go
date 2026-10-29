package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
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
	if version.ArtifactType == models.ArtifactTerraform {
		return s.deployTerraform(ctx, app, version, environment, triggeredBy)
	}

	deployment, err := s.Deployments.Create(appID, version.ID, environment, triggeredBy, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	ctx = deployCtx(ctx, app.Name, version, environment, "")
	s.notifyRequested(ctx, s.slackDeploy(deployment, app, version, nil))
	err = s.execute(ctx, deployment, app, version, nil)
	return deployment, err
}

// execute carries out a pending deployment: it pushes the version's
// manifests to the gitops repo, records the outcome and sends
// notifications. policy is nil for manual deployments. It is safe to call
// again for a deployment that was interrupted.
func (s *Service) execute(ctx context.Context, deployment *models.Deployment, app *models.Application, version *models.Version, policy *models.Policy) error {
	environment := deployment.Environment

	policyName := ""
	commitMsg := fmt.Sprintf("Deploy %s version %s to %s", app.Name, version.VersionID, environment)
	if policy != nil {
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

	notice := s.slackDeploy(deployment, app, version, policy)

	fail := func(stage string, err error) error {
		logging.LogErrorCtx(ctx, "Deployment failed: "+err.Error(), err)
		if updateErr := s.Deployments.UpdateStatus(deployment.ID, "failed", "", err.Error()); updateErr != nil {
			logging.LogErrorCtx(ctx, "Failed to record deployment failure: "+updateErr.Error(), updateErr)
		}
		deployment.Status = "failed"
		deployment.ErrorMessage = err.Error()
		s.notifyFailed(ctx, notice, stage, err.Error())
		publish(events.EventDeploymentFailed, func(e *events.Event) { e.Error = err.Error() })
		return err
	}

	logging.LogInfoCtx(ctx, "Deployment started", logging.Field("deployment_id", deployment.ID))
	publish(events.EventDeploymentStarted, nil)

	// Never commit a bundle as manifests; terraform versions go through
	// deployTerraform and are never auto-deployed or resumed
	if version.ArtifactType != models.ArtifactManifests {
		return fail("artifact type", invalid("unsupported_artifact_type", fmt.Sprintf("Version %s is a %s version, not manifests", version.VersionID, version.ArtifactType)))
	}

	logging.LogDebugCtx(ctx, "Fetching manifests from storage")
	manifests, err := s.Storage.GetAllFiles(app.Name, version.VersionID, true)
	if err != nil {
		return fail("fetching manifests", fmt.Errorf("failed to fetch manifests: %w", err))
	}

	logging.LogDebugCtx(ctx, "Deploying to gitops repo")
	commitSHA, err := s.Gitops.Deploy(app.Name, environment, commitMsg, manifests)
	if err != nil {
		return fail("gitops", err)
	}
	committedAt := time.Now()

	s.FluxReconciler.Trigger(ctx)

	if err := s.Deployments.UpdateStatus(deployment.ID, "success", commitSHA, ""); err != nil {
		// The change is pushed, so the deployment itself succeeded
		logging.LogErrorCtx(ctx, "Failed to record deployment success: "+err.Error(), err)
	}
	deployment.Status = "success"
	deployment.GitopsCommitSHA = commitSHA

	publish(events.EventDeploymentSucceeded, func(e *events.Event) { e.CommitSHA = commitSHA })
	notice.CommitSHA = commitSHA
	watchFlux := s.FluxPoller != nil && s.FluxKustomizationName != ""
	s.notifyCommitted(ctx, notice, watchFlux)
	logging.LogInfoCtx(ctx, "Deployment succeeded",
		logging.Field("deployment_id", deployment.ID),
		logging.Field("commit_sha", commitSHA),
	)

	// Watch Flux in the background so callers don't wait for reconciliation
	if watchFlux {
		go s.watchFlux(context.WithoutCancel(ctx), notice, committedAt)
	}

	return nil
}

const (
	// fluxApplyTimeout is how long to wait for Flux to apply a deployment
	fluxApplyTimeout = 10 * time.Minute
	fluxPollInterval = 10 * time.Second
)

// watchFlux reports whether Flux applies a deployment's commit
func (s *Service) watchFlux(ctx context.Context, notice slack.Deploy, committedAt time.Time) {
	contains := func(revision string) (bool, error) { return s.Gitops.Contains(revision, notice.CommitSHA) }
	result := s.FluxPoller.WaitForCommit(ctx, s.FluxKustomizationName, notice.CommitSHA, contains, fluxApplyTimeout, fluxPollInterval)
	if !result.Ready {
		logging.LogWarnCtx(ctx, "Flux did not apply deployment: "+result.Message)
		s.notifyFailed(ctx, notice, "Flux", result.Message)
		return
	}
	logging.LogInfoCtx(ctx, "Flux applied deployment", logging.Field("commit_sha", notice.CommitSHA))
	s.notifyApplied(ctx, notice, time.Since(committedAt))
}

// slackDeploy describes a deployment for notifications
func (s *Service) slackDeploy(deployment *models.Deployment, app *models.Application, version *models.Version, policy *models.Policy) slack.Deploy {
	notice := slack.Deploy{
		App:         app.Name,
		Version:     version.VersionID,
		Environment: deployment.Environment,
		TriggeredBy: deployment.TriggeredBy,
		CommitSHA:   deployment.GitopsCommitSHA,
	}
	if policy != nil {
		notice.Policy = policy.Name
	}
	notice.Rollback = s.isRollback(deployment, version)
	if s.UIBaseURL != "" {
		notice.URL = strings.TrimSuffix(s.UIBaseURL, "/") + "/ui/deployments/" + deployment.ID
	}
	return notice
}

// isRollback reports whether a deployment puts an older version than the one
// previously deployed into the environment
func (s *Service) isRollback(deployment *models.Deployment, version *models.Version) bool {
	previousID, err := s.Deployments.PreviousSuccessVersion(deployment)
	if err != nil || previousID == "" || previousID == version.ID {
		return false
	}
	previous, err := s.Versions.GetByID(previousID)
	if err != nil {
		return false
	}
	return version.CreatedAt.Before(previous.CreatedAt)
}

// requireDeployable returns an error for versions smithd cannot deploy yet:
// terraform bundles, which are stored but not deployed until smithd can
// commit them to the gitops repo for a controller to plan and apply
func requireDeployable(version *models.Version) error {
	if version.ArtifactType == models.ArtifactTerraform {
		return invalid("unsupported_artifact_type", fmt.Sprintf("Version %s is a terraform bundle; deploying terraform versions is not supported yet", version.VersionID))
	}
	return nil
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
