package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestManageEnvironments(t *testing.T) {
	srv, svc := newTestServer(t)

	_, body := get(t, srv, "/ui/environments", "alice@example.com")
	requireContains(t, body, "No environments yet.")

	// Create
	resp := postForm(t, srv, "/ui/environments", "alice@example.com", url.Values{"name": {"staging"}, "description": {"Pre-production"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: status = %d, want 303", resp.StatusCode)
	}
	envs, err := svc.ListEnvironments()
	if err != nil || len(envs) != 1 || envs[0].Name != "staging" || envs[0].Description == nil || *envs[0].Description != "Pre-production" {
		t.Fatalf("unexpected environments: %+v, %v", envs, err)
	}
	id := envs[0].ID

	// Update the description
	resp = postForm(t, srv, "/ui/environments/"+id, "alice@example.com", url.Values{"description": {"Shared staging"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: status = %d, want 303", resp.StatusCode)
	}
	_, body = get(t, srv, "/ui/environments", "alice@example.com")
	requireContains(t, body, `value="Shared staging"`)

	// Invalid and duplicate names are reported on the form
	for _, name := range []string{"../etc", "staging"} {
		resp = postForm(t, srv, "/ui/environments", "alice@example.com", url.Values{"name": {name}})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("create %q: status = %d, want 422", name, resp.StatusCode)
		}
	}

	// Environments in use cannot be deleted, and the page says why
	app, err := svc.RegisterApp("hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreatePolicy(app.ID, models.CreatePolicyRequest{Name: "p", GitBranchPattern: "main", TargetEnvironment: "staging"}); err != nil {
		t.Fatal(err)
	}
	resp = postForm(t, srv, "/ui/environments/"+id+"/delete", "alice@example.com", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("delete in-use: status = %d, want 409", resp.StatusCode)
	}

	// Unused environments can be deleted
	unused, err := svc.CreateEnvironment(models.CreateEnvironmentRequest{Name: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	resp = postForm(t, srv, "/ui/environments/"+unused.ID+"/delete", "alice@example.com", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("delete unused: status = %d, want 303", resp.StatusCode)
	}
	if exists, _ := svc.Environments.Exists("qa"); exists {
		t.Error("environment still exists after deleting it")
	}
}
