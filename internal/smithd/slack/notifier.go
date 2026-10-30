package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
)

type Notifier struct {
	webhookURL string
}

// NewNotifier returns a Notifier, or nil if webhookURL is empty.
func NewNotifier(webhookURL string) *Notifier {
	if webhookURL == "" {
		return nil
	}
	return &Notifier{webhookURL: webhookURL}
}

// Deploy describes the deployment a message is about
type Deploy struct {
	App         string
	Version     string
	Environment string
	TriggeredBy string // who requested it, for manual deployments
	Policy      string // the auto-deploy policy, if any
	CommitSHA   string
	URL         string // link to the deployment in the web UI, if any
	Rollback    bool   // the version is older than what runs in the environment

	// For GitHub check runs: the deployment, and the repository (owner/name)
	// and commit the version was built from, if known
	DeploymentID string
	SourceRepo   string
	SourceSHA    string

	// Terraform is set for deployments of terraform versions, which are
	// planned, wait for approval and are then applied by OpenTofu, not Flux.
	// Plan is set once there is a plan.
	Terraform bool
	Plan      *PlanSummary
}

// PlanSummary describes a Terraform plan in a notification
type PlanSummary struct {
	Add, Change, Destroy int
	// Resource addresses by action
	Created, Updated, Replaced, Destroyed []string
}

// NoChanges reports whether the plan changes no resources
func (p PlanSummary) NoChanges() bool { return p.Add == 0 && p.Change == 0 && p.Destroy == 0 }

// Destroys reports whether the plan destroys or replaces resources
func (p PlanSummary) Destroys() bool {
	return p.Destroy > 0 || len(p.Destroyed) > 0 || len(p.Replaced) > 0
}

// Counts is the plan's "Plan:" line without the prefix
func (p PlanSummary) Counts() string {
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", p.Add, p.Change, p.Destroy)
}

func (n *Notifier) send(ctx context.Context, text string) {
	if n == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to create Slack request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to send Slack notification: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Slack webhook returned non-2xx status: %d", resp.StatusCode))
	}
}

// Requested reports a deployment that was just requested
func (n *Notifier) Requested(ctx context.Context, d Deploy) {
	by := ""
	switch {
	case d.Policy != "":
		by = fmt.Sprintf(" by policy `%s`", escape(d.Policy))
	case d.TriggeredBy != "":
		by = " by " + escape(d.TriggeredBy)
	}
	if d.Rollback {
		n.send(ctx, fmt.Sprintf(":rewind: *Rollback started*: %s%s%s", target(d), by, link(d)))
		return
	}
	n.send(ctx, fmt.Sprintf(":inbox_tray: *Deployment requested*: %s%s%s", target(d), by, link(d)))
}

// Committed reports a deployment whose manifests are pushed to the gitops
// repository. waitingForFlux says whether a final message will follow.
func (n *Notifier) Committed(ctx context.Context, d Deploy, waitingForFlux bool) {
	next := ""
	switch {
	case waitingForFlux && d.Terraform:
		next = " Waiting for the plan."
	case waitingForFlux:
		next = " Waiting for Flux to apply it."
	}
	what := "Committed to gitops"
	if d.Rollback {
		what = "Rollback committed to gitops"
	}
	n.send(ctx, fmt.Sprintf(":package: *%s*: %s (commit `%s`).%s%s", what, target(d), shortSHA(d.CommitSHA), next, link(d)))
}

// PlanReady reports a Terraform plan that waits for approval
func (n *Notifier) PlanReady(ctx context.Context, d Deploy) {
	if d.Plan == nil {
		return
	}
	text := fmt.Sprintf(":clipboard: *Plan ready for approval*: %s: %s%s", target(d), d.Plan.Counts(), link(d))
	if d.Plan.Destroys() {
		text += "\n:warning: *This plan destroys resources*"
		if list := addresses(append(append([]string(nil), d.Plan.Destroyed...), d.Plan.Replaced...)); list != "" {
			text += ": " + list
		}
	}
	n.send(ctx, text)
}

// Rejected reports a Terraform plan that was rejected, who did and why
func (n *Notifier) Rejected(ctx context.Context, d Deploy, by, reason string) {
	text := fmt.Sprintf(":no_entry_sign: *Plan rejected*: %s by %s", target(d), escape(by))
	if reason != "" {
		text += ": " + escape(reason)
	}
	n.send(ctx, text+link(d))
}

// Applied reports a deployment Flux has applied to the cluster, or for a
// Terraform deployment, that OpenTofu has applied the plan
func (n *Notifier) Applied(ctx context.Context, d Deploy, took time.Duration) {
	if d.Terraform {
		result := "no changes"
		if d.Plan != nil && !d.Plan.NoChanges() {
			result = d.Plan.Counts()
		}
		n.send(ctx, fmt.Sprintf(":white_check_mark: *Applied*: %s, %s%s", target(d), result, link(d)))
		return
	}
	what := "Deployed"
	if d.Rollback {
		what = "Rollback done"
	}
	n.send(ctx, fmt.Sprintf(":white_check_mark: *%s*: %s, applied by Flux in %s%s", what, target(d), took.Round(time.Second), link(d)))
}

// Failed reports a deployment that failed at the given stage
func (n *Notifier) Failed(ctx context.Context, d Deploy, stage, errMsg string) {
	what := "Deployment failed"
	if d.Rollback {
		what = "Rollback failed"
	}
	n.send(ctx, fmt.Sprintf(":x: *%s* (%s): %s%s\n> %s", what, stage, target(d), link(d), strings.ReplaceAll(escape(errMsg), "\n", "\n> ")))
}

// VersionPublished reports a new version that was published
func (n *Notifier) VersionPublished(ctx context.Context, app, version, branch, committer string) {
	detail := ""
	if branch != "" && branch != "unknown" {
		detail = fmt.Sprintf(" on `%s`", escape(branch))
	}
	if committer != "" {
		detail += " by " + escape(committer)
	}
	n.send(ctx, fmt.Sprintf(":sparkles: *New version*: `%s` `%s`%s", escape(app), escape(version), detail))
}

// AppRegistered reports a newly registered application
func (n *Notifier) AppRegistered(ctx context.Context, app string) {
	n.send(ctx, fmt.Sprintf(":new: *New application registered*: `%s`", escape(app)))
}

func target(d Deploy) string {
	return fmt.Sprintf("`%s` `%s` → `%s`", escape(d.App), escape(d.Version), escape(d.Environment))
}

// addresses lists the first resource addresses of a plan
func addresses(list []string) string {
	const shown = 5
	more := len(list) - shown
	if more > 0 {
		list = list[:shown]
	}
	quoted := make([]string, len(list))
	for i, address := range list {
		quoted[i] = "`" + escape(address) + "`"
	}
	text := strings.Join(quoted, ", ")
	if more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func link(d Deploy) string {
	if d.URL == "" {
		return ""
	}
	return fmt.Sprintf(" <%s|View deployment>", d.URL)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// escape makes text safe to embed in a Slack message
func escape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}
