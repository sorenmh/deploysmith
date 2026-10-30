package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// fakeBundleDeleter records the bundles it deletes
type fakeBundleDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (f *fakeBundleDeleter) DeleteVersion(appName, versionID string, published bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, appName+"/"+versionID)
	return nil
}

func (f *fakeBundleDeleter) Deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func userCtx(login string) context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindUser, Login: login})
}

func apiKeyCtx() context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{Kind: auth.KindAPIKey})
}

type localPlanFixture struct {
	*planFixture
	deleter *fakeBundleDeleter
	hash    string // the content hash of the bundle the storage serves
}

func newLocalPlanFixture(t *testing.T) *localPlanFixture {
	t.Helper()
	f := &localPlanFixture{planFixture: newPlanFixture(t), deleter: &fakeBundleDeleter{}}
	f.svc.BundleDeleter = f.deleter
	f.svc.TerraformLocalPlanTTL = 2 * time.Hour

	files, err := tfbundle.Read(bytes.NewReader(f.storage.files[tfbundle.ArchiveName]))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := tfbundle.Validate(files)
	if err != nil {
		t.Fatal(err)
	}
	f.hash = tfbundle.ContentHash(bundle.Files)
	return f
}

// prepare makes the temporary version of the served bundle for a user
func (f *localPlanFixture) prepare(t *testing.T, login string) *models.Version {
	t.Helper()
	_, version, err := f.svc.PrepareLocalPlan(userCtx(login), f.viinoID, models.LocalPlanRequest{Environment: "production", ContentHash: f.hash})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func (f *localPlanFixture) planAs(ctx context.Context, versionID string) (*models.PlanRun, error) {
	return f.svc.PlanVersion(ctx, f.viinoID, versionID, "production", "someone")
}

func TestPrepareLocalPlan(t *testing.T) {
	f := newLocalPlanFixture(t)

	app, version, err := f.svc.PrepareLocalPlan(userCtx("Alice"), f.viinoID, models.LocalPlanRequest{Environment: "production", ContentHash: f.hash})
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "viino" || version.VersionID != "local-alice-"+f.hash[:12] || !version.Temporary || version.CreatedBy != "Alice" || version.Status != "draft" || version.ArtifactType != models.ArtifactTerraform {
		t.Fatalf("unexpected version: %+v", version)
	}
	if version.ExpiresAt == nil || time.Until(*version.ExpiresAt) > 2*time.Hour || time.Until(*version.ExpiresAt) < time.Hour+50*time.Minute {
		t.Errorf("expires at %v, want in 2 hours", version.ExpiresAt)
	}

	// The same files give the same version, and only move its expiry
	if _, err := f.db.Exec(`UPDATE versions SET expires_at = ? WHERE id = ?`, time.Now().Add(time.Minute).UTC(), version.ID); err != nil {
		t.Fatal(err)
	}
	_, again, err := f.svc.PrepareLocalPlan(userCtx("Alice"), f.viinoID, models.LocalPlanRequest{Environment: "production", ContentHash: f.hash})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != version.ID || time.Until(*again.ExpiresAt) < time.Hour {
		t.Errorf("planning the same files again: %+v, want %s with its expiry moved", again, version.ID)
	}
	stored, err := f.svc.Versions.GetByID(version.ID)
	if err != nil || time.Until(*stored.ExpiresAt) < time.Hour {
		t.Errorf("stored version: %+v, %v", stored, err)
	}

	// Another user's version of the same files is another version
	other := f.prepare(t, "bob")
	if other.ID == version.ID || other.VersionID != "local-bob-"+f.hash[:12] || other.CreatedBy != "bob" {
		t.Errorf("bob's version: %+v", other)
	}
}

func TestPrepareLocalPlanRefusals(t *testing.T) {
	f := newLocalPlanFixture(t)
	request := models.LocalPlanRequest{Environment: "production", ContentHash: f.hash}

	// API keys and unauthenticated callers are refused: a local plan is
	// a person's
	for name, ctx := range map[string]context.Context{"api key": apiKeyCtx(), "nobody": context.Background()} {
		_, _, err := f.svc.PrepareLocalPlan(ctx, f.viinoID, request)
		requireCode(t, err, KindForbidden, "login_required")
		if !strings.Contains(err.Error(), "smithctl login") {
			t.Errorf("%s: error does not say how to log in: %v", name, err)
		}
	}
	if versions, _, _ := f.svc.Versions.List(f.viinoID, 10, 0, true); len(versions) != 0 {
		t.Errorf("a refused local plan left versions: %+v", versions)
	}

	// Terraform must be on for the app in the environment
	_, _, err := f.svc.PrepareLocalPlan(userCtx("alice"), f.viinoID, models.LocalPlanRequest{Environment: "staging", ContentHash: f.hash})
	requireCode(t, err, KindInvalid, "terraform_not_enabled")

	_, _, err = f.svc.PrepareLocalPlan(userCtx("alice"), f.viinoID, models.LocalPlanRequest{Environment: "nowhere", ContentHash: f.hash})
	requireKind(t, err, KindInvalid)

	_, _, err = f.svc.PrepareLocalPlan(userCtx("alice"), f.viinoID, models.LocalPlanRequest{Environment: "production", ContentHash: "../x"})
	requireCode(t, err, KindInvalid, "invalid_request")

	// A version of that name that is not a local plan is in the way
	if _, err := f.svc.Versions.Create(f.viinoID, "local-alice-"+f.hash[:12], models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.PrepareLocalPlan(userCtx("alice"), f.viinoID, request)
	requireKind(t, err, KindConflict)

	// Without plan-only runs there are no local plans
	f.svc.PlanRuns = nil
	_, _, err = f.svc.PrepareLocalPlan(userCtx("alice"), f.viinoID, request)
	requireCode(t, err, KindInvalid, "terraform_plans_disabled")
}

func TestPlanLocalVersion(t *testing.T) {
	f := newLocalPlanFixture(t)
	version := f.prepare(t, "alice")

	run, err := f.svc.PlanVersion(userCtx("alice"), f.viinoID, version.VersionID, "production", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != models.PlanRunPlanning || run.VersionID != version.VersionID || run.TriggeredBy != "alice" || run.PlanID == "" {
		t.Fatalf("unexpected run: %+v", run)
	}
	if deploys := f.plans.Deploys(); len(deploys) != 1 || deploys[0].deploy.Source["env/production.tfvars"] == nil {
		t.Errorf("deploys to the plans branch: %+v", deploys)
	}
	// Nothing to report on: no commit or pull request, and no Slack either
	if checks := f.gh.CheckRuns(); len(checks) != 0 {
		t.Errorf("check runs for a local plan: %+v", checks)
	}
	f.slack.waitFor(t, 0)

	// The plan is stored like any other
	f.withChanges(run, plannedAt(1), "hash-1", f.planText)
	f.pollPlans()
	done := f.requireRun(t, run.ID, models.PlanRunPlanned)
	if done.Plan == nil || done.Plan.ToDestroy == 0 {
		t.Errorf("plan: %+v", done.Plan)
	}
	if checks := f.gh.CheckRuns(); len(checks) != 0 {
		t.Errorf("check runs for a finished local plan: %+v", checks)
	}
	f.slack.waitFor(t, 0)
}

func TestPlanLocalVersionRefusals(t *testing.T) {
	f := newLocalPlanFixture(t)
	version := f.prepare(t, "alice")

	// The point of local plans is knowing who ran them, so an API key
	// can't, and neither can someone else
	_, err := f.planAs(apiKeyCtx(), version.VersionID)
	requireCode(t, err, KindForbidden, "login_required")
	_, err = f.planAs(context.Background(), version.VersionID)
	requireCode(t, err, KindForbidden, "login_required")
	_, err = f.planAs(userCtx("bob"), version.VersionID)
	requireCode(t, err, KindForbidden, "not_your_local_plan")
	if deploys := f.plans.Deploys(); len(deploys) != 0 {
		t.Errorf("%d deploys to the plans branch by refused plans", len(deploys))
	}

	// The bundle must be the one the version is named after
	wrong, err := f.svc.Versions.CreateTemporary(f.viinoID, "local-alice-ffffffffffff", "alice", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.planAs(userCtx("alice"), wrong.VersionID)
	requireCode(t, err, KindInvalid, "bundle_mismatch")

	// An expired one has to be made again
	if _, err := f.db.Exec(`UPDATE versions SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).UTC(), version.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.planAs(userCtx("alice"), version.VersionID)
	requireCode(t, err, KindInvalid, "local_plan_expired")

	// Terraform must be on in the environment
	other := f.prepare(t, "carol")
	_, err = f.svc.PlanVersion(userCtx("carol"), f.viinoID, other.VersionID, "staging", "carol")
	requireCode(t, err, KindInvalid, "terraform_not_enabled")
}

func TestTemporaryVersionsCantBeDeployed(t *testing.T) {
	f := newLocalPlanFixture(t)
	version := f.prepare(t, "alice")

	_, err := f.deploy(version.VersionID)
	requireCode(t, err, KindInvalid, "temporary_version")

	// Not even if it were published
	if err := f.svc.Versions.UpdateStatus(version.ID, "published"); err != nil {
		t.Fatal(err)
	}
	_, err = f.deploy(version.VersionID)
	requireCode(t, err, KindInvalid, "temporary_version")

	// Nor by a policy
	published, err := f.svc.Versions.GetByID(version.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.svc.Apps.GetByID(f.viinoID)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.TriggerAutoDeploys(context.Background(), app, published)

	if deployments := f.deployments(t); len(deployments) != 0 {
		t.Errorf("deployments of a temporary version: %+v", deployments)
	}
}

func TestLocalPlansDontInterfere(t *testing.T) {
	f := newLocalPlanFixture(t)

	// CI's plan of a published version, and a real deployment, are under way
	f.version(t, "tf1")
	ci, err := f.planAs(apiKeyCtx(), "tf1")
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.deploy("tf1")
	if err != nil {
		t.Fatal(err)
	}

	alice, bob := f.prepare(t, "alice"), f.prepare(t, "bob")
	first, err := f.planAs(userCtx("alice"), alice.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	bobRun, err := f.planAs(userCtx("bob"), bob.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	f.requireRun(t, ci.ID, models.PlanRunPlanning)
	f.requireRun(t, first.ID, models.PlanRunPlanning)
	f.requireRun(t, bobRun.ID, models.PlanRunPlanning)

	// A newer local plan of alice replaces her earlier one, and only that
	second, err := f.planAs(userCtx("alice"), alice.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.requireRun(t, first.ID, models.PlanRunSuperseded); !strings.Contains(got.ErrorMessage, second.ID) {
		t.Errorf("superseded by: %q", got.ErrorMessage)
	}
	f.requireRun(t, second.ID, models.PlanRunPlanning)
	f.requireRun(t, bobRun.ID, models.PlanRunPlanning)
	f.requireRun(t, ci.ID, models.PlanRunPlanning)

	// And CI's newer plan replaces CI's, not the local ones
	newerCI, err := f.planAs(apiKeyCtx(), "tf1")
	if err != nil {
		t.Fatal(err)
	}
	f.requireRun(t, ci.ID, models.PlanRunSuperseded)
	f.requireRun(t, newerCI.ID, models.PlanRunPlanning)
	f.requireRun(t, second.ID, models.PlanRunPlanning)
	f.requireRun(t, bobRun.ID, models.PlanRunPlanning)

	// The real deployment is untouched
	if got, err := f.svc.Deployments.GetByID(deployment.ID); err != nil || got.Status != models.DeploymentPlanning {
		t.Errorf("deployment: %+v, %v", got, err)
	}

	// The temporary versions are not in the version list, unless asked for
	versions, total, err := f.svc.Versions.List(f.viinoID, 10, 0, false)
	if err != nil || total != 1 || len(versions) != 1 || versions[0].VersionID != "tf1" {
		t.Errorf("versions = %+v, %d, %v; want only tf1", versions, total, err)
	}
}

func TestSweepExpiredTemporaryVersions(t *testing.T) {
	f := newLocalPlanFixture(t)
	f.version(t, "tf1")

	expired, fresh, running := f.prepare(t, "alice"), f.prepare(t, "bob"), f.prepare(t, "carol")
	done, err := f.planAs(userCtx("alice"), expired.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.planAs(userCtx("carol"), running.VersionID); err != nil {
		t.Fatal(err)
	}
	f.withChanges(done, plannedAt(1), "hash-1", f.planText)
	f.pollPlans()
	f.requireRun(t, done.ID, models.PlanRunPlanned)

	past := time.Now().Add(-time.Minute).UTC()
	for _, id := range []string{expired.ID, running.ID} {
		if _, err := f.db.Exec(`UPDATE versions SET expires_at = ? WHERE id = ?`, past, id); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary version never expires
	if _, err := f.db.Exec(`UPDATE versions SET created_at = ? WHERE version_id = 'tf1'`, past.Add(-1000*time.Hour)); err != nil {
		t.Fatal(err)
	}

	f.svc.planSweepAt = time.Time{}
	f.pollPlans()

	// The expired version is gone with its bundle and its plan; the one
	// whose plan is still running waits until it is done
	if got := f.deleter.Deleted(); len(got) != 1 || got[0] != "viino/"+expired.VersionID {
		t.Errorf("deleted bundles = %v, want only %s", got, expired.VersionID)
	}
	if _, err := f.svc.Versions.GetByID(expired.ID); err == nil {
		t.Error("the expired version is still there")
	}
	if run, err := f.svc.PlanRuns.Get(done.ID); err != nil || run != nil {
		t.Errorf("the plan of the expired version = %+v, %v; want it gone", run, err)
	}
	for _, id := range []string{fresh.ID, running.ID} {
		if _, err := f.svc.Versions.GetByID(id); err != nil {
			t.Errorf("version %s was deleted: %v", id, err)
		}
	}
	if _, err := f.svc.Versions.GetByVersionID(f.viinoID, "tf1"); err != nil {
		t.Errorf("an ordinary version was deleted: %v", err)
	}

	// Once its plan is done the expired version goes too
	if _, err := f.svc.PlanRuns.Finish(mustActiveRun(t, f, running.VersionID), models.PlanRunPlanning, models.PlanRunFailed, "boom", nil); err != nil {
		t.Fatal(err)
	}
	f.svc.planSweepAt = time.Time{}
	f.pollPlans()
	if _, err := f.svc.Versions.GetByID(running.ID); err == nil {
		t.Error("the version whose plan ended is still there")
	}
}

func mustActiveRun(t *testing.T, f *localPlanFixture, versionID string) string {
	t.Helper()
	active, err := f.svc.PlanRuns.ListActive()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range active {
		if run.VersionID == versionID {
			return run.ID
		}
	}
	t.Fatalf("no active plan run of %s", versionID)
	return ""
}

func TestSweepKeepsTemporaryVersionsWhenTheBundleCantBeDeleted(t *testing.T) {
	f := newLocalPlanFixture(t)
	version := f.prepare(t, "alice")
	if _, err := f.db.Exec(`UPDATE versions SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).UTC(), version.ID); err != nil {
		t.Fatal(err)
	}

	// Repeated by the next sweep, so the bundle is never left without a record
	f.deleter.err = errors.New("s3 is down")
	f.svc.sweepTemporaryVersions(context.Background())
	if _, err := f.svc.Versions.GetByID(version.ID); err != nil {
		t.Fatalf("the version was deleted although its bundle was not: %v", err)
	}
	f.deleter.err = nil
	f.svc.sweepTemporaryVersions(context.Background())
	if _, err := f.svc.Versions.GetByID(version.ID); err == nil {
		t.Error("the version was kept")
	}
	if got := f.deleter.Deleted(); len(got) != 1 {
		t.Errorf("deleted bundles = %v", got)
	}
}
