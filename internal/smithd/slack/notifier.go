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
	n.send(ctx, fmt.Sprintf(":inbox_tray: *Deployment requested*: %s%s%s", target(d), by, link(d)))
}

// Committed reports a deployment whose manifests are pushed to the gitops
// repository. waitingForFlux says whether a final message will follow.
func (n *Notifier) Committed(ctx context.Context, d Deploy, waitingForFlux bool) {
	next := ""
	if waitingForFlux {
		next = " Waiting for Flux to apply it."
	}
	n.send(ctx, fmt.Sprintf(":package: *Committed to gitops*: %s (commit `%s`).%s%s", target(d), shortSHA(d.CommitSHA), next, link(d)))
}

// Applied reports a deployment Flux has applied to the cluster
func (n *Notifier) Applied(ctx context.Context, d Deploy, took time.Duration) {
	n.send(ctx, fmt.Sprintf(":white_check_mark: *Deployed*: %s, applied by Flux in %s%s", target(d), took.Round(time.Second), link(d)))
}

// Failed reports a deployment that failed at the given stage
func (n *Notifier) Failed(ctx context.Context, d Deploy, stage, errMsg string) {
	n.send(ctx, fmt.Sprintf(":x: *Deployment failed* (%s): %s%s\n> %s", stage, target(d), link(d), escape(errMsg)))
}

func target(d Deploy) string {
	return fmt.Sprintf("`%s` `%s` → `%s`", escape(d.App), escape(d.Version), escape(d.Environment))
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
