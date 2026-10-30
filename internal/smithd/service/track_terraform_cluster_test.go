package service

import (
	"context"
	"os"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform/terraformtest"
)

// TestTrackTerraformAgainstCluster follows a deployment through a real API
// server, as smithd's ServiceAccount: planning, awaiting_approval with the
// summary parsed from a real `tofu show` plan, applying and applied. There
// is no controller; the test writes the CR status and readable plan the way
// tofu-controller does. It needs TEST_KUBECONFIG, a cluster with the RBAC of
// the chart for the ServiceAccount deploysmith/smithd in the namespace
// tf-viino, and optionally TEST_KUBE_IMPERSONATE to run as that user.
func TestTrackTerraformAgainstCluster(t *testing.T) {
	kubeconfig := os.Getenv("TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("TEST_KUBECONFIG not set")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	smithdConfig := *config
	if user := os.Getenv("TEST_KUBE_IMPERSONATE"); user != "" {
		smithdConfig.Impersonate.UserName = user
	}
	smithdClient, err := dynamic.NewForConfig(&smithdConfig)
	if err != nil {
		t.Fatal(err)
	}

	terraformtest.InstallCRD(t, admin)
	namespaces := admin.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	_, _ = namespaces.Create(context.Background(), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": trackNamespace},
	}}, metav1.CreateOptions{})
	cluster := terraformtest.NewFor(t, admin)

	f := newTrackFixture(t)
	f.svc.TerraformReader = terraform.NewReaderFor(smithdClient)
	deployment := f.deployed(t, "tf1")
	source, planID := deployment.Terraform.SourceCommitSHA, deployment.Terraform.PlanID

	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)

	cluster.SetCR(trackNamespace, trackCR, terraformtest.Reconciling(deployment.ID, source))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentPlanning)

	// The plan, in chunks of 400 bytes as the controller splits large plans
	cr := terraformtest.WithChanges(deployment.ID, source, planID, plannedAt(1))
	cluster.SetPlan(trackNamespace, trackCR, planID, "hash-1", f.planText, 400)
	cluster.SetCR(trackNamespace, trackCR, cr)
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentAwaitingApproval)
	plan, err := f.svc.TerraformPlans.Get(deployment.ID)
	if err != nil || plan == nil {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if plan.Text != f.planText || plan.ToAdd != 3 || plan.ToChange != 1 || plan.ToDestroy != 2 || !plan.Destroys || strings.Join(plan.Resources.Replace, ",") != "terraform_data.rep" {
		t.Errorf("unexpected plan %+v", plan)
	}

	// Approved and applied
	if moved, err := f.svc.Deployments.TransitionTerraform(deployment.ID, models.DeploymentAwaitingApproval, models.DeploymentApplying, ""); err != nil || !moved {
		t.Fatal(moved, err)
	}
	cluster.SetCR(trackNamespace, trackCR, cr.Approved().Applied(source))
	f.poll()
	f.requireStatus(t, deployment.ID, models.DeploymentApplied)
}
