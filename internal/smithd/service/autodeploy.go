package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// VersionPublished notifies that a new version was published
func (s *Service) VersionPublished(ctx context.Context, app *models.Application, version *models.Version) {
	go s.notifyVersionPublished(context.WithoutCancel(ctx), app.Name, version.VersionID, version.GitBranch, version.GitCommitter)
}

// TriggerAutoDeploys starts an auto-deploy, in the background, for every
// enabled policy of the app that matches the version's branch
func (s *Service) TriggerAutoDeploys(ctx context.Context, app *models.Application, version *models.Version) {
	if version.GitBranch == "" || version.GitBranch == "unknown" {
		logging.LogInfoCtx(ctx, "Skipping auto-deploy: no git branch metadata on version")
		s.warnIfPoliciesSkipped(ctx, app.ID, version.GitBranch)
		return
	}

	matchingPolicies, err := s.Policies.FindMatchingPolicies(app.ID, version.GitBranch)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to check auto-deploy policies: "+err.Error(), err)
		return
	}

	logging.LogInfoCtx(ctx, fmt.Sprintf("Found %d matching auto-deploy policies for branch %s", len(matchingPolicies), version.GitBranch))
	if len(matchingPolicies) == 0 {
		logging.LogInfoCtx(ctx, "No matching auto-deploy policies found, skipping auto-deploy")
		s.warnIfPoliciesSkipped(ctx, app.ID, version.GitBranch)
	}

	for _, policy := range matchingPolicies {
		logging.LogInfoCtx(ctx, "Matched auto-deploy policy",
			logging.Field("policy", policy.Name),
			logging.Field("branch_pattern", policy.GitBranchPattern),
			logging.Field("target_environment", policy.TargetEnvironment),
		)

		// Detach from the caller (e.g. an HTTP request) so the deploy outlives it
		go s.autoDeploy(context.WithoutCancel(ctx), app, version, policy)
	}
}

// autoDeploy creates a deployment record for a policy and executes it
func (s *Service) autoDeploy(ctx context.Context, app *models.Application, version *models.Version, policy models.Policy) {
	ctx = deployCtx(ctx, app.Name, version, policy.TargetEnvironment, policy.Name)

	policyID := policy.ID
	deployment, err := s.Deployments.Create(app.ID, version.ID, policy.TargetEnvironment, "auto-deploy", &policyID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Auto-deploy failed to create deployment record: "+err.Error(), err)
		return
	}
	s.notifyRequested(ctx, s.slackDeploy(deployment, app, version, &policy))

	// execute records and reports failures itself
	_ = s.execute(ctx, deployment, app, version, &policy)
}

// warnIfPoliciesSkipped logs a warning when an app has enabled auto-deploy
// policies but a published version did not trigger any of them, which
// usually means the build passed missing or unexpected branch metadata
func (s *Service) warnIfPoliciesSkipped(ctx context.Context, appID, branch string) {
	policies, err := s.Policies.List(appID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list policies: "+err.Error(), err)
		return
	}

	var patterns []string
	for _, policy := range policies {
		if policy.Enabled {
			patterns = append(patterns, policy.GitBranchPattern)
		}
	}
	if len(patterns) == 0 {
		return
	}

	logging.LogWarnCtx(ctx, "Published version did not trigger any auto-deploy policy",
		logging.Field("git_branch", branch),
		logging.Field("policy_patterns", strings.Join(patterns, ", ")),
	)
}
