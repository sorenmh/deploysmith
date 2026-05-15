package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNotifier_EmptyURL_ReturnsNil(t *testing.T) {
	n := NewNotifier("")
	assert.Nil(t, n)
}

func TestNewNotifier_NonEmptyURL_ReturnsNotifier(t *testing.T) {
	n := NewNotifier("https://hooks.slack.com/services/test")
	assert.NotNil(t, n)
}

// Nil receiver no-ops — none of these should panic.
func TestNilNotifier_NoOps(t *testing.T) {
	var n *Notifier
	ctx := context.Background()

	assert.NotPanics(t, func() { n.DeployStarted(ctx, "app", "1.0", "prod", "policy") })
	assert.NotPanics(t, func() { n.DeploySucceeded(ctx, "app", "1.0", "prod", "abc123") })
	assert.NotPanics(t, func() { n.DeployFailed(ctx, "app", "1.0", "prod", "something went wrong") })
	assert.NotPanics(t, func() { n.FluxError(ctx, "app", "prod", "infrastructure", "apply failed") })
}

// helper: spin up a test server, call fn, capture the posted message text.
func withTestServer(t *testing.T, fn func(n *Notifier)) string {
	t.Helper()
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		captured = payload["text"]
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	n := NewNotifier(srv.URL)
	fn(n)
	return captured
}

func TestDeployStarted_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.DeployStarted(context.Background(), "myapp", "v1.2.3", "production", "auto-prod")
	})
	assert.Contains(t, text, "Deploy started")
	assert.Contains(t, text, "myapp")
	assert.Contains(t, text, "v1.2.3")
	assert.Contains(t, text, "production")
	assert.Contains(t, text, "auto-prod")
}

func TestDeploySucceeded_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.DeploySucceeded(context.Background(), "myapp", "v1.2.3", "staging", "deadbeef")
	})
	assert.Contains(t, text, "Deploy succeeded")
	assert.Contains(t, text, "myapp")
	assert.Contains(t, text, "v1.2.3")
	assert.Contains(t, text, "staging")
	assert.Contains(t, text, "deadbeef")
}

func TestDeployFailed_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.DeployFailed(context.Background(), "myapp", "v1.2.3", "production", "git push rejected")
	})
	assert.Contains(t, text, "Deploy failed")
	assert.Contains(t, text, "myapp")
	assert.Contains(t, text, "v1.2.3")
	assert.Contains(t, text, "production")
	assert.Contains(t, text, "git push rejected")
}

func TestFluxError_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.FluxError(context.Background(), "myapp", "production", "infrastructure", "health check failed")
	})
	assert.Contains(t, text, "Flux reconciliation error")
	assert.Contains(t, text, "myapp")
	assert.Contains(t, text, "production")
	assert.Contains(t, text, "infrastructure")
	assert.Contains(t, text, "health check failed")
}

func TestSend_Non2xxResponse_HandledGracefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	n := NewNotifier(srv.URL)
	// Should not panic even on a 500 response.
	assert.NotPanics(t, func() {
		n.DeployStarted(context.Background(), "app", "1.0", "prod", "policy")
	})
}
