package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/flux"
	"github.com/sorenmh/deploysmith/internal/smithd/github"
	"github.com/sorenmh/deploysmith/internal/smithd/github/githubtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

const sourceSHA = "0123456789abcdef0123456789abcdef01234567"

// withGitHub reports the fixture's deployments to a fake GitHub, with the
// fixture's version built from acme/hello
func withGitHub(t *testing.T, f *fixture) *githubtest.Server {
	t.Helper()
	gh := githubtest.New(t)
	gh.Install("acme/hello")
	reporter, err := github.NewReporter(githubtest.AppID, gh.PrivateKey, gh.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.GitHub = reporter
	if err := f.svc.Versions.SetSource(f.version.ID, models.VersionSource{Repository: "acme/hello", CommitSHA: sourceSHA, PullRequest: 3}); err != nil {
		t.Fatal(err)
	}
	return gh
}

// waitForCheckRun waits until the only check run reached the given history
func waitForCheckRun(t *testing.T, gh *githubtest.Server, history string) githubtest.CheckRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs := gh.CheckRuns()
		if len(runs) == 1 && strings.Join(runs[0].History, ",") == history {
			return runs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("check runs %+v, want one with history %s", runs, history)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGitHubCheckRuns(t *testing.T) {
	t.Run("deployment applied by Flux", func(t *testing.T) {
		f := newFixture(t)
		gh := withGitHub(t, f)
		f.svc.FluxPoller = &fakeFlux{result: flux.PollResult{Ready: true}, commit: make(chan string, 1)}
		f.svc.FluxKustomizationName = "apps"
		f.svc.UIBaseURL = "https://ui.example.com"

		deployment, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh")
		if err != nil {
			t.Fatal(err)
		}

		run := waitForCheckRun(t, gh, "queued,in_progress,completed:success")
		if run.Name != github.DeployCheckName("production") || run.HeadSHA != sourceSHA || run.ExternalID != deployment.ID {
			t.Errorf("unexpected check run %+v", run)
		}
		if run.DetailsURL != "https://ui.example.com/ui/deployments/"+deployment.ID {
			t.Errorf("details URL = %q", run.DetailsURL)
		}
		if !strings.Contains(run.Summary, "`sha-123`") || !strings.Contains(run.Summary, "sorenmh") {
			t.Errorf("summary = %q", run.Summary)
		}
	})

	t.Run("gitops fails", func(t *testing.T) {
		f := newFixture(t)
		gh := withGitHub(t, f)
		f.gitops.err = errors.New("push rejected")

		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "staging", "sorenmh"); err == nil {
			t.Fatal("expected error")
		}
		run := waitForCheckRun(t, gh, "queued,completed:failure")
		if run.Name != "deploysmith/deploy-staging" || !strings.Contains(run.Text, "push rejected") || run.DetailsURL != "" {
			t.Errorf("unexpected check run %+v", run)
		}
	})

	t.Run("auto-deploy", func(t *testing.T) {
		f := newFixture(t)
		gh := withGitHub(t, f)
		if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "main-to-staging", GitBranchPattern: "main", TargetEnvironment: "staging"}); err != nil {
			t.Fatal(err)
		}

		f.svc.TriggerAutoDeploys(context.Background(), f.app, f.version)
		run := waitForCheckRun(t, gh, "queued,completed:success")
		if !strings.Contains(run.Summary, "policy `main-to-staging`") {
			t.Errorf("summary = %q", run.Summary)
		}
	})

	t.Run("a version without a source is not reported", func(t *testing.T) {
		f := newFixture(t)
		gh := withGitHub(t, f)
		other := f.createVersion(t, "v2", "main", true)

		if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, other.VersionID, "production", "sorenmh"); err != nil {
			t.Fatal(err)
		}
		if runs := gh.CheckRuns(); len(runs) != 0 {
			t.Errorf("got check runs %+v", runs)
		}
	})

	t.Run("GitHub failing does not fail the deployment", func(t *testing.T) {
		f := newFixture(t)
		withGitHub(t, f)
		// The app is not installed on this repository
		if err := f.svc.Versions.SetSource(f.version.ID, models.VersionSource{Repository: "acme/other", CommitSHA: sourceSHA}); err != nil {
			t.Fatal(err)
		}

		deployment, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "sorenmh")
		if err != nil || deployment.Status != "success" {
			t.Fatalf("deployment = %+v, %v", deployment, err)
		}
	})
}
