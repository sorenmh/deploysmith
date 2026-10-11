package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var testSig = &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}

// newTestRemote creates a bare repo with an initial commit and returns its path
func newTestRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}

	seed := filepath.Join(base, "seed")
	repo, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, seed, "README.md", "gitops")
	if err := repo.Push(&git.PushOptions{}); err != nil {
		t.Fatal(err)
	}
	return bare
}

func newTestService(t *testing.T, remote string) *Service {
	t.Helper()
	s := NewService(remote, "")
	s.workDir = filepath.Join(t.TempDir(), "work")
	s.retryDelay = time.Millisecond
	return s
}

func commitFile(t *testing.T, repo *git.Repository, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add "+name, &git.CommitOptions{Author: testSig}); err != nil {
		t.Fatal(err)
	}
}

// pushFromOtherClone simulates someone else pushing to the gitops repo
func pushFromOtherClone(t *testing.T, remote, name, content string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainClone(dir, false, &git.CloneOptions{URL: remote})
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, dir, name, content)
	if err := repo.Push(&git.PushOptions{}); err != nil {
		t.Fatal(err)
	}
}

// readRemote returns the content of a file at the remote's HEAD, or "" if absent
func readRemote(t *testing.T, remote, name string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.PlainClone(dir, false, &git.CloneOptions{URL: remote}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func manifest(app string) map[string][]byte {
	return map[string][]byte{"deployment.yaml": []byte("name: " + app + "\n")}
}

func TestDeployWritesManifestsAndKustomization(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	if _, err := s.Deploy("hello", "production", "deploy hello", manifest("hello")); err != nil {
		t.Fatal(err)
	}

	if got := readRemote(t, remote, "environments/production/apps/hello/deployment.yaml"); got != "name: hello\n" {
		t.Errorf("manifest = %q", got)
	}
	if got := readRemote(t, remote, "environments/production/apps/kustomization.yaml"); !strings.Contains(got, "- hello") {
		t.Errorf("kustomization does not list app:\n%s", got)
	}
}

func TestDeployRecoversFromUnpushedLocalCommit(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	if _, err := s.Deploy("a", "production", "deploy a", manifest("a")); err != nil {
		t.Fatal(err)
	}

	// A previous push failed, leaving a local commit, and then the remote moved on
	commitFile(t, s.repo, s.workDir, "stale.yaml", "stale")
	pushFromOtherClone(t, remote, "human.txt", "edit")

	if _, err := s.Deploy("b", "production", "deploy b", manifest("b")); err != nil {
		t.Fatalf("deploy after diverged history failed: %v", err)
	}

	if readRemote(t, remote, "environments/production/apps/b/deployment.yaml") == "" {
		t.Error("app b was not deployed")
	}
	if readRemote(t, remote, "human.txt") != "edit" {
		t.Error("remote change was lost")
	}
	if readRemote(t, remote, "stale.yaml") != "" {
		t.Error("stale local commit leaked into the remote")
	}
}

func TestDeployDiscardsLeftoverFiles(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	if _, err := s.Deploy("a", "production", "deploy a", manifest("a")); err != nil {
		t.Fatal(err)
	}

	// A previous deploy failed after writing and staging files
	leftover := filepath.Join(s.workDir, "environments/production/apps/half/deployment.yaml")
	if err := os.MkdirAll(filepath.Dir(leftover), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("half"), 0644); err != nil {
		t.Fatal(err)
	}
	wt, _ := s.repo.Worktree()
	if _, err := wt.Add("environments/production/apps/half/deployment.yaml"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Deploy("b", "production", "deploy b", manifest("b")); err != nil {
		t.Fatal(err)
	}

	if readRemote(t, remote, "environments/production/apps/half/deployment.yaml") != "" {
		t.Error("leftover files from a failed deploy were committed")
	}
}

func TestDeployRetriesRejectedPush(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	// Someone pushes between our sync and our push, once
	raced := false
	s.beforePush = func() {
		if !raced {
			raced = true
			pushFromOtherClone(t, remote, "human.txt", "edit")
		}
	}

	if _, err := s.Deploy("a", "production", "deploy a", manifest("a")); err != nil {
		t.Fatalf("deploy failed despite retry: %v", err)
	}

	if readRemote(t, remote, "environments/production/apps/a/deployment.yaml") == "" {
		t.Error("app a was not deployed")
	}
	if readRemote(t, remote, "human.txt") != "edit" {
		t.Error("concurrent remote change was lost")
	}
}

func TestDeployUnchangedManifestsIsNoop(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	first, err := s.Deploy("a", "production", "deploy a", manifest("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Deploy("a", "production", "deploy a again", manifest("a"))
	if err != nil {
		t.Fatalf("redeploying identical manifests failed: %v", err)
	}
	if first != second {
		t.Errorf("expected no new commit, got %s then %s", first, second)
	}
}

func TestConcurrentDeploys(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	apps := []string{"a", "b", "c", "d", "e"}
	var wg sync.WaitGroup
	errs := make(chan error, len(apps))
	for _, app := range apps {
		wg.Add(1)
		go func(app string) {
			defer wg.Done()
			if _, err := s.Deploy(app, "production", "deploy "+app, manifest(app)); err != nil {
				errs <- fmt.Errorf("%s: %w", app, err)
			}
		}(app)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	kustomization := readRemote(t, remote, "environments/production/apps/kustomization.yaml")
	for _, app := range apps {
		if readRemote(t, remote, "environments/production/apps/"+app+"/deployment.yaml") == "" {
			t.Errorf("app %s missing from remote", app)
		}
		if !strings.Contains(kustomization, "- "+app+"\n") {
			t.Errorf("app %s missing from kustomization", app)
		}
	}
}
