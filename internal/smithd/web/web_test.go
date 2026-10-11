package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

const testUserHeader = "X-Auth-Request-Email"

// newTestServer serves the UI the way smithd mounts it, backed by a fresh
// SQLite database
func newTestServer(t *testing.T) (*httptest.Server, *service.Service) {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	svc := service.New(service.Deps{
		Apps:         store.NewApplicationStore(database),
		Versions:     store.NewVersionStore(database),
		Deployments:  store.NewDeploymentStore(database),
		Policies:     store.NewPolicyStore(database),
		Environments: store.NewEnvironmentStore(database),
		Publisher:    events.NewMemoryPublisher(),
		Storage:      fakeStorage{},
		Gitops:       &fakeGitops{},
	})

	ui, err := New(svc, AuthConfig{UserHeader: testUserHeader})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Mount(Prefix, ui)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, svc
}

// get fetches a UI path as the given user ("" for anonymous)
func get(t *testing.T, srv *httptest.Server, path, user string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		req.Header.Set(testUserHeader, user)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

type fakeStorage struct{}

func (fakeStorage) GetAllFiles(appName, versionID string, published bool) (map[string][]byte, error) {
	return map[string][]byte{"deployment.yaml": []byte("name: " + appName)}, nil
}

type fakeGitops struct{ err error }

func (f *fakeGitops) Deploy(appName, environment, message string, manifests map[string][]byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "0123456789abcdef", nil
}

func TestLayout(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, body := get(t, srv, "/ui/apps", "alice@example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	for _, want := range []string{"<title>Apps – deploysmith</title>", "Signed in as alice@example.com", `href="/ui/apps" aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestPagesRequireUser(t *testing.T) {
	srv, _ := newTestServer(t)

	if resp, _ := get(t, srv, "/ui/apps", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStaticAssets(t *testing.T) {
	srv, _ := newTestServer(t)
	_, page := get(t, srv, "/ui/apps", "alice@example.com")

	// Every asset referenced by the layout is served, without authentication
	refs := regexp.MustCompile(`(?:href|src)="(/ui/static/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) != 2 {
		t.Fatalf("expected 2 static asset references, found %d", len(refs))
	}
	for _, ref := range refs {
		resp, body := get(t, srv, ref[1], "")
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: status %d, %d bytes", ref[1], resp.StatusCode, len(body))
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("%s: missing long-lived cache header", ref[1])
		}
		if !strings.Contains(ref[1], "?v=") {
			t.Errorf("%s: asset URL is not versioned", ref[1])
		}
	}
}

func TestAllPagesParse(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"apps", "app", "deployments", "deployment", "environments", "error"} {
		if _, ok := pages[name]; !ok {
			t.Errorf("page %q not parsed", name)
		}
	}
}
