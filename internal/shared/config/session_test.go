package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if session, err := LoadSession("https://smithd.example.com"); err != nil || session != nil {
		t.Fatalf("no sessions yet: %+v, %v", session, err)
	}

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := SaveSession("https://smithd.example.com/", Session{Token: "smithd_a", Login: "octocat", ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSession("https://other.example.com", Session{Token: "smithd_b", Login: "octocat", ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(home, ".deploysmith", "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("sessions file mode = %o, want 600", mode)
	}

	// Trailing slashes don't matter
	session, err := LoadSession("https://smithd.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || session.Token != "smithd_a" || !session.ExpiresAt.Equal(expires) || session.Expired() {
		t.Fatalf("unexpected session: %+v", session)
	}

	if err := DeleteSession("https://smithd.example.com"); err != nil {
		t.Fatal(err)
	}
	if session, _ := LoadSession("https://smithd.example.com"); session != nil {
		t.Errorf("session still there after delete: %+v", session)
	}
	if session, _ := LoadSession("https://other.example.com"); session == nil || session.Token != "smithd_b" {
		t.Errorf("other server's session lost: %+v", session)
	}
}

func TestGetSessionToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	smithdURL = "https://smithd.example.com"
	t.Cleanup(func() { smithdURL = "" })

	if token := GetSessionToken(); token != "" {
		t.Errorf("token without session = %q", token)
	}

	if err := SaveSession(smithdURL, Session{Token: "smithd_old", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if token := GetSessionToken(); token != "" {
		t.Errorf("expired session gives token %q", token)
	}

	if err := SaveSession(smithdURL, Session{Token: "smithd_new", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if token := GetSessionToken(); token != "smithd_new" {
		t.Errorf("token = %q, want smithd_new", token)
	}
}
