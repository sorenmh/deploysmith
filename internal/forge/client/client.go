package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// VersionMetadata represents version metadata
type VersionMetadata struct {
	GitSHA       string `json:"gitSha"`
	GitBranch    string `json:"gitBranch"`
	GitCommitter string `json:"gitCommitter"`
	BuildNumber  string `json:"buildNumber"`
	Timestamp    string `json:"timestamp"`
}

// DraftVersionRequest is the request body for creating a draft version
type DraftVersionRequest struct {
	VersionID string          `json:"versionId"`
	Metadata  VersionMetadata `json:"metadata"`
}

// DraftVersionResponse is the response from creating a draft version
type DraftVersionResponse struct {
	VersionID     string    `json:"versionId"`
	UploadURL     string    `json:"uploadUrl"`
	UploadExpires time.Time `json:"uploadExpires"`
}

// CreateDraftVersion creates a new draft version
func (c *Client) CreateDraftVersion(appID string, req DraftVersionRequest) (*DraftVersionResponse, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/draft", appID))

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

	var draftResp DraftVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&draftResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &draftResp, nil
}

// PublishVersionRequest is the request body for publishing a version
type PublishVersionRequest struct {
	NoValidate bool `json:"noValidate,omitempty"`
}

// PublishVersionResponse is the response from publishing a version
type PublishVersionResponse struct {
	VersionID        string   `json:"versionId"`
	Status           string   `json:"status"`
	AutoDeployments  []string `json:"autoDeployments,omitempty"`
}

// AppInfo represents basic app information
type AppInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListAppsResponse represents the response from listing apps
type ListAppsResponse struct {
	Apps []AppInfo `json:"apps"`
}

// GetAppIDByName looks up an app ID by name
func (c *Client) GetAppIDByName(appName string) (string, error) {
	url := c.joinURL("api/v1/apps")

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp ListAppsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Find app by name
	for _, app := range listResp.Apps {
		if app.Name == appName {
			return app.ID, nil
		}
	}

	return "", fmt.Errorf("application '%s' not found", appName)
}

// CreateDraftVersionByName creates a new draft version using app name
func (c *Client) CreateDraftVersionByName(appName string, req DraftVersionRequest) (*DraftVersionResponse, error) {
	appID, err := c.GetAppIDByName(appName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve app name '%s': %w", appName, err)
	}
	return c.CreateDraftVersion(appID, req)
}

// PublishVersion publishes a draft version
func (c *Client) PublishVersion(appName, versionID string, noValidate bool) (*PublishVersionResponse, error) {
	url := c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s/publish", appName, versionID))

	req := PublishVersionRequest{
		NoValidate: noValidate,
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

	var publishResp PublishVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&publishResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &publishResp, nil
}
