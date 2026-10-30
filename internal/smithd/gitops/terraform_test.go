package gitops

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestWriteTerraformFiles(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	files := map[string][]byte{"namespace.yaml": []byte("kind: Namespace\n")}

	first, err := s.WriteTerraformFiles("viino", "prod", "set up viino", files)
	if err != nil {
		t.Fatal(err)
	}

	if got := readRemote(t, remote, "environments/prod/terraform/viino/namespace.yaml"); got != "kind: Namespace\n" {
		t.Errorf("namespace.yaml = %q", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/kustomization.yaml"); !strings.Contains(got, "- namespace.yaml") {
		t.Errorf("app kustomization does not list namespace.yaml:\n%s", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/kustomization.yaml"); !strings.Contains(got, "- viino") {
		t.Errorf("terraform kustomization does not list the app:\n%s", got)
	}
	// Apps' manifests are elsewhere
	if got := readRemote(t, remote, "environments/prod/apps/kustomization.yaml"); got != "" {
		t.Errorf("apps kustomization written:\n%s", got)
	}

	// Writing the same again commits nothing
	second, err := s.WriteTerraformFiles("viino", "prod", "set up viino", files)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("unchanged write made commit %s after %s", second, first)
	}

	// Files written by others (the Terraform CRs of DEP-18) are kept
	pushFromOtherClone(t, remote, "environments/prod/terraform/viino/prod-main.yaml", "kind: Terraform\n")
	if _, err := s.WriteTerraformFiles("viino", "prod", "update viino", map[string][]byte{"namespace.yaml": []byte("kind: Namespace # v2\n")}); err != nil {
		t.Fatal(err)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/prod-main.yaml"); got != "kind: Terraform\n" {
		t.Errorf("other file = %q", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/namespace.yaml"); got != "kind: Namespace # v2\n" {
		t.Errorf("namespace.yaml = %q", got)
	}
}

func TestWriteTerraformFilesRejectsUnsafeNames(t *testing.T) {
	s := newTestService(t, newTestRemote(t))
	for _, tc := range []struct{ app, env, file string }{
		{"../x", "prod", "namespace.yaml"},
		{"viino", "..", "namespace.yaml"},
		{"viino", "prod", "../namespace.yaml"},
	} {
		if _, err := s.WriteTerraformFiles(tc.app, tc.env, "x", map[string][]byte{tc.file: nil}); err == nil {
			t.Errorf("WriteTerraformFiles(%q, %q, %q) succeeded", tc.app, tc.env, tc.file)
		}
	}
}

func bundle(main string) map[string][]byte {
	return map[string][]byte{
		"main.tf":             []byte(main),
		".terraform.lock.hcl": []byte("# lock\n"),
		"env/prod.tfvars":     []byte("x = 1\n"),
		"modules/m/main.tf":   []byte("# module\n"),
	}
}

// testDeploy renders the root's file from what DeployTerraform passes in
func testDeploy(source map[string][]byte) TerraformDeploy {
	return TerraformDeploy{
		App:         "viino",
		Environment: "prod",
		Root:        "main",
		Source:      source,
		Manifest: func(branch, sourceCommit string) ([]byte, error) {
			return []byte(fmt.Sprintf("branch: %s\ncommit: %s\napprovePlan: \"\"\n", branch, sourceCommit)), nil
		},
		SourceMessage:   "source",
		ManifestMessage: "manifest",
	}
}

// remoteCommit returns a commit of the remote, or its HEAD for ""
func remoteCommit(t *testing.T, remote, sha string) *object.Commit {
	t.Helper()
	repo, err := git.PlainClone(t.TempDir(), true, &git.CloneOptions{URL: remote})
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		head, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		sha = head.Hash().String()
	}
	commit, err := repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

// fileAt returns a file's content in a commit, or "" if absent
func fileAt(t *testing.T, commit *object.Commit, name string) string {
	t.Helper()
	file, err := commit.File(name)
	if err == object.ErrFileNotFound {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	content, err := file.Contents()
	if err != nil {
		t.Fatal(err)
	}
	return content
}

const testSourcePath = "environments/prod/terraform/viino/src/main/"

func TestDeployTerraformCommitsSourceThenManifest(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	commits, err := s.DeployTerraform(testDeploy(bundle("# v1\n")))
	if err != nil {
		t.Fatal(err)
	}
	if commits.Branch != "master" {
		t.Errorf("branch = %q, want the remote's master", commits.Branch)
	}

	// HEAD is the manifest commit, its parent the source commit
	head := remoteCommit(t, remote, "")
	if head.Hash.String() != commits.Manifest || head.Message != "manifest" {
		t.Fatalf("remote HEAD %s %q, want the manifest commit %s", head.Hash, head.Message, commits.Manifest)
	}
	if len(head.ParentHashes) != 1 || head.ParentHashes[0].String() != commits.Source {
		t.Fatalf("manifest commit's parents %v, want the source commit %s", head.ParentHashes, commits.Source)
	}

	// The source commit has the bundle but not yet the manifest
	source := remoteCommit(t, remote, commits.Source)
	if source.Message != "source" {
		t.Errorf("source commit message %q", source.Message)
	}
	for name, content := range bundle("# v1\n") {
		if got := fileAt(t, source, testSourcePath+name); got != string(content) {
			t.Errorf("%s = %q in the source commit", name, got)
		}
	}
	if got := fileAt(t, source, "environments/prod/terraform/viino/main.yaml"); got != "" {
		t.Errorf("manifest already in the source commit:\n%s", got)
	}

	// The manifest is pinned to the source commit and listed
	want := fmt.Sprintf("branch: master\ncommit: %s\napprovePlan: \"\"\n", commits.Source)
	if got := fileAt(t, head, "environments/prod/terraform/viino/main.yaml"); got != want {
		t.Errorf("main.yaml = %q, want %q", got, want)
	}
	if got := fileAt(t, head, "environments/prod/terraform/viino/kustomization.yaml"); !strings.Contains(got, "- main.yaml") || strings.Contains(got, "src") {
		t.Errorf("app kustomization:\n%s", got)
	}
	if got := fileAt(t, head, "environments/prod/terraform/kustomization.yaml"); !strings.Contains(got, "- viino") {
		t.Errorf("terraform kustomization:\n%s", got)
	}
}

func TestDeployTerraformReplacesSource(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	if _, err := s.WriteTerraformFiles("viino", "prod", "namespace", map[string][]byte{"namespace.yaml": []byte("kind: Namespace\n")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeployTerraform(testDeploy(bundle("# v1\n"))); err != nil {
		t.Fatal(err)
	}

	// The next bundle drops the module
	next := bundle("# v2\n")
	delete(next, "modules/m/main.tf")
	commits, err := s.DeployTerraform(testDeploy(next))
	if err != nil {
		t.Fatal(err)
	}

	source := remoteCommit(t, remote, commits.Source)
	if got := fileAt(t, source, testSourcePath+"main.tf"); got != "# v2\n" {
		t.Errorf("main.tf = %q", got)
	}
	if got := fileAt(t, source, testSourcePath+"modules/m/main.tf"); got != "" {
		t.Errorf("file left out of the bundle is still there: %q", got)
	}
	head := remoteCommit(t, remote, commits.Manifest)
	if got := fileAt(t, head, "environments/prod/terraform/viino/kustomization.yaml"); !strings.Contains(got, "- namespace.yaml") || !strings.Contains(got, "- main.yaml") {
		t.Errorf("app kustomization:\n%s", got)
	}
}

func TestDeployTerraformUnchangedSource(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	first, err := s.DeployTerraform(testDeploy(bundle("# v1\n")))
	if err != nil {
		t.Fatal(err)
	}

	// Deploying the same bundle again pins the manifest to the current
	// commit, which has the same source, and so asks for a new plan
	second, err := s.DeployTerraform(testDeploy(bundle("# v1\n")))
	if err != nil {
		t.Fatal(err)
	}
	if second.Source != first.Manifest {
		t.Errorf("source commit %s, want the previous HEAD %s", second.Source, first.Manifest)
	}
	head := remoteCommit(t, remote, "")
	if head.Hash.String() != second.Manifest || head.ParentHashes[0].String() != first.Manifest {
		t.Errorf("remote HEAD %s (parent %v), want a manifest commit %s on %s", head.Hash, head.ParentHashes, second.Manifest, first.Manifest)
	}
}

func TestDeployTerraformIgnoresGitignore(t *testing.T) {
	remote := newTestRemote(t)
	pushFromOtherClone(t, remote, ".gitignore", "*.tfvars\n.terraform*\n")
	s := newTestService(t, remote)

	commits, err := s.DeployTerraform(testDeploy(bundle("# v1\n")))
	if err != nil {
		t.Fatal(err)
	}
	source := remoteCommit(t, remote, commits.Source)
	for _, name := range []string{"env/prod.tfvars", ".terraform.lock.hcl"} {
		if fileAt(t, source, testSourcePath+name) == "" {
			t.Errorf("%s was left out", name)
		}
	}
}

func TestDeployTerraformRetriesRejectedPush(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)

	raced := false
	s.beforePush = func() {
		if !raced {
			raced = true
			pushFromOtherClone(t, remote, "human.txt", "edit")
		}
	}

	commits, err := s.DeployTerraform(testDeploy(bundle("# v1\n")))
	if err != nil {
		t.Fatalf("deploy failed despite retry: %v", err)
	}

	// Both commits were made again on top of the other push, and the
	// manifest is pinned to the new source commit
	source := remoteCommit(t, remote, commits.Source)
	if fileAt(t, source, "human.txt") != "edit" {
		t.Error("source commit is not on top of the concurrent push")
	}
	want := fmt.Sprintf("commit: %s\n", commits.Source)
	if got := fileAt(t, remoteCommit(t, remote, ""), "environments/prod/terraform/viino/main.yaml"); !strings.Contains(got, want) {
		t.Errorf("main.yaml = %q, want it pinned to %s", got, commits.Source)
	}
}

func TestDeployTerraformRejectsUnsafeNames(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	for _, d := range []TerraformDeploy{
		func() TerraformDeploy { d := testDeploy(bundle("")); d.App = ".."; return d }(),
		func() TerraformDeploy { d := testDeploy(bundle("")); d.Root = "a/b"; return d }(),
		testDeploy(map[string][]byte{"../../evil.tf": []byte("")}),
		testDeploy(map[string][]byte{"/etc/evil.tf": []byte("")}),
		testDeploy(nil),
	} {
		if _, err := s.DeployTerraform(d); err == nil {
			t.Errorf("DeployTerraform(%s, %s, %v) succeeded", d.App, d.Root, d.Source)
		}
	}
	if head := remoteCommit(t, remote, ""); head.Message != "add README.md" {
		t.Errorf("something was committed: %q", head.Message)
	}
}

func TestDeployTerraformManifestError(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	d := testDeploy(bundle("# v1\n"))
	d.Manifest = func(string, string) ([]byte, error) { return nil, fmt.Errorf("no image") }

	if _, err := s.DeployTerraform(d); err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("error = %v", err)
	}
	// The source commit is not pushed alone
	if head := remoteCommit(t, remote, ""); head.Message != "add README.md" {
		t.Errorf("something was pushed: %q", head.Message)
	}
}

const testRootFile = "environments/prod/terraform/viino/main.yaml"

// deployRoot deploys a root whose file looks like the one smithd renders:
// annotated with the deployment, approvePlan indented in the spec
func deployRoot(t *testing.T, s *Service, deploymentID string) TerraformCommits {
	t.Helper()
	d := testDeploy(bundle("# v1\n"))
	d.Manifest = func(branch, sourceCommit string) ([]byte, error) {
		return []byte(fmt.Sprintf("metadata:\n  annotations:\n    deploysmith.io/deployment-id: %s\nspec:\n  interval: 6h\n  approvePlan: \"\"\n  path: ./src\n    commit: %s\n", deploymentID, sourceCommit)), nil
	}
	commits, err := s.DeployTerraform(d)
	if err != nil {
		t.Fatal(err)
	}
	return commits
}

func TestSetTerraformApprovalChangesOnlyApprovePlan(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	deployed := deployRoot(t, s, "dep-1")
	before := fileAt(t, remoteCommit(t, remote, ""), testRootFile)

	sha, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "plan-main-0123456789", "approve")
	if err != nil {
		t.Fatal(err)
	}

	// One commit on top of the deploy that changes that line only
	head := remoteCommit(t, remote, "")
	if head.Hash.String() != sha || head.Message != "approve" || len(head.ParentHashes) != 1 || head.ParentHashes[0].String() != deployed.Manifest {
		t.Fatalf("remote HEAD %s %q (parents %v), want one commit %s on %s", head.Hash, head.Message, head.ParentHashes, sha, deployed.Manifest)
	}
	want := strings.Replace(before, `approvePlan: ""`, `approvePlan: "plan-main-0123456789"`, 1)
	if got := fileAt(t, head, testRootFile); got != want {
		t.Errorf("main.yaml = %q, want %q", got, want)
	}
	stats, err := head.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Name != testRootFile || stats[0].Addition != 1 || stats[0].Deletion != 1 {
		t.Errorf("the approval changed %+v, want one line of main.yaml", stats)
	}

	// The same approval again commits nothing
	again, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "plan-main-0123456789", "approve")
	if err != nil || again != sha {
		t.Errorf("repeated approval = %q, %v, want %s and no new commit", again, err, sha)
	}

	// Resetting writes "" again, once
	reset, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "", "reset")
	if err != nil {
		t.Fatal(err)
	}
	if got := fileAt(t, remoteCommit(t, remote, ""), testRootFile); got != before {
		t.Errorf("main.yaml after reset = %q, want %q", got, before)
	}
	if again, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "", "reset"); err != nil || again != reset {
		t.Errorf("repeated reset = %q, %v, want %s and no new commit", again, err, reset)
	}
}

func TestSetTerraformApprovalOnlyForTheCurrentDeployment(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	deployRoot(t, s, "dep-2")
	head := remoteCommit(t, remote, "").Hash

	// A newer deployment owns the file
	if _, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "plan-main-0123456789", "approve"); !errors.Is(err, ErrRootNotCurrent) {
		t.Errorf("approving for another deployment: %v, want ErrRootNotCurrent", err)
	}
	if _, err := s.SetTerraformApproval("viino", "prod", "other", "dep-2", "plan-main-0123456789", "approve"); !errors.Is(err, ErrRootNotCurrent) {
		t.Errorf("approving a root without file: %v, want ErrRootNotCurrent", err)
	}

	// Another plan is approved already
	if _, err := s.SetTerraformApproval("viino", "prod", "main", "dep-2", "plan-main-aaaaaaaaaa", "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTerraformApproval("viino", "prod", "main", "dep-2", "plan-main-bbbbbbbbbb", "approve"); !errors.Is(err, ErrRootNotCurrent) {
		t.Errorf("approving over another approval: %v, want ErrRootNotCurrent", err)
	}
	if remoteCommit(t, remote, "").ParentHashes[0] != head {
		t.Error("refused approvals committed something")
	}

	for _, tc := range []struct{ app, env, root, plan string }{
		{"../x", "prod", "main", ""}, {"viino", "..", "main", ""}, {"viino", "prod", "a/b", ""}, {"viino", "prod", "main", "a/b"},
	} {
		if _, err := s.SetTerraformApproval(tc.app, tc.env, tc.root, "dep-2", tc.plan, "x"); err == nil {
			t.Errorf("SetTerraformApproval(%+v) succeeded", tc)
		}
	}
}

func TestSetTerraformApprovalRetriesRejectedPush(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	deployRoot(t, s, "dep-1")

	raced := false
	s.beforePush = func() {
		if !raced {
			raced = true
			pushFromOtherClone(t, remote, "human.txt", "edit")
		}
	}
	if _, err := s.SetTerraformApproval("viino", "prod", "main", "dep-1", "plan-main-0123456789", "approve"); err != nil {
		t.Fatalf("approval failed despite retry: %v", err)
	}
	head := remoteCommit(t, remote, "")
	if fileAt(t, head, "human.txt") != "edit" || !strings.Contains(fileAt(t, head, testRootFile), `approvePlan: "plan-main-0123456789"`) {
		t.Error("the approval is not on top of the concurrent push")
	}
}
