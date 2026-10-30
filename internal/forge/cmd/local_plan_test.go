package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/sorenmh/deploysmith/internal/shared/config"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
)

// localSmithd is a fakeSmithd that also makes the temporary versions of
// local plans, and records how the requests were authenticated
type localSmithd struct {
	*fakeSmithd
	mu       sync.Mutex
	prepared []map[string]string // the bodies of requests for a local plan
	auth     []string            // the Authorization headers of all requests
	apiKeys  []string            // the X-API-Key headers of all requests
	refuse   string              // if set, local plans are refused with this body
}

func newLocalSmithd(t *testing.T) (*localSmithd, *client.Client) {
	t.Helper()
	l := &localSmithd{fakeSmithd: &fakeSmithd{start: planningRun}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.auth = append(l.auth, r.Header.Get("Authorization"))
		l.apiKeys = append(l.apiKeys, r.Header.Get("X-API-Key"))
		l.mu.Unlock()

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/terraform/local-plans") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			l.mu.Lock()
			l.prepared = append(l.prepared, body)
			l.mu.Unlock()
			if l.refuse != "" {
				http.Error(w, l.refuse, http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"versionId":"local-alice-%s","uploadUrl":"https://s3.example.com/upload","uploadExpires":"2026-10-30T10:05:00Z","expiresAt":"2026-10-31T10:00:00Z"}`, body["contentHash"][:12])
			return
		}
		l.handler(w, r)
	}))
	t.Cleanup(server.Close)
	return l, client.NewClient(server.URL, "").WithSessionToken("session-token")
}

// uploads records what a local plan uploads
type uploads struct {
	urls  []string
	names []string
	files [][]byte
	err   error
}

func (u *uploads) upload(url, name string, content []byte) error {
	if u.err != nil {
		return u.err
	}
	u.urls = append(u.urls, url)
	u.names = append(u.names, name)
	u.files = append(u.files, content)
	return nil
}

// writeBundle writes an OpenTofu root module to a new directory
func writeBundle(t *testing.T, mainTF string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"main.tf":               mainTF,
		tfbundle.LockFile:       "# no providers\n",
		"env/production.tfvars": "name = \"prod\"\n",
		"notes.md":              "not part of the bundle\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const bundleMainTF = "terraform {\n  required_version = \"1.13.2\"\n}\n\nresource \"terraform_data\" \"x\" {}\n"

func TestLocalPlanPacksUploadsAndPlans(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	smithd.polls = []string{planningRun, plannedRun(planWithChanges)}
	up := &uploads{}
	dir := writeBundle(t, bundleMainTF)

	var out, errOut bytes.Buffer
	err := runLocalPlan(context.Background(), testPlanOptions(), dir, c, up.upload, &out, &errOut)
	if code := exitCode(t, err); code != ExitOK {
		t.Fatalf("exit code %d (%v), want 0", code, err)
	}

	// The bundle is packed like forge upload --terraform does
	files, err := tfbundle.Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := tfbundle.Validate(files)
	if err != nil {
		t.Fatal(err)
	}
	wantArchive, err := tfbundle.Pack(bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(up.files) != 1 || !bytes.Equal(up.files[0], wantArchive) || up.names[0] != tfbundle.ArchiveName || up.urls[0] != "https://s3.example.com/upload" {
		t.Fatalf("uploads: %v %v %d files", up.urls, up.names, len(up.files))
	}
	packed, err := tfbundle.Read(bytes.NewReader(up.files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := packed["notes.md"]; ok || len(packed) != 3 {
		t.Errorf("packed files: %v", packed)
	}

	// The temporary version is named after the content and then planned
	hash := tfbundle.ContentHash(bundle.Files)
	if len(smithd.prepared) != 1 || smithd.prepared[0]["environment"] != "production" || smithd.prepared[0]["contentHash"] != hash {
		t.Errorf("prepared %v, want the hash %s", smithd.prepared, hash)
	}
	wantStart := "/api/v1/apps/app-1/versions/local-alice-" + hash[:12] + "/plan production"
	if len(smithd.started) != 1 || smithd.started[0] != wantStart {
		t.Errorf("started %v, want %s", smithd.started, wantStart)
	}

	// Everything is done as the signed-in user, never with an API key
	for _, header := range smithd.auth {
		if header != "Bearer session-token" {
			t.Errorf("request with Authorization %q", header)
		}
	}
	for _, key := range smithd.apiKeys {
		if key != "" {
			t.Errorf("request with API key %q", key)
		}
	}

	for _, want := range []string{"Creating terraform bundle from " + dir, "3 files, OpenTofu 1.13.2", "Uploading terraform bundle as local-alice-" + hash[:12], "Plan of local-alice-" + hash[:12] + " for production: 1 to add"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestLocalPlanIDIsDeterministic(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	smithd.polls = []string{plannedRun(planNoChanges)}

	hashes := func(dir string) string {
		t.Helper()
		before := len(smithd.prepared)
		err := runLocalPlan(context.Background(), testPlanOptions(), dir, c, (&uploads{}).upload, &bytes.Buffer{}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if len(smithd.prepared) != before+1 {
			t.Fatalf("%d requests for a local plan, want %d", len(smithd.prepared), before+1)
		}
		return smithd.prepared[before]["contentHash"]
	}

	first, same, other := hashes(writeBundle(t, bundleMainTF)), hashes(writeBundle(t, bundleMainTF)), hashes(writeBundle(t, bundleMainTF+"\n# changed\n"))
	if first != same {
		t.Errorf("the same files gave the hashes %s and %s", first, same)
	}
	if first == other {
		t.Error("changed files gave the same hash")
	}
}

func TestLocalPlanExitCodes(t *testing.T) {
	tests := []struct {
		name string
		poll string
		want int
	}{
		{"destroys", plannedRun(planWithDestroys), ExitPlanDestroys},
		{"changes", plannedRun(planWithChanges), ExitOK},
		{"failed", `{"id":"run-1","status":"failed","errorMessage":"Error: boom","startedAt":"2026-10-30T10:00:00Z"}`, ExitFailed},
		{"replaced", `{"id":"run-1","status":"superseded","errorMessage":"Replaced by plan run run-2","startedAt":"2026-10-30T10:00:00Z"}`, ExitFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			smithd, c := newLocalSmithd(t)
			smithd.polls = []string{tt.poll}
			err := runLocalPlan(context.Background(), testPlanOptions(), writeBundle(t, bundleMainTF), c, (&uploads{}).upload, &bytes.Buffer{}, &bytes.Buffer{})
			if code := exitCode(t, err); code != tt.want {
				t.Errorf("exit code %d (%v), want %d", code, err, tt.want)
			}
		})
	}
}

func TestLocalPlanJSONOutput(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	smithd.polls = []string{plannedRun(planWithChanges)}
	opts := testPlanOptions()
	opts.JSON = true

	var out bytes.Buffer
	err := runLocalPlan(context.Background(), opts, writeBundle(t, bundleMainTF), c, (&uploads{}).upload, &out, &bytes.Buffer{})
	if code := exitCode(t, err); code != ExitOK {
		t.Fatalf("exit code %d (%v), want 0", code, err)
	}
	var run client.PlanRun
	if err := json.Unmarshal(out.Bytes(), &run); err != nil {
		t.Fatalf("output is not only JSON: %v\n%s", err, out.String())
	}
	if run.Status != client.PlanRunPlanned || run.Plan == nil || run.Plan.ToAdd != 1 {
		t.Errorf("run: %+v", run)
	}
}

func TestLocalPlanInvalidBundleSendsNothing(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	dir := writeBundle(t, bundleMainTF)
	if err := os.Remove(filepath.Join(dir, tfbundle.LockFile)); err != nil {
		t.Fatal(err)
	}

	up := &uploads{}
	err := runLocalPlan(context.Background(), testPlanOptions(), dir, c, up.upload, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), tfbundle.LockFile) {
		t.Fatalf("error = %v, want the problem with the lock file", err)
	}
	if len(smithd.prepared) != 0 || len(up.files) != 0 || len(smithd.started) != 0 {
		t.Errorf("an invalid bundle was sent: %v %d %v", smithd.prepared, len(up.files), smithd.started)
	}
}

func TestLocalPlanRefusedByTheServer(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	smithd.refuse = `{"error":{"code":"login_required","message":"Local plans need a signed-in user, not an API key"}}`
	up := &uploads{}

	err := runLocalPlan(context.Background(), testPlanOptions(), writeBundle(t, bundleMainTF), c, up.upload, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "signed-in user") {
		t.Errorf("error = %v, want smithd's message", err)
	}
	if code := exitCode(t, err); code != ExitFailed {
		t.Errorf("exit code %d, want 1", code)
	}
	if len(up.files) != 0 || len(smithd.started) != 0 {
		t.Errorf("a refused local plan was uploaded or planned: %d %v", len(up.files), smithd.started)
	}
}

func TestLocalPlanUploadFails(t *testing.T) {
	smithd, c := newLocalSmithd(t)
	up := &uploads{err: fmt.Errorf("upload failed with status 403")}

	err := runLocalPlan(context.Background(), testPlanOptions(), writeBundle(t, bundleMainTF), c, up.upload, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "failed to upload bundle") {
		t.Errorf("error = %v", err)
	}
	if len(smithd.started) != 0 {
		t.Errorf("planned after a failed upload: %v", smithd.started)
	}
}

func TestLoadSessionRequiresALogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const url = "https://smithd.example.com"

	_, err := loadSession(url)
	if err == nil || !strings.Contains(err.Error(), "smithctl login") || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("without a session: %v", err)
	}

	if err := config.SaveSession(url, config.Session{Token: "old", Login: "alice", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err = loadSession(url)
	if err == nil || !strings.Contains(err.Error(), "smithctl login") || !strings.Contains(err.Error(), "expired") {
		t.Errorf("with an expired session: %v", err)
	}

	if err := config.SaveSession(url, config.Session{Token: "tok", Login: "alice", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	session, err := loadSession(url)
	if err != nil || session.Token != "tok" {
		t.Errorf("with a session: %+v, %v", session, err)
	}
}
