package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/storage"
)

// newVersionTestServer returns a store test server with S3 storage backed
// by an in-memory fake
func newVersionTestServer(t *testing.T) *Server {
	t.Helper()
	s := newStoreTestServer(t)

	backend := s3mem.New()
	if err := backend.CreateBucket("versions"); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(fake.Close)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")

	s3Storage, err := storage.NewS3Storage("versions", "eu-central-1", fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.storage = s3Storage
	s.svc.Storage = s3Storage
	return s
}

func (s *Server) versionRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/apps/{appId}/versions/draft", s.handleDraftVersion)
	r.Post("/apps/{appId}/versions/{versionId}/publish", s.handlePublishVersion)
	r.Get("/apps/{appId}/versions", s.handleListVersions)
	r.Get("/apps/{appId}/versions/{versionId}", s.handleGetVersion)
	r.Post("/apps/{appId}/versions/{versionId}/deploy", s.handleDeployVersion)
	return r
}

// terraformBundle returns a packed bundle; with backend settings if broken
func terraformBundle(t *testing.T, broken bool) []byte {
	t.Helper()
	backend := `backend "s3" {}`
	if broken {
		backend = `backend "s3" { bucket = "mine" }`
	}
	archive, err := tfbundle.Pack(map[string][]byte{
		"versions.tf":           []byte("terraform {\n  required_version = \"1.10.6\"\n  " + backend + "\n}\n"),
		"main.tf":               []byte("resource \"null_resource\" \"this\" {}\n"),
		tfbundle.LockFile:       []byte("provider \"registry.opentofu.org/hashicorp/null\" {\n  version = \"3.2.3\"\n}\n"),
		"env/production.tfvars": []byte("name = \"prod\"\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestTerraformVersionAPI(t *testing.T) {
	s := newVersionTestServer(t)
	if _, err := s.environmentStore.Create(models.CreateEnvironmentRequest{Name: "production"}); err != nil {
		t.Fatal(err)
	}
	app, err := s.appStore.Create("infra")
	if err != nil {
		t.Fatal(err)
	}

	router := s.versionRouter()
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(data)
		} else {
			reader = bytes.NewReader(nil)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
		return rec
	}
	draft := func(versionID, artifactType string) *httptest.ResponseRecorder {
		t.Helper()
		return do(http.MethodPost, "/apps/"+app.ID+"/versions/draft", models.DraftVersionRequest{
			VersionID:    versionID,
			ArtifactType: artifactType,
			Metadata: models.VersionMetadata{
				GitSHA:    "abc123",
				GitBranch: "main",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		})
	}
	upload := func(url string, archive []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(archive))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload returned %d", resp.StatusCode)
		}
	}
	errorCode := func(rec *httptest.ResponseRecorder) (string, string) {
		t.Helper()
		var resp ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp.Error.Code, resp.Error.Message
	}

	t.Run("unknown artifact type", func(t *testing.T) {
		if rec := draft("bogus", "helm"); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body)
		}
	})

	t.Run("manifests by default", func(t *testing.T) {
		rec := draft("v1", "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var resp models.DraftVersionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.ArtifactType != models.ArtifactManifests || !strings.Contains(resp.UploadURL, "/manifests.tar.gz") {
			t.Errorf("unexpected draft: %+v", resp)
		}
	})

	rec := draft("abc123", models.ArtifactTerraform)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var drafted models.DraftVersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&drafted); err != nil {
		t.Fatal(err)
	}
	if drafted.ArtifactType != models.ArtifactTerraform || !strings.Contains(drafted.UploadURL, "/"+tfbundle.ArchiveName) {
		t.Fatalf("unexpected draft: %+v", drafted)
	}
	publishPath := "/apps/" + app.ID + "/versions/abc123/publish"

	t.Run("publish without a bundle", func(t *testing.T) {
		rec := do(http.MethodPost, publishPath, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
		}
		if _, message := errorCode(rec); !strings.Contains(message, "No terraform.tar.gz uploaded") {
			t.Errorf("message = %q", message)
		}
	})

	t.Run("publish an invalid bundle", func(t *testing.T) {
		upload(drafted.UploadURL, terraformBundle(t, true))
		rec := do(http.MethodPost, publishPath, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
		}
		code, message := errorCode(rec)
		if code != "validation_failed" || !strings.Contains(message, `backend "s3" has settings`) {
			t.Errorf("error = %s: %q", code, message)
		}
		version, err := s.versionStore.GetByVersionID(app.ID, "abc123")
		if err != nil {
			t.Fatal(err)
		}
		if version.Status != "draft" {
			t.Errorf("rejected version is %s", version.Status)
		}
	})

	t.Run("publish a valid bundle", func(t *testing.T) {
		upload(drafted.UploadURL, terraformBundle(t, false))
		rec := do(http.MethodPost, publishPath, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var resp models.PublishVersionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		want := []string{tfbundle.LockFile, "env/production.tfvars", "main.tf", "versions.tf"}
		if resp.Status != "published" || resp.ArtifactType != models.ArtifactTerraform || !slices.Equal(resp.ManifestFiles, want) {
			t.Errorf("unexpected response: %+v", resp)
		}

		files, err := s.storage.ListFiles("infra", "abc123", true)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(files, []string{tfbundle.ArchiveName}) {
			t.Errorf("published files = %v", files)
		}
	})

	t.Run("get and list show the type", func(t *testing.T) {
		rec := do(http.MethodGet, "/apps/"+app.ID+"/versions/abc123", nil)
		var version models.GetVersionResponse
		if err := json.NewDecoder(rec.Body).Decode(&version); err != nil {
			t.Fatal(err)
		}
		if version.ArtifactType != models.ArtifactTerraform {
			t.Errorf("get: artifactType = %q", version.ArtifactType)
		}

		rec = do(http.MethodGet, "/apps/"+app.ID+"/versions", nil)
		var list models.ListVersionsResponse
		if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		types := map[string]string{}
		for _, v := range list.Versions {
			types[v.VersionID] = v.ArtifactType
		}
		if types["abc123"] != models.ArtifactTerraform || types["v1"] != models.ArtifactManifests {
			t.Errorf("list: artifact types = %v", types)
		}
	})

	t.Run("deploy is refused", func(t *testing.T) {
		rec := do(http.MethodPost, "/apps/"+app.ID+"/versions/abc123/deploy", models.DeployVersionRequest{Environment: "production"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
		}
		if code, message := errorCode(rec); code != "unsupported_artifact_type" || !strings.Contains(message, "terraform") {
			t.Errorf("error = %s: %q", code, message)
		}
	})
}
