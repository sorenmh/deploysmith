package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/flux"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// slackRecorder is a fake Slack webhook that records message texts
type slackRecorder struct {
	mu       sync.Mutex
	messages []string
}

func newSlackRecorder(t *testing.T) (*slackRecorder, *slack.Notifier) {
	t.Helper()
	rec := &slackRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		rec.mu.Lock()
		rec.messages = append(rec.messages, payload["text"])
		rec.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return rec, slack.NewNotifier(srv.URL)
}

// waitFor waits until n messages arrived and returns them
func (r *slackRecorder) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		messages := append([]string(nil), r.messages...)
		r.mu.Unlock()
		if len(messages) >= n || time.Now().After(deadline) {
			if len(messages) != n {
				t.Fatalf("got %d Slack messages, want %d: %q", len(messages), n, messages)
			}
			return messages
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeFlux reports a fixed outcome for every commit
type fakeFlux struct {
	result flux.PollResult
	commit chan string
}

func (f *fakeFlux) WaitForCommit(ctx context.Context, name, commitSHA string, contains func(string) (bool, error), maxWait, pollInterval time.Duration) flux.PollResult {
	f.commit <- commitSHA
	return f.result
}

func requireMessage(t *testing.T, message string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(message, part) {
			t.Errorf("message %q does not contain %q", message, part)
		}
	}
}

func TestDeployNotifications(t *testing.T) {
	t.Run("requested, committed and applied", func(t *testing.T) {
		f := newFixture(t)
		rec, notifier := newSlackRecorder(t)
		fluxFake := &fakeFlux{result: flux.PollResult{Ready: true}, commit: make(chan string, 1)}
		f.svc.Slack = notifier
		f.svc.FluxPoller = fluxFake
		f.svc.FluxKustomizationName = "apps"
		f.svc.UIBaseURL = "https://ui.example.com/"

		deployment, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh")
		if err != nil {
			t.Fatal(err)
		}

		messages := rec.waitFor(t, 3)
		link := "<https://ui.example.com/ui/deployments/" + deployment.ID + "|View deployment>"
		requireMessage(t, messages[0], "Deployment requested", "`hello` `v1` → `production`", "by sorenmh", link)
		requireMessage(t, messages[1], "Committed to gitops", "`sha-123`", "Waiting for Flux")
		requireMessage(t, messages[2], "Deployed", "applied by Flux")
		if got := <-fluxFake.commit; got != "sha-123" {
			t.Errorf("waited for commit %q, want sha-123", got)
		}
	})

	t.Run("Flux fails to apply", func(t *testing.T) {
		f := newFixture(t)
		rec, notifier := newSlackRecorder(t)
		f.svc.Slack = notifier
		f.svc.FluxPoller = &fakeFlux{result: flux.PollResult{Message: "kustomize build failed"}, commit: make(chan string, 1)}
		f.svc.FluxKustomizationName = "apps"

		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh"); err != nil {
			t.Fatal(err)
		}

		messages := rec.waitFor(t, 3)
		requireMessage(t, messages[2], "Deployment failed", "(Flux)", "kustomize build failed")
	})

	t.Run("gitops fails", func(t *testing.T) {
		f := newFixture(t)
		rec, notifier := newSlackRecorder(t)
		f.svc.Slack = notifier
		f.gitops.err = errors.New("push rejected")

		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh"); err == nil {
			t.Fatal("expected error")
		}

		messages := rec.waitFor(t, 2)
		requireMessage(t, messages[0], "Deployment requested")
		requireMessage(t, messages[1], "Deployment failed", "(gitops)", "push rejected")
	})

	t.Run("without Flux there is no final message", func(t *testing.T) {
		f := newFixture(t)
		rec, notifier := newSlackRecorder(t)
		f.svc.Slack = notifier

		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh"); err != nil {
			t.Fatal(err)
		}

		messages := rec.waitFor(t, 2)
		if strings.Contains(messages[1], "Waiting for Flux") {
			t.Errorf("committed message promises a Flux update: %q", messages[1])
		}
	})
}

func TestRollbackNotifications(t *testing.T) {
	f := newFixture(t)
	rec, notifier := newSlackRecorder(t)
	f.svc.Slack = notifier
	f.createVersion(t, "v2", "main", true)

	for _, version := range []string{"v2", "v1"} {
		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, version, "production", "sorenmh"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	messages := rec.waitFor(t, 4)
	requireMessage(t, messages[0], "Deployment requested", "`v2`")
	requireMessage(t, messages[1], "Committed to gitops")
	requireMessage(t, messages[2], "Rollback started", "`v1`")
	requireMessage(t, messages[3], "Rollback committed to gitops")
}

func TestAppRegisteredNotification(t *testing.T) {
	f := newFixture(t)
	rec, notifier := newSlackRecorder(t)
	f.svc.Slack = notifier

	if _, err := f.svc.RegisterApp("brand-new"); err != nil {
		t.Fatal(err)
	}
	requireMessage(t, rec.waitFor(t, 1)[0], "New application registered", "`brand-new`")
}

func TestVersionPublishedNotification(t *testing.T) {
	f := newFixture(t)
	rec, notifier := newSlackRecorder(t)
	f.svc.Slack = notifier

	f.svc.VersionPublished(context.Background(), f.app, f.version)
	requireMessage(t, rec.waitFor(t, 1)[0], "New version", "`hello` `v1`", "`main`")
}
