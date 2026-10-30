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
	token   string // session token from `smithctl login`; used instead of apiKey
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

// WithSessionToken makes the client authenticate as the signed-in user,
// instead of with the API key; an empty token keeps the API key
func (c *Client) WithSessionToken(token string) *Client {
	c.token = token
	return c
}

// setAuth authenticates a request with the session token if there is one,
// else with the API key
func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		return
	}
	req.Header.Set("X-API-Key", c.apiKey)
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
	Version      string     `json:"versionId"`
	Status       string     `json:"status"`
	ArtifactType string     `json:"artifactType,omitempty"` // manifests, terraform
	GitSHA       *string    `json:"gitSha,omitempty"`
	GitBranch    *string    `json:"gitBranch,omitempty"`
	GitCommitter *string    `json:"gitCommitter,omitempty"`
	BuildNumber  *string    `json:"buildNumber,omitempty"`
	Files        []string   `json:"files,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
	Deployments  []string   `json:"deployedTo,omitempty"`
}

// Deployment represents a deployment, as the API returns it with the names
// of its app, version and policy
type Deployment struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	AppName         string     `json:"appName"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Status          string     `json:"status"`
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	PolicyName      string     `json:"policyName,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	// Terraform is set for terraform versions
	Terraform *TerraformRun `json:"terraform,omitempty"`
}

// Policy represents an auto-deployment policy
type Policy struct {
	ID                string    `json:"id"`
	AppID             string    `json:"appId"`
	Name              string    `json:"name"`
	GitBranchPattern  string    `json:"gitBranchPattern"`
	TargetEnvironment string    `json:"targetEnvironment"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Environment represents an environment
type Environment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CreateEnvironmentRequest is the request to create a new environment
type CreateEnvironmentRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateEnvironmentRequest is the request to update an environment
type UpdateEnvironmentRequest struct {
	Description *string `json:"description,omitempty"`
}

// ListEnvironmentsResponse is the response from listing environments
type ListEnvironmentsResponse struct {
	Environments []*Environment `json:"environments"`
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
	c.setAuth(httpReq)

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

	c.setAuth(httpReq)

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

// GetAppIDByName resolves an app name to its app ID
func (c *Client) GetAppIDByName(appName string) (string, error) {
	// List all applications and find the one with matching name
	resp, err := c.ListApplications(100, 0) // Get up to 100 apps
	if err != nil {
		return "", fmt.Errorf("failed to list applications: %w", err)
	}

	for _, app := range resp.Apps {
		if app.Name == appName {
			return app.ID, nil
		}
	}

	return "", fmt.Errorf("application not found: %s", appName)
}

// resolveToAppID resolves an app name or ID to an app ID
// If the input is already an app ID (UUID format), returns it directly
// If the input is an app name, resolves it to the app ID
func (c *Client) resolveToAppID(appNameOrID string) (string, error) {
	// Simple heuristic: if it looks like a UUID (contains hyphens and is ~36 chars), treat as ID
	if len(appNameOrID) > 30 && strings.Contains(appNameOrID, "-") {
		return appNameOrID, nil
	}

	// Otherwise, treat as name and resolve to ID
	return c.GetAppIDByName(appNameOrID)
}

// GetApplication gets an application by name or ID
func (c *Client) GetApplication(appNameOrID string) (*Application, error) {
	// Try to resolve name to ID if it's not already an ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s", appID))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

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
func (c *Client) ListVersions(appNameOrID, status string, limit, offset int) (*ListVersionsResponse, error) {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions", appID)))
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

	c.setAuth(httpReq)

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
func (c *Client) GetVersion(appNameOrID, versionID string) (*Version, error) {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s", appID, versionID))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

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
	DeploymentID    string    `json:"deploymentId"`
	VersionID       string    `json:"versionId"`
	Environment     string    `json:"environment"`
	Status          string    `json:"status"`
	GitopsCommitSHA string    `json:"gitopsCommitSha,omitempty"`
	StartedAt       time.Time `json:"startedAt"`
	// Terraform is set for terraform versions
	Terraform *TerraformRun `json:"terraform,omitempty"`
}

// TerraformRun is what a terraform deployment committed for the controller
type TerraformRun struct {
	Root            string `json:"root"`
	SourceCommitSHA string `json:"sourceCommitSha,omitempty"`
	PlanID          string `json:"planId,omitempty"`
	// Plan is the readable plan, once the controller has made it
	Plan *TerraformPlan `json:"plan,omitempty"`
}

// TerraformPlan is the readable plan of a terraform deployment
type TerraformPlan struct {
	PlanID        string     `json:"planId"`
	PlanHash      string     `json:"planHash,omitempty"`
	PlannedAt     *time.Time `json:"plannedAt,omitempty"`
	ToAdd         int        `json:"toAdd"`
	ToChange      int        `json:"toChange"`
	ToDestroy     int        `json:"toDestroy"`
	Destroys      bool       `json:"destroys"`
	OutputChanges bool       `json:"outputChanges,omitempty"`
	Resources     struct {
		Create  []string `json:"create"`
		Update  []string `json:"update"`
		Replace []string `json:"replace"`
		Destroy []string `json:"destroy"`
	} `json:"resources"`
	Text            string `json:"text,omitempty"`
	AppliedPlanHash string `json:"appliedPlanHash,omitempty"`
	AppliedDiffered bool   `json:"appliedDiffered,omitempty"`
}

// DeployVersion deploys a version to an environment
func (c *Client) DeployVersion(appNameOrID, versionID, environment string) (*DeployVersionResponse, error) {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s/deploy", appID, versionID))

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
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
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
	Name              string `json:"name"`
	GitBranchPattern  string `json:"gitBranchPattern"`
	TargetEnvironment string `json:"targetEnvironment"`
	Enabled           *bool  `json:"enabled,omitempty"`
}

// CreatePolicy creates a new auto-deployment policy
func (c *Client) CreatePolicy(appNameOrID string, req CreatePolicyRequest) (*Policy, error) {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies", appID))

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

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
func (c *Client) ListPolicies(appNameOrID string) (*ListPoliciesResponse, error) {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies", appID))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

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
func (c *Client) DeletePolicy(appNameOrID, policyID string) error {
	// Resolve app name to ID
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return err
	}

	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/policies/%s", appID, policyID))

	httpReq, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

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

// Environment methods

// CreateEnvironment creates a new environment
func (c *Client) CreateEnvironment(req CreateEnvironmentRequest) (*Environment, error) {
	url := c.joinURL("api/v1/environments")

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var env Environment
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &env, nil
}

// ListEnvironments lists all environments
func (c *Client) ListEnvironments() (*ListEnvironmentsResponse, error) {
	url := c.joinURL("api/v1/environments")

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListEnvironmentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &listResp, nil
}

// GetEnvironment gets an environment by ID
func (c *Client) GetEnvironment(environmentID string) (*Environment, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/environments/%s", environmentID))

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var env Environment
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &env, nil
}

// UpdateEnvironment updates an environment
func (c *Client) UpdateEnvironment(environmentID string, req UpdateEnvironmentRequest) (*Environment, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/environments/%s", environmentID))

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var env Environment
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &env, nil
}

// ExportDB exports the full database dump as raw JSON bytes.
// The payload is opaque to the CLI (it is not unmarshalled into a Go
// struct) so smithctl stays decoupled from the server's TableDump shape.
func (c *Client) ExportDB() ([]byte, error) {
	url := c.joinURL("api/v1/admin/db-export")

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ImportDB imports a database dump previously produced by ExportDB.
// The target database must be empty.
func (c *Client) ImportDB(data []byte) error {
	url := c.joinURL("api/v1/admin/db-import")

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteEnvironment deletes an environment
func (c *Client) DeleteEnvironment(environmentID string) error {
	url := c.joinURL(fmt.Sprintf("api/v1/environments/%s", environmentID))

	httpReq, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuth(httpReq)

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
