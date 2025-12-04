package models

import "time"

type Service struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	CurrentVersion  string `json:"current_version,omitempty"`
	ManifestPath    string `json:"manifest_path"`
	ImageRepository string `json:"image_repository"`
}

type ImageVersion struct {
	Tag       string    `json:"tag"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"created_at"`
	Deployed  bool      `json:"deployed"`
}

type Deployment struct {
	ID         string    `json:"id"`
	ServiceName string   `json:"service"`
	Version    string    `json:"version"`
	DeployedAt time.Time `json:"deployed_at"`
	DeployedBy string    `json:"deployed_by"`
	GitCommit  string    `json:"git_commit"`
	Status     string    `json:"status"` // pending, success, failed
	Type       string    `json:"type"`   // deploy, rollback
	Message    string    `json:"message,omitempty"`
}

type DeployRequest struct {
	Version    string `json:"version" binding:"required"`
	DeployedBy string `json:"deployed_by" binding:"required"`
	Message    string `json:"message"`
}

type RollbackRequest struct {
	Version    string `json:"version"`
	DeployedBy string `json:"deployed_by" binding:"required"`
}

type WebhookRequest struct {
	Service    string `json:"service" binding:"required"`
	Version    string `json:"version" binding:"required"`
	Image      string `json:"image" binding:"required"`
	GitSHA     string `json:"git_sha"`
	AutoDeploy bool   `json:"auto_deploy"`
}

type HealthResponse struct {
	Status            string `json:"status"`
	Version           string `json:"version"`
	GitRepoAccessible bool   `json:"git_repo_accessible"`
	DatabaseAccessible bool  `json:"database_accessible"`
}

// Service Abstraction Layer API Models

type GenerateManifestsRequest struct {
	ServiceDefinition *ServiceDefinition `json:"service_definition" binding:"required"`
}

type GenerateManifestsResponse struct {
	ServiceName string            `json:"service_name"`
	Manifests   map[string]string `json:"manifests"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type ValidateServiceRequest struct {
	ServiceDefinition *ServiceDefinition `json:"service_definition" binding:"required"`
}

type ValidateServiceResponse struct {
	Valid            bool              `json:"valid"`
	Errors           []ValidationError `json:"errors,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
	ValidationSummary string           `json:"validation_summary"`
	Validated        time.Time         `json:"validated"`
}

type ErrorResponse struct {
	Error   string    `json:"error"`
	Details string    `json:"details,omitempty"`
	Time    time.Time `json:"time"`
}

type DeployServiceRequest struct {
	ServiceDefinition *ServiceDefinition `json:"service_definition" binding:"required"`
	TargetDirectory   string             `json:"target_directory,omitempty"` // Optional, defaults to "services/{service_name}"
	DeployedBy        string             `json:"deployed_by" binding:"required"`
	Message           string             `json:"message,omitempty"`
}

type DeployServiceResponse struct {
	ServiceName     string            `json:"service_name"`
	TargetDirectory string            `json:"target_directory"`
	Manifests       map[string]string `json:"manifests"`
	GitCommit       string            `json:"git_commit"`
	DeployedAt      time.Time         `json:"deployed_at"`
	DeployedBy      string            `json:"deployed_by"`
}
