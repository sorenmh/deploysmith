package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Plan run states
const (
	PlanRunPending    = "pending"
	PlanRunPlanning   = "planning"
	PlanRunPlanned    = "planned"
	PlanRunFailed     = "failed"
	PlanRunSuperseded = "superseded"
)

// PlanResources are the addresses of the resources a plan changes, by action
type PlanResources struct {
	Create  []string `json:"create"`
	Update  []string `json:"update"`
	Replace []string `json:"replace"`
	Destroy []string `json:"destroy"`
}

// Plan is the plan of a plan-only run
type Plan struct {
	PlanID        string        `json:"planId"`
	PlanHash      string        `json:"planHash,omitempty"`
	PlannedAt     *time.Time    `json:"plannedAt,omitempty"`
	ToAdd         int           `json:"toAdd"`
	ToChange      int           `json:"toChange"`
	ToDestroy     int           `json:"toDestroy"`
	Destroys      bool          `json:"destroys"`
	OutputChanges bool          `json:"outputChanges,omitempty"`
	Resources     PlanResources `json:"resources"`
	Text          string        `json:"text,omitempty"`
}

// PlanRun is a plan-only run of a terraform version for an environment
type PlanRun struct {
	ID              string     `json:"id"`
	AppID           string     `json:"appId"`
	AppName         string     `json:"appName,omitempty"`
	VersionID       string     `json:"versionId"`
	Environment     string     `json:"environment"`
	Root            string     `json:"root"`
	Status          string     `json:"status"`
	TriggeredBy     string     `json:"triggeredBy,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	SourceCommitSHA string     `json:"sourceCommitSha,omitempty"`
	GitopsCommitSHA string     `json:"gitopsCommitSha,omitempty"`
	PlanID          string     `json:"planId,omitempty"`
	Plan            *Plan      `json:"plan,omitempty"`
}

// Done reports whether the run has ended
func (r PlanRun) Done() bool {
	return r.Status == PlanRunPlanned || r.Status == PlanRunFailed || r.Status == PlanRunSuperseded
}

// StartPlan starts a plan-only run of a draft or published terraform version
// for an environment
func (c *Client) StartPlan(appID, versionID, environment string) (*PlanRun, error) {
	body, err := json.Marshal(map[string]string{"environment": environment})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequest("POST", c.joinURL(fmt.Sprintf("api/v1/apps/%s/versions/%s/plan", url.PathEscape(appID), url.PathEscape(versionID))), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doPlanRun(req, http.StatusAccepted)
}

// GetPlanRun returns a plan-only run with its plan
func (c *Client) GetPlanRun(id string) (*PlanRun, error) {
	req, err := http.NewRequest("GET", c.joinURL("api/v1/plan-runs/"+url.PathEscape(id)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	return c.doPlanRun(req, http.StatusOK)
}

func (c *Client) doPlanRun(req *http.Request, wantStatus int) (*PlanRun, error) {
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		return nil, &APIError{Status: resp.StatusCode, Body: string(body)}
	}

	var run PlanRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &run, nil
}

// APIError is an error response of smithd
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API returned status %d: %s", e.Status, e.Body)
}
