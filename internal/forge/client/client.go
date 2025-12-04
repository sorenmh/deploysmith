package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DraftVersionRequest is the request body for creating a draft version
type DraftVersionRequest struct {
	Version      string  `json:"version"`
	GitSHA       *string `json:"gitSha,omitempty"`
	GitBranch    *string `json:"gitBranch,omitempty"`
	GitCommitter *string `json:"gitCommitter,omitempty"`
	BuildNumber  *int    `json:"buildNumber,omitempty"`
}

// DraftVersionResponse is the response from creating a draft version
type DraftVersionResponse struct {
	VersionID     string    `json:"versionId"`
	UploadURL     string    `json:"uploadUrl"`
	UploadExpires time.Time `json:"uploadExpires"`
}

// CreateDraftVersion creates a new draft version
func (c *Client) CreateDraftVersion(appName string, req DraftVersionRequest) (*DraftVersionResponse, error) {
	url := fmt.Sprintf("%s/api/v1/apps/%s/versions/draft", c.baseURL, appName)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

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

// PublishVersion publishes a draft version
func (c *Client) PublishVersion(appName, versionID string, noValidate bool) (*PublishVersionResponse, error) {
	url := fmt.Sprintf("%s/api/v1/apps/%s/versions/%s/publish", c.baseURL, appName, versionID)

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
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

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
