package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// subscriptionRequest is the JSON a browser's PushSubscription serialises to
type subscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (ui *UI) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var req subscriptionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	err := ui.svc.SubscribePush(r.Context(), UserFromContext(r.Context()), store.PushSubscription{
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	})
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ui *UI) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req subscriptionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := ui.svc.UnsubscribePush(strings.TrimSpace(req.Endpoint)); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
