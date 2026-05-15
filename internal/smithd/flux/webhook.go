package flux

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
)

// Reconciler triggers a Flux webhook receiver to start immediate reconciliation.
type Reconciler struct {
	url   string
	token string
}

// NewReconciler creates a Reconciler. Returns nil if url is empty (disabled).
func NewReconciler(url, token string) *Reconciler {
	if url == "" {
		return nil
	}
	return &Reconciler{url: url, token: token}
}

// Trigger sends a POST to the Flux webhook receiver.
// A failure is logged as a warning but does NOT return an error — the deployment
// already succeeded; reconciliation will happen on the next Flux poll interval.
func (r *Reconciler) Trigger(ctx context.Context) {
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader([]byte("{}")))
	if err != nil {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to create Flux webhook request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("X-Flux-Token", r.token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to call Flux webhook: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		logging.LogWarnCtx(ctx, fmt.Sprintf("Flux webhook returned non-2xx status: %d", resp.StatusCode))
		return
	}
	logging.LogInfoCtx(ctx, "Flux reconciliation triggered", logging.Field("status", resp.StatusCode))
}
