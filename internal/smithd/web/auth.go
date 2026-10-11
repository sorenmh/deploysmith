package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// AuthConfig configures how UI users are authenticated. Authentication
// itself is done by a proxy in front of smithd (e.g. oauth2-proxy), which
// passes the user's identity in a request header.
type AuthConfig struct {
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

type userKey struct{}

// UserFromContext returns the authenticated UI user, or "" if there is none
func UserFromContext(ctx context.Context) string {
	user, _ := ctx.Value(userKey{}).(string)
	return user
}

// Authenticate returns middleware that rejects requests without an allowed
// user identity, stores the identity in the request context, and rejects
// cross-origin form submissions (CSRF), since the proxy authenticates the
// browser with a cookie.
func Authenticate(cfg AuthConfig) (func(http.Handler) http.Handler, error) {
	if cfg.UserHeader == "" {
		return nil, fmt.Errorf("user header must be configured")
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
			user := strings.TrimSpace(r.Header.Get(cfg.UserHeader))
			if user == "" {
				http.Error(w, "Not authenticated", http.StatusUnauthorized)
				return
			}
			if !userAllowed(user, cfg.AllowedUsers) {
				http.Error(w, "Access denied for "+user, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
		})
		return csrf.Handler(authenticated)
	}, nil
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
