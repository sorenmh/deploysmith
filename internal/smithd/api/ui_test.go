package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
)

func TestUIRouting(t *testing.T) {
	get := func(t *testing.T, h http.Handler, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Forwarded-User", "sorenmh")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	t.Run("disabled without user header", func(t *testing.T) {
		s := newStoreTestServer(t)
		s.cfg = &config.Config{}
		s.router = chi.NewRouter()
		s.setupRoutes()

		if code := get(t, s.router, "/ui/"); code != http.StatusNotFound {
			t.Errorf("/ui/ = %d, want 404", code)
		}
	})

	t.Run("served on the API port", func(t *testing.T) {
		s := newStoreTestServer(t)
		s.cfg = &config.Config{UIUserHeader: "X-Forwarded-User"}
		s.router = chi.NewRouter()
		s.setupRoutes()

		if code := get(t, s.router, "/ui/apps"); code != http.StatusOK {
			t.Errorf("/ui/apps = %d, want 200", code)
		}
	})

	t.Run("served only on its own listener", func(t *testing.T) {
		s := newStoreTestServer(t)
		s.cfg = &config.Config{UIUserHeader: "X-Forwarded-User", UIListenAddr: "127.0.0.1:0"}
		s.router = chi.NewRouter()
		s.setupRoutes()

		if s.uiRouter == nil {
			t.Fatal("expected a separate UI router")
		}
		if code := get(t, s.uiRouter, "/ui/apps"); code != http.StatusOK {
			t.Errorf("UI listener /ui/apps = %d, want 200", code)
		}
		if code := get(t, s.uiRouter, "/"); code != http.StatusFound {
			t.Errorf("UI listener / = %d, want redirect", code)
		}
		if code := get(t, s.router, "/ui/apps"); code != http.StatusNotFound {
			t.Errorf("API port /ui/apps = %d, want 404", code)
		}
		if code := get(t, s.uiRouter, "/health"); code != http.StatusNotFound {
			t.Errorf("UI listener /health = %d, want 404", code)
		}
	})
}
