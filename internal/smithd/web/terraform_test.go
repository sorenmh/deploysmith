package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/db/dbtest"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

// fakeDecisions records the approvals and rejections the UI makes, and
// fails them with err
type fakeDecisions struct {
	err      error
	approved []string // deployment id and confirmDestroy
	rejected []string // deployment id and reason
	by       []string // who made them
}

func (f *fakeDecisions) ApproveTerraform(ctx context.Context, id string, confirmDestroy bool) (*models.DeploymentDetail, error) {
	f.by = append(f.by, auth.Actor(ctx))
	if f.err != nil {
		return nil, f.err
	}
	f.approved = append(f.approved, id+" "+map[bool]string{true: "confirmed", false: "unconfirmed"}[confirmDestroy])
	return &models.DeploymentDetail{ID: id}, nil
}

func (f *fakeDecisions) RejectTerraform(ctx context.Context, id, reason string) (*models.DeploymentDetail, error) {
	f.by = append(f.by, auth.Actor(ctx))
	if f.err != nil {
		return nil, f.err
	}
	f.rejected = append(f.rejected, id+" "+reason)
	return &models.DeploymentDetail{ID: id}, nil
}

// terraformSeed is a terraform deployment in the UI's database
type terraformSeed struct {
	app        *models.Application
	deployment *models.Deployment
}

// seedTerraform creates a terraform deployment in status with a plan: one
// that destroys and replaces resources, or a plain one
func seedTerraform(t *testing.T, svc *service.Service, status string, destroys bool) terraformSeed {
	t.Helper()
	app := seed(t, svc)

	version, err := svc.Versions.Create(app.ID, "tf1", models.ArtifactTerraform, models.VersionMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Versions.SetSource(version.ID, models.VersionSource{Repository: "acme/infra", CommitSHA: "0123456789abcdef0123456789abcdef01234567"}); err != nil {
		t.Fatal(err)
	}
	deployment, _, err := svc.Deployments.CreateTerraform(app.ID, version.ID, "production", "main", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Deployments.SetTerraformCommitted(deployment.ID, models.DeploymentPlanning, "cr-sha", "abcdef1234567890", "plan-main-abcdef1234"); err != nil {
		t.Fatal(err)
	}

	plan := models.TerraformPlan{
		PlanID: "plan-main-abcdef1234", PlanHash: "hash-1",
		ToAdd: 2, ToChange: 1,
		Resources: models.TerraformPlanResources{
			Create:  []string{"aws_s3_bucket.logs", `aws_iam_role.app["web"]`},
			Update:  []string{"aws_lambda_function.worker"},
			Replace: []string{},
			Destroy: []string{},
		},
		Text: "Terraform will perform the following actions:\n  # aws_s3_bucket.logs will be created\nPlan: 2 to add, 1 to change, 0 to destroy.\n",
	}
	if destroys {
		plan.ToDestroy = 2
		plan.Destroys = true
		plan.Resources.Replace = []string{"aws_db_instance.main"}
		plan.Resources.Destroy = []string{"aws_s3_bucket.old"}
	}
	if err := svc.TerraformPlans.Save(deployment.ID, plan); err != nil {
		t.Fatal(err)
	}
	if status != models.DeploymentPlanning {
		if err := svc.Deployments.UpdateStatus(deployment.ID, status, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	return terraformSeed{app: app, deployment: deployment}
}

func TestTerraformDeploymentStates(t *testing.T) {
	tests := []struct {
		status   string
		label    string
		polls    bool
		decision bool
	}{
		{models.DeploymentPlanning, "Planning", true, false},
		{models.DeploymentAwaitingApproval, "Awaiting approval", false, true},
		{models.DeploymentApplying, "Applying", true, false},
		{models.DeploymentApplied, "Applied", false, false},
		{models.DeploymentFailed, "Failed", false, false},
		{models.DeploymentStale, "Replaced", false, false},
		{models.DeploymentRejected, "Rejected", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			srv, svc := newTestServer(t)
			s := seedTerraform(t, svc, tt.status, false)

			resp, body := get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.StatusCode, body)
			}
			requireContains(t, body, "status--"+tt.status, ">"+tt.label+"<", "plan-main-abcdef1234", "+2 to add", "~1 to change", "-0 to destroy")
			if polls := strings.Contains(body, `hx-trigger="every 3s"`); polls != tt.polls {
				t.Errorf("polls = %v, want %v", polls, tt.polls)
			}
			if decides := strings.Contains(body, "/approve"); decides != tt.decision {
				t.Errorf("approve form = %v, want %v", decides, tt.decision)
			}
			if rejects := strings.Contains(body, "/reject"); rejects != tt.decision {
				t.Errorf("reject form = %v, want %v", rejects, tt.decision)
			}

			// The deployments list and the app page link to it
			_, list := get(t, srv, "/ui/deployments", "alice@example.com")
			requireContains(t, list, "status--"+tt.status, `href="/ui/deployments/`+s.deployment.ID+`"`, "&#43;2 ~1 -0")
			if needsAction := strings.Contains(list, `class="needs-action"`); needsAction != tt.decision {
				t.Errorf("highlighted in the list = %v, want %v", needsAction, tt.decision)
			}
			if polls := strings.Contains(list, `hx-trigger="every 3s"`); polls != tt.polls {
				t.Errorf("list polls = %v, want %v", polls, tt.polls)
			}
		})
	}
}

func TestTerraformPlanView(t *testing.T) {
	srv, svc := newTestServer(t)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, true)

	_, body := get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body,
		"+2 to add", "~1 to change", "plan-count--destroy", "-2 to destroy",
		"This plan destroys or replaces resources.",
		"<code>aws_s3_bucket.old</code>", "<code>aws_db_instance.main</code>", "<code>aws_lambda_function.worker</code>", "<code>aws_iam_role.app[&#34;web&#34;]</code>",
		"Replace (destroy and create again)",
		`<details class="plan-text">`, "Plan: 2 to add, 1 to change, 0 to destroy.",
		`href="https://github.com/acme/infra/commit/0123456789abcdef0123456789abcdef01234567"`,
		`name="confirm"`, "Type the environment name, production",
	)
	if strings.Index(body, "<code>aws_s3_bucket.old</code>") > strings.Index(body, "<code>aws_s3_bucket.logs</code>") {
		t.Error("destroys are not listed before creates")
	}

	// The list marks destroys
	_, list := get(t, srv, "/ui/deployments", "alice@example.com")
	requireContains(t, list, "plan-summary--destroy", "&#43;2 ~1 -2")

	// A plan without destroys needs no typed confirmation
	srv, svc = newTestServer(t)
	s = seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)
	_, body = get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body, "/approve", "Approve and apply")
	if strings.Contains(body, `name="confirm"`) || strings.Contains(body, "plan-warning") {
		t.Error("a plan without destroys asks for confirmation")
	}
}

func TestTerraformDecisionRecords(t *testing.T) {
	srv, svc := newTestServer(t)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)
	if moved, err := svc.Deployments.Approve(s.deployment.ID, "bob", true); err != nil || !moved {
		t.Fatal(moved, err)
	}
	if err := svc.Deployments.UpdateStatus(s.deployment.ID, models.DeploymentApplied, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.TerraformPlans.SetApplied(s.deployment.ID, "hash-2", true); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body, "The applied plan differed from the reviewed one", "Approved by", "bob", "(destroys confirmed)")

	// Rejected, with a reason; and a stale plan is not shown as a failure
	srv, svc = newTestServer(t)
	s = seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)
	if moved, err := svc.Deployments.Reject(s.deployment.ID, "carol", "wrong bucket"); err != nil || !moved {
		t.Fatal(moved, err)
	}
	_, body = get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body, "Rejected by", "carol", "wrong bucket")

	srv, svc = newTestServer(t)
	s = seedTerraform(t, svc, models.DeploymentPlanning, false)
	if err := svc.Deployments.UpdateStatus(s.deployment.ID, models.DeploymentStale, "", "Stale: the root was deployed again"); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body, "notice--stale", "Stale: the root was deployed again")

	srv, svc = newTestServer(t)
	s = seedTerraform(t, svc, models.DeploymentFailed, false)
	if err := svc.Deployments.UpdateStatus(s.deployment.ID, models.DeploymentFailed, "", "Error: no such bucket"); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, "/ui/deployments/"+s.deployment.ID, "alice@example.com")
	requireContains(t, body, `class="error"`, "Error: no such bucket")
}

func TestTerraformApprove(t *testing.T) {
	decisions := &fakeDecisions{}
	srv, svc := newTestServerWithDecisions(t, dbtest.Open(t), AuthConfig{UserHeader: testUserHeader}, decisions)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)
	id := s.deployment.ID

	resp := postForm(t, srv, "/ui/deployments/"+id+"/approve", "alice@example.com", url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/deployments/"+id {
		t.Fatalf("approve: status = %d, location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(decisions.approved) != 1 || decisions.approved[0] != id+" unconfirmed" || decisions.by[0] != "alice@example.com" {
		t.Errorf("approved %v by %v", decisions.approved, decisions.by)
	}

	resp = postForm(t, srv, "/ui/deployments/"+id+"/reject", "alice@example.com", url.Values{"reason": {" too risky "}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reject: status = %d", resp.StatusCode)
	}
	if len(decisions.rejected) != 1 || decisions.rejected[0] != id+"  too risky " {
		t.Errorf("rejected %q", decisions.rejected)
	}
}

func TestTerraformApproveDestroyConfirmation(t *testing.T) {
	decisions := &fakeDecisions{}
	srv, svc := newTestServerWithDecisions(t, dbtest.Open(t), AuthConfig{UserHeader: testUserHeader}, decisions)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, true)
	path := "/ui/deployments/" + s.deployment.ID + "/approve"

	// Nothing, or the wrong word, does not approve and says why
	for _, typed := range []string{"", "staging", "yes"} {
		body := postBody(t, srv, path, url.Values{"confirm": {typed}}, http.StatusUnprocessableEntity)
		requireContains(t, body, "Type the environment name (production) to approve it.", "plan-count--destroy")
	}
	if len(decisions.approved) != 0 {
		t.Fatalf("approved without confirmation: %v", decisions.approved)
	}

	// The environment name approves, and sends the confirmation to the API
	resp := postForm(t, srv, path, "alice@example.com", url.Values{"confirm": {" production "}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(decisions.approved) != 1 || decisions.approved[0] != s.deployment.ID+" confirmed" {
		t.Errorf("approved %v", decisions.approved)
	}
}

// postBody submits a form as alice and returns the page, which must have status
func postBody(t *testing.T, srv *httptest.Server, path string, form url.Values, status int) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(testUserHeader, "alice@example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, status, body)
	}
	return string(body)
}

func TestTerraformApproveRefused(t *testing.T) {
	decisions := &fakeDecisions{}
	srv, svc := newTestServerWithDecisions(t, dbtest.Open(t), AuthConfig{UserHeader: testUserHeader}, decisions)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)
	path := "/ui/deployments/" + s.deployment.ID + "/approve"

	// The plan changed since the page was loaded
	decisions.err = &service.Error{Kind: service.KindConflict, Code: "plan_not_current", Message: "The controller planned again."}
	body := postBody(t, srv, path, url.Values{}, http.StatusConflict)
	requireContains(t, body, "The plan changed, so nothing was approved.", "The controller planned again.", "Reload the page", `role="alert"`)

	// Other conflicts, e.g. the plan was rejected by someone else meanwhile
	decisions.err = &service.Error{Kind: service.KindConflict, Message: "Deployment is rejected, not awaiting approval."}
	body = postBody(t, srv, path, url.Values{}, http.StatusConflict)
	requireContains(t, body, "Deployment is rejected, not awaiting approval.", "Reload the page")

	// The API's own refusal to approve destroys without confirmation
	decisions.err = &service.Error{Kind: service.KindInvalid, Code: "destroy_confirmation_required", Message: "The plan destroys 1 resource."}
	body = postBody(t, srv, path, url.Values{}, http.StatusUnprocessableEntity)
	requireContains(t, body, "The plan destroys 1 resource.")

	decisions.err = &service.Error{Kind: service.KindNotFound, Message: "Deployment not found"}
	postBody(t, srv, "/ui/deployments/nope/reject", url.Values{}, http.StatusNotFound)
	postBody(t, srv, "/ui/deployments/nope/approve", url.Values{}, http.StatusNotFound)
}

func TestTerraformDecisionsRefuseCrossOriginRequests(t *testing.T) {
	decisions := &fakeDecisions{}
	srv, svc := newTestServerWithDecisions(t, dbtest.Open(t), AuthConfig{UserHeader: testUserHeader}, decisions)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)

	for _, action := range []string{"approve", "reject"} {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/ui/deployments/"+s.deployment.ID+"/"+action, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example.com")
		req.Header.Set(testUserHeader, "alice@example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s from another origin: status = %d, want 403", action, resp.StatusCode)
		}
	}
	if len(decisions.approved)+len(decisions.rejected) != 0 {
		t.Errorf("decisions were made: %v %v", decisions.approved, decisions.rejected)
	}
}

func TestTerraformDecisionWithoutTerraformSupport(t *testing.T) {
	// Through the real service of a smithd without Terraform: its message is
	// shown, and nothing is approved
	srv, svc := newTestServer(t)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)

	body := postBody(t, srv, "/ui/deployments/"+s.deployment.ID+"/approve", url.Values{}, http.StatusUnprocessableEntity)
	requireContains(t, body, "Terraform support is off", "status--awaiting_approval")
}

func TestOverviewLinksTerraformDeployments(t *testing.T) {
	srv, svc := newTestServer(t)
	s := seedTerraform(t, svc, models.DeploymentAwaitingApproval, false)

	_, body := get(t, srv, "/ui/apps", "alice@example.com")
	requireContains(t, body, `href="/ui/deployments/`+s.deployment.ID+`"`, "status--awaiting_approval", "Awaiting approval")

	// Applied is what runs there: no separate attempt is shown
	if err := svc.Deployments.UpdateStatus(s.deployment.ID, models.DeploymentApplied, "", ""); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, "/ui/apps", "alice@example.com")
	if strings.Contains(body, "board-attempt") {
		t.Errorf("an applied deployment is shown as an attempt:\n%s", body)
	}
}
