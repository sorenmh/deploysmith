package terraform

import (
	"strings"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

var testRootOptions = RootOptions{
	SourceURL:       "ssh://git@github.com/sorenmh/gitops.git",
	SourceSecret:    "gitops-readonly",
	SourceNamespace: "flux-system",
	RunnerImages: map[string]string{
		"1.12.9": "ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.12.9",
		"1.13.2": "ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.13.2",
	},
	OpenBaoAddr:  "https://openbao.openbao.svc:8200",
	AuthMount:    "kubernetes",
	AWSMount:     "aws",
	TransitMount: "transit",
}

func testRoot() Root {
	return Root{
		App:          "viino",
		Environment:  "dev",
		Name:         "main",
		VersionID:    "42540c4-123",
		DeploymentID: "dep-789",
		Branch:       "main",
		SourceCommit: "8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7",
		SourcePath:   "environments/dev/terraform/viino/src/main",
		TFVarsFile:   "environments/dev/terraform/viino/src/main/env/dev.tfvars",
		RunnerImage:  "ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.13.2",
		Backend: models.TerraformBackend{
			Bucket:    "deploysmith-tfstate",
			Key:       "viino/dev/main/terraform.tfstate",
			Region:    "eu-north-1",
			LockTable: "deploysmith-tflock",
		},
	}
}

const wantRootManifest = `# Terraform root main of viino in dev, version 42540c4-123, managed by smithd
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: tf-viino-dev-main
  namespace: flux-system
  labels:
    deploysmith.io/app: viino
    deploysmith.io/environment: dev
    deploysmith.io/root: main
spec:
  interval: 10m
  url: ssh://git@github.com/sorenmh/gitops.git
  secretRef:
    name: gitops-readonly
  ref:
    branch: main
    commit: 8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7
  ignore: |
    /*
    !/environments/dev/terraform/viino/src/main/
---
apiVersion: infra.contrib.fluxcd.io/v1alpha2
kind: Terraform
metadata:
  name: dev-main
  namespace: tf-viino
  labels:
    deploysmith.io/app: viino
    deploysmith.io/environment: dev
    deploysmith.io/root: main
  annotations:
    deploysmith.io/deployment-id: dep-789
    deploysmith.io/version: 42540c4-123
spec:
  interval: 6h
  retryInterval: 5m
  remediation:
    retries: 1
  sourceRef:
    kind: GitRepository
    name: tf-viino-dev-main
    namespace: flux-system
  path: ./environments/dev/terraform/viino/src/main
  tfVarsFiles:
    - environments/dev/terraform/viino/src/main/env/dev.tfvars
  approvePlan: ""
  storeReadablePlan: human
  serviceAccountName: tf-runner-dev
  disableDriftDetection: true
  destroyResourcesOnDeletion: false
  alwaysCleanupRunnerPod: true
  backendConfig:
    customConfiguration: |
      backend "s3" {
        bucket         = "deploysmith-tfstate"
        key            = "viino/dev/main/terraform.tfstate"
        region         = "eu-north-1"
        dynamodb_table = "deploysmith-tflock"
        encrypt        = true
      }
  runnerPodTemplate:
    spec:
      image: ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.13.2
      env:
        - name: BAO_ADDR
          value: https://openbao.openbao.svc:8200
        - name: SMITH_BAO_AUTH_ROLE
          value: tf-viino-dev
        - name: SMITH_BAO_AUTH_MOUNT
          value: kubernetes
        - name: SMITH_BAO_AWS_ROLE
          value: viino-dev
        - name: SMITH_BAO_AWS_MOUNT
          value: aws
        - name: TF_ENCRYPTION
          value: |
            key_provider "openbao" "app" {
              key_name            = "tf-viino"
              transit_engine_path = "/transit"
            }
            method "aes_gcm" "app" {
              keys = key_provider.openbao.app
            }
            state {
              method   = method.aes_gcm.app
              enforced = true
            }
            plan {
              method   = method.aes_gcm.app
              enforced = true
            }
      resources:
        requests:
          cpu: 100m
          memory: 256Mi
        limits:
          memory: 1Gi
`

func TestRootManifest(t *testing.T) {
	got, err := RootManifest(testRoot(), testRootOptions)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantRootManifest {
		t.Errorf("RootManifest =\n%s\nwant\n%s", got, wantRootManifest)
	}
}

// The variants: an existing app's own backend without a lock table, the
// migration fallback, no tfvars file, a public source and a token audience
func TestRootManifestVariants(t *testing.T) {
	root := testRoot()
	root.Backend = models.TerraformBackend{Bucket: "legacy-tfstate", Key: "prod/terraform.tfstate", Region: "eu-central-1"}
	root.EncryptionFallback = true
	root.TFVarsFile = ""
	opts := testRootOptions
	opts.SourceSecret = ""
	opts.Audience = "openbao"

	got, err := RootManifest(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`      backend "s3" {
        bucket         = "legacy-tfstate"
        key            = "prod/terraform.tfstate"
        region         = "eu-central-1"
        encrypt        = true
      }
`,
		`            method "unencrypted" "migrate" {}
            state {
              method   = method.aes_gcm.app
              enforced = false
              fallback {
                method = method.unencrypted.migrate
              }
            }
            plan {
              method   = method.aes_gcm.app
              enforced = false
              fallback {
                method = method.unencrypted.migrate
              }
            }
`,
		`        - name: SMITH_BAO_JWT_PATH
          value: /var/run/secrets/deploysmith/openbao/token
`,
		`      volumes:
        - name: openbao-token
          projected:
            sources:
              - serviceAccountToken:
                  audience: openbao
                  expirationSeconds: 600
                  path: token
      volumeMounts:
        - name: openbao-token
          mountPath: /var/run/secrets/deploysmith/openbao
          readOnly: true
`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("manifest does not contain\n%s\ngot\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"tfVarsFiles", "secretRef", "dynamodb_table"} {
		if strings.Contains(string(got), unwanted) {
			t.Errorf("manifest contains %s:\n%s", unwanted, got)
		}
	}
}

func TestRootManifestRequiresSourceCommit(t *testing.T) {
	root := testRoot()
	root.SourceCommit = ""
	if _, err := RootManifest(root, testRootOptions); err == nil {
		t.Error("RootManifest without a source commit succeeded")
	}
}

func TestRunnerImage(t *testing.T) {
	image, err := testRootOptions.RunnerImage("1.12.9")
	if err != nil || image != "ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.12.9" {
		t.Errorf("RunnerImage(1.12.9) = %q, %v", image, err)
	}

	_, err = testRootOptions.RunnerImage("1.10.6")
	if err == nil || !strings.Contains(err.Error(), "no runner image for OpenTofu 1.10.6; pin one of the supported versions (1.12.9, 1.13.2)") {
		t.Errorf("RunnerImage(1.10.6) error = %v", err)
	}

	_, err = RootOptions{}.RunnerImage("1.12.9")
	if err == nil || !strings.Contains(err.Error(), "TERRAFORM_RUNNER_IMAGES") {
		t.Errorf("RunnerImage without images error = %v", err)
	}
}

func TestPlanID(t *testing.T) {
	if got := PlanID("main", "8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7"); got != "plan-main-8fa3d36e98" {
		t.Errorf("PlanID = %q", got)
	}
}

func TestHCLString(t *testing.T) {
	if got := hclString(`a"b\c${x}%{y}`); got != `"a\"b\\c$${x}%%{y}"` {
		t.Errorf("hclString = %s", got)
	}
}
