package store

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
)

// PushSubscription is a browser's Web Push subscription
type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
	User     string
}

// PushSubscriptionStore stores Web Push subscriptions
type PushSubscriptionStore struct {
	db *db.DB
}

// NewPushSubscriptionStore creates a new PushSubscriptionStore
func NewPushSubscriptionStore(db *db.DB) *PushSubscriptionStore {
	return &PushSubscriptionStore{db: db}
}

// Save stores a subscription, replacing the keys and user of an existing
// subscription with the same endpoint
func (s *PushSubscriptionStore) Save(sub PushSubscription) error {
	_, err := s.db.Exec(`
		INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, user_name)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, user_name = excluded.user_name
	`, uuid.New().String(), sub.Endpoint, sub.P256dh, sub.Auth, sub.User)
	if err != nil {
		return fmt.Errorf("failed to save push subscription: %w", err)
	}
	return nil
}

// Delete removes the subscription with the given endpoint, if any
func (s *PushSubscriptionStore) Delete(endpoint string) error {
	if _, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint); err != nil {
		return fmt.Errorf("failed to delete push subscription: %w", err)
	}
	return nil
}

// List returns every subscription
func (s *PushSubscriptionStore) List() ([]PushSubscription, error) {
	rows, err := s.db.Query(`SELECT endpoint, p256dh, auth, user_name FROM push_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("failed to list push subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.User); err != nil {
			return nil, fmt.Errorf("failed to scan push subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}
