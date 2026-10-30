package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

// reportTimeout bounds one report, which may take a few API calls
const reportTimeout = 20 * time.Second

// DeployCheckName is the name of the check run for deployments to env
func DeployCheckName(env string) string { return "deploysmith/deploy-" + env }

// PlanCheckName is the name of the check run for Terraform plans for env
func PlanCheckName(env string) string { return "deploysmith/plan-" + env }

// Reporter reports deployments and plans as check runs on the commit a
// version was built from. Like the Slack notifier, a nil Reporter is valid
// and does nothing, and failing to report is logged, never returned.
type Reporter struct {
	app *App

	mu   sync.Mutex
	runs map[string]int64 // ids of open check runs, by runKey
}

// NewReporter returns a Reporter for a GitHub App, or nil if the app id or
// private key is empty
func NewReporter(appID, privateKeyPEM, apiURL string) (*Reporter, error) {
	if appID == "" || privateKeyPEM == "" {
		return nil, nil
	}
	app, err := NewApp(appID, privateKeyPEM, apiURL)
	if err != nil {
		return nil, err
	}
	return newReporter(app), nil
}

func newReporter(app *App) *Reporter {
	return &Reporter{app: app, runs: map[string]int64{}}
}

func runKey(run CheckRun) string {
	return strings.Join([]string{run.Repo, run.SHA, run.Name, run.ExternalID}, "\x00")
}

// report creates the check run, or updates it if it exists. The id is
// remembered while the run is open; after a restart the run is found by its
// external id.
func (r *Reporter) report(ctx context.Context, run CheckRun) error {
	key := runKey(run)
	r.mu.Lock()
	id := r.runs[key]
	r.mu.Unlock()

	var err error
	if id == 0 && run.ExternalID != "" {
		if id, err = r.app.FindCheckRun(ctx, run); err != nil {
			return err
		}
	}
	if id != 0 {
		err = r.app.UpdateCheckRun(ctx, id, run)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			id = 0 // deleted meanwhile; start a new one
		} else if err != nil {
			return err
		}
	}
	if id == 0 {
		if id, err = r.app.CreateCheckRun(ctx, run); err != nil {
			return err
		}
	}

	r.mu.Lock()
	if run.Status == StatusCompleted {
		delete(r.runs, key)
	} else {
		r.runs[key] = id
	}
	r.mu.Unlock()
	return nil
}

// send reports run, logging failures
func (r *Reporter) send(ctx context.Context, run CheckRun) {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	err := r.report(ctx, run)
	switch {
	case errors.Is(err, errNotInstalled):
		// Installing the app only on some repositories is a choice
		logging.LogInfoCtx(ctx, "Not reporting to GitHub: "+err.Error())
	case err != nil:
		logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to report %s to GitHub: %v", run.Name, err))
	}
}

// sendDeploy reports a deployment's state, if its version's source is known
func (r *Reporter) sendDeploy(ctx context.Context, d slack.Deploy, status, conclusion, title, summary, text string) {
	if r == nil || d.SourceRepo == "" || d.SourceSHA == "" {
		return
	}
	r.send(ctx, CheckRun{
		Repo:       d.SourceRepo,
		SHA:        d.SourceSHA,
		Name:       DeployCheckName(d.Environment),
		ExternalID: d.DeploymentID,
		Status:     status,
		Conclusion: conclusion,
		Title:      title,
		Summary:    summary,
		Text:       text,
		DetailsURL: d.URL,
	})
}

// Requested reports a deployment that was just requested
func (r *Reporter) Requested(ctx context.Context, d slack.Deploy) {
	title := fmt.Sprintf("Deploying %s to %s", d.Version, d.Environment)
	if d.Rollback {
		title = fmt.Sprintf("Rolling %s back to %s", d.Environment, d.Version)
	}
	r.sendDeploy(ctx, d, StatusQueued, "", title, summary(d, "Requested."), "")
}

// Committed reports a deployment whose manifests are pushed to gitops.
// waitingForFlux says whether a final report will follow; if not, this is
// the final one.
func (r *Reporter) Committed(ctx context.Context, d slack.Deploy, waitingForFlux bool) {
	if d.Terraform {
		r.Planning(ctx, d)
		return
	}
	if waitingForFlux {
		r.sendDeploy(ctx, d, StatusInProgress, "",
			fmt.Sprintf("Waiting for Flux to apply %s to %s", d.Version, d.Environment),
			summary(d, "Committed to gitops, waiting for Flux to apply it."), "")
		return
	}
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionSuccess,
		fmt.Sprintf("Committed %s to %s", d.Version, d.Environment),
		summary(d, "Committed to gitops."), "")
}

// Applied reports a deployment Flux has applied to the cluster
func (r *Reporter) Applied(ctx context.Context, d slack.Deploy, took time.Duration) {
	if d.Terraform {
		r.terraformApplied(ctx, d)
		return
	}
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionSuccess,
		fmt.Sprintf("Deployed %s to %s", d.Version, d.Environment),
		summary(d, fmt.Sprintf("Applied by Flux in %s.", took.Round(time.Second))), "")
}

// Failed reports a deployment that failed at the given stage
func (r *Reporter) Failed(ctx context.Context, d slack.Deploy, stage, errMsg string) {
	if d.Terraform {
		r.terraformFailed(ctx, d, stage, errMsg)
		return
	}
	r.sendDeploy(ctx, d, StatusCompleted, ConclusionFailure,
		fmt.Sprintf("Deployment to %s failed (%s)", d.Environment, stage),
		summary(d, fmt.Sprintf("Failed at %s.", stage)),
		"~~~~\n"+errMsg+"\n~~~~")
}

// summary describes a deployment in markdown
func summary(d slack.Deploy, state string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", state)
	fmt.Fprintf(&b, "- **App**: `%s`\n", d.App)
	version := fmt.Sprintf("`%s`", d.Version)
	if d.Rollback {
		version += " (rollback to an older version)"
	}
	fmt.Fprintf(&b, "- **Version**: %s\n", version)
	fmt.Fprintf(&b, "- **Environment**: `%s`\n", d.Environment)
	switch {
	case d.Policy != "":
		fmt.Fprintf(&b, "- **Triggered by**: auto-deploy policy `%s`\n", d.Policy)
	case d.TriggeredBy != "":
		fmt.Fprintf(&b, "- **Triggered by**: %s\n", d.TriggeredBy)
	}
	if d.CommitSHA != "" {
		fmt.Fprintf(&b, "- **Gitops commit**: `%s`\n", shortSHA(d.CommitSHA))
	}
	if d.URL != "" {
		fmt.Fprintf(&b, "\n[View deployment](%s)\n", d.URL)
	}
	return b.String()
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Plan is the state of a Terraform plan of a version for an environment,
// reported as the check run deploysmith/plan-<env> on the version's commit
type Plan struct {
	Repo        string // the version's source repository (owner/name)
	SHA         string // and commit
	Environment string
	// RunID identifies the plan run (e.g. its deployment id), so later
	// reports update the same check run
	RunID string
	// Status is StatusQueued, StatusInProgress or StatusCompleted, with a
	// Conclusion: ConclusionSuccess, ConclusionNeutral (e.g. changes waiting
	// for approval) or ConclusionFailure
	Status     string
	Conclusion string
	Title      string // e.g. "2 to add, 0 to change, 1 to destroy"
	Summary    string // markdown, with destroys highlighted
	URL        string // the plan in the web UI, optional
}

// ReportPlan creates or updates the check run for a plan. It does nothing
// without the version's source.
func (r *Reporter) ReportPlan(ctx context.Context, p Plan) {
	if r == nil || p.Repo == "" || p.SHA == "" {
		return
	}
	r.send(ctx, CheckRun{
		Repo:       p.Repo,
		SHA:        p.SHA,
		Name:       PlanCheckName(p.Environment),
		ExternalID: p.RunID,
		Status:     p.Status,
		Conclusion: p.Conclusion,
		Title:      p.Title,
		Summary:    p.Summary,
		DetailsURL: p.URL,
	})
}
