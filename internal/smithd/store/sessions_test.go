package store

import (
	"errors"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
)

func TestUsersAndSessions(t *testing.T) {
	database := dbtest.Open(t)
	users := NewUserStore(database)
	sessions := NewSessionStore(database)

	user, err := users.Upsert(583231, "octocat", "The Octocat")
	if err != nil {
		t.Fatal(err)
	}
	// Signing in again after a rename keeps the user, with the new login
	renamed, err := users.Upsert(583231, "octocat2", "The Octocat")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != user.ID || renamed.Login != "octocat2" {
		t.Fatalf("unexpected user after rename: %+v, was %+v", renamed, user)
	}

	expires := time.Now().Add(time.Hour)
	if err := sessions.Create("hash-1", user.ID, SessionCLI, expires); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create("hash-2", user.ID, SessionWeb, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	session, err := sessions.GetByTokenHash("hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.Login != "octocat2" || session.Kind != SessionCLI || session.UserID != user.ID {
		t.Errorf("unexpected session: %+v", session)
	}
	if diff := session.ExpiresAt.Sub(expires); diff > time.Second || diff < -time.Second {
		t.Errorf("expires at %v, want %v", session.ExpiresAt, expires)
	}

	if _, err := sessions.GetByTokenHash("unknown"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("unknown token: err = %v, want ErrSessionNotFound", err)
	}

	// Only the expired session is cleaned up
	if err := sessions.DeleteExpired(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.GetByTokenHash("hash-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expired session: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := sessions.GetByTokenHash("hash-1"); err != nil {
		t.Errorf("valid session was deleted: %v", err)
	}

	if err := sessions.DeleteByTokenHash("hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.GetByTokenHash("hash-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("deleted session: err = %v, want ErrSessionNotFound", err)
	}
}
