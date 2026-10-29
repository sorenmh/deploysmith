package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
)

// Session kinds: smithctl sessions from the device flow, and web UI sessions
const (
	SessionCLI = "cli"
	SessionWeb = "web"
)

// ErrSessionNotFound is returned for an unknown session token
var ErrSessionNotFound = errors.New("session not found")

// Session is a signed-in user's session
type Session struct {
	ID        string
	UserID    string
	Login     string
	Kind      string
	ExpiresAt time.Time
}

// SessionStore stores sessions by the hash of their token
type SessionStore struct {
	db *db.DB
}

// NewSessionStore creates a new SessionStore
func NewSessionStore(db *db.DB) *SessionStore {
	return &SessionStore{db: db}
}

// Create stores a new session for the user
func (s *SessionStore) Create(tokenHash, userID, kind string, expiresAt time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, token_hash, user_id, kind, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, uuid.New().String(), tokenHash, userID, kind, time.Now().UTC().Truncate(time.Second), expiresAt.UTC().Truncate(time.Second))
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetByTokenHash returns the session with the given token hash, expired or
// not, or ErrSessionNotFound
func (s *SessionStore) GetByTokenHash(tokenHash string) (*Session, error) {
	session := &Session{}
	err := s.db.QueryRow(`
		SELECT s.id, s.user_id, u.login, s.kind, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?
	`, tokenHash).Scan(&session.ID, &session.UserID, &session.Login, &session.Kind, &session.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return session, nil
}

// DeleteByTokenHash removes the session with the given token hash, if any
func (s *SessionStore) DeleteByTokenHash(tokenHash string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// DeleteExpired removes the sessions that expired before now
func (s *SessionStore) DeleteExpired(now time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("failed to delete expired sessions: %w", err)
	}
	return nil
}
