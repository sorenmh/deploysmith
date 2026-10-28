package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TerraformBackend is the S3 state backend of a Terraform app in an
// environment
type TerraformBackend struct {
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	Region    string `json:"region"`
	LockTable string `json:"lockTable,omitempty"`
}

// TerraformEnvironment is an environment an app has Terraform enabled in
type TerraformEnvironment struct {
	ID             string           `json:"id"`
	AppID          string           `json:"appId"`
	AppName        string           `json:"appName"`
	Environment    string           `json:"environment"`
	Backend        TerraformBackend `json:"backend"`
	AWSRoleARN     string           `json:"awsRoleArn,omitempty"`
	ProvisionedAt  *time.Time       `json:"provisionedAt,omitempty"`
	ProvisionError *string          `json:"provisionError,omitempty"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
}

// EnableTerraformRequest is the request body for enabling Terraform for an
// app in an environment; empty backend fields default to smithd's shared
// backend
type EnableTerraformRequest struct {
	Environment string           `json:"environment"`
	Backend     TerraformBackend `json:"backend"`
	AWSRoleARN  string           `json:"awsRoleArn,omitempty"`
}

// ListTerraformEnvironmentsResponse is the response from listing or syncing
// an app's Terraform environments
type ListTerraformEnvironmentsResponse struct {
	Environments []TerraformEnvironment `json:"environments"`
}

// EnableTerraform enables Terraform for an app in an environment and
// provisions it
func (c *Client) EnableTerraform(appNameOrID string, req EnableTerraformRequest) (*TerraformEnvironment, error) {
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	var env TerraformEnvironment
	if err := c.doJSON(http.MethodPost, fmt.Sprintf("api/v1/apps/%s/terraform/environments", appID), req, http.StatusCreated, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// ListTerraformEnvironments lists the environments an app has Terraform
// enabled in
func (c *Client) ListTerraformEnvironments(appNameOrID string) (*ListTerraformEnvironmentsResponse, error) {
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	var resp ListTerraformEnvironmentsResponse
	if err := c.doJSON(http.MethodGet, fmt.Sprintf("api/v1/apps/%s/terraform/environments", appID), nil, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SyncTerraform provisions an app's Terraform environment again, or all of
// them if environment is empty
func (c *Client) SyncTerraform(appNameOrID, environment string) (*ListTerraformEnvironmentsResponse, error) {
	appID, err := c.resolveToAppID(appNameOrID)
	if err != nil {
		return nil, err
	}

	var resp ListTerraformEnvironmentsResponse
	body := map[string]string{"environment": environment}
	if err := c.doJSON(http.MethodPost, fmt.Sprintf("api/v1/apps/%s/terraform/sync", appID), body, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// doJSON sends body as JSON (if not nil) and decodes a response with
// status want into out
func (c *Client) doJSON(method, path string, body any, want int, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	httpReq, err := http.NewRequest(method, c.joinURL(path), reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &errResp) == nil && errResp.Error.Message != "" {
			return fmt.Errorf("%s", errResp.Error.Message)
		}
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(data))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}
