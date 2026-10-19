// Package push delivers notifications to browsers and installed web apps
// (including iPhone home screen apps) with Web Push.
package push

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// Subscriptions is where the browsers that opted in are kept
type Subscriptions interface {
	List() ([]store.PushSubscription, error)
	Delete(endpoint string) error
}

// Notifier sends a push notification to every subscribed browser. Like the
// Slack notifier, a nil Notifier is valid and does nothing.
type Notifier struct {
	subs       Subscriptions
	publicKey  string
	privateKey string
	subject    string
	client     *http.Client
}

// NewNotifier returns a Notifier, or nil if the VAPID keys are not set.
// subject is a mailto: or https: URL that identifies the sender to push services.
func NewNotifier(subs Subscriptions, publicKey, privateKey, subject string) *Notifier {
	if publicKey == "" || privateKey == "" {
		return nil
	}
	return &Notifier{
		subs:       subs,
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    subject,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// PublicKey returns the key browsers subscribe with, or "" if push is disabled
func (n *Notifier) PublicKey() string {
	if n == nil {
		return ""
	}
	return n.publicKey
}

type message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

func (n *Notifier) send(ctx context.Context, msg message) {
	if n == nil {
		return
	}
	subs, err := n.subs.List()
	if err != nil {
		logging.LogWarnCtx(ctx, "Failed to list push subscriptions: "+err.Error())
		return
	}
	payload, _ := json.Marshal(msg)

	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.sendTo(ctx, sub, payload)
		}()
	}
	wg.Wait()
}

func (n *Notifier) sendTo(ctx context.Context, sub store.PushSubscription, payload []byte) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      n.client,
		Subscriber:      n.subject,
		VAPIDPublicKey:  n.publicKey,
		VAPIDPrivateKey: n.privateKey,
		TTL:             3600,
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		logging.LogWarnCtx(ctx, "Failed to send push notification: "+err.Error())
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
		// The browser unsubscribed or the app was removed
		if err := n.subs.Delete(sub.Endpoint); err != nil {
			logging.LogWarnCtx(ctx, "Failed to delete expired push subscription: "+err.Error())
		}
	case resp.StatusCode >= 300:
		logging.LogWarnCtx(ctx, fmt.Sprintf("Push service returned non-2xx status: %d", resp.StatusCode))
	}
}

// Test sends a notification to every subscription, to check the setup
func (n *Notifier) Test(ctx context.Context) {
	n.send(ctx, message{Title: "deploysmith", Body: "Notifications are working.", Tag: "test"})
}

// Requested reports a deployment that was just requested
func (n *Notifier) Requested(ctx context.Context, d slack.Deploy) {
	title := "Deployment requested"
	if d.Rollback {
		title = "Rollback started"
	}
	by := ""
	switch {
	case d.Policy != "":
		by = " (policy " + d.Policy + ")"
	case d.TriggeredBy != "":
		by = " (by " + d.TriggeredBy + ")"
	}
	n.send(ctx, message{Title: title, Body: target(d) + by, URL: d.URL, Tag: tag(d)})
}

// Committed reports a deployment whose manifests are pushed to gitops
func (n *Notifier) Committed(ctx context.Context, d slack.Deploy, waitingForFlux bool) {
	title := "Committed to gitops"
	if d.Rollback {
		title = "Rollback committed to gitops"
	}
	body := target(d)
	if waitingForFlux {
		body += ". Waiting for Flux."
	}
	n.send(ctx, message{Title: title, Body: body, URL: d.URL, Tag: tag(d)})
}

// Applied reports a deployment Flux has applied to the cluster
func (n *Notifier) Applied(ctx context.Context, d slack.Deploy, took time.Duration) {
	title := "Deployed"
	if d.Rollback {
		title = "Rollback done"
	}
	n.send(ctx, message{Title: title, Body: fmt.Sprintf("%s, applied in %s", target(d), took.Round(time.Second)), URL: d.URL, Tag: tag(d)})
}

// Failed reports a deployment that failed at the given stage
func (n *Notifier) Failed(ctx context.Context, d slack.Deploy, stage, errMsg string) {
	title := "Deployment failed"
	if d.Rollback {
		title = "Rollback failed"
	}
	n.send(ctx, message{Title: title, Body: fmt.Sprintf("%s (%s): %s", target(d), stage, errMsg), URL: d.URL, Tag: tag(d)})
}

// VersionPublished reports a new version that was published
func (n *Notifier) VersionPublished(ctx context.Context, app, version, branch, committer string) {
	body := app + " " + version
	if branch != "" && branch != "unknown" {
		body += " on " + branch
	}
	if committer != "" {
		body += " by " + committer
	}
	n.send(ctx, message{Title: "New version", Body: body})
}

// AppRegistered reports a newly registered application
func (n *Notifier) AppRegistered(ctx context.Context, app string) {
	n.send(ctx, message{Title: "New application registered", Body: app})
}

func target(d slack.Deploy) string {
	return fmt.Sprintf("%s %s → %s", d.App, d.Version, d.Environment)
}

// tag makes the messages about one deployment replace each other, so the
// lock screen shows its latest state
func tag(d slack.Deploy) string {
	return "deploy:" + d.App + ":" + d.Environment
}
