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
