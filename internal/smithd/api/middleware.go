package api

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
)

// Logger middleware logs HTTP requests
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Probes hit the health endpoint constantly; logging them is noise
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Create a response writer wrapper to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		log.Printf(
			"%s %s %s %d %s",
			r.Method,
			r.RequestURI,
			r.RemoteAddr,
			rw.statusCode,
			time.Since(start),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Auth middleware authenticates a machine by API key (X-API-Key) or a
// signed-in user by session token (Authorization: Bearer), and puts the
// identity in the request context (see auth.FromContext). login is nil when
// GitHub login is not configured; then only API keys are accepted.
func Auth(apiKeys []string, login *auth.Login) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token, ok := bearerToken(r); ok {
				if login == nil {
					writeError(w, http.StatusUnauthorized, "unauthorized", "Login is not configured on this server, use an API key")
					return
				}
				id, err := login.Authenticate(token)
				switch {
				case errors.Is(err, auth.ErrSessionExpired):
					writeError(w, http.StatusUnauthorized, "session_expired", "Session expired, run `smithctl login`")
					return
				case errors.Is(err, auth.ErrInvalidSession):
					writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid session token")
					return
				case err != nil:
					log.Printf("Failed to authenticate session: %v", err)
					writeError(w, http.StatusInternalServerError, "internal_error", "Failed to authenticate")
					return
				}
				next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
				return
			}

			// Get API key from header
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "X-API-Key or Authorization header is required")
				return
			}

			// Validate API key
			valid := false
			for _, key := range apiKeys {
				if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1 {
					valid = true
					break
				}
			}

			if !valid {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid API key")
				return
			}

			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{Kind: auth.KindAPIKey})))
		})
	}
}

// bearerToken returns the token of an "Authorization: Bearer" header
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// CORS middleware adds CORS headers
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ContentType middleware sets JSON content type
func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only set for non-OPTIONS requests
		if r.Method != "OPTIONS" && !strings.HasPrefix(r.URL.Path, "/health") {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}
