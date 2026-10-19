package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

type fakeSubs struct {
	mu      sync.Mutex
	subs    []store.PushSubscription
	deleted []string
}

func (f *fakeSubs) List() ([]store.PushSubscription, error) { return f.subs, nil }
func (f *fakeSubs) Delete(endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, endpoint)
	return nil
}

// subscription returns a valid browser subscription pointing at endpoint
func subscription(t *testing.T, endpoint string) store.PushSubscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return store.PushSubscription{
		Endpoint: endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

func newNotifier(t *testing.T, subs Subscriptions) *Notifier {
	t.Helper()
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	return NewNotifier(subs, public, private, "mailto:test@example.com")
}

func TestNewNotifier_WithoutKeys_ReturnsNil(t *testing.T) {
	if NewNotifier(&fakeSubs{}, "", "", "") != nil {
		t.Error("expected nil Notifier without VAPID keys")
	}
}

func TestNilNotifier_NoOps(t *testing.T) {
	var n *Notifier
	n.Requested(context.Background(), slack.Deploy{})
	n.AppRegistered(context.Background(), "app")
	if n.PublicKey() != "" {
		t.Error("nil Notifier has a public key")
	}
}

func TestSend_DeliversToEverySubscription(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	subs := &fakeSubs{subs: []store.PushSubscription{subscription(t, srv.URL+"/a"), subscription(t, srv.URL+"/b")}}
	n := newNotifier(t, subs)
	n.client = srv.Client()

	n.Failed(context.Background(), slack.Deploy{App: "hello", Version: "v1", Environment: "production"}, "gitops", "boom")

	if hits != 2 {
		t.Errorf("got %d pushes, want 2", hits)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("deleted subscriptions after success: %v", subs.deleted)
	}
}

func TestSend_RemovesExpiredSubscription(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(srv.Close)

	subs := &fakeSubs{subs: []store.PushSubscription{subscription(t, srv.URL+"/gone")}}
	n := newNotifier(t, subs)
	n.client = srv.Client()

	n.AppRegistered(context.Background(), "hello")

	if len(subs.deleted) != 1 || subs.deleted[0] != srv.URL+"/gone" {
		t.Errorf("deleted = %v, want the expired endpoint", subs.deleted)
	}
}
