package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func (n *Notifier) DeployStarted(ctx context.Context, app, version, env, policy string) {
	n.send(ctx, fmt.Sprintf(":rocket: *Deploy started* — `%s` version `%s` → `%s` (policy: %s)", app, version, env, policy))
}

func (n *Notifier) DeploySucceeded(ctx context.Context, app, version, env, commitSHA string) {
	n.send(ctx, fmt.Sprintf(":white_check_mark: *Deploy succeeded* — `%s` version `%s` → `%s` (commit: `%s`)", app, version, env, commitSHA))
}

func (n *Notifier) DeployFailed(ctx context.Context, app, version, env, errMsg string) {
	n.send(ctx, fmt.Sprintf(":x: *Deploy failed* — `%s` version `%s` → `%s`\nError: %s", app, version, env, errMsg))
}

func (n *Notifier) FluxError(ctx context.Context, app, env, kustomizationName, errMsg string) {
	n.send(ctx, fmt.Sprintf(":warning: *Flux reconciliation error* — `%s` in `%s` (kustomization: `%s`)\nError: %s", app, env, kustomizationName, errMsg))
}
