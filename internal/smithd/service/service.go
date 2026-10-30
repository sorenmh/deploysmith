// Package service implements smithd's operations (deploying versions,
// managing policies, auto-deploying) independently of any transport, so
// that both the JSON API and the web UI can use them.
package service

import (
	"context"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/flux"
	"github.com/sorenmh/deploysmith/internal/smithd/github"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/push"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// ManifestStorage provides the manifest files of a version
type ManifestStorage interface {
	GetAllFiles(appName, versionID string, published bool) (map[string][]byte, error)
}

// GitopsRepo writes an app's manifests to the gitops repository and returns
// the resulting commit SHA
type GitopsRepo interface {
	Deploy(appName, environment, message string, manifests map[string][]byte) (string, error)
	// Contains reports whether revision is commit or one of its descendants
	Contains(revision, commit string) (bool, error)
}

// FluxPoller waits for a Flux Kustomization to apply a commit
type FluxPoller interface {
	WaitForCommit(ctx context.Context, name, commitSHA string, contains func(revision string) (bool, error), maxWait, pollInterval time.Duration) flux.PollResult
}

// Deps holds the dependencies of a Service. Optional integrations (Flux,
// Slack, Web Push, GitHub) may be nil.
type Deps struct {
	Apps         *store.ApplicationStore
	Versions     *store.VersionStore
	Deployments  *store.DeploymentStore
	Policies     *store.PolicyStore
	Environments *store.EnvironmentStore

	Storage   ManifestStorage
	Gitops    GitopsRepo
	Publisher events.Publisher

	FluxReconciler        *flux.Reconciler
	FluxPoller            FluxPoller
	FluxKustomizationName string
	Slack                 *slack.Notifier
	Push                  *push.Notifier
	PushSubscriptions     *store.PushSubscriptionStore
	GitHub                *github.Reporter

	// UIBaseURL is the web UI's public URL, used to link to deployments
	// from notifications. Empty if there is none.
	UIBaseURL string

	// Terraform apps. Terraform support is off while Terraform (the
	// OpenBao provisioner) is nil, i.e. when OpenBao is not configured.
	TerraformEnvironments  *store.TerraformStore
	Terraform              *terraform.Provisioner
	TerraformGitops        TerraformGitops
	TerraformManifests     terraform.ManifestOptions
	TerraformRoots         terraform.RootOptions
	TerraformSharedBackend models.TerraformBackend // default backend of new apps; Key is unused
}

// Service implements smithd's operations
type Service struct {
	Deps
}

// New creates a Service
func New(deps Deps) *Service {
	return &Service{Deps: deps}
}
