package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/auth/authtest"
	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
)

var octocat = authtest.User{ID: 583231, Login: "octocat", Name: "The Octocat", Orgs: []string{"acme"}}

// noRedirects is a client that shows redirects instead of following them
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

// newLoginTestServer serves the UI with login through a fake GitHub
func newLoginTestServer(t *testing.T, user authtest.User, publicURL string) (*httptest.Server, *authtest.GitHub) {
	t.Helper()
	gh := authtest.NewGitHub(t, user)
	database := dbtest.Open(t)
	srv, _ := newTestServerWithAuth(t, database, AuthConfig{Login: gh.NewLogin(database, auth.Config{AllowedOrg: "acme"}), PublicURL: publicURL})
	return srv, gh
}

// do sends a request without following redirects
func do(t *testing.T, method, rawURL string, cookies []*http.Cookie, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func cookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in response", name)
	return nil
}

// startLogin follows the sign-in page to GitHub and returns the state
// cookie and the state GitHub would send back
func startLogin(t *testing.T, srv *httptest.Server, next string) (*http.Cookie, string) {
	t.Helper()
	resp, _ := do(t, http.MethodGet, srv.URL+"/ui/auth/github?next="+url.QueryEscape(next), nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/ui/auth/github = %d, want redirect to GitHub", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/login/oauth/authorize" {
		t.Fatalf("redirected to %s, want GitHub's authorize page", location)
	}
	if got := location.Query().Get("redirect_uri"); got != srv.URL+"/ui/auth/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	return cookie(t, resp, stateCookie), location.Query().Get("state")
}

func TestLoginWithGitHub(t *testing.T) {
	srv, gh := newLoginTestServer(t, octocat, "")

	// Pages need a session
	resp, _ := do(t, http.MethodGet, srv.URL+"/ui/apps?page=2", nil, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/auth/login?next="+url.QueryEscape("/ui/apps?page=2") {
		t.Fatalf("without session: %d to %q, want the sign-in page", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = do(t, http.MethodGet, srv.URL+"/ui/apps", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("HX-Redirect") != "/ui/auth/login" {
		t.Errorf("htmx without session: %d, HX-Redirect %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}

	resp, body := do(t, http.MethodGet, srv.URL+"/ui/auth/login?next=/ui/apps", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `href="/ui/auth/github?next=%2fui%2fapps"`) {
		t.Fatalf("sign-in page = %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "Signed in as") {
		t.Error("sign-in page shows a user")
	}

	state, stateValue := startLogin(t, srv, "/ui/apps")
	if !state.HttpOnly || state.SameSite != http.SameSiteLaxMode || state.Secure {
		t.Errorf("state cookie = %+v, want HttpOnly, SameSite=Lax, not Secure over http", state)
	}

	resp, _ = do(t, http.MethodGet, srv.URL+"/ui/auth/callback?code="+authtest.WebCode+"&state="+stateValue, []*http.Cookie{state}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/apps" {
		t.Fatalf("callback = %d to %q, want back to /ui/apps", resp.StatusCode, resp.Header.Get("Location"))
	}
	session := cookie(t, resp, sessionCookie)
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/ui" || !strings.HasPrefix(session.Value, "smithd_") {
		t.Errorf("session cookie = %+v", session)
	}
	if gh.LiveTokens() != 0 {
		t.Error("GitHub token kept after sign-in")
	}

	resp, body = do(t, http.MethodGet, srv.URL+"/ui/apps", []*http.Cookie{session}, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Signed in as octocat") || !strings.Contains(body, "Sign out") {
		t.Fatalf("with session: %d: %s", resp.StatusCode, body)
	}

	// Signing out ends the session
	resp, _ = do(t, http.MethodPost, srv.URL+"/ui/auth/logout", []*http.Cookie{session}, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/auth/login" {
		t.Fatalf("logout = %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if cleared := cookie(t, resp, sessionCookie); cleared.MaxAge >= 0 {
		t.Errorf("session cookie not cleared: %+v", cleared)
	}
	if resp, _ = do(t, http.MethodGet, srv.URL+"/ui/apps", []*http.Cookie{session}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout: %d, want redirect to sign in", resp.StatusCode)
	}
}

func TestLoginCallbackRejects(t *testing.T) {
	srv, _ := newLoginTestServer(t, octocat, "")
	state, stateValue := startLogin(t, srv, "/ui/apps")

	tests := []struct {
		name    string
		query   string
		cookies []*http.Cookie
		want    int
	}{
		{"no state cookie", "code=" + authtest.WebCode + "&state=" + stateValue, nil, http.StatusBadRequest},
		{"wrong state", "code=" + authtest.WebCode + "&state=other", []*http.Cookie{state}, http.StatusBadRequest},
		{"cancelled on GitHub", "error=access_denied&state=" + stateValue, []*http.Cookie{state}, http.StatusForbidden},
		{"bad code", "code=wrong&state=" + stateValue, []*http.Cookie{state}, http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, http.MethodGet, srv.URL+"/ui/auth/callback?"+tt.query, tt.cookies, nil)
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tt.want, body)
			}
			for _, c := range resp.Cookies() {
				if c.Name == sessionCookie && c.MaxAge >= 0 {
					t.Error("got a session")
				}
			}
		})
	}
}

func TestLoginNotAllowed(t *testing.T) {
	outsider := authtest.User{ID: 1, Login: "mallory", Orgs: []string{"evil"}}
	srv, _ := newLoginTestServer(t, outsider, "")
	state, stateValue := startLogin(t, srv, "/ui/apps")

	resp, body := do(t, http.MethodGet, srv.URL+"/ui/auth/callback?code="+authtest.WebCode+"&state="+stateValue, []*http.Cookie{state}, nil)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "may not sign in") {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
}

func TestLoginSecureCookies(t *testing.T) {
	srv, _ := newLoginTestServer(t, octocat, "https://smithd.example.com")

	resp, _ := do(t, http.MethodGet, srv.URL+"/ui/auth/github", nil, nil)
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := location.Query().Get("redirect_uri"); got != "https://smithd.example.com/ui/auth/callback" {
		t.Errorf("redirect_uri = %q, want the public URL's callback", got)
	}
	state := cookie(t, resp, stateCookie)
	if !state.Secure {
		t.Error("state cookie not Secure behind https")
	}

	resp, _ = do(t, http.MethodGet, srv.URL+"/ui/auth/callback?code="+authtest.WebCode+"&state="+location.Query().Get("state"), []*http.Cookie{state}, nil)
	if session := cookie(t, resp, sessionCookie); !session.Secure {
		t.Error("session cookie not Secure behind https")
	}
}

func TestSafeNext(t *testing.T) {
	for next, want := range map[string]string{
		"/ui/apps?page=2":     "/ui/apps?page=2",
		"/ui":                 "/ui",
		"":                    "/ui/",
		"https://evil.test/":  "/ui/",
		"//evil.test/ui/apps": "/ui/",
		"/uifoo":              "/ui/",
	} {
		if got := safeNext(next); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
		}
	}
}
