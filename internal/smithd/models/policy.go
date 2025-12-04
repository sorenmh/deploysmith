package models

import "time"

// Policy represents an auto-deployment policy
type Policy struct {
	ID               string    `json:"id"`
	AppID            string    `json:"appId"`
	Name             string    `json:"name"`
	GitBranchPattern string    `json:"gitBranchPattern"`
	TargetEnvironment string   `json:"targetEnvironment"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"createdAt"`
}

// CreatePolicyRequest is the request to create a new policy
type CreatePolicyRequest struct {
	Name              string `json:"name"`
	GitBranchPattern  string `json:"gitBranchPattern"`
	TargetEnvironment string `json:"targetEnvironment"`
	Enabled           *bool  `json:"enabled,omitempty"` // Optional, defaults to true
}

// PolicyResponse is the response for a single policy
type PolicyResponse struct {
	ID                string    `json:"id"`
	AppID             string    `json:"appId"`
	Name              string    `json:"name"`
	GitBranchPattern  string    `json:"gitBranchPattern"`
	TargetEnvironment string    `json:"targetEnvironment"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"createdAt"`
}

// ListPoliciesResponse is the response for listing policies
type ListPoliciesResponse struct {
	Policies []Policy `json:"policies"`
	Total    int      `json:"total"`
}
