package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetAuth(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.Header.Get("X-API-Key"), r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"environments": []any{}})
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.URL, "key").ListEnvironments(); err != nil {
		t.Fatal(err)
	}
	if gotKey != "key" || gotAuth != "" {
		t.Errorf("API key client sent X-API-Key %q, Authorization %q", gotKey, gotAuth)
	}

	if _, err := NewClient(srv.URL, "key").WithSessionToken("smithd_t").ListEnvironments(); err != nil {
		t.Fatal(err)
	}
	if gotKey != "" || gotAuth != "Bearer smithd_t" {
		t.Errorf("session client sent X-API-Key %q, Authorization %q", gotKey, gotAuth)
	}
}

func TestWaitForLogin(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/device":
			json.NewEncoder(w).Encode(DeviceLogin{DeviceCode: "dc", UserCode: "ABCD-1234", VerificationURI: "https://github.com/login/device", ExpiresIn: 900, Interval: 5})
		case "/api/v1/auth/device/token":
			var req struct {
				DeviceCode string `json:"deviceCode"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.DeviceCode != "dc" {
				http.Error(w, "wrong device code", http.StatusBadRequest)
				return
			}
			polls++
			switch polls {
			case 1:
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
			case 2:
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{"status": "pending", "interval": 10})
			default:
				json.NewEncoder(w).Encode(LoginSession{Token: "smithd_t", Login: "octocat", ExpiresAt: time.Now().Add(time.Hour)})
			}
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "")
	login, err := c.StartLogin()
	if err != nil {
		t.Fatal(err)
	}

	var slept []time.Duration
	session, err := c.WaitForLogin(login, func(d time.Duration) { slept = append(slept, d) })
	if err != nil {
		t.Fatal(err)
	}
	if session.Token != "smithd_t" || session.Login != "octocat" {
		t.Errorf("unexpected session: %+v", session)
	}
	// Slows down when told to
	if len(slept) != 3 || slept[0] != 5*time.Second || slept[2] != 10*time.Second {
		t.Errorf("slept %v, want 5s, 5s, 10s", slept)
	}
}

func TestWaitForLoginFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_allowed", "message": "Your GitHub account may not sign in to this server"}})
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "").WaitForLogin(&DeviceLogin{DeviceCode: "dc", ExpiresIn: 900, Interval: 5}, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "may not sign in") {
		t.Errorf("err = %v, want smithd's message", err)
	}
}

func TestWaitForLoginExpires(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "").WaitForLogin(&DeviceLogin{DeviceCode: "dc", ExpiresIn: 20, Interval: 5}, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %v, want expired", err)
	}
}
