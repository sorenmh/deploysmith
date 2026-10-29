package auth_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/auth/authtest"
	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
)

var octocat = authtest.User{ID: 583231, Login: "octocat", Name: "The Octocat", Orgs: []string{"acme"}, Teams: []string{"acme/deployers"}}

// newLogin returns a login service through the fake GitHub on a fresh
// database
func newLogin(t *testing.T, gh *authtest.GitHub, cfg auth.Config) *auth.Login {
	t.Helper()
	return gh.NewLogin(dbtest.Open(t), cfg)
}

func oauthCode(err error) string {
	var oauthErr *auth.OAuthError
	if errors.As(err, &oauthErr) {
		return oauthErr.Code
	}
	return ""
}

func TestDeviceFlow(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	login := newLogin(t, gh, auth.Config{AllowedOrg: "acme"})
	ctx := context.Background()

	code, err := login.StartDeviceFlow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code.UserCode != authtest.UserCode || code.DeviceCode != authtest.DeviceCode || code.Interval != 5 {
		t.Fatalf("unexpected device code: %+v", code)
	}

	// Until the user enters the code
	if _, err := login.PollDeviceFlow(ctx, code.DeviceCode); oauthCode(err) != "authorization_pending" {
		t.Fatalf("err = %v, want authorization_pending", err)
	}
	gh.SlowDown()
	_, err = login.PollDeviceFlow(ctx, code.DeviceCode)
	var oauthErr *auth.OAuthError
	if !errors.As(err, &oauthErr) || oauthErr.Code != "slow_down" || oauthErr.Interval != 10 {
		t.Fatalf("err = %v, want slow_down with interval 10", err)
	}

	gh.Approve()
	session, err := login.PollDeviceFlow(ctx, code.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if session.Login != "octocat" || !strings.HasPrefix(session.Token, "smithd_") {
		t.Errorf("unexpected session: %+v", session)
	}
	if until := time.Until(session.ExpiresAt); until < 59*time.Minute || until > time.Hour {
		t.Errorf("session expires in %v, want an hour", until)
	}
	// The GitHub token is not kept
	if gh.LiveTokens() != 0 || len(gh.Revoked()) != 1 {
		t.Errorf("GitHub token not revoked: %d live, revoked %v", gh.LiveTokens(), gh.Revoked())
	}

	id, err := login.Authenticate(session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != auth.KindUser || id.Login != "octocat" || id.Actor() != "octocat" || id.ExpiresAt == nil {
		t.Errorf("unexpected identity: %+v", id)
	}

	if err := login.Logout(session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := login.Authenticate(session.Token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("after logout: err = %v, want ErrInvalidSession", err)
	}
}

func TestDeviceFlowDenied(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	login := newLogin(t, gh, auth.Config{AllowedOrg: "acme"})

	gh.Deny()
	if _, err := login.PollDeviceFlow(context.Background(), authtest.DeviceCode); oauthCode(err) != "access_denied" {
		t.Fatalf("err = %v, want access_denied", err)
	}
}

func TestWebLogin(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	login := newLogin(t, gh, auth.Config{AllowedOrg: "acme"})
	ctx := context.Background()

	authorize, err := url.Parse(login.AuthorizeURL("https://smithd.example.com/ui/auth/callback", "state-1"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorize.Query()
	if authorize.Path != "/login/oauth/authorize" || query.Get("client_id") != authtest.ClientID ||
		query.Get("state") != "state-1" || query.Get("scope") != "read:org" {
		t.Errorf("unexpected authorize URL: %s", authorize)
	}

	if _, err := login.CompleteWebLogin(ctx, "wrong-code", "https://smithd.example.com/ui/auth/callback"); oauthCode(err) != "bad_verification_code" {
		t.Errorf("wrong code: err = %v, want bad_verification_code", err)
	}

	session, err := login.CompleteWebLogin(ctx, authtest.WebCode, "https://smithd.example.com/ui/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := login.Authenticate(session.Token); err != nil || id.Login != "octocat" {
		t.Errorf("Authenticate = %+v, %v", id, err)
	}
}

func TestAllowed(t *testing.T) {
	tests := []struct {
		name string
		cfg  auth.Config
		want bool
	}{
		{"org member", auth.Config{AllowedOrg: "acme"}, true},
		{"org member, case-insensitive", auth.Config{AllowedOrg: "ACME"}, true},
		{"not in org", auth.Config{AllowedOrg: "other"}, false},
		{"team member", auth.Config{AllowedOrg: "acme", AllowedTeam: "deployers"}, true},
		{"org member, not in team", auth.Config{AllowedOrg: "acme", AllowedTeam: "admins"}, false},
		{"allowlisted", auth.Config{AllowedUsers: []string{"someone", "OctoCat"}}, true},
		{"not allowlisted", auth.Config{AllowedUsers: []string{"someone"}}, false},
		{"allowlisted outside the org", auth.Config{AllowedOrg: "other", AllowedUsers: []string{"octocat"}}, true},
		{"nothing configured", auth.Config{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := authtest.NewGitHub(t, octocat)
			login := newLogin(t, gh, tt.cfg)

			_, err := login.CompleteWebLogin(context.Background(), authtest.WebCode, "https://smithd.example.com/ui/auth/callback")
			if tt.want && err != nil {
				t.Fatalf("expected sign-in, got %v", err)
			}
			if !tt.want && !errors.Is(err, auth.ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			// Revoked either way
			if gh.LiveTokens() != 0 {
				t.Errorf("%d GitHub tokens not revoked", gh.LiveTokens())
			}
		})
	}
}

func TestAuthenticateInvalidAndExpired(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	// Sessions that are over as soon as they start
	login := newLogin(t, gh, auth.Config{AllowedOrg: "acme", SessionTTL: -time.Minute})

	session, err := login.CompleteWebLogin(context.Background(), authtest.WebCode, "https://smithd.example.com/ui/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := login.Authenticate(session.Token); !errors.Is(err, auth.ErrSessionExpired) {
		t.Errorf("expired: err = %v, want ErrSessionExpired", err)
	}
	for _, token := range []string{"", "nonsense", "smithd_unknown"} {
		if _, err := login.Authenticate(token); !errors.Is(err, auth.ErrInvalidSession) {
			t.Errorf("%q: err = %v, want ErrInvalidSession", token, err)
		}
	}
}

func TestActor(t *testing.T) {
	ctx := context.Background()
	if actor := auth.Actor(ctx); actor != "" {
		t.Errorf("unauthenticated actor = %q", actor)
	}
	if actor := auth.Actor(auth.WithIdentity(ctx, auth.Identity{Kind: auth.KindAPIKey})); actor != "api-key" {
		t.Errorf("API key actor = %q", actor)
	}
	if actor := auth.Actor(auth.WithIdentity(ctx, auth.Identity{Kind: auth.KindUser, Login: "octocat"})); actor != "octocat" {
		t.Errorf("user actor = %q", actor)
	}
}
