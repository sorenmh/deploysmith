package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

func TestPlanAPI(t *testing.T) {
	s := newStoreTestServer(t)
	if _, err := s.environmentStore.Create(models.CreateEnvironmentRequest{Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	app, err := s.appStore.Create("viino")
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.versionStore.Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	planRuns := store.NewPlanRunStore(s.db)

	router := chi.NewRouter()
	router.Post("/apps/{appId}/versions/{versionId}/plan", s.handlePlanVersion)
	router.Get("/apps/{appId}/versions/{versionId}/plan", s.handleGetVersionPlan)
	router.Get("/plan-runs/{planRunId}", s.handleGetPlanRun)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	// Plan-only runs are off without Terraform and the plans branch
	rec := do(http.MethodPost, "/apps/"+app.ID+"/versions/tf1/plan", `{"environment": "prod"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "terraform_disabled") {
		t.Errorf("plan = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodPost, "/apps/"+app.ID+"/versions/tf1/plan", `not json`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("plan with a bad body = %d: %s", rec.Code, rec.Body)
	}

	// Nothing planned yet
	rec = do(http.MethodGet, "/apps/"+app.ID+"/versions/tf1/plan", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("get plan without environment = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodGet, "/apps/"+app.ID+"/versions/tf1/plan?environment=prod", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("get plan before any run = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodGet, "/plan-runs/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("get unknown run = %d: %s", rec.Code, rec.Body)
	}

	// With a run, by id and by version
	s.svc.PlanRuns = planRuns
	run, _, err := planRuns.Create(app.ID, version, "prod", "main", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planRuns.SetCommitted(run.ID, "cr", "src", "plan-plans-src"); err != nil {
		t.Fatal(err)
	}
	plan := models.TerraformPlan{PlanID: "plan-plans-src", ToAdd: 1, Resources: models.TerraformPlanResources{Create: []string{"a.one"}}, Text: "Plan: 1 to add, 0 to change, 0 to destroy.\n"}
	if _, err := planRuns.Finish(run.ID, models.PlanRunPlanning, models.PlanRunPlanned, "", &plan); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/plan-runs/" + run.ID, "/apps/" + app.ID + "/versions/tf1/plan?environment=prod"} {
		rec = do(http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
		}
		var got models.PlanRun
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != run.ID || got.Status != models.PlanRunPlanned || got.VersionID != "tf1" || got.AppName != "viino" || got.Plan == nil || got.Plan.ToAdd != 1 || got.Plan.Text == "" || got.Plan.Resources.Create[0] != "a.one" {
			t.Errorf("GET %s = %+v, plan %+v", path, got, got.Plan)
		}
	}
}
