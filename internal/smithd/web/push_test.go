package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/push"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

func postJSON(t *testing.T, url, user, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set(testUserHeader, user)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestServiceWorkerIsPublicAndRevalidated(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, body := get(t, srv, "/ui/sw.js", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "showNotification") {
		t.Fatalf("status %d, body %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", resp.Header.Get("Cache-Control"))
	}
}

func TestPushDisabledByDefault(t *testing.T) {
	srv, _ := newTestServer(t)

	_, page := get(t, srv, "/ui/apps", "alice@example.com")
	if strings.Contains(page, "push-toggle") {
		t.Error("notification button shown without VAPID keys")
	}

	resp := postJSON(t, srv.URL+"/ui/push/subscribe", "alice@example.com",
		`{"endpoint":"https://push.example/1","keys":{"p256dh":"k","auth":"a"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("subscribe status = %d, want 400", resp.StatusCode)
	}
}

func TestPushSubscribeAndUnsubscribe(t *testing.T) {
	srv, svc := newTestServer(t)
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	subs := store.NewPushSubscriptionStore(database)
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	svc.PushSubscriptions = subs
	svc.Push = push.NewNotifier(subs, public, private, "mailto:test@example.com")

	_, page := get(t, srv, "/ui/apps", "alice@example.com")
	if !strings.Contains(page, `data-key="`+public+`"`) {
		t.Error("notification button missing the public key")
	}

	// The endpoint is unreachable; the confirmation notification just fails
	body := `{"endpoint":"https://127.0.0.1:1/device","keys":{"p256dh":"k","auth":"a"}}`
	if resp := postJSON(t, srv.URL+"/ui/push/subscribe", "alice@example.com", body); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("subscribe status = %d, want 204", resp.StatusCode)
	}
	list, _ := subs.List()
	if len(list) != 1 || list[0].User != "alice@example.com" {
		t.Fatalf("subscriptions = %+v", list)
	}

	if resp := postJSON(t, srv.URL+"/ui/push/unsubscribe", "alice@example.com", `{"endpoint":"https://127.0.0.1:1/device"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unsubscribe status = %d, want 204", resp.StatusCode)
	}
	if list, _ = subs.List(); len(list) != 0 {
		t.Fatalf("subscriptions after unsubscribe = %+v", list)
	}

	if resp := postJSON(t, srv.URL+"/ui/push/subscribe", "", body); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous subscribe status = %d, want 401", resp.StatusCode)
	}
}
