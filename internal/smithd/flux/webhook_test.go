package flux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewReconciler_EmptyURL(t *testing.T) {
	r := NewReconciler("", "")
	if r != nil {
		t.Error("expected nil Reconciler when url is empty")
	}
}

func TestNewReconciler_WithURL(t *testing.T) {
	r := NewReconciler("http://example.com/hook", "")
	if r == nil {
		t.Error("expected non-nil Reconciler when url is provided")
	}
}

func TestTrigger_NilReconciler(t *testing.T) {
	// Should be a no-op and not panic
	var r *Reconciler
	r.Trigger(context.Background())
}

func TestTrigger_SendsPOSTWithCorrectHeaders(t *testing.T) {
	var gotMethod string
	var gotContentType string
	var gotToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotToken = r.Header.Get("X-Flux-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	r := NewReconciler(server.URL, "my-token")
	r.Trigger(context.Background())

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", gotContentType)
	}
	if gotToken != "my-token" {
		t.Errorf("expected X-Flux-Token my-token, got %s", gotToken)
	}
}

func TestTrigger_NoTokenHeader(t *testing.T) {
	var gotToken string
	var tokenPresent bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, tokenPresent = r.Header["X-Flux-Token"]
		gotToken = r.Header.Get("X-Flux-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	r := NewReconciler(server.URL, "")
	r.Trigger(context.Background())

	if tokenPresent {
		t.Errorf("expected no X-Flux-Token header, but got: %s", gotToken)
	}
}

func TestTrigger_Non2xxResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r := NewReconciler(server.URL, "")
	// Should not panic and should not return an error
	r.Trigger(context.Background())
}

func TestTrigger_404Response(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := NewReconciler(server.URL, "token")
	// Should handle gracefully without panic
	r.Trigger(context.Background())
}
