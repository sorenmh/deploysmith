package terraform

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"gopkg.in/yaml.v3"
)

// What smithd writes for one root of a Terraform app in an environment: a
// GitRepository pinned to the gitops commit that holds the bundle source,
// and the Terraform CR that tofu-controller plans and applies. See "Resources
// smithd Generates" in docs/specs/terraform-controller.md for the reasons
// behind each setting.

// Runner pod resources; the namespace's ResourceQuota requires requests and
// a memory limit
const (
	runnerRequestsCPU    = "100m"
	runnerRequestsMemory = "256Mi"
	runnerLimitsMemory   = "1Gi"
)

// openBaoTokenDir is where the runner's projected ServiceAccount token for
// OpenBao is mounted when OpenBao requires an audience
const openBaoTokenDir = "/var/run/secrets/deploysmith/openbao"

// RootOptions configure the roots of all apps
type RootOptions struct {
	// SourceURL is the gitops repository as source-controller fetches it,
	// e.g. ssh://git@github.com/sorenmh/gitops.git, and SourceSecret the
	// Secret with its read-only deploy key (none if empty)
	SourceURL    string
	SourceSecret string
	// SourceNamespace holds the GitRepositories, away from the runner
	// namespaces; tofu-controller needs allowCrossNamespaceRefs
	SourceNamespace string
	// RunnerImages maps the OpenTofu versions smithd supports to their
	// runner images
	RunnerImages map[string]string
	// OpenBaoAddr is OpenBao's address as runners reach it; AuthMount,
	// AWSMount and TransitMount the mounts they use, and Audience the
	// audience their ServiceAccount tokens must have (any if empty)
	OpenBaoAddr  string
	AuthMount    string
	AWSMount     string
	TransitMount string
	Audience     string
}

// Root is one root of an app in an environment, as a deploy writes it
type Root struct {
	App         string
	Environment string
	Name        string
	// VersionID and DeploymentID are recorded on the CR
	VersionID    string
	DeploymentID string
	// PlanRun makes it a plan-only run (RootPlanOnlyManifest) instead of the
	// root itself: the run's id, recorded on the CR in place of
	// DeploymentID, and from which its CR and source are named
	PlanRun string
	// Branch and SourceCommit are the gitops branch and the commit holding
	// the source in SourcePath (repo-relative)
	Branch       string
	SourceCommit string
	SourcePath   string
	// TFVarsFile is the environment's tfvars file, repo-relative; empty if
	// the bundle has none
	TFVarsFile string
	// RunnerImage runs the bundle's OpenTofu version (see RunnerImage)
	RunnerImage        string
	Backend            models.TerraformBackend
	EncryptionFallback bool
}

// RootFile is the file in environments/<env>/terraform/<app>/ that holds a
// root's GitRepository and Terraform CR
func RootFile(root string) string { return root + ".yaml" }

// CRName is the name of a root's Terraform CR in the app's namespace. The
// runner pod is named after it (<cr>-tf-runner), so it is kept short.
func CRName(env, root string) string { return env + "-" + root }

// SourceName is the name of a root's GitRepository, unique among all apps
func SourceName(app, env, root string) string { return "tf-" + app + "-" + env + "-" + root }

// PlanName is the name of a plan-only run: of its Terraform CR in the app's
// namespace and of its files on the plans branch. It is unique per run, so
// that each run has a readable plan (ConfigMap tfplan-default-<name>) of
// its own, and short, since the runner pod is named after it.
func PlanName(planRun string) string {
	id := strings.ReplaceAll(planRun, "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return "plan-" + id
}

// PlanSourceName is the name of the GitRepository of a plan-only run
func PlanSourceName(app, planRun string) string { return "tf-" + app + "-" + PlanName(planRun) }

// PlanID is the id tofu-controller gives the plan of a source commit on a
// branch (status.plan.pending): plan-<branch>-<first 10 characters>
func PlanID(branch, commit string) string {
	if len(commit) > 10 {
		commit = commit[:10]
	}
	return "plan-" + branch + "-" + commit
}

// RunnerImage returns the runner image for an OpenTofu version, or an error
// listing the supported versions
func (o RootOptions) RunnerImage(version string) (string, error) {
	if image := o.RunnerImages[version]; image != "" {
		return image, nil
	}
	supported := make([]string, 0, len(o.RunnerImages))
	for v := range o.RunnerImages {
		supported = append(supported, v)
	}
	slices.Sort(supported)
	if len(supported) == 0 {
		return "", fmt.Errorf("smithd has no runner images configured (TERRAFORM_RUNNER_IMAGES), so it can't run OpenTofu %s", version)
	}
	return "", fmt.Errorf("no runner image for OpenTofu %s; pin one of the supported versions (%s) in required_version", version, strings.Join(supported, ", "))
}

type gitRepositorySpec struct {
	Interval  string     `yaml:"interval"`
	URL       string     `yaml:"url"`
	SecretRef *nameRef   `yaml:"secretRef,omitempty"`
	Ref       gitRefSpec `yaml:"ref"`
	Ignore    string     `yaml:"ignore"`
}

type nameRef struct {
	Name string `yaml:"name"`
}

type gitRefSpec struct {
	Branch string `yaml:"branch"`
	Commit string `yaml:"commit"`
}

type terraformSpec struct {
	Interval      string `yaml:"interval"`
	RetryInterval string `yaml:"retryInterval"`
	Remediation   struct {
		Retries int `yaml:"retries"`
	} `yaml:"remediation"`
	SourceRef struct {
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"sourceRef"`
	Path        string   `yaml:"path"`
	TFVarsFiles []string `yaml:"tfVarsFiles,omitempty"`
	// ApprovePlan is always written, also when empty: a leftover approval
	// would apply the next plan of the same revision. Plan-only runs have
	// none.
	ApprovePlan *string `yaml:"approvePlan,omitempty"`
	// PlanOnly runs never apply, whatever approvePlan says; Plan.Lock false
	// skips the state lock, which the read-only role can't take
	PlanOnly                   bool      `yaml:"planOnly,omitempty"`
	Plan                       *planSpec `yaml:"plan,omitempty"`
	StoreReadablePlan          string    `yaml:"storeReadablePlan"`
	ServiceAccountName         string    `yaml:"serviceAccountName"`
	DisableDriftDetection      bool      `yaml:"disableDriftDetection"`
	DestroyResourcesOnDeletion bool      `yaml:"destroyResourcesOnDeletion"`
	AlwaysCleanupRunnerPod     bool      `yaml:"alwaysCleanupRunnerPod"`
	BackendConfig              struct {
		CustomConfiguration string `yaml:"customConfiguration"`
	} `yaml:"backendConfig"`
	RunnerPodTemplate struct {
		Spec runnerPodSpec `yaml:"spec"`
	} `yaml:"runnerPodTemplate"`
}

type planSpec struct {
	Lock bool `yaml:"lock"`
}

type runnerPodSpec struct {
	Image        string        `yaml:"image"`
	Env          []envVar      `yaml:"env"`
	Volumes      []any         `yaml:"volumes,omitempty"`
	VolumeMounts []volumeMount `yaml:"volumeMounts,omitempty"`
	Resources    struct {
		Requests map[string]string `yaml:"requests"`
		Limits   map[string]string `yaml:"limits"`
	} `yaml:"resources"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type volumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	ReadOnly  bool   `yaml:"readOnly"`
}

// RootManifest returns the root's file: its GitRepository, pinned to the
// source commit, and its Terraform CR with approvePlan "", so the controller
// plans the source and waits for approval
func RootManifest(root Root, opts RootOptions) ([]byte, error) {
	root.PlanRun = ""
	return rootManifest(root, opts)
}

// RootPlanOnlyManifest returns the file of a plan-only run of the root (see
// docs/specs/terraform-controller.md, "Plan-Only Runs"): the same
// GitRepository and CR as RootManifest, named after the run, with
// planOnly: true, no approvePlan, no state lock, and the read-only
// ServiceAccount and OpenBao and AWS roles. The controller plans the source
// and keeps the plan, and never applies it.
func RootPlanOnlyManifest(root Root, planRun string, opts RootOptions) ([]byte, error) {
	if planRun == "" {
		return nil, fmt.Errorf("the plan-only run's id is required")
	}
	root.PlanRun = planRun
	return rootManifest(root, opts)
}

func rootManifest(root Root, opts RootOptions) ([]byte, error) {
	if root.SourceCommit == "" || root.Branch == "" {
		return nil, fmt.Errorf("the root's source commit and branch are required")
	}
	planOnly := root.PlanRun != ""
	namespace := Namespace(root.App)
	sourceName := SourceName(root.App, root.Environment, root.Name)
	crName := CRName(root.Environment, root.Name)
	annotations := map[string]string{
		"deploysmith.io/version":       root.VersionID,
		"deploysmith.io/deployment-id": root.DeploymentID,
	}
	labels := map[string]string{
		"deploysmith.io/app":         root.App,
		"deploysmith.io/environment": root.Environment,
		"deploysmith.io/root":        root.Name,
	}
	if planOnly {
		sourceName = PlanSourceName(root.App, root.PlanRun)
		crName = PlanName(root.PlanRun)
		annotations = map[string]string{
			"deploysmith.io/version": root.VersionID,
			PlanRunAnnotation:        root.PlanRun,
		}
		labels["deploysmith.io/plan-only"] = "true"
	}

	source := gitRepositorySpec{
		Interval: "10m",
		URL:      opts.SourceURL,
		Ref:      gitRefSpec{Branch: root.Branch, Commit: root.SourceCommit},
		// The artifact holds the root's source only
		Ignore: "/*\n!/" + root.SourcePath + "/\n",
	}
	if opts.SourceSecret != "" {
		source.SecretRef = &nameRef{Name: opts.SourceSecret}
	}

	var spec terraformSpec
	// Plans with no changes every interval; deploy commits trigger at once
	spec.Interval = "6h"
	spec.RetryInterval = "5m"
	// One attempt per generation and revision, no automatic re-apply
	spec.Remediation.Retries = 1
	spec.SourceRef.Kind = "GitRepository"
	spec.SourceRef.Name = sourceName
	spec.SourceRef.Namespace = opts.SourceNamespace
	spec.Path = "./" + root.SourcePath
	if root.TFVarsFile != "" {
		// Relative to the repo root, not to path
		spec.TFVarsFiles = []string{root.TFVarsFile}
	}
	if planOnly {
		spec.PlanOnly = true
		spec.Plan = &planSpec{Lock: false}
	} else {
		approve := ""
		spec.ApprovePlan = &approve
	}
	spec.StoreReadablePlan = "human"
	spec.ServiceAccountName = RunnerServiceAccount(root.Environment, planOnly)
	spec.DisableDriftDetection = true
	spec.DestroyResourcesOnDeletion = false
	spec.AlwaysCleanupRunnerPod = true
	spec.BackendConfig.CustomConfiguration = BackendConfig(root.Backend)
	spec.RunnerPodTemplate.Spec = runnerPod(root, opts)

	objects := []object{
		{
			APIVersion: "source.toolkit.fluxcd.io/v1",
			Kind:       "GitRepository",
			Metadata:   metadata{Name: sourceName, Namespace: opts.SourceNamespace, Labels: labels},
			Spec:       source,
		},
		{
			APIVersion: "infra.contrib.fluxcd.io/v1alpha2",
			Kind:       "Terraform",
			Metadata: metadata{
				Name:        crName,
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: spec,
		},
	}

	var buf bytes.Buffer
	if planOnly {
		fmt.Fprintf(&buf, "# Plan-only run of Terraform root %s of %s in %s, version %s, managed by smithd\n", root.Name, root.App, root.Environment, root.VersionID)
	} else {
		fmt.Fprintf(&buf, "# Terraform root %s of %s in %s, version %s, managed by smithd\n", root.Name, root.App, root.Environment, root.VersionID)
	}
	for _, obj := range objects {
		buf.WriteString("---\n")
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(obj); err != nil {
			return nil, fmt.Errorf("failed to encode %s %s: %w", obj.Kind, obj.Metadata.Name, err)
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// runnerPod is the runner of a root: the image for its OpenTofu version and
// what tf-runner-auth needs to log in to OpenBao and get AWS credentials
// (docs/terraform-cluster-setup.md, "Runner Environment")
func runnerPod(root Root, opts RootOptions) runnerPodSpec {
	readOnly := root.PlanRun != ""
	pod := runnerPodSpec{Image: root.RunnerImage}
	pod.Env = []envVar{
		{Name: "BAO_ADDR", Value: opts.OpenBaoAddr},
		{Name: "SMITH_BAO_AUTH_ROLE", Value: AuthRole(root.App, root.Environment, readOnly)},
	}
	if opts.AuthMount != "" {
		pod.Env = append(pod.Env, envVar{Name: "SMITH_BAO_AUTH_MOUNT", Value: opts.AuthMount})
	}
	pod.Env = append(pod.Env, envVar{Name: "SMITH_BAO_AWS_ROLE", Value: AWSRole(root.App, root.Environment, readOnly)})
	if opts.AWSMount != "" {
		pod.Env = append(pod.Env, envVar{Name: "SMITH_BAO_AWS_MOUNT", Value: opts.AWSMount})
	}
	if opts.Audience != "" {
		// The default ServiceAccount token has the API server's audience
		pod.Env = append(pod.Env, envVar{Name: "SMITH_BAO_JWT_PATH", Value: openBaoTokenDir + "/token"})
		pod.Volumes = []any{map[string]any{
			"name": "openbao-token",
			"projected": map[string]any{"sources": []any{map[string]any{
				"serviceAccountToken": map[string]any{"audience": opts.Audience, "expirationSeconds": 600, "path": "token"},
			}}},
		}}
		pod.VolumeMounts = []volumeMount{{Name: "openbao-token", MountPath: openBaoTokenDir, ReadOnly: true}}
	}
	pod.Env = append(pod.Env, envVar{Name: "TF_ENCRYPTION", Value: EncryptionConfig(root.App, opts.TransitMount, root.EncryptionFallback)})

	pod.Resources.Requests = map[string]string{"cpu": runnerRequestsCPU, "memory": runnerRequestsMemory}
	pod.Resources.Limits = map[string]string{"memory": runnerLimitsMemory}
	return pod
}

// BackendConfig is the S3 backend block of a root. The controller writes it
// as backend_override.tf, which replaces any backend in the bundle.
func BackendConfig(backend models.TerraformBackend) string {
	settings := [][2]string{
		{"bucket", hclString(backend.Bucket)},
		{"key", hclString(backend.Key)},
		{"region", hclString(backend.Region)},
	}
	if backend.LockTable != "" {
		settings = append(settings, [2]string{"dynamodb_table", hclString(backend.LockTable)})
	}
	settings = append(settings, [2]string{"encrypt", "true"})

	var b strings.Builder
	b.WriteString("backend \"s3\" {\n")
	for _, s := range settings {
		fmt.Fprintf(&b, "  %-14s = %s\n", s[0], s[1])
	}
	b.WriteString("}\n")
	return b.String()
}

// EncryptionConfig is the OpenTofu state and plan encryption of an app
// (TF_ENCRYPTION), with its OpenBao Transit key. With fallback, plaintext
// state and plans are still read, and written back encrypted; that is for
// the first apply of an existing app only.
func EncryptionConfig(app, transitMount string, fallback bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `key_provider "openbao" "app" {
  key_name            = %s
  transit_engine_path = %s
}
method "aes_gcm" "app" {
  keys = key_provider.openbao.app
}
`, hclString(TransitKey(app)), hclString("/"+strings.Trim(transitMount, "/")))
	if fallback {
		b.WriteString("method \"unencrypted\" \"migrate\" {}\n")
	}
	for _, target := range []string{"state", "plan"} {
		if fallback {
			fmt.Fprintf(&b, `%s {
  method   = method.aes_gcm.app
  enforced = false
  fallback {
    method = method.unencrypted.migrate
  }
}
`, target)
		} else {
			fmt.Fprintf(&b, `%s {
  method   = method.aes_gcm.app
  enforced = true
}
`, target)
		}
	}
	return b.String()
}

// hclString quotes s as an HCL string literal without interpolation
func hclString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "${", "$${", "%{", "%%{")
	return `"` + r.Replace(s) + `"`
}
