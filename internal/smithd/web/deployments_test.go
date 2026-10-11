package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestDeploymentsPage(t *testing.T) {
	srv, svc := newTestServer(t)

	_, body := get(t, srv, "/ui/deployments", "alice@example.com")
	requireContains(t, body, "No deployments yet.")

	app := seed(t, svc)
	done, err := svc.DeployVersion(t.Context(), app.ID, "v1", "production", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, srv, "/ui/deployments", "alice@example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	requireContains(t, body,
		`href="/ui/deployments/`+done.ID+`"`,
		`<option value="`+app.ID+`">hello</option>`,
		"alice@example.com",
	)
	if strings.Contains(body, "hx-trigger") {
		t.Error("page polls although nothing is pending")
	}

	// Filters narrow the list
	_, body = get(t, srv, "/ui/deployments?environment=staging", "alice@example.com")
	requireContains(t, body, "No deployments match these filters.", `<option value="staging" selected>`)

	if resp, _ := get(t, srv, "/ui/deployments?status=bogus", "alice@example.com"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid status filter: status = %d, want 400", resp.StatusCode)
	}
}

func TestDeploymentsPollWhilePending(t *testing.T) {
	srv, svc := newTestServer(t)
	app := seed(t, svc)

	// A deployment record without a running worker stays pending
	version, err := svc.Versions.GetByVersionID(app.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Deployments.Create(app.ID, version.ID, "staging", "bob@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/ui/deployments?app="+app.ID, "alice@example.com")
	requireContains(t, body,
		`hx-get="/ui/deployments?app=`+app.ID+`&amp;partial=1"`,
		`hx-trigger="every 3s"`,
		"status--pending",
	)

	// The refresh returns just the table, which stops polling once nothing is pending
	_, fragment := get(t, srv, "/ui/deployments?app="+app.ID+"&partial=1", "alice@example.com")
	if strings.Contains(fragment, "<html") || !strings.Contains(fragment, `id="deployments"`) {
		t.Errorf("partial response is not the table fragment:\n%s", fragment)
	}
	if err := svc.Deployments.UpdateStatus(pending.ID, "failed", "", "push rejected"); err != nil {
		t.Fatal(err)
	}
	_, fragment = get(t, srv, "/ui/deployments?app="+app.ID+"&partial=1", "alice@example.com")
	if strings.Contains(fragment, "hx-trigger") {
		t.Error("fragment keeps polling after the deployment finished")
	}

	// The detail page shows the error
	resp, body := get(t, srv, "/ui/deployments/"+pending.ID, "alice@example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	requireContains(t, body, "push rejected", "bob@example.com", "status--failed")

	if resp, _ := get(t, srv, "/ui/deployments/nope", "alice@example.com"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown deployment: status = %d, want 404", resp.StatusCode)
	}
}
