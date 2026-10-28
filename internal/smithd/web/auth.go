package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
)

// AuthConfig configures how UI users are authenticated: smithd signs them
// in with GitHub itself (Login), or a proxy in front of smithd (e.g.
// oauth2-proxy) authenticates them and passes the user's identity in a
// request header (UserHeader).
type AuthConfig struct {
	// Login signs users in with GitHub and keeps their sessions
	Login *auth.Login
	// PublicURL is where users reach the UI, e.g. https://smithd.example.com;
	// it gives GitHub's callback URL. Without it the callback URL is derived
	// from the request.
	PublicURL string

	// UserHeader is the header carrying the authenticated user's identity,
	// e.g. "X-Auth-Request-Email"
	UserHeader string
	// AllowedUsers restricts access to these emails, or to a whole domain
	// with an "@example.com" entry. Empty allows every authenticated user.
	AllowedUsers []string

	// TrustedOrigins are additional origins (e.g. "https://deploy.example.com")
	// allowed to submit forms, for setups where the Host header seen by
	// smithd differs from the public origin
	TrustedOrigins []string
}

// sessionCookie holds the smithd session token of a user signed in with
// GitHub
const sessionCookie = "smithd_session"

// UserFromContext returns the authenticated UI user, or "" if there is none
func UserFromContext(ctx context.Context) string {
	if id, ok := auth.FromContext(ctx); ok && id.Kind == auth.KindUser {
		return id.Login
	}
	return ""
}

// Authenticate returns middleware that rejects requests without an allowed
// user identity, stores the identity in the request context, and rejects
// cross-origin form submissions (CSRF), since the browser is authenticated
// with a cookie. Users signed in with GitHub are recognised by their session
// cookie; without one they are sent to the sign-in page.
func Authenticate(cfg AuthConfig) (func(http.Handler) http.Handler, error) {
	if cfg.UserHeader == "" && cfg.Login == nil {
		return nil, fmt.Errorf("user header or login with GitHub must be configured")
	}
	if cfg.UserHeader != "" && cfg.Login != nil {
		return nil, fmt.Errorf("user header and login with GitHub can't both be configured")
	}

	csrf := http.NewCrossOriginProtection()
	for _, origin := range cfg.TrustedOrigins {
		if err := csrf.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("invalid trusted origin %q: %w", origin, err)
		}
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Cross-origin request rejected", http.StatusForbidden)
	}))

	return func(next http.Handler) http.Handler {
		authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.Login != nil {
				id, ok := sessionIdentity(cfg.Login, r)
				if !ok {
					requireLogin(w, r)
					return
				}
				next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
				return
			}

			user := strings.TrimSpace(r.Header.Get(cfg.UserHeader))
			if user == "" {
				http.Error(w, "Not authenticated", http.StatusUnauthorized)
				return
			}
			if !userAllowed(user, cfg.AllowedUsers) {
				http.Error(w, "Access denied for "+user, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{Kind: auth.KindUser, Login: user})))
		})
		return csrf.Handler(authenticated)
	}, nil
}

// sessionIdentity returns the user of the request's session cookie, if it
// holds a valid session
func sessionIdentity(login *auth.Login, r *http.Request) (auth.Identity, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Identity{}, false
	}
	id, err := login.Authenticate(cookie.Value)
	return id, err == nil
}

// requireLogin sends the browser to the sign-in page, which brings it back
// to the requested page afterwards
func requireLogin(w http.ResponseWriter, r *http.Request) {
	next := ""
	if r.Method == http.MethodGet && r.Header.Get("HX-Request") != "true" {
		next = r.URL.RequestURI()
	}
	loginURL := Prefix + "/auth/login"
	if next != "" {
		loginURL += "?next=" + url.QueryEscape(next)
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", loginURL)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}

// userAllowed reports whether user matches the allowlist; an empty
// allowlist allows everyone
func userAllowed(user string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}

	user = strings.ToLower(user)
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if strings.HasPrefix(entry, "@") {
			if strings.HasSuffix(user, entry) {
				return true
			}
		} else if user == entry {
			return true
		}
	}
	return false
}
