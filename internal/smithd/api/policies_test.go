package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestUpdatePolicy(t *testing.T) {
	s := newStoreTestServer(t)

	for _, env := range []string{"staging", "production"} {
		if _, err := s.environmentStore.Create(models.CreateEnvironmentRequest{Name: env}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := s.appStore.Create("hello")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.appStore.Create("other")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.policyStore.Create(app.ID, "main-to-staging", "main", "staging", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.policyStore.Create(app.ID, "release-to-prod", "release/*", "production", true); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Patch("/apps/{appId}/policies/{policyId}", s.handleUpdatePolicy)
	patch := func(appID, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/apps/"+appID+"/policies/"+policy.ID, strings.NewReader(body))
		router.ServeHTTP(rec, req)
		return rec
	}

	t.Run("disable stops matching", func(t *testing.T) {
		rec := patch(app.ID, `{"enabled": false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var resp models.PolicyResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Enabled || resp.Name != "main-to-staging" || resp.GitBranchPattern != "main" || resp.TargetEnvironment != "staging" {
			t.Errorf("unexpected policy after partial update: %+v", resp)
		}

		matching, err := s.policyStore.FindMatchingPolicies(app.ID, "main")
		if err != nil {
			t.Fatal(err)
		}
		if len(matching) != 0 {
			t.Errorf("disabled policy still matches: %+v", matching)
		}
	})

	t.Run("re-enable and retarget", func(t *testing.T) {
		rec := patch(app.ID, `{"enabled": true, "gitBranchPattern": "hotfix/*", "targetEnvironment": "production"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		matching, err := s.policyStore.FindMatchingPolicies(app.ID, "hotfix/123")
		if err != nil {
			t.Fatal(err)
		}
		if len(matching) != 1 || matching[0].TargetEnvironment != "production" {
			t.Errorf("unexpected matching policies: %+v", matching)
		}
	})

	errorCases := []struct {
		name  string
		appID string
		body  string
		want  int
	}{
		{"unknown environment", app.ID, `{"targetEnvironment": "nope"}`, http.StatusBadRequest},
		{"empty name", app.ID, `{"name": " "}`, http.StatusBadRequest},
		{"invalid pattern", app.ID, `{"gitBranchPattern": "[unclosed"}`, http.StatusBadRequest},
		{"duplicate name", app.ID, `{"name": "release-to-prod"}`, http.StatusConflict},
		{"policy of another app", other.ID, `{"enabled": false}`, http.StatusNotFound},
		{"malformed body", app.ID, `{`, http.StatusBadRequest},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := patch(tc.appID, tc.body); rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}
