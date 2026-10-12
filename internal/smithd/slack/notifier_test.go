package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	d := Deploy{App: "app", Version: "1.0", Environment: "prod"}

	assert.NotPanics(t, func() { n.Requested(ctx, d) })
	assert.NotPanics(t, func() { n.Committed(ctx, d, true) })
	assert.NotPanics(t, func() { n.Applied(ctx, d, time.Minute) })
	assert.NotPanics(t, func() { n.Failed(ctx, d, "gitops", "something went wrong") })
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

func TestRequested_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.Requested(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "production", TriggeredBy: "sorenmh", URL: "https://ui.example.com/ui/deployments/1"})
	})
	assert.Contains(t, text, "Deployment requested")
	assert.Contains(t, text, "`myapp` `v1.2.3` → `production`")
	assert.Contains(t, text, "by sorenmh")
	assert.Contains(t, text, "<https://ui.example.com/ui/deployments/1|View deployment>")

	text = withTestServer(t, func(n *Notifier) {
		n.Requested(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "production", TriggeredBy: "auto-deploy", Policy: "main-to-prod"})
	})
	assert.Contains(t, text, "by policy `main-to-prod`")
	assert.NotContains(t, text, "View deployment")
}

func TestCommitted_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.Committed(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "staging", CommitSHA: "deadbeefcafe"}, true)
	})
	assert.Contains(t, text, "Committed to gitops")
	assert.Contains(t, text, "`deadbee`")
	assert.Contains(t, text, "Waiting for Flux")

	text = withTestServer(t, func(n *Notifier) {
		n.Committed(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "staging", CommitSHA: "deadbeefcafe"}, false)
	})
	assert.NotContains(t, text, "Waiting for Flux")
}

func TestApplied_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.Applied(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "production"}, 72400*time.Millisecond)
	})
	assert.Contains(t, text, "Deployed")
	assert.Contains(t, text, "`myapp` `v1.2.3` → `production`")
	assert.Contains(t, text, "1m12s")
}

func TestFailed_MessageContent(t *testing.T) {
	text := withTestServer(t, func(n *Notifier) {
		n.Failed(context.Background(), Deploy{App: "myapp", Version: "v1.2.3", Environment: "production"}, "Flux", "Deployment/x <failed> & stuck")
	})
	assert.Contains(t, text, "Deployment failed")
	assert.Contains(t, text, "(Flux)")
	assert.Contains(t, text, "Deployment/x &lt;failed&gt; &amp; stuck")
}

func TestSend_Non2xxResponse_HandledGracefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	n := NewNotifier(srv.URL)
	// Should not panic even on a 500 response.
	assert.NotPanics(t, func() {
		n.Requested(context.Background(), Deploy{App: "app", Version: "1.0", Environment: "prod"})
	})
}
