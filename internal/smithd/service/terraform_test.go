package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/openbao"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

type terraformWrite struct {
	app, env string
	files    map[string][]byte
}

type fakeTerraformGitops struct {
	mu     sync.Mutex
	writes []terraformWrite
	err    error
}

func (f *fakeTerraformGitops) WriteTerraformFiles(appName, environment, message string, files map[string][]byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.writes = append(f.writes, terraformWrite{appName, environment, files})
	return "sha-tf", nil
}

// fakeOpenBao counts what is written; err fails every write
type fakeOpenBao struct {
	mu     sync.Mutex
	writes []string
	err    error
}

func (f *fakeOpenBao) record(what string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.writes = append(f.writes, what)
	return nil
}

func (f *fakeOpenBao) WritePolicy(_ context.Context, name, _ string) error {
	return f.record("policy " + name)
}

func (f *fakeOpenBao) WriteKubernetesRole(_ context.Context, _, name string, _ openbao.KubernetesRole) error {
	return f.record("k8s-role " + name)
}

func (f *fakeOpenBao) WriteAWSRole(_ context.Context, _, name string, _ openbao.AWSRole) error {
	return f.record("aws-role " + name)
}

func (f *fakeOpenBao) EnsureTransitKey(_ context.Context, _, name string) (bool, error) {
	return true, f.record("transit-key " + name)
}

type terraformFixture struct {
	*fixture
	tfGitops *fakeTerraformGitops
	bao      *fakeOpenBao
}

func newTerraformFixture(t *testing.T) *terraformFixture {
	t.Helper()
	f := &terraformFixture{fixture: newFixture(t), tfGitops: &fakeTerraformGitops{}, bao: &fakeOpenBao{}}
	if _, err := f.svc.Apps.Create("viino"); err != nil {
		t.Fatal(err)
	}
	f.svc.TerraformEnvironments = store.NewTerraformStore(f.db)
	f.svc.TerraformGitops = f.tfGitops
	f.svc.Terraform = &terraform.Provisioner{Bao: f.bao, Opts: terraform.OpenBaoOptions{
		KVMount: "secret", TransitMount: "transit", AWSMount: "aws", KubernetesMount: "kubernetes",
		RunnerTokenTTL: 2 * time.Hour, STSTTL: time.Hour, AWSAccountID: "123456789012",
	}}
	f.svc.TerraformManifests = terraform.ManifestOptions{SmithdNamespace: "deploysmith", SmithdServiceAccount: "smithd", ControllerNamespace: "flux-system", OpenBaoNamespace: "openbao", OpenBaoPort: 8200}
	f.svc.TerraformSharedBackend = models.TerraformBackend{Bucket: "deploysmith-tfstate", Region: "eu-north-1", LockTable: "deploysmith-tflock"}
	return f
}

func (f *terraformFixture) appID(t *testing.T, name string) string {
	t.Helper()
	app, err := f.svc.Apps.GetByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func TestEnableTerraform(t *testing.T) {
	f := newTerraformFixture(t)
	appID := f.appID(t, "viino")

	tfEnv, err := f.svc.EnableTerraform(context.Background(), appID, models.EnableTerraformRequest{Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	want := models.TerraformBackend{Bucket: "deploysmith-tfstate", Key: "viino/production/main/terraform.tfstate", Region: "eu-north-1", LockTable: "deploysmith-tflock"}
	if tfEnv.Backend != want || tfEnv.AppName != "viino" || tfEnv.Environment != "production" {
		t.Errorf("unexpected environment: %+v", tfEnv)
	}
	if tfEnv.ProvisionedAt == nil || tfEnv.ProvisionError != nil {
		t.Errorf("not provisioned: %+v", tfEnv)
	}

	if len(f.tfGitops.writes) != 1 || f.tfGitops.writes[0].app != "viino" || f.tfGitops.writes[0].env != "production" {
		t.Fatalf("unexpected gitops writes: %+v", f.tfGitops.writes)
	}
	if ns := string(f.tfGitops.writes[0].files["namespace.yaml"]); !strings.Contains(ns, "name: tf-viino") || !strings.Contains(ns, "name: tf-runner-production") {
		t.Errorf("unexpected namespace.yaml:\n%s", ns)
	}
	wantWrites := "transit-key tf-viino, policy tf-viino-production, k8s-role tf-viino-production, aws-role viino-production, " +
		"policy tf-viino-production-readonly, k8s-role tf-viino-production-readonly, aws-role viino-production-readonly"
	if got := strings.Join(f.bao.writes, ", "); got != wantWrites {
		t.Errorf("openbao writes = %s\nwant %s", got, wantWrites)
	}

	_, err = f.svc.EnableTerraform(context.Background(), appID, models.EnableTerraformRequest{Environment: "production"})
	requireKind(t, err, KindConflict)

	// The environment can no longer be deleted
	env, err := f.svc.Environments.GetByName("production")
	if err != nil {
		t.Fatal(err)
	}
	requireKind(t, f.svc.DeleteEnvironment(env.ID), KindConflict)
}

func TestEnableTerraformExistingBackend(t *testing.T) {
	f := newTerraformFixture(t)

	tfEnv, err := f.svc.EnableTerraform(context.Background(), f.appID(t, "viino"), models.EnableTerraformRequest{
		Environment: "staging",
		Backend:     models.TerraformBackend{Bucket: "viino-state", Key: "staging/terraform.tfstate", Region: "eu-central-1"},
		AWSRoleARN:  "arn:aws:iam::999999999999:role/tf-viino-staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	// An existing backend keeps its own settings, without the shared lock table
	want := models.TerraformBackend{Bucket: "viino-state", Key: "staging/terraform.tfstate", Region: "eu-central-1"}
	if tfEnv.Backend != want || tfEnv.AWSRoleARN != "arn:aws:iam::999999999999:role/tf-viino-staging" {
		t.Errorf("unexpected environment: %+v", tfEnv)
	}
}

func TestEnableTerraformInvalid(t *testing.T) {
	f := newTerraformFixture(t)
	ctx := context.Background()
	appID := f.appID(t, "viino")

	_, err := f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "nope"})
	requireKind(t, err, KindInvalid)
	_, err = f.svc.EnableTerraform(ctx, "no-such-app", models.EnableTerraformRequest{Environment: "production"})
	requireKind(t, err, KindNotFound)
	_, err = f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "production", Backend: models.TerraformBackend{Bucket: "b"}})
	requireKind(t, err, KindInvalid) // no region

	// Names that are not DNS labels
	app, err := f.svc.Apps.Create("Viino_API")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.EnableTerraform(ctx, app.ID, models.EnableTerraformRequest{Environment: "production"})
	requireKind(t, err, KindInvalid)

	// No shared backend, no AWS account
	f.svc.TerraformSharedBackend = models.TerraformBackend{}
	_, err = f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "production"})
	requireKind(t, err, KindInvalid)
	f.svc.Terraform.Opts.AWSAccountID = ""
	_, err = f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "production", Backend: models.TerraformBackend{Bucket: "b", Region: "r"}})
	requireKind(t, err, KindInvalid)

	if envs, _ := f.svc.ListTerraformEnvironments(appID); len(envs) != 0 {
		t.Errorf("invalid requests enabled Terraform: %+v", envs)
	}
}

func TestTerraformDisabledWithoutOpenBao(t *testing.T) {
	f := newTerraformFixture(t)
	f.svc.Terraform = nil

	_, err := f.svc.EnableTerraform(context.Background(), f.appID(t, "viino"), models.EnableTerraformRequest{Environment: "production"})
	requireKind(t, err, KindInvalid)
	if svcErr, _ := AsError(err); svcErr.Code != "terraform_disabled" {
		t.Errorf("code = %s", svcErr.Code)
	}
	_, err = f.svc.SyncTerraform(context.Background(), f.appID(t, "viino"), "")
	requireKind(t, err, KindInvalid)

	// Does nothing
	f.svc.SyncAllTerraform(context.Background())
}

func TestSyncTerraform(t *testing.T) {
	f := newTerraformFixture(t)
	ctx := context.Background()
	appID := f.appID(t, "viino")

	// Provisioning fails: Terraform is still enabled, with the error recorded
	f.bao.err = errors.New("permission denied")
	f.tfGitops.err = errors.New("push rejected")
	tfEnv, err := f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if tfEnv.ProvisionedAt != nil || tfEnv.ProvisionError == nil ||
		!strings.Contains(*tfEnv.ProvisionError, "gitops: push rejected") || !strings.Contains(*tfEnv.ProvisionError, "openbao: transit key tf-viino: permission denied") {
		t.Fatalf("unexpected environment: %+v", tfEnv)
	}
	if _, err := f.svc.EnableTerraform(ctx, appID, models.EnableTerraformRequest{Environment: "staging"}); err != nil {
		t.Fatal(err)
	}

	// Sync once it works
	f.bao.err, f.tfGitops.err = nil, nil
	envs, err := f.svc.SyncTerraform(ctx, appID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].ProvisionedAt == nil || envs[0].ProvisionError != nil {
		t.Errorf("unexpected environments after sync: %+v", envs)
	}

	envs, err = f.svc.SyncTerraform(ctx, appID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].Environment != "production" || envs[1].Environment != "staging" || envs[1].ProvisionError != nil {
		t.Errorf("unexpected environments after syncing all: %+v", envs)
	}

	_, err = f.svc.SyncTerraform(ctx, f.app.ID, "production")
	requireKind(t, err, KindNotFound)

	// On start every environment of every app is synced
	f.tfGitops.writes = nil
	f.svc.SyncAllTerraform(ctx)
	if len(f.tfGitops.writes) != 2 {
		t.Errorf("SyncAllTerraform wrote %d environments, want 2", len(f.tfGitops.writes))
	}
}
