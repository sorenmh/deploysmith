package flux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var kustomizationGVR = schema.GroupVersionResource{
	Group:    "kustomize.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "kustomizations",
}

// KustomizationPoller polls a Flux Kustomization CRD for reconciliation status.
type KustomizationPoller struct {
	client    dynamic.Interface
	namespace string
}

// NewKustomizationPoller creates a poller using in-cluster config.
// Returns nil if running outside a cluster (no in-cluster config available).
func NewKustomizationPoller(namespace string) *KustomizationPoller {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Not running in-cluster (local dev); silently disable
		return nil
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return &KustomizationPoller{client: client, namespace: namespace}
}

// PollResult is the outcome of waiting for a commit to be applied
type PollResult struct {
	Ready   bool
	Message string // why the commit was not applied, if it was not
}

// WaitForCommit waits up to maxWait for the Kustomization to apply a
// revision that includes commitSHA, checking every pollInterval. contains
// reports whether a revision includes the commit, i.e. is the commit or a
// descendant of it; a later commit by someone else applies ours too.
func (p *KustomizationPoller) WaitForCommit(ctx context.Context, name, commitSHA string, contains func(revision string) (bool, error), maxWait, pollInterval time.Duration) PollResult {
	includes := revisionMatcher(commitSHA, contains)

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		status, err := p.status(ctx, name)
		if err != nil {
			logging.LogWarnCtx(ctx, fmt.Sprintf("Error polling Flux Kustomization %s: %v", name, err))
		} else if result, done := decide(status, includes); done {
			return result
		}

		select {
		case <-ctx.Done():
			return PollResult{Message: "stopped waiting for Flux: " + ctx.Err().Error()}
		case <-time.After(pollInterval):
		}
	}
	return PollResult{Message: fmt.Sprintf("Flux did not apply commit %s within %s", shortSHA(commitSHA), maxWait)}
}

// kustomizationStatus is the part of a Kustomization's status that tells
// whether a revision was applied
type kustomizationStatus struct {
	applied   string // SHA of the last applied revision
	attempted string // SHA of the last attempted revision
	ready     string // status of the Ready condition: True, False or Unknown
	message   string // message of the Ready condition
}

// decide reports whether waiting is over, and with what result
func decide(status kustomizationStatus, includes func(sha string) bool) (PollResult, bool) {
	if status.applied != "" && includes(status.applied) {
		return PollResult{Ready: true, Message: "Applied"}, true
	}
	// Ready is Unknown while Flux is still working on the revision
	if status.ready == "False" && status.attempted != "" && includes(status.attempted) {
		return PollResult{Message: status.message}, true
	}
	return PollResult{}, false
}

// revisionMatcher returns a function reporting whether a revision includes
// commitSHA. Lookups that fail count as "no" and are retried next time.
func revisionMatcher(commitSHA string, contains func(revision string) (bool, error)) func(string) bool {
	known := map[string]bool{}
	return func(revision string) bool {
		if revision == commitSHA {
			return true
		}
		if included, ok := known[revision]; ok {
			return included
		}
		included, err := contains(revision)
		if err != nil {
			logging.LogWarn(fmt.Sprintf("Failed to check whether %s includes %s: %v", revision, commitSHA, err))
			return false
		}
		known[revision] = included
		return included
	}
}

func (p *KustomizationPoller) status(ctx context.Context, name string) (kustomizationStatus, error) {
	obj, err := p.client.Resource(kustomizationGVR).Namespace(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return kustomizationStatus{}, fmt.Errorf("failed to get kustomization: %w", err)
	}
	return parseStatus(obj.Object), nil
}

func parseStatus(obj map[string]interface{}) kustomizationStatus {
	var status kustomizationStatus
	raw, ok := obj["status"].(map[string]interface{})
	if !ok {
		return status
	}

	applied, _ := raw["lastAppliedRevision"].(string)
	attempted, _ := raw["lastAttemptedRevision"].(string)
	status.applied = revisionSHA(applied)
	status.attempted = revisionSHA(attempted)

	conditions, _ := raw["conditions"].([]interface{})
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok || cond["type"] != "Ready" {
			continue
		}
		status.ready, _ = cond["status"].(string)
		status.message, _ = cond["message"].(string)
	}
	return status
}

// revisionSHA extracts the commit SHA from a Flux revision such as
// "master@sha1:abc123" or, from older Flux versions, "master/abc123"
func revisionSHA(revision string) string {
	if i := strings.LastIndexAny(revision, ":/"); i >= 0 {
		return revision[i+1:]
	}
	return revision
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
