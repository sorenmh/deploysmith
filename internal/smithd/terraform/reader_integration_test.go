package terraform_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

// TestReaderAgainstCluster checks the Reader against a real API server: the
// CR with its status subresource, the plan ConfigMaps found by label and
// the events found by field selector. It needs TEST_KUBECONFIG, e.g. of a
// kind cluster, and makes a CRD for Terraform (without a controller), the
// namespace tf-it and what is in it. With TEST_KUBE_IMPERSONATE (a user such
// as system:serviceaccount:deploysmith:smithd) the Reader runs as that user,
// to check what RBAC the chart gives smithd.
func TestReaderAgainstCluster(t *testing.T) {
	kubeconfig := os.Getenv("TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("TEST_KUBECONFIG not set")
	}
	ctx := context.Background()

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	readerConfig := *config
	if user := os.Getenv("TEST_KUBE_IMPERSONATE"); user != "" {
		readerConfig.Impersonate.UserName = user
	}
	readerClient, err := dynamic.NewForConfig(&readerConfig)
	if err != nil {
		t.Fatal(err)
	}
	reader := terraform.NewReaderFor(readerClient)

	terraformtest.InstallCRD(t, admin)
	namespace := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "tf-it"},
	}}
	createIfMissing(t, admin.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}), namespace)

	// The CR and its status as the controller wrote them
	cluster := terraformtest.NewFor(t, admin)
	cluster.SetCR("tf-it", "it-main", terraformtest.WithChanges("dep-1", "8fa3d36e98d0d2f5d1a8f1c8c1c4b0a1e4b2f3d4", "plan-main-8fa3d36e98", time.Date(2026, 10, 29, 12, 30, 0, 0, time.UTC)))

	run, err := reader.Run(ctx, "tf-it", "it-main")
	if err != nil || run == nil {
		t.Fatalf("Run = %+v, %v", run, err)
	}
	if run.DeploymentID != "dep-1" || run.PendingPlan != "plan-main-8fa3d36e98" || run.PlannedRevision != "8fa3d36e98d0d2f5d1a8f1c8c1c4b0a1e4b2f3d4" ||
		run.ObservedGeneration != 1 || run.Generation != 1 || run.PlannedAt.IsZero() ||
		run.Condition(terraform.ConditionPlan).Reason != terraform.ReasonPlannedWithChanges {
		t.Errorf("unexpected run %+v", run)
	}
	if missing, err := reader.Run(ctx, "tf-it", "nope"); err != nil || missing != nil {
		t.Errorf("a CR that doesn't exist is %+v, %v", missing, err)
	}

	// A readable plan in two chunks, and another CR's plan in the namespace
	cms := admin.Resource(terraform.ConfigMapGVR).Namespace("tf-it")
	for i, data := range []string{"Plan: 1 to add, ", "0 to change, 0 to destroy.\n"} {
		name := "tfplan-default-it-main-" + string(rune('0'+i))
		_ = cms.Delete(ctx, name, metav1.DeleteOptions{})
		if _, err := cms.Create(ctx, planConfigMap(name, "it-main", string(rune('0'+i)), data), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	_ = cms.Delete(ctx, "tfplan-default-it-other", metav1.DeleteOptions{})
	if _, err := cms.Create(ctx, planConfigMap("tfplan-default-it-other", "it-other", "", "other"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	plan, err := reader.ReadablePlan(ctx, "tf-it", "it-main")
	if err != nil || plan == nil {
		t.Fatalf("ReadablePlan = %+v, %v", plan, err)
	}
	if plan.Text != "Plan: 1 to add, 0 to change, 0 to destroy.\n" || plan.PlanID != "plan-main-8fa3d36e98" || plan.Hash != "hash" {
		t.Errorf("unexpected plan %+v", plan)
	}

	// Events of the CR and of another object
	events := admin.Resource(terraform.EventGVR).Namespace("tf-it")
	for _, e := range []struct{ name, object, reason, message string }{
		{"it-main.1", "it-main", "TerraformPlanSucceed", "Plan generated"},
		{"it-other.1", "it-other", "TFExecPlanFailed", "not ours"},
	} {
		_ = events.Delete(ctx, e.name, metav1.DeleteOptions{})
		_, err := events.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Event",
			"metadata":       map[string]any{"name": e.name, "namespace": "tf-it"},
			"involvedObject": map[string]any{"apiVersion": "infra.contrib.fluxcd.io/v1alpha2", "kind": "Terraform", "name": e.object, "namespace": "tf-it"},
			"type":           "Normal", "reason": e.reason, "message": e.message,
			"lastTimestamp": "2026-10-29T12:30:01Z",
		}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := reader.Events(ctx, "tf-it", "it-main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Reason != "TerraformPlanSucceed" || got[0].Time.IsZero() {
		t.Errorf("unexpected events %+v", got)
	}

	// smithd has no access to Secrets, where the binary plan is
	if user := os.Getenv("TEST_KUBE_IMPERSONATE"); user != "" {
		_, err := readerClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}).Namespace("tf-it").List(ctx, metav1.ListOptions{})
		if !apierrors.IsForbidden(err) {
			t.Errorf("listing secrets as %s: %v, want forbidden", user, err)
		}
	}
}

func planConfigMap(name, cr, chunk, data string) *unstructured.Unstructured {
	annotations := map[string]any{"savedPlan": "plan-main-8fa3d36e98", "infra.contrib.fluxcd.io/plan-hash": "hash"}
	if chunk != "" {
		annotations["infra.contrib.fluxcd.io/plan-chunk"] = chunk
	}
	if strings.HasSuffix(name, "other") {
		annotations["savedPlan"] = "plan-main-other"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name": name, "namespace": "tf-it", "annotations": annotations,
			"labels": map[string]any{"infra.contrib.fluxcd.io/plan-name": cr, "infra.contrib.fluxcd.io/plan-workspace": "default"},
		},
		"data": map[string]any{"tfplan": data},
	}}
}

// createIfMissing creates a cluster-wide object unless it exists
func createIfMissing(t *testing.T, resource dynamic.ResourceInterface, obj *unstructured.Unstructured) {
	t.Helper()
	if _, err := resource.Create(context.Background(), obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
}
