// Package terraformtest is a fake cluster for tests of code that follows
// Terraform runs: Terraform CRs, readable plans and events as tofu-controller
// writes them, behind the dynamic client terraform.Reader uses.
package terraformtest

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// Cluster is a fake cluster
type Cluster struct {
	t      *testing.T
	Client *dynamicfake.FakeDynamicClient
}

// New creates an empty cluster
func New(t *testing.T) *Cluster {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		terraform.TerraformGVR: "TerraformList",
		terraform.ConfigMapGVR: "ConfigMapList",
		terraform.EventGVR:     "EventList",
	})
	return &Cluster{t: t, Client: client}
}

// Reader returns a reader of the cluster
func (c *Cluster) Reader() *terraform.Reader { return terraform.NewReaderFor(c.Client) }

// CR is the part of a Terraform CR that tofu-controller fills in and smithd
// reads. The helpers below make the ones a run goes through.
type CR struct {
	// DeploymentID is the annotation smithd sets
	DeploymentID string
	// ApprovePlan is spec.approvePlan
	ApprovePlan string

	PendingPlan     string
	LastAppliedPlan string
	// Commits of the source last planned, attempted and applied
	PlannedRevision   string
	AttemptedRevision string
	AppliedRevision   string
	PlannedAt         time.Time

	Ready, Plan, Apply terraform.Condition
}

func revision(commit string) string {
	if commit == "" {
		return ""
	}
	return "main@sha1:" + commit
}

// Reconciling is a CR whose source is being planned
func Reconciling(deploymentID, source string) CR {
	return CR{
		DeploymentID: deploymentID, AttemptedRevision: source,
		Ready: terraform.Condition{Status: "Unknown", Reason: "Progressing", Message: "Reconciliation in progress"},
	}
}

// WithChanges is a CR with a plan that waits for approval
func WithChanges(deploymentID, source, planID string, plannedAt time.Time) CR {
	return CR{
		DeploymentID: deploymentID, PendingPlan: planID, PlannedAt: plannedAt,
		PlannedRevision: source, AttemptedRevision: source,
		Plan:  terraform.Condition{Status: "True", Reason: terraform.ReasonPlannedWithChanges, Message: "Plan generated"},
		Ready: terraform.Condition{Status: "Unknown", Reason: terraform.ReasonPlannedWithChanges, Message: fmt.Sprintf("Plan generated: set approvePlan: %q to approve this plan.", planID)},
	}
}

// WithoutChanges is a CR whose plan has no changes
func WithoutChanges(deploymentID, source string, plannedAt time.Time) CR {
	return CR{
		DeploymentID: deploymentID, PlannedAt: plannedAt, PlannedRevision: source, AttemptedRevision: source,
		Plan:  terraform.Condition{Status: "False", Reason: terraform.ReasonPlannedNoChanges, Message: "Plan no changes"},
		Ready: terraform.Condition{Status: "True", Reason: terraform.ReasonPlannedNoChanges, Message: "Plan no changes: main@sha1:" + source},
	}
}

// Approved turns a CR with a plan into one that applies it
func (c CR) Approved() CR {
	c.ApprovePlan = c.PendingPlan
	c.Apply = terraform.Condition{Status: "Unknown", Reason: "Progressing", Message: "Apply started"}
	c.Ready = terraform.Condition{Status: "Unknown", Reason: "Progressing", Message: "Reconciliation in progress"}
	return c
}

// Applied turns a CR that applies a plan into one that has applied it
func (c CR) Applied(source string) CR {
	c.LastAppliedPlan = c.PendingPlan
	c.AppliedRevision = source
	c.Apply = terraform.Condition{Status: "True", Reason: terraform.ReasonAppliedSucceed, Message: "Applied successfully"}
	c.Ready = terraform.Condition{Status: "True", Reason: terraform.ReasonAppliedSucceed, Message: "Applied successfully: main@sha1:" + source}
	return c
}

// Failed is a CR whose run failed with the given reason and message
func (c CR) Failed(reason, message string) CR {
	c.Ready = terraform.Condition{Status: "False", Reason: reason, Message: message}
	return c
}

func (cr CR) object(namespace, name string, generation int64) *unstructured.Unstructured {
	conditions := []any{}
	for _, c := range []struct {
		conditionType string
		condition     terraform.Condition
	}{{terraform.ConditionReady, cr.Ready}, {terraform.ConditionPlan, cr.Plan}, {terraform.ConditionApply, cr.Apply}} {
		if c.condition.Status == "" {
			continue
		}
		conditions = append(conditions, map[string]any{
			"type": c.conditionType, "status": c.condition.Status, "reason": c.condition.Reason, "message": c.condition.Message,
		})
	}
	status := map[string]any{
		"observedGeneration":    generation,
		"lastPlannedGeneration": generation,
		"plan":                  map[string]any{"pending": cr.PendingPlan, "lastApplied": cr.LastAppliedPlan},
		"lastPlannedRevision":   revision(cr.PlannedRevision),
		"lastAttemptedRevision": revision(cr.AttemptedRevision),
		"lastAppliedRevision":   revision(cr.AppliedRevision),
		"conditions":            conditions,
	}
	if !cr.PlannedAt.IsZero() {
		status["lastPlanAt"] = cr.PlannedAt.UTC().Format(time.RFC3339)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infra.contrib.fluxcd.io/v1alpha2",
		"kind":       "Terraform",
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "generation": generation,
			"annotations": map[string]any{terraform.DeploymentAnnotation: cr.DeploymentID},
		},
		"spec":   map[string]any{"approvePlan": cr.ApprovePlan},
		"status": status,
	}}
}

// SetCR creates or replaces a Terraform CR
func (c *Cluster) SetCR(namespace, name string, cr CR) {
	c.t.Helper()
	c.set(terraform.TerraformGVR, namespace, cr.object(namespace, name, 1))
}

// DeleteCR removes a Terraform CR
func (c *Cluster) DeleteCR(namespace, name string) {
	c.t.Helper()
	if err := c.Client.Resource(terraform.TerraformGVR).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		c.t.Fatal(err)
	}
}

// SetPlan stores the readable plan of a CR as tofu-controller does, in
// chunks of at most chunkSize bytes (all in one ConfigMap if 0). Plans
// replace each other.
func (c *Cluster) SetPlan(namespace, cr, planID, hash, text string, chunkSize int) {
	c.t.Helper()
	// Remove the previous plan, which may have had other chunks
	list, err := c.Client.Resource(terraform.ConfigMapGVR).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		c.t.Fatal(err)
	}
	for _, item := range list.Items {
		if item.GetLabels()["infra.contrib.fluxcd.io/plan-name"] == cr {
			if err := c.Client.Resource(terraform.ConfigMapGVR).Namespace(namespace).Delete(context.Background(), item.GetName(), metav1.DeleteOptions{}); err != nil {
				c.t.Fatal(err)
			}
		}
	}

	if chunkSize <= 0 {
		c.SetPlanChunk(namespace, cr, -1, planID, hash, text)
		return
	}
	for i := 0; i*chunkSize < len(text) || i == 0; i++ {
		end := min((i+1)*chunkSize, len(text))
		c.SetPlanChunk(namespace, cr, i, planID, hash, text[i*chunkSize:end])
	}
}

// SetPlanChunk stores one chunk of the readable plan of a CR, or all of it
// in the ConfigMap without a chunk number if chunk is negative
func (c *Cluster) SetPlanChunk(namespace, cr string, chunk int, planID, hash, data string) {
	c.t.Helper()
	name := "tfplan-default-" + cr
	annotations := map[string]any{"savedPlan": planID, "infra.contrib.fluxcd.io/plan-hash": hash}
	if chunk >= 0 {
		name = fmt.Sprintf("%s-%d", name, chunk)
		annotations["infra.contrib.fluxcd.io/plan-chunk"] = strconv.Itoa(chunk)
	}
	c.set(terraform.ConfigMapGVR, namespace, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "annotations": annotations,
			"labels": map[string]any{"infra.contrib.fluxcd.io/plan-name": cr, "infra.contrib.fluxcd.io/plan-workspace": "default"},
		},
		"data": map[string]any{"tfplan": data},
	}})
}

// AddEvent adds an event about a CR
func (c *Cluster) AddEvent(namespace, cr, eventType, reason, message string, at time.Time) {
	c.t.Helper()
	c.set(terraform.EventGVR, namespace, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Event",
		"metadata":       map[string]any{"name": fmt.Sprintf("%s.%d", cr, at.UnixNano()), "namespace": namespace},
		"involvedObject": map[string]any{"kind": "Terraform", "name": cr, "namespace": namespace},
		"type":           eventType, "reason": reason, "message": message,
		"lastTimestamp": at.UTC().Format(time.RFC3339),
	}})
}

// set creates an object, or replaces it if it exists
func (c *Cluster) set(gvr schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) {
	c.t.Helper()
	resource := c.Client.Resource(gvr).Namespace(namespace)
	_, err := resource.Create(context.Background(), obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = resource.Update(context.Background(), obj, metav1.UpdateOptions{})
	}
	if err != nil {
		c.t.Fatal(err)
	}
}
