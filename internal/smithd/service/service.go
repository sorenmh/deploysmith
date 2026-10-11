// Package service implements smithd's operations (deploying versions,
// managing policies, auto-deploying) independently of any transport, so
// that both the JSON API and the web UI can use them.
package service

import (
	"context"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/flux"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// ManifestStorage provides the manifest files of a version
type ManifestStorage interface {
	GetAllFiles(appName, versionID string, published bool) (map[string][]byte, error)
}

// GitopsRepo writes an app's manifests to the gitops repository and returns
// the resulting commit SHA
type GitopsRepo interface {
	Deploy(appName, environment, message string, manifests map[string][]byte) (string, error)
}

// FluxPoller waits for a Flux Kustomization to become ready
type FluxPoller interface {
	Poll(ctx context.Context, name string, maxWait, pollInterval time.Duration) flux.PollResult
}

// Deps holds the dependencies of a Service. Optional integrations (Flux,
// Slack) may be nil.
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
}

// Service implements smithd's operations
type Service struct {
	Deps
}

// New creates a Service
func New(deps Deps) *Service {
	return &Service{Deps: deps}
}
