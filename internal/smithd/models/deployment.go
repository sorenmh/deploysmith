package models

import "time"

// Deployment states. Manifest deployments go from pending to success or
// failed. Terraform deployments go from pending (being committed) to
// planning, awaiting_approval, applying and applied, or end as failed,
// stale (replaced by a newer deploy of the same root) or rejected.
const (
	DeploymentPending          = "pending"
	DeploymentSuccess          = "success"
	DeploymentFailed           = "failed"
	DeploymentPlanning         = "planning"
	DeploymentAwaitingApproval = "awaiting_approval"
	DeploymentApplying         = "applying"
	DeploymentApplied          = "applied"
	DeploymentStale            = "stale"
	DeploymentRejected         = "rejected"
)

// ActiveTerraformStatuses are the states of a terraform deployment that is
// still running; only one per app, environment and root can be in them
var ActiveTerraformStatuses = []string{DeploymentPending, DeploymentPlanning, DeploymentAwaitingApproval, DeploymentApplying}

// Deployment represents a deployment of a version to an environment
type Deployment struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Status          string     `json:"status"` // see the Deployment* states
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	PolicyID        *string    `json:"policyId,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	// Terraform is set for deployments of terraform versions
	Terraform *TerraformRun `json:"terraform,omitempty"`
}

// TerraformRun is what a terraform deployment committed for the controller
type TerraformRun struct {
	// Root is the root module the deployment is for
	Root string `json:"root"`
	// SourceCommitSHA is the gitops commit holding the bundle source, which
	// the root's GitRepository is pinned to. GitopsCommitSHA is the later
	// commit of the Terraform CR.
	SourceCommitSHA string `json:"sourceCommitSha,omitempty"`
	// PlanID is the id tofu-controller gives the plan of that commit
	// (status.plan.pending), e.g. plan-main-8fa3d36e98
	PlanID string `json:"planId,omitempty"`
	// Rollback is true if the version is older than the one that was applied
	// when the deployment was made. It is a normal deploy with a new plan.
	Rollback bool `json:"rollback,omitempty"`
	// ApprovedBy is who approved the plan (a GitHub login, "api-key", or
	// "git" for an approval committed outside smithd) and ApprovedAt when.
	// DestroyConfirmed is true if the approval confirmed a plan with
	// destroys. ApprovalResetAt is when smithd committed approvePlan: ""
	// again after the apply.
	ApprovedBy       string     `json:"approvedBy,omitempty"`
	ApprovedAt       *time.Time `json:"approvedAt,omitempty"`
	DestroyConfirmed bool       `json:"destroyConfirmed,omitempty"`
	ApprovalResetAt  *time.Time `json:"approvalResetAt,omitempty"`
	// RejectedBy, RejectedAt and RejectReason are set for a rejected plan
	RejectedBy   string     `json:"rejectedBy,omitempty"`
	RejectedAt   *time.Time `json:"rejectedAt,omitempty"`
	RejectReason string     `json:"rejectReason,omitempty"`
	// Plan is the readable plan smithd copied from the cluster, once the
	// controller has made it. Only the deployment GET has it, with the text.
	Plan *TerraformPlan `json:"plan,omitempty"`
}

// TerraformPlan is the readable plan of a terraform deployment, copied from
// the cluster when it is ready: the summary the CLI, web UI and GitHub show,
// and the full text of `tofu show` (sensitive values are masked by OpenTofu).
// It is a durable record; the cluster overwrites the plan on the next run.
// The binary plan never leaves the cluster.
type TerraformPlan struct {
	// PlanID and PlanHash identify the plan together with PlannedAt: the
	// controller reuses the id for every plan of a revision. PlanHash is the
	// hash of the text the controller recorded.
	PlanID    string     `json:"planId"`
	PlanHash  string     `json:"planHash,omitempty"`
	PlannedAt *time.Time `json:"plannedAt,omitempty"`
	// ToAdd, ToChange and ToDestroy are the counts of the "Plan:" line
	// (a replacement counts as an add and a destroy)
	ToAdd     int `json:"toAdd"`
	ToChange  int `json:"toChange"`
	ToDestroy int `json:"toDestroy"`
	// Destroys is true if the plan destroys or replaces resources, which an
	// approval has to confirm separately
	Destroys bool `json:"destroys"`
	// OutputChanges is true if the plan changes outputs
	OutputChanges bool `json:"outputChanges,omitempty"`
	// Resources are the addresses of the resources by action
	Resources TerraformPlanResources `json:"resources"`
	// Text is the full plan. It is left out of listings.
	Text string `json:"text,omitempty"`
	// AppliedPlanHash is the hash of the plan the controller made again right
	// before applying, if it was read after the apply; AppliedDiffered is
	// true if that differs from the reviewed plan
	AppliedPlanHash string `json:"appliedPlanHash,omitempty"`
	AppliedDiffered bool   `json:"appliedDiffered,omitempty"`
}

// TerraformPlanResources are the addresses of the resources a plan changes
type TerraformPlanResources struct {
	Create  []string `json:"create"`
	Update  []string `json:"update"`
	Replace []string `json:"replace"`
	Destroy []string `json:"destroy"`
}

// DeploymentDetail is a deployment enriched with the names of the app,
// version and policy it refers to
type DeploymentDetail struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	AppName         string     `json:"appName"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Status          string     `json:"status"` // see the Deployment* states
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	PolicyID        string     `json:"policyId,omitempty"`
	PolicyName      string     `json:"policyName,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	// Terraform is set for deployments of terraform versions
	Terraform *TerraformRun `json:"terraform,omitempty"`
}

// ListDeploymentsResponse is the response for listing deployments
type ListDeploymentsResponse struct {
	Deployments []DeploymentDetail `json:"deployments"`
	Total       int                `json:"total"`
	Limit       int                `json:"limit"`
	Offset      int                `json:"offset"`
}

// DeployVersionRequest is the request to deploy a version
type DeployVersionRequest struct {
	Environment string `json:"environment"`
	TriggeredBy string `json:"triggeredBy,omitempty"`
}

// DeployVersionResponse is the response for deploying a version
type DeployVersionResponse struct {
	DeploymentID    string    `json:"deploymentId"`
	VersionID       string    `json:"versionId"`
	Environment     string    `json:"environment"`
	Status          string    `json:"status"`
	GitopsCommitSHA string    `json:"gitopsCommitSha,omitempty"`
	StartedAt       time.Time `json:"startedAt"`
	// Terraform is set for deployments of terraform versions
	Terraform *TerraformRun `json:"terraform,omitempty"`
}

// ApproveDeploymentRequest is the request to approve the plan of a terraform
// deployment. ConfirmDestroy is required if the plan destroys or replaces
// resources.
type ApproveDeploymentRequest struct {
	ConfirmDestroy bool `json:"confirmDestroy,omitempty"`
}

// RejectDeploymentRequest is the request to reject the plan of a terraform
// deployment
type RejectDeploymentRequest struct {
	Reason string `json:"reason,omitempty"`
}

// Overview shows, for every app, what runs in each environment
type Overview struct {
	Environments []string      `json:"environments"`
	Apps         []AppOverview `json:"apps"`
}

// AppOverview is one app's row in the Overview
type AppOverview struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	Targets map[string]TargetState `json:"targets"` // keyed by environment
}

// TargetState describes an app in one environment
type TargetState struct {
	// CurrentVersion is the version of the latest successful deployment
	CurrentVersion string `json:"currentVersion,omitempty"`
	// LastDeployment is the most recent deployment attempt, which may be
	// pending or failed
	LastDeployment *DeploymentDetail `json:"lastDeployment,omitempty"`
}
