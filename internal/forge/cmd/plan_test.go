package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/forge/client"
)

// fakeSmithd is a smithd that plans: StartPlan answers with a run that is
// planning, and the run is what the test sets after that
type fakeSmithd struct {
	mu sync.Mutex
	// start is the response to the request to plan; polls are the responses
	// to asking for the run, the last one repeating
	start    string
	polls    []string
	startErr string
	pollErr  int // number of failing polls before the first poll answers

	started []string // the paths and bodies of requests to plan
	gets    int
	apiKeys []string
}

func (f *fakeSmithd) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apiKeys = append(f.apiKeys, r.Header.Get("X-API-Key"))
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/plan"):
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.started = append(f.started, r.URL.Path+" "+body["environment"])
		if f.startErr != "" {
			http.Error(w, f.startErr, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprint(w, f.start)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/plan-runs/"):
		f.gets++
		if f.gets <= f.pollErr {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		i := min(f.gets-f.pollErr-1, len(f.polls)-1)
		_, _ = fmt.Fprint(w, f.polls[i])
	default:
		http.NotFound(w, r)
	}
}

func newFakeSmithd(t *testing.T) (*fakeSmithd, *client.Client) {
	t.Helper()
	f := &fakeSmithd{start: planningRun}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(server.Close)
	return f, client.NewClient(server.URL, "sk_test")
}

const planningRun = `{"id":"run-1","appId":"app-1","versionId":"tf1","environment":"production","root":"main","status":"planning","startedAt":"2026-10-30T10:00:00Z"}`

func plannedRun(plan string) string {
	return `{"id":"run-1","appId":"app-1","versionId":"tf1","environment":"production","root":"main","status":"planned","startedAt":"2026-10-30T10:00:00Z","plan":` + plan + `}`
}

const (
	planWithDestroys = `{"planId":"plan-plans-abc","toAdd":3,"toChange":1,"toDestroy":2,"destroys":true,"resources":{"create":["a.new"],"update":["b.web"],"replace":["c.rep"],"destroy":["d.old","e.old"]},"text":"Plan: 3 to add, 1 to change, 2 to destroy.\n"}`
	planWithChanges  = `{"planId":"plan-plans-abc","toAdd":1,"toChange":0,"toDestroy":0,"destroys":false,"resources":{"create":["a.new"],"update":[],"replace":[],"destroy":[]}}`
	planNoChanges    = `{"planId":"plan-plans-abc","toAdd":0,"toChange":0,"toDestroy":0,"destroys":false,"resources":{"create":[],"update":[],"replace":[],"destroy":[]}}`
)

func testPlanOptions() planOptions {
	return planOptions{AppID: "app-1", AppName: "infra", Version: "tf1", Environment: "production", Wait: true, Timeout: 5 * time.Second, PollInterval: time.Millisecond}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return ExitOK
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) {
		return ExitFailed
	}
	return exit.Code
}

func TestPlanWaitPrintsTheSummary(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{planningRun, plannedRun(planWithChanges)}

	var out, errOut bytes.Buffer
	err := runPlan(context.Background(), testPlanOptions(), c, &out, &errOut)
	if code := exitCode(t, err); code != ExitOK {
		t.Fatalf("exit code %d (%v), want 0", code, err)
	}
	if len(smithd.started) != 1 || smithd.started[0] != "/api/v1/apps/app-1/versions/tf1/plan production" {
		t.Errorf("started %v", smithd.started)
	}
	for _, key := range smithd.apiKeys {
		if key != "sk_test" {
			t.Errorf("request with API key %q", key)
		}
	}
	if smithd.gets != 2 {
		t.Errorf("asked %d times, want 2", smithd.gets)
	}
	for _, want := range []string{"Planning tf1 for production of infra", "Plan run run-1 started", "Plan of tf1 for production: 1 to add, 0 to change, 0 to destroy", "Created (1):\n  a.new"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("warned about destroys:\n%s", out.String())
	}
}

func TestPlanWaitNoChanges(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{plannedRun(planNoChanges)}

	var out bytes.Buffer
	err := runPlan(context.Background(), testPlanOptions(), c, &out, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitOK {
		t.Fatalf("exit code %d (%v), want 0", code, err)
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestPlanWaitExitsWithTwoForDestroys(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{plannedRun(planWithDestroys)}

	var out bytes.Buffer
	opts := testPlanOptions()
	opts.Full = true
	err := runPlan(context.Background(), opts, c, &out, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitPlanDestroys {
		t.Fatalf("exit code %d (%v), want 2", code, err)
	}
	for _, want := range []string{
		"3 to add, 1 to change, 2 to destroy", "WARNING: this plan destroys or replaces resources.",
		"Destroyed (2):\n  d.old\n  e.old", "Replaced (destroyed and created again) (1):\n  c.rep", "Updated in place (1):\n  b.web",
		"Plan: 3 to add, 1 to change, 2 to destroy.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestPlanWaitFailedPlan(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{`{"id":"run-1","versionId":"tf1","environment":"production","status":"failed","errorMessage":"TerraformPlanFailed: Error: bad provider\n\nEvents of the Terraform CR:\n- x","startedAt":"2026-10-30T10:00:00Z"}`}

	var out bytes.Buffer
	err := runPlan(context.Background(), testPlanOptions(), c, &out, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed {
		t.Fatalf("exit code %d (%v), want 1", code, err)
	}
	if !strings.Contains(err.Error(), "the plan failed: TerraformPlanFailed: Error: bad provider") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(out.String(), "Events of the Terraform CR") {
		t.Errorf("the full error is not printed:\n%s", out.String())
	}
}

func TestPlanWaitSuperseded(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{`{"id":"run-1","versionId":"tf1","environment":"production","status":"superseded","errorMessage":"Replaced by plan run run-2","startedAt":"2026-10-30T10:00:00Z"}`}

	err := runPlan(context.Background(), testPlanOptions(), c, &bytes.Buffer{}, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed || !strings.Contains(err.Error(), "replaced by a newer one") {
		t.Errorf("exit code %d, error %v", code, err)
	}
}

func TestPlanWaitTimesOut(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{planningRun}

	opts := testPlanOptions()
	opts.Timeout = 50 * time.Millisecond
	err := runPlan(context.Background(), opts, c, &bytes.Buffer{}, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed || !strings.Contains(err.Error(), "timed out after 50ms") || !strings.Contains(err.Error(), "run-1") {
		t.Errorf("exit code %d, error %v", code, err)
	}
}

func TestPlanWaitStopsWhenInterrupted(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.polls = []string{planningRun}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runPlan(ctx, testPlanOptions(), c, &bytes.Buffer{}, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed || !strings.Contains(err.Error(), "stopped waiting") {
		t.Errorf("exit code %d, error %v", code, err)
	}
}

func TestPlanWaitSurvivesAFewFailedPolls(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.pollErr = maxPollErrors - 1
	smithd.polls = []string{plannedRun(planNoChanges)}

	var errOut bytes.Buffer
	err := runPlan(context.Background(), testPlanOptions(), c, &bytes.Buffer{}, &errOut)
	if code := exitCode(t, err); code != ExitOK {
		t.Fatalf("exit code %d (%v), want 0", code, err)
	}
	if strings.Count(errOut.String(), "Warning: failed to get plan run run-1") != maxPollErrors-1 {
		t.Errorf("warnings:\n%s", errOut.String())
	}

	smithd2, c2 := newFakeSmithd(t)
	smithd2.pollErr = maxPollErrors
	smithd2.polls = []string{plannedRun(planNoChanges)}
	err = runPlan(context.Background(), testPlanOptions(), c2, &bytes.Buffer{}, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed || !strings.Contains(err.Error(), "failed to get plan run run-1") {
		t.Errorf("exit code %d, error %v", code, err)
	}
}

func TestPlanStartRefused(t *testing.T) {
	smithd, c := newFakeSmithd(t)
	smithd.startErr = `{"error":{"code":"terraform_not_enabled","message":"Terraform is not enabled for infra in production"}}`

	err := runPlan(context.Background(), testPlanOptions(), c, &bytes.Buffer{}, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitFailed {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(err.Error(), "failed to start the plan") {
		t.Errorf("error = %v", err)
	}

	// Without a message in the body the response is shown as it is
	smithd.startErr = "nope"
	err = runPlan(context.Background(), testPlanOptions(), c, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "status 400: nope") {
		t.Errorf("error = %v", err)
	}
}

func TestPlanWithoutWaitStartsAndReturns(t *testing.T) {
	smithd, c := newFakeSmithd(t)

	opts := testPlanOptions()
	opts.Wait = false
	var out bytes.Buffer
	if err := runPlan(context.Background(), opts, c, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if smithd.gets != 0 {
		t.Errorf("asked for the result %d times without --wait", smithd.gets)
	}
	if !strings.Contains(out.String(), "/api/v1/plan-runs/run-1") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestPlanJSONOutput(t *testing.T) {
	t.Run("wait", func(t *testing.T) {
		smithd, c := newFakeSmithd(t)
		smithd.polls = []string{plannedRun(planWithDestroys)}

		opts := testPlanOptions()
		opts.JSON = true
		var out, errOut bytes.Buffer
		err := runPlan(context.Background(), opts, c, &out, &errOut)
		// The exit code is the same, and stdout is only the JSON
		if code := exitCode(t, err); code != ExitPlanDestroys {
			t.Fatalf("exit code %d (%v), want 2", code, err)
		}
		var run client.PlanRun
		if err := json.Unmarshal(out.Bytes(), &run); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out.String())
		}
		if run.ID != "run-1" || run.Status != "planned" || run.Plan == nil || run.Plan.ToDestroy != 2 || !run.Plan.Destroys || len(run.Plan.Resources.Destroy) != 2 {
			t.Errorf("run = %+v, plan = %+v", run, run.Plan)
		}
	})

	t.Run("without wait", func(t *testing.T) {
		_, c := newFakeSmithd(t)
		opts := testPlanOptions()
		opts.JSON, opts.Wait = true, false
		var out bytes.Buffer
		if err := runPlan(context.Background(), opts, c, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		var run client.PlanRun
		if err := json.Unmarshal(out.Bytes(), &run); err != nil || run.Status != "planning" {
			t.Errorf("output %s: %v", out.String(), err)
		}
	})

	t.Run("failed", func(t *testing.T) {
		smithd, c := newFakeSmithd(t)
		smithd.polls = []string{`{"id":"run-1","versionId":"tf1","environment":"production","status":"failed","errorMessage":"boom","startedAt":"2026-10-30T10:00:00Z"}`}
		opts := testPlanOptions()
		opts.JSON = true
		var out bytes.Buffer
		err := runPlan(context.Background(), opts, c, &out, &bytes.Buffer{})
		if code := exitCode(t, err); code != ExitFailed {
			t.Fatalf("exit code %d, want 1", code)
		}
		var run client.PlanRun
		if err := json.Unmarshal(out.Bytes(), &run); err != nil || run.ErrorMessage != "boom" {
			t.Errorf("output %s: %v", out.String(), err)
		}
	})
}

func TestPlanRequiresEnvironmentAndTimeout(t *testing.T) {
	_, c := newFakeSmithd(t)
	opts := testPlanOptions()
	opts.Environment = ""
	if err := runPlan(context.Background(), opts, c, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("planning without an environment succeeded")
	}
	opts = testPlanOptions()
	opts.Timeout = 0
	if err := runPlan(context.Background(), opts, c, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("waiting with no timeout succeeded")
	}
}
