package flux

import (
	"context"
	"fmt"
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

// PollResult is the outcome of a poll.
type PollResult struct {
	Ready   bool
	Message string // reason or error message from the Kustomization status
}

// Poll waits up to maxWait for the Kustomization to report Ready=True.
// It polls every pollInterval. Returns a PollResult indicating success or failure.
func (p *KustomizationPoller) Poll(ctx context.Context, name string, maxWait, pollInterval time.Duration) PollResult {
	if p == nil {
		return PollResult{Ready: true} // disabled; assume success
	}

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		result, err := p.checkOnce(ctx, name)
		if err != nil {
			logging.LogWarnCtx(ctx, fmt.Sprintf("Error polling Flux Kustomization %s: %v", name, err))
			time.Sleep(pollInterval)
			continue
		}
		if result.Ready {
			return result
		}
		logging.LogInfoCtx(ctx, fmt.Sprintf("Waiting for Flux Kustomization %s: %s", name, result.Message))
		time.Sleep(pollInterval)
	}
	return PollResult{Ready: false, Message: fmt.Sprintf("timed out waiting for Kustomization %s to become ready", name)}
}

func (p *KustomizationPoller) checkOnce(ctx context.Context, name string) (PollResult, error) {
	obj, err := p.client.Resource(kustomizationGVR).Namespace(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return PollResult{}, fmt.Errorf("failed to get kustomization: %w", err)
	}

	// Navigate: .status.conditions[] where type=="Ready"
	status, ok := obj.Object["status"].(map[string]interface{})
	if !ok {
		return PollResult{Message: "no status yet"}, nil
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return PollResult{Message: "no conditions yet"}, nil
	}

	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Ready" {
			if cond["status"] == "True" {
				return PollResult{Ready: true, Message: "Ready"}, nil
			}
			msg, _ := cond["message"].(string)
			return PollResult{Ready: false, Message: msg}, nil
		}
	}
	return PollResult{Message: "Ready condition not found"}, nil
}
