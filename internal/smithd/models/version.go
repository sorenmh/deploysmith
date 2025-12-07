package models

import "time"

// Version represents an application version
type Version struct {
	ID                string    `json:"id"`
	AppID             string    `json:"appId"`
	VersionID         string    `json:"versionId"`
	Status            string    `json:"status"` // draft, published
	GitSHA            string    `json:"gitSha,omitempty"`
	GitBranch         string    `json:"gitBranch,omitempty"`
	GitCommitter      string    `json:"gitCommitter,omitempty"`
	BuildNumber       string    `json:"buildNumber,omitempty"`
	MetadataTimestamp time.Time `json:"metadataTimestamp,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
}

// VersionMetadata represents the metadata in version.yml
type VersionMetadata struct {
	GitSHA       string `yaml:"gitSha" json:"gitSha"`
	GitBranch    string `yaml:"gitBranch" json:"gitBranch"`
	GitCommitter string `yaml:"gitCommitter" json:"gitCommitter"`
	BuildNumber  string `yaml:"buildNumber" json:"buildNumber"`
	Timestamp    string `yaml:"timestamp" json:"timestamp"`
}

// DraftVersionRequest is the request to draft a new version
type DraftVersionRequest struct {
	VersionID string          `json:"versionId"`
	Metadata  VersionMetadata `json:"metadata"`
}

// DraftVersionResponse is the response for drafting a version
type DraftVersionResponse struct {
	VersionID     string    `json:"versionId"`
	UploadURL     string    `json:"uploadUrl"`
	UploadExpires time.Time `json:"uploadExpires"`
	Status        string    `json:"status"`
}

// PublishVersionResponse is the response for publishing a version
type PublishVersionResponse struct {
	VersionID     string    `json:"versionId"`
	Status        string    `json:"status"`
	PublishedAt   time.Time `json:"publishedAt"`
	ManifestFiles []string  `json:"manifestFiles"`
}

// ListVersionsResponse is the response for listing versions
type ListVersionsResponse struct {
	Versions []VersionWithDeployment `json:"versions"`
	Total    int                      `json:"total"`
	Limit    int                      `json:"limit"`
	Offset   int                      `json:"offset"`
}

// VersionWithDeployment includes deployment information
type VersionWithDeployment struct {
	Version
	DeployedTo []string `json:"deployedTo,omitempty"`
}

// GetVersionResponse is the response for getting a version
type GetVersionResponse struct {
	VersionID         string    `json:"versionId"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
	Metadata          VersionMetadata `json:"metadata"`
	ManifestFiles     []string  `json:"manifestFiles"`
	DeployedTo        []string  `json:"deployedTo,omitempty"`
}
