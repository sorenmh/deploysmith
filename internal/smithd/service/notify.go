package service

import (
	"context"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// The notify* methods send an event to every notification channel (Slack and
// Web Push), each of which may be unconfigured.

func (s *Service) notifyRequested(ctx context.Context, d slack.Deploy) {
	s.Slack.Requested(ctx, d)
	s.Push.Requested(ctx, d)
}

func (s *Service) notifyCommitted(ctx context.Context, d slack.Deploy, waitingForFlux bool) {
	s.Slack.Committed(ctx, d, waitingForFlux)
	s.Push.Committed(ctx, d, waitingForFlux)
}

func (s *Service) notifyApplied(ctx context.Context, d slack.Deploy, took time.Duration) {
	s.Slack.Applied(ctx, d, took)
	s.Push.Applied(ctx, d, took)
}

func (s *Service) notifyFailed(ctx context.Context, d slack.Deploy, stage, errMsg string) {
	s.Slack.Failed(ctx, d, stage, errMsg)
	s.Push.Failed(ctx, d, stage, errMsg)
}

func (s *Service) notifyVersionPublished(ctx context.Context, app, version, branch, committer string) {
	s.Slack.VersionPublished(ctx, app, version, branch, committer)
	s.Push.VersionPublished(ctx, app, version, branch, committer)
}

func (s *Service) notifyAppRegistered(ctx context.Context, app string) {
	s.Slack.AppRegistered(ctx, app)
	s.Push.AppRegistered(ctx, app)
}
