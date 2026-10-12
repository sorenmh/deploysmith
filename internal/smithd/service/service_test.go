package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

type fakeStorage struct{ err error }

func (f *fakeStorage) GetAllFiles(appName, versionID string, published bool) (map[string][]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[string][]byte{"deployment.yaml": []byte("name: " + appName)}, nil
}

type gitopsCall struct{ app, env, message string }

type fakeGitops struct {
	mu    sync.Mutex
	calls []gitopsCall
	err   error
}

func (f *fakeGitops) Deploy(appName, environment, message string, manifests map[string][]byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, gitopsCall{appName, environment, message})
	if f.err != nil {
		return "", f.err
	}
	return "sha-123", nil
}

func (f *fakeGitops) Calls() []gitopsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitopsCall(nil), f.calls...)
}

type fixture struct {
	db      *db.DB
	svc     *Service
	gitops  *fakeGitops
	storage *fakeStorage
	app     *models.Application
	version *models.Version
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	f := &fixture{db: database, gitops: &fakeGitops{}, storage: &fakeStorage{}}
	f.svc = New(Deps{
		Apps:         store.NewApplicationStore(database),
		Versions:     store.NewVersionStore(database),
		Deployments:  store.NewDeploymentStore(database),
		Policies:     store.NewPolicyStore(database),
		Environments: store.NewEnvironmentStore(database),
		Storage:      f.storage,
		Gitops:       f.gitops,
		Publisher:    events.NewMemoryPublisher(),
	})

	for _, env := range []string{"staging", "production"} {
		if _, err := f.svc.Environments.Create(models.CreateEnvironmentRequest{Name: env}); err != nil {
			t.Fatal(err)
		}
	}
	if f.app, err = f.svc.Apps.Create("hello"); err != nil {
		t.Fatal(err)
	}
	f.version = f.createVersion(t, "v1", "main", true)
	return f
}

func (f *fixture) createVersion(t *testing.T, versionID, branch string, publish bool) *models.Version {
	t.Helper()
	version, err := f.svc.Versions.Create(f.app.ID, versionID, models.VersionMetadata{
		GitBranch: branch,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		if err := f.svc.Versions.UpdateStatus(version.ID, "published"); err != nil {
			t.Fatal(err)
		}
		version.Status = "published"
	}
	return version
}

func requireKind(t *testing.T, err error, kind Kind) {
	t.Helper()
	svcErr, ok := AsError(err)
	if !ok || svcErr.Kind != kind {
		t.Fatalf("error = %v, want service error of kind %d", err, kind)
	}
}

func TestDeployVersion(t *testing.T) {
	f := newFixture(t)

	deployment, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Status != "success" || deployment.GitopsCommitSHA != "sha-123" {
		t.Errorf("unexpected deployment: %+v", deployment)
	}

	stored, err := f.svc.Deployments.GetDetailed(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "success" || stored.TriggeredBy != "alice@example.com" || stored.GitopsCommitSHA != "sha-123" {
		t.Errorf("unexpected stored deployment: %+v", stored)
	}

	calls := f.gitops.Calls()
	if len(calls) != 1 || calls[0].app != "hello" || calls[0].env != "production" || calls[0].message != "Deploy hello version v1 to production" {
		t.Errorf("unexpected gitops calls: %+v", calls)
	}
}

func TestDeployVersionRecordsFailure(t *testing.T) {
	f := newFixture(t)
	f.gitops.err = errors.New("push rejected")

	deployment, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := AsError(err); ok {
		t.Errorf("gitops failure should be an internal error, got %v", err)
	}

	stored, err := f.svc.Deployments.GetDetailed(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" || stored.ErrorMessage != "push rejected" {
		t.Errorf("unexpected stored deployment: %+v", stored)
	}
}

func TestDeployVersionValidation(t *testing.T) {
	f := newFixture(t)
	f.createVersion(t, "draft", "main", false)

	tests := []struct {
		name        string
		appID       string
		versionID   string
		environment string
		kind        Kind
	}{
		{"missing environment", f.app.ID, "v1", "", KindInvalid},
		{"unknown environment", f.app.ID, "v1", "nope", KindInvalid},
		{"unknown app", "nope", "v1", "production", KindNotFound},
		{"unknown version", f.app.ID, "nope", "production", KindNotFound},
		{"unpublished version", f.app.ID, "draft", "production", KindInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.DeployVersion(context.Background(), tt.appID, tt.versionID, tt.environment, "")
			requireKind(t, err, tt.kind)
		})
	}

	if calls := f.gitops.Calls(); len(calls) != 0 {
		t.Errorf("invalid deploys reached gitops: %+v", calls)
	}
}

func TestTriggerAutoDeploys(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "main-to-prod", GitBranchPattern: "main", TargetEnvironment: "production"}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "main-to-staging", GitBranchPattern: "main", TargetEnvironment: "staging", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "release", GitBranchPattern: "release/*", TargetEnvironment: "staging"}); err != nil {
		t.Fatal(err)
	}

	f.svc.TriggerAutoDeploys(context.Background(), f.app, f.version)

	// Auto-deploys run in the background
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _, err := f.svc.Deployments.ListDetailed(store.DeploymentFilter{Status: "success"}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) == 1 {
			if list[0].Environment != "production" || list[0].PolicyName != "main-to-prod" || list[0].TriggeredBy != "auto-deploy" {
				t.Errorf("unexpected deployment: %+v", list[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-deploy did not complete, got %d successful deployments", len(list))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Only the enabled policy matching "main" deployed
	time.Sleep(50 * time.Millisecond)
	if calls := f.gitops.Calls(); len(calls) != 1 || calls[0].message != "Auto-deploy hello version v1 to production (policy: main-to-prod)" {
		t.Errorf("unexpected gitops calls: %+v", calls)
	}
}

func TestCreatePolicyValidation(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "p", GitBranchPattern: "main", TargetEnvironment: "production"}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		appID string
		req   models.CreatePolicyRequest
		kind  Kind
	}{
		{"unknown app", "nope", models.CreatePolicyRequest{Name: "x", GitBranchPattern: "main", TargetEnvironment: "production"}, KindNotFound},
		{"missing name", f.app.ID, models.CreatePolicyRequest{GitBranchPattern: "main", TargetEnvironment: "production"}, KindInvalid},
		{"missing pattern", f.app.ID, models.CreatePolicyRequest{Name: "x", TargetEnvironment: "production"}, KindInvalid},
		{"invalid pattern", f.app.ID, models.CreatePolicyRequest{Name: "x", GitBranchPattern: "[", TargetEnvironment: "production"}, KindInvalid},
		{"unknown environment", f.app.ID, models.CreatePolicyRequest{Name: "x", GitBranchPattern: "main", TargetEnvironment: "nope"}, KindInvalid},
		{"duplicate name", f.app.ID, models.CreatePolicyRequest{Name: "p", GitBranchPattern: "main", TargetEnvironment: "production"}, KindConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.CreatePolicy(tt.appID, tt.req)
			requireKind(t, err, tt.kind)
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	f := newFixture(t)
	other, err := f.svc.Apps.Create("other")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "p", GitBranchPattern: "main", TargetEnvironment: "production"})
	if err != nil {
		t.Fatal(err)
	}

	requireKind(t, f.svc.DeletePolicy(other.ID, policy.ID), KindNotFound)
	if err := f.svc.DeletePolicy(f.app.ID, policy.ID); err != nil {
		t.Fatal(err)
	}
	requireKind(t, f.svc.DeletePolicy(f.app.ID, policy.ID), KindNotFound)
}
