package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/storage"
)

func TestDraftVersionSource(t *testing.T) {
	s := newStoreTestServer(t)
	// Presigning needs no server, only credentials
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	var err error
	if s.storage, err = storage.NewS3Storage("versions", "eu-central-1", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	app, err := s.appStore.Create("hello")
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Post("/apps/{appId}/versions/draft", s.handleDraftVersion)
	r.Get("/apps/{appId}/versions/{versionId}", s.handleGetVersion)

	draft := func(versionID string, source *models.VersionSource) *httptest.ResponseRecorder {
		body, _ := json.Marshal(models.DraftVersionRequest{
			VersionID: versionID,
			Metadata:  models.VersionMetadata{GitSHA: "abc", GitBranch: "main", Timestamp: "2026-10-29T10:00:00Z"},
			Source:    source,
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/apps/"+app.ID+"/versions/draft", bytes.NewReader(body)))
		return rec
	}

	source := &models.VersionSource{Repository: "acme/web", CommitSHA: "0123456789abcdef0123456789abcdef01234567", PullRequest: 7}
	if rec := draft("v1", source); rec.Code != http.StatusCreated {
		t.Fatalf("draft: status %d: %s", rec.Code, rec.Body)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/apps/"+app.ID+"/versions/v1", nil))
	var got models.GetVersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Source == nil || *got.Source != *source {
		t.Errorf("source = %+v, want %+v", got.Source, source)
	}

	// Without a source there is none in the response
	if rec := draft("v2", nil); rec.Code != http.StatusCreated {
		t.Fatalf("draft: status %d: %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/apps/"+app.ID+"/versions/v2", nil))
	got = models.GetVersionResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Source != nil {
		t.Errorf("expected no source, got %+v", got.Source)
	}

	// Malformed values end up in GitHub API paths, so they are refused
	for _, bad := range []models.VersionSource{
		{Repository: "acme/web/../x"},
		{Repository: "acme"},
		{CommitSHA: "main"},
		{CommitSHA: "0123456789ABCDEF0123456789ABCDEF01234567"},
		{PullRequest: -1},
	} {
		if rec := draft("bad", &bad); rec.Code != http.StatusBadRequest {
			t.Errorf("source %+v: status %d, want 400", bad, rec.Code)
		}
	}
}
