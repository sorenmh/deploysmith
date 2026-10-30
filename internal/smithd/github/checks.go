package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"
)

// Check run states
const (
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"

	ConclusionSuccess = "success"
	ConclusionFailure = "failure"
	ConclusionNeutral = "neutral"
)

// CheckRun is the state of a check run on a commit
type CheckRun struct {
	Repo string // owner/name
	SHA  string // the commit it is on
	Name string // e.g. deploysmith/deploy-production
	// ExternalID identifies what the run is about (a deployment id), so the
	// run can be found again to update it
	ExternalID string
	Status     string // StatusQueued, StatusInProgress or StatusCompleted
	Conclusion string // with StatusCompleted
	Title      string
	Summary    string // markdown
	Text       string // markdown, optional details below the summary
	DetailsURL string // optional
}

type checkRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Text    string `json:"text,omitempty"`
}

type checkRunRequest struct {
	Name        string         `json:"name,omitempty"`
	HeadSHA     string         `json:"head_sha,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	Status      string         `json:"status"`
	Conclusion  string         `json:"conclusion,omitempty"`
	StartedAt   string         `json:"started_at,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	DetailsURL  string         `json:"details_url,omitempty"`
	Output      checkRunOutput `json:"output"`
}

func (a *App) request(run CheckRun) checkRunRequest {
	req := checkRunRequest{
		ExternalID: run.ExternalID,
		Status:     run.Status,
		DetailsURL: run.DetailsURL,
		Output:     checkRunOutput{Title: run.Title, Summary: truncate(run.Summary), Text: truncate(run.Text)},
	}
	now := a.now().UTC().Format(time.RFC3339)
	switch run.Status {
	case StatusInProgress:
		req.StartedAt = now
	case StatusCompleted:
		req.Conclusion = run.Conclusion
		req.CompletedAt = now
	}
	return req
}

// maxOutput is GitHub's limit for a check run's summary and text
const maxOutput = 65535

func truncate(markdown string) string {
	if len(markdown) <= maxOutput {
		return markdown
	}
	const note = "\n\n… (truncated)"
	cut := maxOutput - len(note)
	for cut > 0 && !utf8.RuneStart(markdown[cut]) {
		cut--
	}
	return markdown[:cut] + note
}

// CreateCheckRun creates a check run and returns its id
func (a *App) CreateCheckRun(ctx context.Context, run CheckRun) (int64, error) {
	token, err := a.Token(ctx, run.Repo)
	if err != nil {
		return 0, err
	}
	req := a.request(run)
	req.Name = run.Name
	req.HeadSHA = run.SHA
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, http.MethodPost, "/repos/"+run.Repo+"/check-runs", token, req, &resp); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// UpdateCheckRun updates a check run's state
func (a *App) UpdateCheckRun(ctx context.Context, id int64, run CheckRun) error {
	token, err := a.Token(ctx, run.Repo)
	if err != nil {
		return err
	}
	return a.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/check-runs/%d", run.Repo, id), token, a.request(run), nil)
}

// FindCheckRun returns the id of the check run with run's name and external
// id on its commit, or 0 if there is none
func (a *App) FindCheckRun(ctx context.Context, run CheckRun) (int64, error) {
	token, err := a.Token(ctx, run.Repo)
	if err != nil {
		return 0, err
	}
	query := url.Values{"check_name": {run.Name}, "filter": {"all"}, "per_page": {"100"}}
	var resp struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			ExternalID string `json:"external_id"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/commits/%s/check-runs?%s", run.Repo, run.SHA, query.Encode())
	if err := a.do(ctx, http.MethodGet, path, token, nil, &resp); err != nil {
		return 0, err
	}
	for _, r := range resp.CheckRuns {
		if r.ExternalID == run.ExternalID {
			return r.ID, nil
		}
	}
	return 0, nil
}
