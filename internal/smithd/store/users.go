package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
)

// User is a person who signed in with GitHub
type User struct {
	ID       string
	GitHubID int64
	Login    string
	Name     string
}

// UserStore stores the users who signed in
type UserStore struct {
	db *db.DB
}

// NewUserStore creates a new UserStore
func NewUserStore(db *db.DB) *UserStore {
	return &UserStore{db: db}
}

// Upsert records a sign-in of the GitHub user, creating the user on the
// first sign-in and updating login and name (which can change) after that
func (s *UserStore) Upsert(githubID int64, login, name string) (*User, error) {
	now := time.Now().UTC().Truncate(time.Second)
	_, err := s.db.Exec(`
		INSERT INTO users (id, github_id, login, name, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (github_id) DO UPDATE SET login = excluded.login, name = excluded.name, last_login_at = excluded.last_login_at
	`, uuid.New().String(), githubID, login, name, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to save user: %w", err)
	}

	user := &User{}
	err = s.db.QueryRow(`SELECT id, github_id, login, name FROM users WHERE github_id = ?`, githubID).
		Scan(&user.ID, &user.GitHubID, &user.Login, &user.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}
