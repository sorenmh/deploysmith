package gitops

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// newPlansService returns a service of the plans branch of the remote
func newPlansService(t *testing.T, remote string) *Service {
	t.Helper()
	s := NewBranchService(remote, "", PlansBranch)
	s.workDir = filepath.Join(t.TempDir(), "work")
	s.retryDelay = time.Millisecond
	return s
}

func testPlanDeploy(name string, source map[string][]byte) TerraformPlanDeploy {
	d := testDeploy(source)
	d.Manifest = func(branch, sourceCommit string) ([]byte, error) {
		return []byte(fmt.Sprintf("branch: %s\ncommit: %s\nplanOnly: true\n", branch, sourceCommit)), nil
	}
	return TerraformPlanDeploy{TerraformDeploy: d, Name: name}
}

// planFile returns a file at the tip of the plans branch of the remote
func planFile(t *testing.T, remote, name string) string {
	t.Helper()
	repo, err := git.PlainClone(t.TempDir(), true, &git.CloneOptions{URL: remote, ReferenceName: plumbing.NewBranchReferenceName(PlansBranch)})
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	return fileAt(t, commit, name)
}

// branchLength counts the commits of a branch of the remote
func branchLength(t *testing.T, remote, branch string) int {
	t.Helper()
	repo, err := git.PlainClone(t.TempDir(), true, &git.CloneOptions{URL: remote, ReferenceName: plumbing.NewBranchReferenceName(branch)})
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	log, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, err := log.Next(); err != nil {
			return n
		}
		n++
	}
}

func TestDeployTerraformPlanWritesToPlansBranchOnly(t *testing.T) {
	remote := newTestRemote(t)
	mainBefore := remoteHead(t, remote)
	s := newPlansService(t, remote)

	commits, err := s.DeployTerraformPlan(testPlanDeploy("plan-abc", bundle("# v1\n")))
	if err != nil {
		t.Fatal(err)
	}
	if commits.Branch != PlansBranch {
		t.Errorf("branch = %q, want %q", commits.Branch, PlansBranch)
	}
	if commits.Source == commits.Manifest {
		t.Errorf("source and manifest share commit %s", commits.Source)
	}

	// The branch was created without the main branch's history
	if got := planFile(t, remote, "README.md"); !strings.Contains(got, "plans") {
		t.Errorf("README.md on the plans branch = %q", got)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/plan-abc/src/main/main.tf"); got != "# v1\n" {
		t.Errorf("source = %q", got)
	}
	manifest := planFile(t, remote, "environments/prod/terraform-plans/viino/plan-abc.yaml")
	if !strings.Contains(manifest, "branch: plans") || !strings.Contains(manifest, "commit: "+commits.Source) {
		t.Errorf("manifest is not pinned to the source commit on the plans branch:\n%s", manifest)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/kustomization.yaml"); !strings.Contains(got, "- plan-abc.yaml") {
		t.Errorf("app kustomization:\n%s", got)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/kustomization.yaml"); !strings.Contains(got, "- viino") {
		t.Errorf("environment kustomization:\n%s", got)
	}

	// The main branch is untouched
	if got := remoteHead(t, remote); got != mainBefore {
		t.Errorf("main moved from %s to %s", mainBefore, got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform-plans/viino/plan-abc.yaml"); got != "" {
		t.Errorf("plan on main:\n%s", got)
	}

	// A second plan joins the first
	if _, err := s.DeployTerraformPlan(testPlanDeploy("plan-def", bundle("# v2\n"))); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/kustomization.yaml"); !strings.Contains(got, "plan-abc.yaml") || !strings.Contains(got, "plan-def.yaml") {
		t.Errorf("app kustomization:\n%s", got)
	}
}

func TestDeployTerraformPlanRejectsUnsafeNames(t *testing.T) {
	s := newPlansService(t, newTestRemote(t))
	for _, name := range []string{"", "..", "a/b"} {
		if _, err := s.DeployTerraformPlan(testPlanDeploy(name, bundle("x"))); err == nil {
			t.Errorf("DeployTerraformPlan with name %q succeeded", name)
		}
	}
}

func TestCleanTerraformPlans(t *testing.T) {
	remote := newTestRemote(t)
	mainBefore := remoteHead(t, remote)
	s := newPlansService(t, remote)
	for _, name := range []string{"plan-a", "plan-b", "plan-c"} {
		if _, err := s.DeployTerraformPlan(testPlanDeploy(name, bundle("# "+name+"\n"))); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.CleanTerraformPlans("clean up", func(e PlanEntry) bool { return e.Name == "plan-b" })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0].Name != "plan-a" || removed[1].Name != "plan-c" {
		t.Fatalf("removed = %+v", removed)
	}
	for _, name := range []string{"plan-a", "plan-c"} {
		if got := planFile(t, remote, "environments/prod/terraform-plans/viino/"+name+".yaml"); got != "" {
			t.Errorf("%s.yaml still there:\n%s", name, got)
		}
		if got := planFile(t, remote, "environments/prod/terraform-plans/viino/"+name+"/src/main/main.tf"); got != "" {
			t.Errorf("%s source still there", name)
		}
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/plan-b/src/main/main.tf"); got != "# plan-b\n" {
		t.Errorf("kept source = %q", got)
	}
	kustomization := planFile(t, remote, "environments/prod/terraform-plans/viino/kustomization.yaml")
	if strings.Contains(kustomization, "plan-a") || strings.Contains(kustomization, "plan-c") || !strings.Contains(kustomization, "plan-b.yaml") {
		t.Errorf("app kustomization:\n%s", kustomization)
	}

	// The last run takes the app with it, but the environment's
	// kustomization stays, empty, for its Flux Kustomization
	if _, err := s.CleanTerraformPlans("clean up", func(PlanEntry) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/kustomization.yaml"); got != "" {
		t.Errorf("app kustomization still there:\n%s", got)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/kustomization.yaml"); !strings.Contains(got, "resources: []") {
		t.Errorf("environment kustomization:\n%s", got)
	}

	// Nothing left, nothing committed
	plansBefore := repoPlansHead(t, remote)
	removed, err = s.CleanTerraformPlans("clean up", func(PlanEntry) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v", removed)
	}
	if got := repoPlansHead(t, remote); got != plansBefore {
		t.Errorf("plans moved from %s to %s", plansBefore, got)
	}
	if got := remoteHead(t, remote); got != mainBefore {
		t.Errorf("main moved from %s to %s", mainBefore, got)
	}
}

// repoPlansHead returns the tip of the plans branch of the remote
func repoPlansHead(t *testing.T, remote string) string {
	t.Helper()
	repo, err := git.PlainClone(t.TempDir(), true, &git.CloneOptions{URL: remote, ReferenceName: plumbing.NewBranchReferenceName(PlansBranch)})
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head.Hash().String()
}

func TestCleanTerraformPlansNothingToDoCommitsNothing(t *testing.T) {
	remote := newTestRemote(t)
	s := newPlansService(t, remote)
	if _, err := s.DeployTerraformPlan(testPlanDeploy("plan-a", bundle("x"))); err != nil {
		t.Fatal(err)
	}
	before := repoPlansHead(t, remote)

	removed, err := s.CleanTerraformPlans("clean up", func(PlanEntry) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v", removed)
	}
	if after := repoPlansHead(t, remote); after != before {
		t.Errorf("plans moved from %s to %s", before, after)
	}
}

func TestSquashTerraformPlans(t *testing.T) {
	remote := newTestRemote(t)
	s := newPlansService(t, remote)
	for _, name := range []string{"plan-a", "plan-b"} {
		if _, err := s.DeployTerraformPlan(testPlanDeploy(name, bundle("# "+name+"\n"))); err != nil {
			t.Fatal(err)
		}
	}

	// A run is left: nothing is squashed
	if squashed, err := s.SquashTerraformPlans(1); err != nil || squashed {
		t.Fatalf("SquashTerraformPlans with runs left = %v, %v", squashed, err)
	}
	if _, err := s.CleanTerraformPlans("clean up", func(PlanEntry) bool { return false }); err != nil {
		t.Fatal(err)
	}
	length := branchLength(t, remote, PlansBranch)
	if length < 5 {
		t.Fatalf("plans branch has %d commits", length)
	}

	// Short enough
	if squashed, err := s.SquashTerraformPlans(length); err != nil || squashed {
		t.Fatalf("SquashTerraformPlans on a short branch = %v, %v", squashed, err)
	}
	if squashed, err := s.SquashTerraformPlans(length - 1); err != nil || !squashed {
		t.Fatalf("SquashTerraformPlans = %v, %v", squashed, err)
	}
	if got := branchLength(t, remote, PlansBranch); got != 1 {
		t.Errorf("plans branch has %d commits after the squash, want 1", got)
	}
	// The tree is what it was
	if got := planFile(t, remote, "environments/prod/terraform-plans/kustomization.yaml"); !strings.Contains(got, "resources: []") {
		t.Errorf("environment kustomization:\n%s", got)
	}

	// And the service goes on from the new history
	if _, err := s.DeployTerraformPlan(testPlanDeploy("plan-c", bundle("# c\n"))); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, remote, "environments/prod/terraform-plans/viino/plan-c/src/main/main.tf"); got != "# c\n" {
		t.Errorf("source = %q", got)
	}
}

func TestSquashTerraformPlansSeesNewRuns(t *testing.T) {
	remote := newTestRemote(t)
	s := newPlansService(t, remote)
	if _, err := s.DeployTerraformPlan(testPlanDeploy("plan-a", bundle("x"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanTerraformPlans("clean up", func(PlanEntry) bool { return false }); err != nil {
		t.Fatal(err)
	}

	// Another smithd starts a run
	other := newPlansService(t, remote)
	if _, err := other.DeployTerraformPlan(testPlanDeploy("plan-z", bundle("z"))); err != nil {
		t.Fatal(err)
	}
	moved := repoPlansHead(t, remote)

	// The squash syncs first, so it sees the run and does nothing
	if squashed, err := s.SquashTerraformPlans(1); err != nil || squashed {
		t.Fatalf("SquashTerraformPlans = %v, %v", squashed, err)
	}
	if got := repoPlansHead(t, remote); got != moved {
		t.Errorf("plans moved from %s to %s", moved, got)
	}
}
