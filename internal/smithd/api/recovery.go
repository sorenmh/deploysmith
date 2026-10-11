package api

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

	staleDeploymentCheckInterval = time.Minute
)

// watchStaleDeployments periodically recovers deployments that were left
// pending, so an auto-deploy interrupted by a restart is not silently lost
func (s *Server) watchStaleDeployments(ctx context.Context) {
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

// recoverStaleDeployments resumes stale pending auto-deploys and fails stale
// manual deploys, whose requesting client has long since given up
func (s *Server) recoverStaleDeployments(ctx context.Context) {
	cutoff := time.Now().Add(-staleDeploymentAge)

	deployments, err := s.deploymentStore.ListStalePending(cutoff)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list stale deployments: "+err.Error(), err)
		return
	}

	for i := range deployments {
		deployment := &deployments[i]

		// Claim first, so that with several smithd replicas only one acts
		claimed, err := s.deploymentStore.ClaimStale(deployment.ID, cutoff)
		if err != nil {
			logging.LogErrorCtx(ctx, "Failed to claim stale deployment: "+err.Error(), err)
			continue
		}
		if !claimed {
			continue
		}

		if err := s.resumeDeployment(ctx, deployment); err != nil {
			logging.LogWarnCtx(ctx, "Stale deployment could not be resumed",
				logging.Field("deployment_id", deployment.ID),
				logging.Field("reason", err.Error()),
			)
			s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", err.Error())
		}
	}
}

// resumeDeployment restarts an interrupted auto-deploy in the background. It
// returns an error if the deployment cannot be resumed.
func (s *Server) resumeDeployment(ctx context.Context, deployment *models.Deployment) error {
	if deployment.PolicyID == nil {
		return fmt.Errorf("deployment was interrupted before completing (smithd stopped)")
	}

	policy, err := s.policyStore.GetByID(*deployment.PolicyID)
	if err != nil {
		return fmt.Errorf("auto-deploy was interrupted and its policy is gone: %w", err)
	}
	if !policy.Enabled {
		return fmt.Errorf("auto-deploy was interrupted and policy %s has since been disabled", policy.Name)
	}

	app, err := s.appStore.GetByID(deployment.AppID)
	if err != nil {
		return fmt.Errorf("auto-deploy was interrupted and its application is gone: %w", err)
	}

	version, err := s.versionStore.GetByID(deployment.VersionID)
	if err != nil {
		return fmt.Errorf("auto-deploy was interrupted and its version is gone: %w", err)
	}

	ctx = autoDeployCtx(ctx, app.Name, version, *policy)
	logging.LogInfoCtx(ctx, "Resuming interrupted auto-deploy", logging.Field("deployment_id", deployment.ID))

	go s.runAutoDeploy(ctx, app.Name, app.ID, version, *policy, deployment)
	return nil
}
