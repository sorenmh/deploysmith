package models

import "time"

// Deployment represents a deployment of a version to an environment
type Deployment struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Status          string     `json:"status"` // pending, success, failed
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	PolicyID        *string    `json:"policyId,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

// DeploymentDetail is a deployment enriched with the names of the app,
// version and policy it refers to
type DeploymentDetail struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	AppName         string     `json:"appName"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Status          string     `json:"status"` // pending, success, failed
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	PolicyID        string     `json:"policyId,omitempty"`
	PolicyName      string     `json:"policyName,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
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
