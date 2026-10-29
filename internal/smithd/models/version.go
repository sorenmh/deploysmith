package models

import (
	"fmt"
	"regexp"
	"time"
)

// Version represents an application version
type Version struct {
	ID                string     `json:"id"`
	AppID             string     `json:"appId"`
	VersionID         string     `json:"versionId"`
	Status            string     `json:"status"` // draft, published
	GitSHA            string     `json:"gitSha,omitempty"`
	GitBranch         string     `json:"gitBranch,omitempty"`
	GitCommitter      string     `json:"gitCommitter,omitempty"`
	BuildNumber       string     `json:"buildNumber,omitempty"`
	MetadataTimestamp time.Time  `json:"metadataTimestamp,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
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

// VersionSource is where a version was built from, sent by forge from the
// CI environment. smithd reports the version's deployments as check runs on
// this commit in GitHub.
type VersionSource struct {
	Repository  string `json:"repository,omitempty"` // owner/name
	CommitSHA   string `json:"commitSha,omitempty"`  // a pull request's head, not its merge commit
	PullRequest int    `json:"pullRequest,omitempty"`
}

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitSHAPattern  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// Validate checks that the fields that are set are well-formed, since they
// end up in GitHub API paths
func (s VersionSource) Validate() error {
	if s.Repository != "" && !repositoryPattern.MatchString(s.Repository) {
		return fmt.Errorf("source.repository must be owner/name")
	}
	if s.CommitSHA != "" && !commitSHAPattern.MatchString(s.CommitSHA) {
		return fmt.Errorf("source.commitSha must be a full lowercase commit SHA")
	}
	if s.PullRequest < 0 {
		return fmt.Errorf("source.pullRequest must not be negative")
	}
	return nil
}

// Reportable reports whether the repository and commit are known, which
// reporting to GitHub needs
func (s VersionSource) Reportable() bool {
	return s.Repository != "" && s.CommitSHA != ""
}

// DraftVersionRequest is the request to draft a new version
type DraftVersionRequest struct {
	VersionID string          `json:"versionId"`
	Metadata  VersionMetadata `json:"metadata"`
	// Source is optional
	Source *VersionSource `json:"source,omitempty"`
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
	Total    int                     `json:"total"`
	Limit    int                     `json:"limit"`
	Offset   int                     `json:"offset"`
}

// VersionWithDeployment includes deployment information
type VersionWithDeployment struct {
	Version
	DeployedTo []string `json:"deployedTo,omitempty"`
}

// GetVersionResponse is the response for getting a version
type GetVersionResponse struct {
	VersionID     string          `json:"versionId"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"createdAt"`
	PublishedAt   *time.Time      `json:"publishedAt,omitempty"`
	Metadata      VersionMetadata `json:"metadata"`
	Source        *VersionSource  `json:"source,omitempty"`
	ManifestFiles []string        `json:"manifestFiles"`
	DeployedTo    []string        `json:"deployedTo,omitempty"`
}
