package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestManagePolicies(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)
	appURL := "/ui/apps/" + app.ID

	// Create
	resp := postForm(t, srv, appURL+"/policies", "alice@example.com", url.Values{
		"name":              {"release-to-prod"},
		"gitBranchPattern":  {"release/*"},
		"targetEnvironment": {"production"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: status = %d, want 303", resp.StatusCode)
	}
	policies, err := svc.ListPolicies(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var created string
	for _, p := range policies {
		if p.Name == "release-to-prod" {
			created = p.ID
			if p.GitBranchPattern != "release/*" || p.TargetEnvironment != "production" || !p.Enabled {
				t.Errorf("unexpected policy: %+v", p)
			}
		}
	}
	if created == "" {
		t.Fatal("policy was not created")
	}

	// Turn off
	resp = postForm(t, srv, appURL+"/policies/"+created+"/enabled", "alice@example.com", url.Values{"enabled": {"false"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("toggle: status = %d, want 303", resp.StatusCode)
	}
	if matching, _ := svc.Policies.FindMatchingPolicies(app.ID, "release/1"); len(matching) != 0 {
		t.Error("policy still active after turning it off")
	}
	_, body := get(t, srv, appURL, "alice@example.com")
	requireContains(t, body, `<tr class="is-disabled">`, "Turn on")

	// Delete
	resp = postForm(t, srv, appURL+"/policies/"+created+"/delete", "alice@example.com", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: status = %d, want 303", resp.StatusCode)
	}
	if _, err := svc.Policies.GetByID(created); err == nil {
		t.Error("policy still exists after deleting it")
	}
}

func TestCreatePolicyValidationKeepsInput(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)

	// seed already created "main-to-staging"
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/ui/apps/"+app.ID+"/policies", strings.NewReader(url.Values{
		"name":              {"main-to-staging"},
		"gitBranchPattern":  {"hotfix/*"},
		"targetEnvironment": {"production"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(testUserHeader, "alice@example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	requireContains(t, body,
		"Policy &#39;main-to-staging&#39; already exists",
		`value="hotfix/*"`,
		`<option selected>production</option>`,
	)

	// Policies of other apps cannot be changed through this app
	other, err := svc.Apps.Create("other")
	if err != nil {
		t.Fatal(err)
	}
	policies, _ := svc.ListPolicies(app.ID)
	resp = postForm(t, srv, "/ui/apps/"+other.ID+"/policies/"+policies[0].ID+"/delete", "alice@example.com", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-app delete: status = %d, want 404", resp.StatusCode)
	}
}
