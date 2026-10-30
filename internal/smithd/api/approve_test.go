package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestApproveAndRejectRoutes(t *testing.T) {
	s := newStoreTestServer(t)
	router := chi.NewRouter()
	router.Post("/deployments/{deploymentId}/approve", s.handleApproveDeployment)
	router.Post("/deployments/{deploymentId}/reject", s.handleRejectDeployment)
	do := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}

	// The body is optional
	for _, path := range []string{"/deployments/nope/approve", "/deployments/nope/reject"} {
		for _, body := range []string{"", `{}`} {
			if rec := do(path, body); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s %q = %d: %s", path, body, rec.Code, rec.Body)
			}
		}
		if rec := do(path, `{`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_request") {
			t.Errorf("POST %s with bad JSON = %d: %s", path, rec.Code, rec.Body)
		}
	}
}
