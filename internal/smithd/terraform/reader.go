package terraform

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var (
	// TerraformGVR is the Terraform CR of tofu-controller
	TerraformGVR = schema.GroupVersionResource{Group: "infra.contrib.fluxcd.io", Version: "v1alpha2", Resource: "terraforms"}
	// ConfigMapGVR and EventGVR are read in the runner namespaces
	// (ClusterRole smithd-terraform-reader). smithd has no access to Secrets,
	// where the binary plan is.
	ConfigMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	EventGVR     = schema.GroupVersionResource{Version: "v1", Resource: "events"}
)

// The labels and annotations tofu-controller puts on the readable plan
const (
	planNameLabel      = "infra.contrib.fluxcd.io/plan-name"
	planWorkspaceLabel = "infra.contrib.fluxcd.io/plan-workspace"
	planChunkAnno      = "infra.contrib.fluxcd.io/plan-chunk"
	planHashAnno       = "infra.contrib.fluxcd.io/plan-hash"
	savedPlanAnno      = "savedPlan"
	planKey            = "tfplan"
	planWorkspace      = "default"
)

// Reader reads what tofu-controller records about runs: the Terraform CRs
// with the dynamic client, like the Flux Kustomization poller, and the
// readable plans and events in the runner namespaces. It only reads.
type Reader struct {
	client dynamic.Interface
}

// NewReader creates a Reader using in-cluster config. It returns nil if
// running outside a cluster (local dev), where Terraform runs are not
// followed.
func NewReader() *Reader {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return NewReaderFor(client)
}

// NewReaderFor creates a Reader using a dynamic client
func NewReaderFor(client dynamic.Interface) *Reader {
	return &Reader{client: client}
}

// Run returns the state of a Terraform CR, or nil if there is no such CR
// (yet)
func (r *Reader) Run(ctx context.Context, namespace, name string) (*Run, error) {
	obj, err := r.client.Resource(TerraformGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get terraform %s/%s: %w", namespace, name, err)
	}
	return parseRun(obj.Object), nil
}

// ReadablePlan is the plan stored for people to read, with what identifies it
type ReadablePlan struct {
	// Text is the output of `tofu show` of the plan, sensitive values masked
	Text string
	// PlanID is the id of the plan the text is of (annotation savedPlan)
	PlanID string
	// Hash is the hash the controller recorded for the text
	Hash string
}

// ReadablePlan returns the readable plan of a CR, which the controller keeps
// in ConfigMaps tfplan-default-<cr>, split in chunks over 1 MiB. It returns
// nil if there is none. The plan is overwritten by every plan of the CR, so
// callers compare PlanID and Hash with what they expect.
func (r *Reader) ReadablePlan(ctx context.Context, namespace, cr string) (*ReadablePlan, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s", planNameLabel, cr, planWorkspaceLabel, planWorkspace)
	list, err := r.client.Resource(ConfigMapGVR).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list the plan of %s/%s: %w", namespace, cr, err)
	}
	if len(list.Items) == 0 {
		return nil, nil
	}

	chunks := list.Items
	sort.SliceStable(chunks, func(i, j int) bool { return chunkNumber(chunks[i]) < chunkNumber(chunks[j]) })

	plan := &ReadablePlan{
		PlanID: chunks[0].GetAnnotations()[savedPlanAnno],
		Hash:   chunks[0].GetAnnotations()[planHashAnno],
	}
	var text strings.Builder
	for _, chunk := range chunks {
		// A plan made while the chunks were read leaves them mixed up
		if chunk.GetAnnotations()[savedPlanAnno] != plan.PlanID || chunk.GetAnnotations()[planHashAnno] != plan.Hash {
			return nil, fmt.Errorf("the plan of %s/%s changed while it was read", namespace, cr)
		}
		data, _, _ := unstructured.NestedString(chunk.Object, "data", planKey)
		text.WriteString(data)
	}
	plan.Text = text.String()
	return plan, nil
}

func chunkNumber(chunk unstructured.Unstructured) int {
	n, _ := strconv.Atoi(chunk.GetAnnotations()[planChunkAnno])
	return n
}

// Event is a Kubernetes event about a Terraform CR
type Event struct {
	Type    string // Normal or Warning
	Reason  string
	Message string
	Time    time.Time
}

// Events returns the events about a CR, oldest first. The runner pods are
// deleted after each run and their logs are gone; the events and the
// condition messages are what is left of a failed run.
func (r *Reader) Events(ctx context.Context, namespace, cr string) ([]Event, error) {
	selector := fmt.Sprintf("involvedObject.kind=Terraform,involvedObject.name=%s", cr)
	list, err := r.client.Resource(EventGVR).Namespace(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list the events of %s/%s: %w", namespace, cr, err)
	}

	var events []Event
	for _, item := range list.Items {
		// The fake client used in tests and some API servers ignore the selector
		if name, _, _ := unstructured.NestedString(item.Object, "involvedObject", "name"); name != cr {
			continue
		}
		event := Event{}
		event.Type, _, _ = unstructured.NestedString(item.Object, "type")
		event.Reason, _, _ = unstructured.NestedString(item.Object, "reason")
		event.Message, _, _ = unstructured.NestedString(item.Object, "message")
		for _, field := range []string{"lastTimestamp", "eventTime", "firstTimestamp"} {
			if at, _, _ := unstructured.NestedString(item.Object, field); at != "" {
				event.Time, _ = time.Parse(time.RFC3339, at)
				break
			}
		}
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}
