package service

import (
	"context"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// PushPublicKey returns the key browsers subscribe to push with, or "" if
// push notifications are not configured
func (s *Service) PushPublicKey() string {
	if s.PushSubscriptions == nil {
		return ""
	}
	return s.Push.PublicKey()
}

// SubscribePush stores a browser's push subscription for a user
func (s *Service) SubscribePush(ctx context.Context, user string, sub store.PushSubscription) error {
	if s.PushPublicKey() == "" {
		return invalid("push_disabled", "Push notifications are not configured")
	}
	if !strings.HasPrefix(sub.Endpoint, "https://") || sub.P256dh == "" || sub.Auth == "" {
		return invalid("invalid_subscription", "Invalid push subscription")
	}
	sub.User = user
	if err := s.PushSubscriptions.Save(sub); err != nil {
		return err
	}
	s.Push.Test(ctx)
	return nil
}

// UnsubscribePush removes a browser's push subscription
func (s *Service) UnsubscribePush(endpoint string) error {
	if s.PushSubscriptions == nil {
		return nil
	}
	return s.PushSubscriptions.Delete(endpoint)
}
