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
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// Cluster is a cluster to put Terraform CRs, plans and events in: a fake one
// (New), or a real one (NewFor) in which the CRD of InstallCRD is installed
type Cluster struct {
	t      *testing.T
	Client dynamic.Interface
	// realCluster is set for a real API server, where status is written to
	// the status subresource
	realCluster bool
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

// NewFor uses a real cluster through its dynamic client. The namespaces used
// must exist, and the CRD of InstallCRD be installed.
func NewFor(t *testing.T, client dynamic.Interface) *Cluster {
	t.Helper()
	return &Cluster{t: t, Client: client, realCluster: true}
}

// InstallCRD installs a CRD for Terraform CRs (without tofu-controller) in a
// real cluster, with a status subresource like the controller's, and waits
// until the API server serves it
func InstallCRD(t *testing.T, client dynamic.Interface) {
	t.Helper()
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": "terraforms.infra.contrib.fluxcd.io"},
		"spec": map[string]any{
			"group": "infra.contrib.fluxcd.io",
			"names": map[string]any{"kind": "Terraform", "listKind": "TerraformList", "plural": "terraforms", "singular": "terraform"},
			"scope": "Namespaced",
			"versions": []any{map[string]any{
				"name": "v1alpha2", "served": true, "storage": true,
				"subresources": map[string]any{"status": map[string]any{}},
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object", "x-kubernetes-preserve-unknown-fields": true,
				}},
			}},
		},
	}}
	crds := client.Resource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"})
	if _, err := crds.Create(context.Background(), crd, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := client.Resource(terraform.TerraformGVR).Namespace("default").List(context.Background(), metav1.ListOptions{})
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Terraform CRD is not served: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Reader returns a reader of the cluster
func (c *Cluster) Reader() *terraform.Reader { return terraform.NewReaderFor(c.Client) }

// CR is the part of a Terraform CR that tofu-controller fills in and smithd
// reads. The helpers below make the ones a run goes through.
type CR struct {
	// DeploymentID is the annotation smithd sets
	DeploymentID string
	// PlanRunID is the annotation smithd sets on the CR of a plan-only run
	// instead (see ForPlanRun)
	PlanRunID string
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

// ForPlanRun turns a CR into that of a plan-only run: written by the run
// instead of a deployment, and planOnly
func (c CR) ForPlanRun(planRunID string) CR {
	c.DeploymentID = ""
	c.PlanRunID = planRunID
	return c
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
	annotations := map[string]any{terraform.DeploymentAnnotation: cr.DeploymentID}
	spec := map[string]any{"approvePlan": cr.ApprovePlan}
	if cr.PlanRunID != "" {
		annotations = map[string]any{terraform.PlanRunAnnotation: cr.PlanRunID}
		spec = map[string]any{"planOnly": true}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infra.contrib.fluxcd.io/v1alpha2",
		"kind":       "Terraform",
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "generation": generation,
			"annotations": annotations,
		},
		"spec":   spec,
		"status": status,
	}}
}

// SetCR creates or replaces a Terraform CR
func (c *Cluster) SetCR(namespace, name string, cr CR) {
	c.t.Helper()
	obj := cr.object(namespace, name, 1)
	if !c.realCluster {
		c.set(terraform.TerraformGVR, namespace, obj)
		return
	}

	// A real API server ignores the status of a new object: write it to the
	// status subresource
	status := obj.Object["status"]
	delete(obj.Object, "status")
	resource := c.Client.Resource(terraform.TerraformGVR).Namespace(namespace)
	_ = resource.Delete(context.Background(), name, metav1.DeleteOptions{})
	created, err := resource.Create(context.Background(), obj, metav1.CreateOptions{})
	if err != nil {
		c.t.Fatal(err)
	}
	created.Object["status"] = status
	if _, err := resource.UpdateStatus(context.Background(), created, metav1.UpdateOptions{}); err != nil {
		c.t.Fatal(err)
	}
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
