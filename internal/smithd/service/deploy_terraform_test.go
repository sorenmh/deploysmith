package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// packBundle returns the archive of a bundle pinning an OpenTofu version
func packBundle(t *testing.T, tofuVersion string) []byte {
	t.Helper()
	archive, err := tfbundle.Pack(map[string][]byte{
		"main.tf":               []byte("terraform {\n  required_version = \"" + tofuVersion + "\"\n}\n\nresource \"terraform_data\" \"x\" {}\n"),
		tfbundle.LockFile:       []byte("# no providers\n"),
		"env/production.tfvars": []byte("name = \"prod\"\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

type terraformDeployFixture struct {
	*terraformFixture
	viinoID string
}

// newTerraformDeployFixture enables Terraform for viino in production and
// stores a bundle pinning OpenTofu 1.13.2 for every version
func newTerraformDeployFixture(t *testing.T) *terraformDeployFixture {
	t.Helper()
	f := &terraformDeployFixture{terraformFixture: newTerraformFixture(t)}
	f.viinoID = f.appID(t, "viino")
	f.svc.TerraformRoots = terraform.RootOptions{
		SourceURL:       "ssh://git@github.com/sorenmh/gitops.git",
		SourceSecret:    "gitops-readonly",
		SourceNamespace: "flux-system",
		RunnerImages:    map[string]string{"1.13.2": "ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.13.2"},
		OpenBaoAddr:     "https://openbao.openbao.svc:8200",
		TransitMount:    "transit",
	}
	f.storage.files = map[string][]byte{tfbundle.ArchiveName: packBundle(t, "1.13.2")}
	if _, err := f.svc.EnableTerraform(context.Background(), f.viinoID, models.EnableTerraformRequest{Environment: "production"}); err != nil {
		t.Fatal(err)
	}
	return f
}

// version creates a published terraform version of viino
func (f *terraformDeployFixture) version(t *testing.T, versionID string) *models.Version {
	t.Helper()
	version, err := f.svc.Versions.Create(f.viinoID, versionID, models.ArtifactTerraform, models.VersionMetadata{
		GitBranch: "main",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Versions.UpdateStatus(version.ID, "published"); err != nil {
		t.Fatal(err)
	}
	return version
}

func (f *terraformDeployFixture) deploy(versionID string) (*models.Deployment, error) {
	return f.svc.DeployVersion(context.Background(), f.viinoID, versionID, "production", "alice@example.com")
}

func (f *terraformDeployFixture) setStatus(t *testing.T, id, status string) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE deployments SET status = ? WHERE id = ?`, status, id); err != nil {
		t.Fatal(err)
	}
}

func (f *terraformDeployFixture) deployments(t *testing.T) []models.Deployment {
	t.Helper()
	deployments, _, err := f.svc.Deployments.List(f.viinoID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return deployments
}

func requireCode(t *testing.T, err error, kind Kind, code string) {
	t.Helper()
	requireKind(t, err, kind)
	if svcErr, _ := AsError(err); svcErr.Code != code {
		t.Fatalf("error code = %s (%v), want %s", svcErr.Code, err, code)
	}
}

func TestDeployTerraformVersion(t *testing.T) {
	f := newTerraformDeployFixture(t)
	f.version(t, "tf1")

	deployment, err := f.deploy("tf1")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("b", 40)
	wantRun := models.TerraformRun{Root: "main", SourceCommitSHA: source, PlanID: "plan-main-bbbbbbbbbb"}
	if deployment.Status != models.DeploymentPlanning || deployment.Terraform == nil || *deployment.Terraform != wantRun || deployment.GitopsCommitSHA != strings.Repeat("1", 40) {
		t.Fatalf("unexpected deployment: %+v %+v", deployment, deployment.Terraform)
	}

	// Recorded as planning, not completed
	stored, err := f.svc.Deployments.GetByID(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.DeploymentPlanning || stored.CompletedAt != nil || stored.Terraform == nil || *stored.Terraform != wantRun || stored.GitopsCommitSHA != deployment.GitopsCommitSHA {
		t.Errorf("unexpected stored deployment: %+v %+v", stored, stored.Terraform)
	}

	// The bundle and the CR, pinned to the source commit
	deploys := f.tfGitops.Deploys()
	if len(deploys) != 1 {
		t.Fatalf("%d gitops deploys, want 1", len(deploys))
	}
	d := deploys[0]
	if d.deploy.App != "viino" || d.deploy.Environment != "production" || d.deploy.Root != "main" || string(d.deploy.Source["env/production.tfvars"]) != "name = \"prod\"\n" {
		t.Errorf("unexpected deploy: %+v", d.deploy)
	}
	for _, want := range []string{
		"    commit: " + source + "\n",
		"  name: production-main\n  namespace: tf-viino\n",
		"    deploysmith.io/deployment-id: " + deployment.ID + "\n",
		"    deploysmith.io/version: tf1\n",
		"  approvePlan: \"\"\n",
		"    - environments/production/terraform/viino/src/main/env/production.tfvars\n",
		"  serviceAccountName: tf-runner-production\n",
		"        key            = \"viino/production/main/terraform.tfstate\"\n",
		"      image: ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.13.2\n",
		"          value: tf-viino-production\n",
		"              enforced = true\n",
	} {
		if !strings.Contains(d.manifest, want) {
			t.Errorf("manifest does not contain %q:\n%s", want, d.manifest)
		}
	}
}

func TestDeployTerraformReplacesPendingPlan(t *testing.T) {
	for _, status := range []string{models.DeploymentPlanning, models.DeploymentAwaitingApproval} {
		t.Run(status, func(t *testing.T) {
			f := newTerraformDeployFixture(t)
			f.version(t, "tf1")
			f.version(t, "tf2")

			first, err := f.deploy("tf1")
			if err != nil {
				t.Fatal(err)
			}
			f.setStatus(t, first.ID, status)

			second, err := f.deploy("tf2")
			if err != nil {
				t.Fatal(err)
			}
			if second.Status != models.DeploymentPlanning {
				t.Errorf("new deployment is %s", second.Status)
			}

			// The old plan is stale and can't be approved any more
			old, err := f.svc.Deployments.GetByID(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if old.Status != models.DeploymentStale || old.CompletedAt == nil || old.ErrorMessage != "Replaced by deployment "+second.ID {
				t.Errorf("replaced deployment: %+v", old)
			}

			// The CR is rewritten for the new version, unapproved
			deploys := f.tfGitops.Deploys()
			if len(deploys) != 2 {
				t.Fatalf("%d gitops deploys, want 2", len(deploys))
			}
			if m := deploys[1].manifest; !strings.Contains(m, "deploysmith.io/version: tf2") || !strings.Contains(m, "approvePlan: \"\"") || !strings.Contains(m, "commit: "+second.Terraform.SourceCommitSHA) {
				t.Errorf("manifest of the new deploy:\n%s", m)
			}
		})
	}
}

func TestDeployTerraformBusyRoot(t *testing.T) {
	for status, code := range map[string]string{
		models.DeploymentApplying: "terraform_applying",
		models.DeploymentPending:  "terraform_deploying",
	} {
		t.Run(status, func(t *testing.T) {
			f := newTerraformDeployFixture(t)
			f.version(t, "tf1")
			f.version(t, "tf2")

			first, err := f.deploy("tf1")
			if err != nil {
				t.Fatal(err)
			}
			f.setStatus(t, first.ID, status)

			// Refused: the running deployment stays, nothing is committed
			_, err = f.deploy("tf2")
			requireCode(t, err, KindConflict, code)
			if !strings.Contains(err.Error(), first.ID) {
				t.Errorf("error does not name the running deployment: %v", err)
			}
			if deployments := f.deployments(t); len(deployments) != 1 || deployments[0].Status != status {
				t.Errorf("deployments: %+v", deployments)
			}
			if n := len(f.tfGitops.Deploys()); n != 1 {
				t.Errorf("%d gitops deploys, want 1", n)
			}

			// Once it has finished, the next deploy goes ahead
			f.setStatus(t, first.ID, models.DeploymentApplied)
			if _, err := f.deploy("tf2"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeployTerraformFinishedRunsAreKept(t *testing.T) {
	f := newTerraformDeployFixture(t)
	f.version(t, "tf1")

	statuses := []string{models.DeploymentApplied, models.DeploymentFailed, models.DeploymentRejected}
	var ids []string
	for _, status := range statuses {
		deployment, err := f.deploy("tf1")
		if err != nil {
			t.Fatal(err)
		}
		f.setStatus(t, deployment.ID, status)
		ids = append(ids, deployment.ID)
	}
	for i, id := range ids {
		stored, err := f.svc.Deployments.GetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != statuses[i] {
			t.Errorf("deployment %d is %s, want %s", i, stored.Status, statuses[i])
		}
	}
}

func TestDeployTerraformRefused(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(t *testing.T, f *terraformDeployFixture) string // returns the environment
		kind  Kind
		code  string
	}{
		{
			name:  "not enabled in the environment",
			setup: func(t *testing.T, f *terraformDeployFixture) string { return "staging" },
			kind:  KindInvalid,
			code:  "terraform_not_enabled",
		},
		{
			name: "never provisioned",
			setup: func(t *testing.T, f *terraformDeployFixture) string {
				f.bao.err = errors.New("permission denied")
				if _, err := f.svc.EnableTerraform(ctx, f.viinoID, models.EnableTerraformRequest{Environment: "staging"}); err != nil {
					t.Fatal(err)
				}
				return "staging"
			},
			kind: KindInvalid,
			code: "terraform_not_provisioned",
		},
		{
			name: "no runner for the OpenTofu version",
			setup: func(t *testing.T, f *terraformDeployFixture) string {
				f.storage.files = map[string][]byte{tfbundle.ArchiveName: packBundle(t, "1.10.6")}
				return "production"
			},
			kind: KindInvalid,
			code: "unsupported_opentofu_version",
		},
		{
			name: "invalid bundle",
			setup: func(t *testing.T, f *terraformDeployFixture) string {
				archive, err := tfbundle.Pack(map[string][]byte{"main.tf": []byte("resource \"x\" \"y\" {}\n")})
				if err != nil {
					t.Fatal(err)
				}
				f.storage.files = map[string][]byte{tfbundle.ArchiveName: archive}
				return "production"
			},
			kind: KindInvalid,
			code: "validation_failed",
		},
		{
			name: "Terraform support off",
			setup: func(t *testing.T, f *terraformDeployFixture) string {
				f.svc.Terraform = nil
				return "production"
			},
			kind: KindInvalid,
			code: "terraform_disabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTerraformDeployFixture(t)
			f.version(t, "tf1")
			environment := tt.setup(t, f)

			_, err := f.svc.DeployVersion(ctx, f.viinoID, "tf1", environment, "")
			requireCode(t, err, tt.kind, tt.code)
			if deployments := f.deployments(t); len(deployments) != 0 {
				t.Errorf("refused deploy left deployments: %+v", deployments)
			}
			if n := len(f.tfGitops.Deploys()); n != 0 {
				t.Errorf("refused deploy reached gitops %d times", n)
			}
		})
	}
}

func TestDeployTerraformGitopsFailure(t *testing.T) {
	f := newTerraformDeployFixture(t)
	f.version(t, "tf1")
	f.tfGitops.deployErr = errors.New("push rejected")

	deployment, err := f.deploy("tf1")
	if err == nil || deployment == nil || deployment.Status != models.DeploymentFailed {
		t.Fatalf("deploy = %+v, %v; want a failed deployment", deployment, err)
	}
	stored, err := f.svc.Deployments.GetByID(deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.DeploymentFailed || stored.ErrorMessage != "push rejected" {
		t.Errorf("stored deployment: %+v", stored)
	}

	// A failed deploy doesn't block the root
	f.tfGitops.deployErr = nil
	if _, err := f.deploy("tf1"); err != nil {
		t.Fatal(err)
	}
}

func TestDeployTerraformEncryptionFallback(t *testing.T) {
	f := newTerraformDeployFixture(t)
	f.version(t, "tf1")

	on := true
	tfEnv, err := f.svc.UpdateTerraform(f.viinoID, "production", models.UpdateTerraformRequest{EncryptionFallback: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !tfEnv.EncryptionFallback {
		t.Fatalf("fallback not set: %+v", tfEnv)
	}
	if _, err := f.deploy("tf1"); err != nil {
		t.Fatal(err)
	}
	if m := f.tfGitops.Deploys()[0].manifest; !strings.Contains(m, "method = method.unencrypted.migrate") || strings.Contains(m, "enforced = true") {
		t.Errorf("manifest without the fallback:\n%s", m)
	}

	_, err = f.svc.UpdateTerraform(f.viinoID, "staging", models.UpdateTerraformRequest{EncryptionFallback: &on})
	requireKind(t, err, KindNotFound)
}
