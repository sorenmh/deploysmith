package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

// seed creates environments, an app with a published and a draft version,
// and a policy
func seed(t *testing.T, svc *service.Service) *models.Application {
	t.Helper()
	for _, env := range []string{"production", "staging"} {
		if _, err := svc.Environments.Create(models.CreateEnvironmentRequest{Name: env}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := svc.Apps.Create("hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		id      string
		publish bool
	}{{"v1", true}, {"v2-draft", false}} {
		version, err := svc.Versions.Create(app.ID, v.id, "", models.VersionMetadata{
			GitBranch: "main",
			GitSHA:    "abcdef1234567890",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		if v.publish {
			if err := svc.Versions.UpdateStatus(version.ID, "published"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := svc.CreatePolicy(app.ID, models.CreatePolicyRequest{Name: "main-to-staging", GitBranchPattern: "main", TargetEnvironment: "staging"}); err != nil {
		t.Fatal(err)
	}
	return app
}

// postForm submits a form as a browser would from the same origin
func postForm(t *testing.T, srv *httptest.Server, path, user string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(testUserHeader, user)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func requireContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestAppsOverview(t *testing.T) {
	srv, svc := newTestServer(t)

	_, body := get(t, srv, "/ui/apps", "alice@example.com")
	requireContains(t, body, "No apps yet.")

	app := seed(t, svc)
	if _, err := svc.DeployVersion(t.Context(), app.ID, "v1", "production", "alice@example.com"); err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, srv, "/ui/apps", "alice@example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	requireContains(t, body,
		`<th scope="col">production</th>`,
		`<th scope="col">staging</th>`,
		`href="/ui/apps/`+app.ID+`">hello</a>`,
		`<span class="id">v1</span>`,
		"Not deployed",
	)
}

func TestAppPage(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)

	resp, body := get(t, srv, "/ui/apps/"+app.ID, "alice@example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	requireContains(t, body,
		"<title>hello – deploysmith</title>",
		"abcdef1",         // short SHA
		"v2-draft",        // drafts are listed
		"main-to-staging", // policies are listed
		`hx-confirm="Deploy v1 to production?"`,
	)
	// Only the published version can be deployed
	if strings.Contains(body, "/versions/v2-draft/deploy") {
		t.Error("draft version offers a deploy action")
	}

	if resp, _ := get(t, srv, "/ui/apps/nope", "alice@example.com"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown app: status = %d, want 404", resp.StatusCode)
	}
}

func TestDeployFromUI(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)

	resp := postForm(t, srv, "/ui/apps/"+app.ID+"/versions/v1/deploy", "alice@example.com", url.Values{"environment": {"production"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/ui/apps/"+app.ID+"?deployment=") {
		t.Fatalf("redirected to %q", location)
	}

	// The deployment is attributed to the signed-in user
	deploymentID := strings.TrimPrefix(location, "/ui/apps/"+app.ID+"?deployment=")
	deployment, err := svc.GetDeployment(deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Status != "success" || deployment.TriggeredBy != "alice@example.com" || deployment.Environment != "production" {
		t.Errorf("unexpected deployment: %+v", deployment)
	}

	// The app page reports the outcome and what now runs where
	_, body := get(t, srv, location, "alice@example.com")
	requireContains(t, body, "Deployed <span class=\"id\">v1</span> to production.", "Running in production")
}

func TestDeployFromUIErrors(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)

	tests := []struct {
		name string
		path string
		env  string
		want int
	}{
		{"unknown environment", "/ui/apps/" + app.ID + "/versions/v1/deploy", "nope", http.StatusBadRequest},
		{"draft version", "/ui/apps/" + app.ID + "/versions/v2-draft/deploy", "production", http.StatusBadRequest},
		{"unknown version", "/ui/apps/" + app.ID + "/versions/nope/deploy", "production", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := postForm(t, srv, tt.path, "alice@example.com", url.Values{"environment": {tt.env}})
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}

	// Cross-site form posts are rejected before deploying anything
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/ui/apps/"+app.ID+"/versions/v1/deploy", strings.NewReader("environment=production"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set(testUserHeader, "alice@example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site deploy: status = %d, want 403", resp.StatusCode)
	}
}
