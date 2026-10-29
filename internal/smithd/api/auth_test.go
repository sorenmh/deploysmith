package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/auth/authtest"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

var octocat = authtest.User{ID: 583231, Login: "octocat", Name: "The Octocat", Orgs: []string{"acme"}}

// newLoginTestServer serves the API with API key "key" and, if gh is not
// nil, login through that fake GitHub
func newLoginTestServer(t *testing.T, gh *authtest.GitHub, cfg auth.Config) *Server {
	t.Helper()
	s := newStoreTestServer(t)
	s.cfg = &config.Config{APIKeys: []string{"key"}}
	if gh != nil {
		s.login = gh.NewLogin(s.db, cfg)
	}
	s.router = chi.NewRouter()
	s.setupRoutes()
	return s
}

func call(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("not an error response: %s", rec.Body)
	}
	return resp.Error.Code
}

// signIn returns a session token for octocat
func signIn(t *testing.T, login *auth.Login) string {
	t.Helper()
	session, err := login.CompleteWebLogin(context.Background(), authtest.WebCode, "http://smithd.test/ui/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func TestAuthMiddleware(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})
	token := signIn(t, s.login)
	// Over as soon as it starts
	expired := signIn(t, gh.NewLogin(s.db, auth.Config{AllowedOrg: "acme", SessionTTL: -time.Minute}))

	tests := []struct {
		name      string
		headers   map[string]string
		want      int
		wantCode  string
		wantActor string
	}{
		{"API key", map[string]string{"X-API-Key": "key"}, http.StatusOK, "", "api-key"},
		{"wrong API key", map[string]string{"X-API-Key": "nope"}, http.StatusUnauthorized, "unauthorized", ""},
		{"nothing", nil, http.StatusUnauthorized, "unauthorized", ""},
		{"session", map[string]string{"Authorization": "Bearer " + token}, http.StatusOK, "", "octocat"},
		{"session takes precedence", map[string]string{"Authorization": "Bearer " + token, "X-API-Key": "nope"}, http.StatusOK, "", "octocat"},
		{"invalid session", map[string]string{"Authorization": "Bearer smithd_nope"}, http.StatusUnauthorized, "unauthorized", ""},
		{"expired session", map[string]string{"Authorization": "Bearer " + expired}, http.StatusUnauthorized, "session_expired", ""},
		{"other scheme", map[string]string{"Authorization": "Basic Zm9vOmJhcg=="}, http.StatusUnauthorized, "unauthorized", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := call(t, s, http.MethodGet, "/api/v1/auth/whoami", "", tt.headers)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if tt.wantCode != "" {
				if code := errorCode(t, rec); code != tt.wantCode {
					t.Errorf("error code = %q, want %q", code, tt.wantCode)
				}
				return
			}
			var id auth.Identity
			if err := json.Unmarshal(rec.Body.Bytes(), &id); err != nil {
				t.Fatal(err)
			}
			if id.Actor() != tt.wantActor {
				t.Errorf("actor = %q, want %q", id.Actor(), tt.wantActor)
			}
		})
	}
}

func TestAuthMiddlewareWithoutLogin(t *testing.T) {
	s := newLoginTestServer(t, nil, auth.Config{})

	if rec := call(t, s, http.MethodGet, "/api/v1/auth/whoami", "", map[string]string{"X-API-Key": "key"}); rec.Code != http.StatusOK {
		t.Errorf("API key: %d", rec.Code)
	}
	if rec := call(t, s, http.MethodGet, "/api/v1/auth/whoami", "", map[string]string{"Authorization": "Bearer smithd_x"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("session token without login: %d, want 401", rec.Code)
	}
	if rec := call(t, s, http.MethodPost, "/api/v1/auth/device", "", nil); rec.Code != http.StatusNotFound || errorCode(t, rec) != "login_disabled" {
		t.Errorf("device login without login: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, http.MethodGet, "/ui/apps", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("UI without login or proxy: %d, want 404", rec.Code)
	}
}

func TestDeviceLoginAPI(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})

	rec := call(t, s, http.MethodPost, "/api/v1/auth/device", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	var start models.DeviceLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	if start.UserCode != authtest.UserCode || start.DeviceCode == "" || start.Interval != 5 || start.VerificationURI == "" {
		t.Fatalf("unexpected start: %+v", start)
	}

	poll := func() *httptest.ResponseRecorder {
		return call(t, s, http.MethodPost, "/api/v1/auth/device/token", `{"deviceCode":"`+start.DeviceCode+`"}`, nil)
	}

	if rec := poll(); rec.Code != http.StatusAccepted {
		t.Fatalf("pending poll = %d: %s", rec.Code, rec.Body)
	}
	gh.SlowDown()
	rec = poll()
	var pending models.DeviceTokenPendingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted || pending.Interval != 10 {
		t.Fatalf("slow down = %d: %s", rec.Code, rec.Body)
	}

	gh.Approve()
	rec = poll()
	if rec.Code != http.StatusOK {
		t.Fatalf("approved poll = %d: %s", rec.Code, rec.Body)
	}
	var session models.SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Login != "octocat" || session.Token == "" || session.ExpiresAt.Before(time.Now()) {
		t.Fatalf("unexpected session: %+v", session)
	}

	bearer := map[string]string{"Authorization": "Bearer " + session.Token}
	if rec := call(t, s, http.MethodGet, "/api/v1/auth/whoami", "", bearer); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"login":"octocat"`) {
		t.Errorf("whoami = %d: %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"X-API-Key": "key"}); rec.Code != http.StatusBadRequest {
		t.Errorf("logout with API key = %d, want 400", rec.Code)
	}
	if rec := call(t, s, http.MethodPost, "/api/v1/auth/logout", "", bearer); rec.Code != http.StatusNoContent {
		t.Errorf("logout = %d: %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, http.MethodGet, "/api/v1/auth/whoami", "", bearer); rec.Code != http.StatusUnauthorized {
		t.Errorf("whoami after logout = %d, want 401", rec.Code)
	}
}

// TestSmithctlLogin runs smithctl's side of the login against the API
func TestSmithctlLogin(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	c := client.NewClient(srv.URL, "")
	login, err := c.StartLogin()
	if err != nil {
		t.Fatal(err)
	}
	if login.UserCode != authtest.UserCode {
		t.Fatalf("unexpected login: %+v", login)
	}

	polls := 0
	session, err := c.WaitForLogin(login, func(time.Duration) {
		// The user confirms on GitHub after a while
		if polls++; polls == 2 {
			gh.Approve()
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	id, err := client.NewClient(srv.URL, "").WithSessionToken(session.Token).WhoAmI()
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != "user" || id.Login != "octocat" || id.ExpiresAt == nil {
		t.Errorf("unexpected identity: %+v", id)
	}
	if id, err := client.NewClient(srv.URL, "key").WhoAmI(); err != nil || id.Kind != "api-key" {
		t.Errorf("API key identity = %+v, %v", id, err)
	}

	if err := client.NewClient(srv.URL, "").WithSessionToken(session.Token).Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewClient(srv.URL, "").WithSessionToken(session.Token).WhoAmI(); err == nil || !strings.Contains(err.Error(), "Invalid session") {
		t.Errorf("whoami after logout: err = %v", err)
	}
}

func TestDeviceLoginAPIRejects(t *testing.T) {
	t.Run("not allowed", func(t *testing.T) {
		gh := authtest.NewGitHub(t, octocat)
		s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "other"})
		gh.Approve()
		rec := call(t, s, http.MethodPost, "/api/v1/auth/device/token", `{"deviceCode":"`+authtest.DeviceCode+`"}`, nil)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "not_allowed" {
			t.Errorf("status = %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("denied", func(t *testing.T) {
		gh := authtest.NewGitHub(t, octocat)
		s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})
		gh.Deny()
		rec := call(t, s, http.MethodPost, "/api/v1/auth/device/token", `{"deviceCode":"`+authtest.DeviceCode+`"}`, nil)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "access_denied" {
			t.Errorf("status = %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("no device code", func(t *testing.T) {
		gh := authtest.NewGitHub(t, octocat)
		s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})
		if rec := call(t, s, http.MethodPost, "/api/v1/auth/device/token", `{}`, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d: %s", rec.Code, rec.Body)
		}
	})
}

func TestUIWithGitHubLogin(t *testing.T) {
	gh := authtest.NewGitHub(t, octocat)
	s := newLoginTestServer(t, gh, auth.Config{AllowedOrg: "acme"})

	if rec := call(t, s, http.MethodGet, "/ui/apps", "", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("/ui/apps without session = %d, want redirect to sign in", rec.Code)
	}
	if rec := call(t, s, http.MethodGet, "/ui/auth/login", "", nil); rec.Code != http.StatusOK {
		t.Errorf("/ui/auth/login = %d", rec.Code)
	}
}
