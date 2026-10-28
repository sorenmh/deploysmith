package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session is a smithd session from `smithctl login`, for one smithd URL.
// forge can use it too, for runs by a person (e.g. local plans).
type Session struct {
	Token     string    `json:"token"`
	Login     string    `json:"login"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Expired reports whether the session is over
func (s Session) Expired() bool {
	return !time.Now().Before(s.ExpiresAt)
}

// sessionsFile is where sessions are stored, next to the config file. It
// holds credentials, so it is only readable by the user.
func sessionsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".deploysmith", "sessions.json"), nil
}

// sessionKey normalises a smithd URL, so trailing slashes don't matter
func sessionKey(url string) string {
	return strings.TrimRight(url, "/")
}

func loadSessions() (map[string]Session, error) {
	path, err := sessionsFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Session{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	sessions := map[string]Session{}
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return sessions, nil
}

func saveSessions(sessions map[string]Session) error {
	path, err := sessionsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	// Write a new file and rename it, so the file is never readable by
	// others, not even briefly, and never half-written
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sessions-*.json")
	if err != nil {
		return fmt.Errorf("failed to write sessions: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write sessions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write sessions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write sessions: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to write sessions: %w", err)
	}
	return nil
}

// LoadSession returns the stored session for the smithd URL, expired or
// not, or nil if there is none
func LoadSession(url string) (*Session, error) {
	sessions, err := loadSessions()
	if err != nil {
		return nil, err
	}
	session, ok := sessions[sessionKey(url)]
	if !ok {
		return nil, nil
	}
	return &session, nil
}

// SaveSession stores the session for the smithd URL
func SaveSession(url string, session Session) error {
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	sessions[sessionKey(url)] = session
	return saveSessions(sessions)
}

// DeleteSession removes the stored session for the smithd URL, if any
func DeleteSession(url string) error {
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	if _, ok := sessions[sessionKey(url)]; !ok {
		return nil
	}
	delete(sessions, sessionKey(url))
	return saveSessions(sessions)
}

// GetSessionToken returns the token of an unexpired session for the
// configured smithd URL, or "" if there is none
func GetSessionToken() string {
	url := GetSmithdURL()
	if url == "" {
		return ""
	}
	session, err := LoadSession(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		return ""
	}
	if session == nil || session.Expired() {
		return ""
	}
	return session.Token
}
