package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a smithd API client
type Client struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewClient creates a new smithd API client
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// joinURL safely joins a base URL with a path, handling trailing slashes
func (c *Client) joinURL(path string) string {
	return c.baseURL + "/" + strings.TrimLeft(path, "/")
}

// Application represents an application
type Application struct {
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	GitopsRepo      string                       `json:"gitopsRepo"`
	GitopsPath      string                       `json:"gitopsPath"`
	CreatedAt       time.Time                    `json:"createdAt"`
	UpdatedAt       time.Time                    `json:"updatedAt"`
	CurrentVersions map[string]CurrentDeployment `json:"currentVersions,omitempty"`
}

// CurrentDeployment represents the current deployment in an environment
type CurrentDeployment struct {
	VersionID  string    `json:"versionId"`
	DeployedAt time.Time `json:"deployedAt"`
}

// Version represents a version
type Version struct {
	ID           string     `json:"id"`
	AppID        string     `json:"appId"`
	Version      string     `json:"version"`
	Status       string     `json:"status"`
	GitSHA       *string    `json:"gitSha,omitempty"`
	GitBranch    *string    `json:"gitBranch,omitempty"`
	GitCommitter *string    `json:"gitCommitter,omitempty"`
	BuildNumber  *int       `json:"buildNumber,omitempty"`
	Files        []string   `json:"files,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
	Deployments  []string   `json:"deployments,omitempty"`
}

// Deployment represents a deployment
type Deployment struct {
	ID          string    `json:"id"`
	AppID       string    `json:"appId"`
	VersionID   string    `json:"versionId"`
	Environment string    `json:"environment"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Policy represents an auto-deployment policy
type Policy struct {
	ID          string    `json:"id"`
	AppID       string    `json:"appId"`
	Name        string    `json:"name"`
	BranchMatch string    `json:"branchMatch"`
	Environment string    `json:"environment"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

// RegisterApplicationRequest is the request body for registering an application
type RegisterApplicationRequest struct {
	Name string `json:"name"`
}

// RegisterApplication registers a new application
func (c *Client) RegisterApplication(req RegisterApplicationRequest) (*Application, error) {
	url := c.joinURL("api/v1/apps")

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var app Application
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &app, nil
}

// ListApplicationsResponse is the response from listing applications
type ListApplicationsResponse struct {
	Apps       []Application `json:"apps"`
	TotalCount int           `json:"totalCount"`
}

// ListApplications lists all applications
func (c *Client) ListApplications(limit, offset int) (*ListApplicationsResponse, error) {
	u, err := url.Parse(c.joinURL("api/v1/apps"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListApplicationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &listResp, nil
}

// GetApplication gets an application by name
func (c *Client) GetApplication(appName string) (*Application, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s", appName))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var app Application
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &app, nil
}

// ListVersionsResponse is the response from listing versions
type ListVersionsResponse struct {
	Versions   []Version `json:"versions"`
	TotalCount int       `json:"totalCount"`
}

// ListVersions lists all versions for an application
func (c *Client) ListVersions(appName, status string, limit, offset int) (*ListVersionsResponse, error) {
	u, err := url.Parse(c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions", appName)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListVersionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &listResp, nil
}

// GetVersion gets a specific version
func (c *Client) GetVersion(appName, versionID string) (*Version, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s", appName, versionID))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var version Version
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &version, nil
}

// DeployVersionRequest is the request body for deploying a version
type DeployVersionRequest struct {
	Environment string `json:"environment"`
}

// DeployVersionResponse is the response from deploying a version
type DeployVersionResponse struct {
	DeploymentID string `json:"deploymentId"`
}

// DeployVersion deploys a version to an environment
func (c *Client) DeployVersion(appName, versionID, environment string) (*DeployVersionResponse, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s/deploy", appName, versionID))

	req := DeployVersionRequest{
		Environment: environment,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var deployResp DeployVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&deployResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &deployResp, nil
}

// CreatePolicyRequest is the request body for creating a policy
type CreatePolicyRequest struct {
	Name        string `json:"name"`
	BranchMatch string `json:"branchMatch"`
	Environment string `json:"environment"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// CreatePolicy creates a new auto-deployment policy
func (c *Client) CreatePolicy(appName string, req CreatePolicyRequest) (*Policy, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies", appName))

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var policy Policy
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &policy, nil
}

// ListPoliciesResponse is the response from listing policies
type ListPoliciesResponse struct {
	Policies []Policy `json:"policies"`
}

// ListPolicies lists all policies for an application
func (c *Client) ListPolicies(appName string) (*ListPoliciesResponse, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies", appName))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListPoliciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &listResp, nil
}

// DeletePolicy deletes a policy
func (c *Client) DeletePolicy(appName, policyID string) error {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies/%s", appName, policyID))

	httpReq, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
