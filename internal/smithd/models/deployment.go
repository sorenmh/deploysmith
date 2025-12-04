package models

import "time"

// Deployment represents a deployment of a version to an environment
type Deployment struct {
	ID               string     `json:"id"`
	AppID            string     `json:"appId"`
	VersionID        string     `json:"versionId"`
	Environment      string     `json:"environment"`
	Status           string     `json:"status"` // pending, success, failed
	TriggeredBy      string     `json:"triggeredBy,omitempty"`
	PolicyID         *string    `json:"policyId,omitempty"`
	GitopsCommitSHA  string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage     string     `json:"errorMessage,omitempty"`
	StartedAt        time.Time  `json:"startedAt"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
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

// ListDeploymentsResponse is the response for listing deployments
type ListDeploymentsResponse struct {
	Deployments []Deployment `json:"deployments"`
	Total       int          `json:"total"`
	Limit       int          `json:"limit"`
	Offset      int          `json:"offset"`
}
