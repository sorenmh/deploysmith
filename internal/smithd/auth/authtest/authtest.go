// Package authtest fakes GitHub's OAuth endpoints and the parts of its API
// that login with GitHub uses, for tests
package authtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

const (
	ClientID     = "test-client-id"
	ClientSecret = "test-client-secret"
	// DeviceCode and WebCode are the codes the fake hands out for the
	// device flow and the web flow
	DeviceCode = "device-code-1"
	UserCode   = "ABCD-1234"
	WebCode    = "web-code-1"
)

// User is a GitHub account of the fake
type User struct {
	ID    int64
	Login string
	Name  string
	Orgs  []string
	// Teams are "org/team-slug" entries
	Teams []string
}

// GitHub is a fake GitHub. The device flow is pending until Approve is
// called; the token it then hands out (and the web flow's) belongs to User.
type GitHub struct {
	Server *httptest.Server

	mu       sync.Mutex
	user     User
	approved bool
	denied   bool
	slowDown bool
	tokens   map[string]bool // live tokens
	issued   int
	revoked  []string
}

// NewGitHub starts a fake GitHub signing in as user
func NewGitHub(t testing.TB, user User) *GitHub {
	t.Helper()
	g := &GitHub{user: user, tokens: map[string]bool{}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", g.handleDeviceCode)
	mux.HandleFunc("POST /login/oauth/access_token", g.handleAccessToken)
	mux.HandleFunc("GET /user", g.withToken(g.handleUser))
	mux.HandleFunc("GET /user/memberships/orgs/{org}", g.withToken(g.handleOrgMembership))
	mux.HandleFunc("GET /orgs/{org}/teams/{team}/memberships/{login}", g.withToken(g.handleTeamMembership))
	mux.HandleFunc("DELETE /applications/{clientID}/token", g.handleRevoke)

	g.Server = httptest.NewServer(mux)
	t.Cleanup(g.Server.Close)
	return g
}

// Client returns a GitHub client for the fake
func (g *GitHub) Client() *auth.GitHub {
	return auth.NewGitHub(ClientID, ClientSecret, g.Server.URL, g.Server.URL)
}

// NewLogin returns a login service signing in through the fake, storing
// users and sessions in database. Sessions last an hour unless cfg says
// otherwise.
func (g *GitHub) NewLogin(database *db.DB, cfg auth.Config) *auth.Login {
	cfg.GitHub = g.Client()
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = time.Hour
	}
	return auth.NewLogin(cfg, store.NewUserStore(database), store.NewSessionStore(database))
}

// Approve lets the pending device flow complete
func (g *GitHub) Approve() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.approved = true
}

// Deny makes the user cancel the device flow
func (g *GitHub) Deny() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied = true
}

// SlowDown makes the next device flow poll answer "slow_down"
func (g *GitHub) SlowDown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.slowDown = true
}

// Revoked returns the tokens that were revoked
func (g *GitHub) Revoked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.revoked...)
}

// LiveTokens returns how many tokens were handed out and not revoked
func (g *GitHub) LiveTokens() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.tokens)
}

func (g *GitHub) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != ClientID || r.Header.Get("Accept") != "application/json" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"device_code":      DeviceCode,
		"user_code":        UserCode,
		"verification_uri": g.Server.URL + "/login/device",
		"expires_in":       900,
		"interval":         5,
	})
}

func (g *GitHub) handleAccessToken(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if r.FormValue("client_id") != ClientID {
		writeJSON(w, map[string]any{"error": "incorrect_client_credentials"})
		return
	}

	switch {
	case r.FormValue("grant_type") == "urn:ietf:params:oauth:grant-type:device_code":
		switch {
		case r.FormValue("device_code") != DeviceCode:
			writeJSON(w, map[string]any{"error": "incorrect_device_code"})
		case g.slowDown:
			g.slowDown = false
			writeJSON(w, map[string]any{"error": "slow_down", "interval": 10})
		case g.denied:
			writeJSON(w, map[string]any{"error": "access_denied"})
		case !g.approved:
			writeJSON(w, map[string]any{"error": "authorization_pending"})
		default:
			writeJSON(w, map[string]any{"access_token": g.issueToken(), "token_type": "bearer"})
		}
	case r.FormValue("code") != "":
		// The web flow needs the client secret
		if r.FormValue("client_secret") != ClientSecret {
			writeJSON(w, map[string]any{"error": "incorrect_client_credentials"})
			return
		}
		if r.FormValue("code") != WebCode {
			writeJSON(w, map[string]any{"error": "bad_verification_code"})
			return
		}
		writeJSON(w, map[string]any{"access_token": g.issueToken(), "token_type": "bearer"})
	default:
		writeJSON(w, map[string]any{"error": "unsupported_grant_type"})
	}
}

func (g *GitHub) issueToken() string {
	g.issued++
	token := fmt.Sprintf("gho_test%d", g.issued)
	g.tokens[token] = true
	return token
}

// withToken rejects API requests without a live token
func (g *GitHub) withToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		live := g.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		g.mu.Unlock()
		if !live {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (g *GitHub) handleUser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"id": g.user.ID, "login": g.user.Login, "name": g.user.Name})
}

func (g *GitHub) handleOrgMembership(w http.ResponseWriter, r *http.Request) {
	for _, org := range g.user.Orgs {
		if strings.EqualFold(org, r.PathValue("org")) {
			writeJSON(w, map[string]any{"state": "active", "role": "member"})
			return
		}
	}
	http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
}

func (g *GitHub) handleTeamMembership(w http.ResponseWriter, r *http.Request) {
	want := r.PathValue("org") + "/" + r.PathValue("team")
	if strings.EqualFold(r.PathValue("login"), g.user.Login) {
		for _, team := range g.user.Teams {
			if strings.EqualFold(team, want) {
				writeJSON(w, map[string]any{"state": "active", "role": "member"})
				return
			}
		}
	}
	http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
}

func (g *GitHub) handleRevoke(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if !ok || id != ClientID || secret != ClientSecret || r.PathValue("clientID") != ClientID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusUnprocessableEntity)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.tokens, body.AccessToken)
	g.revoked = append(g.revoked, body.AccessToken)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
