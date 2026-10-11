package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticate(t *testing.T) {
	auth, err := Authenticate(AuthConfig{
		UserHeader:   "X-Auth-Request-Email",
		AllowedUsers: []string{"bob@example.org", "@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotUser string
	handler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
	}))

	tests := []struct {
		name    string
		method  string
		user    string
		headers map[string]string
		want    int
	}{
		{"no user", http.MethodGet, "", nil, http.StatusUnauthorized},
		{"allowed by domain", http.MethodGet, "alice@example.com", nil, http.StatusOK},
		{"allowed by domain, case-insensitive", http.MethodGet, "Alice@Example.COM", nil, http.StatusOK},
		{"allowed by email", http.MethodGet, "bob@example.org", nil, http.StatusOK},
		{"domain suffix lookalike", http.MethodGet, "mallory@evilexample.com", nil, http.StatusForbidden},
		{"not allowed", http.MethodGet, "mallory@example.org", nil, http.StatusForbidden},
		{"same-origin post", http.MethodPost, "alice@example.com", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"cross-site post", http.MethodPost, "alice@example.com", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"cross-origin post by Origin", http.MethodPost, "alice@example.com", map[string]string{"Origin": "https://evil.test"}, http.StatusForbidden},
		{"same-origin post by Origin", http.MethodPost, "alice@example.com", map[string]string{"Origin": "http://deploy.example.com"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUser = ""
			req := httptest.NewRequest(tt.method, "http://deploy.example.com/ui/", nil)
			if tt.user != "" {
				req.Header.Set("X-Auth-Request-Email", tt.user)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if tt.want == http.StatusOK && gotUser != tt.user {
				t.Errorf("user in context = %q, want %q", gotUser, tt.user)
			}
		})
	}
}

func TestAuthenticateWithoutAllowlist(t *testing.T) {
	auth, err := Authenticate(AuthConfig{UserHeader: "X-Forwarded-Email"})
	if err != nil {
		t.Fatal(err)
	}
	handler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	req.Header.Set("X-Forwarded-Email", "anyone@anywhere.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuthenticateConfigErrors(t *testing.T) {
	if _, err := Authenticate(AuthConfig{}); err == nil {
		t.Error("expected error without user header")
	}
	if _, err := Authenticate(AuthConfig{UserHeader: "X-User", TrustedOrigins: []string{"not a url"}}); err == nil {
		t.Error("expected error for invalid trusted origin")
	}
}
