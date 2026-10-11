package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// newStoreTestServer returns a Server backed by a fresh SQLite database,
// with only the stores wired up
func newStoreTestServer(t *testing.T) *Server {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	return &Server{
		appStore:         store.NewApplicationStore(database),
		versionStore:     store.NewVersionStore(database),
		deploymentStore:  store.NewDeploymentStore(database),
		policyStore:      store.NewPolicyStore(database),
		environmentStore: store.NewEnvironmentStore(database),
	}
}

func (s *Server) testRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/deployments", s.handleListDeployments)
	r.Get("/deployments/{deploymentId}", s.handleGetDeployment)
	return r
}

func TestDeploymentsAPI(t *testing.T) {
	s := newStoreTestServer(t)

	app, err := s.appStore.Create("hello")
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.versionStore.Create(app.ID, "v1.2.3", models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.policyStore.Create(app.ID, "main-to-prod", "main", "production", true)
	if err != nil {
		t.Fatal(err)
	}

	succeeded, err := s.deploymentStore.Create(app.ID, version.ID, "production", "auto-deploy", &policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.deploymentStore.UpdateStatus(succeeded.ID, "success", "abc123", ""); err != nil {
		t.Fatal(err)
	}
	// Left pending: gitops_commit_sha and error_message stay NULL
	pending, err := s.deploymentStore.Create(app.ID, version.ID, "staging", "alice@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	router := s.testRouter()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	t.Run("list all", func(t *testing.T) {
		rec := get("/deployments")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var resp models.ListDeploymentsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Total != 2 || len(resp.Deployments) != 2 {
			t.Fatalf("got %d deployments (total %d), want 2", len(resp.Deployments), resp.Total)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		rec := get("/deployments?status=success&appId=" + app.ID)
		var resp models.ListDeploymentsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Total != 1 || resp.Deployments[0].ID != succeeded.ID {
			t.Fatalf("unexpected result: %+v", resp)
		}
		d := resp.Deployments[0]
		if d.AppName != "hello" || d.VersionID != "v1.2.3" || d.PolicyName != "main-to-prod" || d.GitopsCommitSHA != "abc123" {
			t.Errorf("details not joined correctly: %+v", d)
		}
	})

	t.Run("filter by environment", func(t *testing.T) {
		rec := get("/deployments?environment=staging")
		var resp models.ListDeploymentsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Total != 1 || resp.Deployments[0].ID != pending.ID {
			t.Fatalf("unexpected result: %+v", resp)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		if rec := get("/deployments?status=bogus"); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("get pending", func(t *testing.T) {
		rec := get("/deployments/" + pending.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var d models.DeploymentDetail
		if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
			t.Fatal(err)
		}
		if d.Status != "pending" || d.TriggeredBy != "alice@example.com" || d.CompletedAt != nil {
			t.Errorf("unexpected deployment: %+v", d)
		}
	})

	t.Run("get missing", func(t *testing.T) {
		if rec := get("/deployments/nope"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}
