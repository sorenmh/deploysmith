package service

import (
	"context"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// The notify* methods send an event to every notification channel (Slack,
// Web Push and, for deployments, GitHub check runs), each of which may be
// unconfigured.

func (s *Service) notifyRequested(ctx context.Context, d slack.Deploy) {
	s.Slack.Requested(ctx, d)
	s.Push.Requested(ctx, d)
	s.GitHub.Requested(ctx, d)
}

func (s *Service) notifyCommitted(ctx context.Context, d slack.Deploy, waitingForFlux bool) {
	s.Slack.Committed(ctx, d, waitingForFlux)
	s.Push.Committed(ctx, d, waitingForFlux)
	s.GitHub.Committed(ctx, d, waitingForFlux)
}

func (s *Service) notifyApplied(ctx context.Context, d slack.Deploy, took time.Duration) {
	s.Slack.Applied(ctx, d, took)
	s.Push.Applied(ctx, d, took)
	s.GitHub.Applied(ctx, d, took)
}

func (s *Service) notifyFailed(ctx context.Context, d slack.Deploy, stage, errMsg string) {
	s.Slack.Failed(ctx, d, stage, errMsg)
	s.Push.Failed(ctx, d, stage, errMsg)
	s.GitHub.Failed(ctx, d, stage, errMsg)
}

// notifyPlanReady reports a Terraform plan that waits for approval
func (s *Service) notifyPlanReady(ctx context.Context, d slack.Deploy) {
	s.Slack.PlanReady(ctx, d)
	s.Push.PlanReady(ctx, d)
	s.GitHub.PlanReady(ctx, d)
}

// notifyApplying and notifyStale only update the check runs on GitHub: an
// approval and a replacement are done by people who see the result, and
// Slack and Web Push stay with the events that need someone to act.

func (s *Service) notifyApplying(ctx context.Context, d slack.Deploy) {
	s.GitHub.Applying(ctx, d)
}

func (s *Service) notifyStale(ctx context.Context, d slack.Deploy, reason string) {
	s.GitHub.Stale(ctx, d, reason)
}

func (s *Service) notifyVersionPublished(ctx context.Context, app, version, branch, committer string) {
	s.Slack.VersionPublished(ctx, app, version, branch, committer)
	s.Push.VersionPublished(ctx, app, version, branch, committer)
}

func (s *Service) notifyAppRegistered(ctx context.Context, app string) {
	s.Slack.AppRegistered(ctx, app)
	s.Push.AppRegistered(ctx, app)
}
