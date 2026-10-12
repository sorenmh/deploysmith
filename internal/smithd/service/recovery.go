package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

const (
	// staleDeploymentAge is how long a deployment may stay pending before it
	// is considered abandoned (e.g. smithd restarted mid-deploy)
	staleDeploymentAge = 10 * time.Minute

	// maxResumeAge is how old an interrupted auto-deploy may be and still be
	// resumed. Older ones are failed instead of surprising anyone by rolling
	// out a version long after it was built.
	maxResumeAge = time.Hour

	staleDeploymentCheckInterval = time.Minute
)

// WatchStaleDeployments periodically recovers deployments that were left
// pending, so an auto-deploy interrupted by a restart is not silently lost.
// It runs until ctx is cancelled.
func (s *Service) WatchStaleDeployments(ctx context.Context) {
	ticker := time.NewTicker(staleDeploymentCheckInterval)
	defer ticker.Stop()

	for {
		s.recoverStaleDeployments(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// recoverStaleDeployments resumes stale pending auto-deploys and fails the
// rest: manual deploys, whose requesting client has long since given up, and
// auto-deploys that are too old or would roll back a newer deployment
func (s *Service) recoverStaleDeployments(ctx context.Context) {
	cutoff := time.Now().Add(-staleDeploymentAge)

	deployments, err := s.Deployments.ListStalePending(cutoff)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list stale deployments: "+err.Error(), err)
		return
	}

	for i := range deployments {
		deployment := &deployments[i]

		resume, err := s.prepareResume(deployment)
		if err != nil {
			// Failing keeps the start time, which the superseded check of
			// later deployments to the same target relies on
			failed, failErr := s.Deployments.FailStale(deployment.ID, cutoff, err.Error())
			if failErr != nil {
				logging.LogErrorCtx(ctx, "Failed to fail stale deployment: "+failErr.Error(), failErr)
			} else if failed {
				logging.LogWarnCtx(ctx, "Stale deployment could not be resumed",
					logging.Field("deployment_id", deployment.ID),
					logging.Field("reason", err.Error()),
				)
			}
			continue
		}

		// Claim before resuming, so that with several smithd replicas only
		// one acts
		claimed, err := s.Deployments.ClaimStale(deployment.ID, cutoff)
		if err != nil {
			logging.LogErrorCtx(ctx, "Failed to claim stale deployment: "+err.Error(), err)
			continue
		}
		if !claimed {
			continue
		}

		resumeCtx := deployCtx(ctx, resume.app.Name, resume.version, resume.policy.TargetEnvironment, resume.policy.Name)
		logging.LogInfoCtx(resumeCtx, "Resuming interrupted auto-deploy", logging.Field("deployment_id", deployment.ID))

		// execute records and reports failures itself
		go func() { _ = s.execute(resumeCtx, deployment, resume.app, resume.version, resume.policy) }()
	}
}

type resumable struct {
	app     *models.Application
	version *models.Version
	policy  *models.Policy
}

// prepareResume loads what is needed to resume an interrupted auto-deploy.
// It returns an error saying why the deployment must not be resumed.
func (s *Service) prepareResume(deployment *models.Deployment) (*resumable, error) {
	if deployment.PolicyID == nil {
		return nil, fmt.Errorf("deployment was interrupted before completing (smithd stopped)")
	}
	if time.Since(deployment.StartedAt) > maxResumeAge {
		return nil, fmt.Errorf("auto-deploy was interrupted more than %s ago and was not resumed", maxResumeAge)
	}

	// Resuming would roll back whatever was deployed to the target since
	newer, err := s.Deployments.HasNewer(deployment)
	if err != nil {
		return nil, err
	}
	if newer {
		return nil, fmt.Errorf("auto-deploy was interrupted and has been superseded by a newer deployment")
	}

	policy, err := s.Policies.GetByID(*deployment.PolicyID)
	if err != nil {
		return nil, fmt.Errorf("auto-deploy was interrupted and its policy is gone: %w", err)
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("auto-deploy was interrupted and policy %s has since been disabled", policy.Name)
	}

	app, err := s.Apps.GetByID(deployment.AppID)
	if err != nil {
		return nil, fmt.Errorf("auto-deploy was interrupted and its application is gone: %w", err)
	}

	version, err := s.Versions.GetByID(deployment.VersionID)
	if err != nil {
		return nil, fmt.Errorf("auto-deploy was interrupted and its version is gone: %w", err)
	}

	return &resumable{app: app, version: version, policy: policy}, nil
}
