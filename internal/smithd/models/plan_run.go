package models

import "time"

// Plan run states. A run goes from pending (being committed to the plans
// branch) to planning and then planned, or ends as failed or superseded
// (replaced by a newer run of the same root).
const (
	PlanRunPending    = "pending"
	PlanRunPlanning   = "planning"
	PlanRunPlanned    = "planned"
	PlanRunFailed     = "failed"
	PlanRunSuperseded = "superseded"
)

// PlanRun is a plan-only run: the plan of a draft or published terraform
// version for an environment, made without deploying it. It is not a
// deployment and can never be approved or applied.
type PlanRun struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	AppName     string `json:"appName,omitempty"`
	VersionID   string `json:"versionId"` // the version's id, e.g. its git SHA
	Environment string `json:"environment"`
	Root        string `json:"root"`
	Status      string `json:"status"` // see the PlanRun* states
	TriggeredBy string `json:"triggeredBy,omitempty"`
	// ErrorMessage is why the run failed, or what replaced it
	ErrorMessage string     `json:"errorMessage,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
	// SourceCommitSHA and GitopsCommitSHA are the commits on the plans
	// branch that hold the source and the Terraform CR
	SourceCommitSHA string `json:"sourceCommitSha,omitempty"`
	GitopsCommitSHA string `json:"gitopsCommitSha,omitempty"`
	// PlanID is the id tofu-controller gives the plan of the source commit
	PlanID string `json:"planId,omitempty"`
	// Plan is the plan, once the run is planned. Only the GET of a run has
	// it, with the text.
	Plan *TerraformPlan `json:"plan,omitempty"`
}

// Done reports whether the run has ended
func (r PlanRun) Done() bool {
	return r.Status == PlanRunPlanned || r.Status == PlanRunFailed || r.Status == PlanRunSuperseded
}

// PlanVersionRequest is the request to make a plan-only run of a version
type PlanVersionRequest struct {
	Environment string `json:"environment"`
	TriggeredBy string `json:"triggeredBy,omitempty"`
}
