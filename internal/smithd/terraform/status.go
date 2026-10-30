package terraform

import (
	"strings"
	"time"
)

// What smithd reads from a root's Terraform CR to follow a run. See "Reading
// Status" in docs/specs/terraform-controller.md for the fields and for how
// the conditions map to deployment states.

// Condition types and reasons of tofu-controller that smithd relies on
const (
	ConditionReady = "Ready"
	ConditionPlan  = "Plan"
	ConditionApply = "Apply"

	ReasonPlannedWithChanges = "TerraformPlannedWithChanges"
	ReasonPlannedNoChanges   = "TerraformPlannedNoChanges"
	ReasonAppliedSucceed     = "TerraformAppliedSucceed"
)

// DeploymentAnnotation is set by smithd on a root's Terraform CR to the
// deployment that wrote it
const DeploymentAnnotation = "deploysmith.io/deployment-id"

// Condition is one status condition of a Terraform CR
type Condition struct {
	// Status is True, False or Unknown; empty if the CR has no such condition
	Status  string
	Reason  string
	Message string
}

// Run is the part of a Terraform CR's metadata, spec and status that tells
// how a run is going
type Run struct {
	// DeploymentID is the deployment that wrote the CR (DeploymentAnnotation)
	DeploymentID string
	// ApprovePlan is spec.approvePlan: the plan id approved, "" if none
	ApprovePlan string

	Generation         int64
	ObservedGeneration int64

	// PendingPlan is the id of the plan waiting for approval and
	// LastAppliedPlan the id of the last applied plan; both can be empty
	PendingPlan     string
	LastAppliedPlan string
	// PlannedRevision, AttemptedRevision and AppliedRevision are the commits
	// (not the "main@sha1:" revisions) of the source last planned,
	// attempted and applied
	PlannedRevision   string
	AttemptedRevision string
	AppliedRevision   string
	// PlannedGeneration is the generation the pending plan was made for, and
	// PlannedAt when the last plan was made. Plan ids repeat for every plan
	// of a revision; the time does not.
	PlannedGeneration int64
	PlannedAt         time.Time

	Conditions map[string]Condition
}

// Condition returns the condition of a type; Status is empty if there is none
func (r *Run) Condition(conditionType string) Condition { return r.Conditions[conditionType] }

// parseRun reads a Terraform CR as returned by the dynamic client
func parseRun(obj map[string]any) *Run {
	run := &Run{Conditions: map[string]Condition{}}

	metadata, _ := obj["metadata"].(map[string]any)
	run.Generation = intField(metadata, "generation")
	annotations, _ := metadata["annotations"].(map[string]any)
	run.DeploymentID, _ = annotations[DeploymentAnnotation].(string)

	spec, _ := obj["spec"].(map[string]any)
	run.ApprovePlan, _ = spec["approvePlan"].(string)

	status, _ := obj["status"].(map[string]any)
	run.ObservedGeneration = intField(status, "observedGeneration")
	run.PlannedGeneration = intField(status, "lastPlannedGeneration")
	plan, _ := status["plan"].(map[string]any)
	run.PendingPlan, _ = plan["pending"].(string)
	run.LastAppliedPlan, _ = plan["lastApplied"].(string)

	planned, _ := status["lastPlannedRevision"].(string)
	attempted, _ := status["lastAttemptedRevision"].(string)
	applied, _ := status["lastAppliedRevision"].(string)
	run.PlannedRevision = revisionSHA(planned)
	run.AttemptedRevision = revisionSHA(attempted)
	run.AppliedRevision = revisionSHA(applied)

	if at, _ := status["lastPlanAt"].(string); at != "" {
		run.PlannedAt, _ = time.Parse(time.RFC3339, at)
	}

	conditions, _ := status["conditions"].([]any)
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		conditionType, _ := cond["type"].(string)
		var parsed Condition
		parsed.Status, _ = cond["status"].(string)
		parsed.Reason, _ = cond["reason"].(string)
		parsed.Message, _ = cond["message"].(string)
		run.Conditions[conditionType] = parsed
	}
	return run
}

// intField reads an integer from an unstructured object, where it is an
// int64 when it comes from the API and a float64 after other JSON decoding
func intField(obj map[string]any, field string) int64 {
	switch v := obj[field].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// revisionSHA extracts the commit from a source revision such as
// "main@sha1:8fa3d36e98..."
func revisionSHA(revision string) string {
	if i := strings.LastIndexAny(revision, ":/"); i >= 0 {
		return revision[i+1:]
	}
	return revision
}
