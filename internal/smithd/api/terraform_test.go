package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
)

func TestTerraformAPIWithoutOpenBao(t *testing.T) {
	s := newStoreTestServer(t)
	app, err := s.appStore.Create("viino")
	if err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Post("/apps/{appId}/terraform/environments", s.handleEnableTerraform)
	router.Get("/apps/{appId}/terraform/environments", s.handleListTerraformEnvironments)
	router.Post("/apps/{appId}/terraform/sync", s.handleSyncTerraform)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := do(http.MethodPost, "/apps/"+app.ID+"/terraform/environments", `{"environment": "prod"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "terraform_disabled") {
		t.Errorf("enable = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodPost, "/apps/"+app.ID+"/terraform/sync", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "terraform_disabled") {
		t.Errorf("sync = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodGet, "/apps/"+app.ID+"/terraform/environments", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"environments":[]}` {
		t.Errorf("list = %d: %s", rec.Code, rec.Body)
	}
	rec = do(http.MethodGet, "/apps/nope/terraform/environments", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("list of unknown app = %d: %s", rec.Code, rec.Body)
	}
}

func TestTerraformManifestOptionsOpenBaoPort(t *testing.T) {
	for addr, want := range map[string]int{
		"https://openbao.openbao.svc:8200": 8200,
		"https://bao.example.com":          443,
		"http://openbao:80":                80,
		"http://openbao":                   80,
	} {
		opts, err := terraformManifestOptions(&config.Config{OpenBaoAddr: addr})
		if err != nil {
			t.Fatal(err)
		}
		if opts.OpenBaoPort != want {
			t.Errorf("port of %s = %d, want %d", addr, opts.OpenBaoPort, want)
		}
	}
}
