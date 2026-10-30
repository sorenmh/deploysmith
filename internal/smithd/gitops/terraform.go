package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
)

// terraformPath returns the repo-relative directory holding an
// environment's Terraform apps: environments/{environment}/terraform. Flux
// applies it with its own Kustomization, without health checks, since a
// Terraform CR waiting for approval is never Ready.
func terraformPath(environment string) string {
	return filepath.Join("environments", environment, "terraform")
}

// terraformAppPath returns the repo-relative directory of a Terraform app:
// environments/{environment}/terraform/{app_name}
func terraformAppPath(environment, appName string) string {
	return filepath.Join(terraformPath(environment), appName)
}

// TerraformSourcePath returns the repo-relative directory holding the
// bundle source of a Terraform app's root:
// environments/{environment}/terraform/{app_name}/src/{root}
func TerraformSourcePath(environment, appName, root string) string {
	return filepath.Join(terraformAppPath(environment, appName), "src", root)
}

// WriteTerraformFiles writes files (name to content) into a Terraform app's
// directory, lists them in the app's kustomization and the app in the
// environment's terraform kustomization, and commits and pushes the change.
// Other files in the directory, such as the Terraform CRs, are kept. It
// returns the commit SHA; if nothing changed, nothing is committed and the
// current remote SHA is returned.
func (s *Service) WriteTerraformFiles(appName, environment, message string, files map[string][]byte) (string, error) {
	for _, segment := range []string{appName, environment} {
		if !isSafePathSegment(segment) {
			return "", fmt.Errorf("invalid app or environment name %q", segment)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !isSafePathSegment(name) {
			return "", fmt.Errorf("invalid terraform file name %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)

	return s.change(message, func() error {
		appDir := filepath.Join(s.workDir, terraformAppPath(environment, appName))
		if err := os.MkdirAll(appDir, 0755); err != nil {
			return fmt.Errorf("failed to create terraform app directory: %w", err)
		}
		worktree, err := s.repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}

		for _, name := range names {
			if err := os.WriteFile(filepath.Join(appDir, name), files[name], 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", name, err)
			}
			if _, err := worktree.Add(filepath.Join(terraformAppPath(environment, appName), name)); err != nil {
				return fmt.Errorf("failed to add %s to git: %w", name, err)
			}
		}

		return s.addTerraformResources(worktree, appName, environment, names)
	})
}

// addTerraformResources lists files in a Terraform app's kustomization and
// the app in the environment's terraform kustomization
func (s *Service) addTerraformResources(worktree *git.Worktree, appName, environment string, files []string) error {
	addResource := func(dir, resource string) error {
		filename, changed, err := ensureKustomizationResource(filepath.Join(s.workDir, dir), resource)
		if err != nil {
			return fmt.Errorf("failed to update %s kustomization: %w", dir, err)
		}
		if changed {
			if _, err := worktree.Add(filepath.Join(dir, filename)); err != nil {
				return fmt.Errorf("failed to add %s to git: %w", filepath.Join(dir, filename), err)
			}
		}
		return nil
	}
	for _, name := range files {
		if err := addResource(terraformAppPath(environment, appName), name); err != nil {
			return err
		}
	}
	return addResource(terraformPath(environment), appName)
}

// TerraformDeploy is a deploy of one root of a Terraform app
type TerraformDeploy struct {
	App         string
	Environment string
	Root        string
	// Source is the bundle: slash-separated paths relative to the root
	Source map[string][]byte
	// Manifest returns the root's file (GitRepository and Terraform CR)
	// for the gitops branch and the commit holding the source
	Manifest func(branch, sourceCommit string) ([]byte, error)
	// Commit messages of the source and the manifest commit
	SourceMessage   string
	ManifestMessage string
}

// TerraformCommits are the commits of a Terraform deploy
type TerraformCommits struct {
	// Branch is the gitops branch
	Branch string
	// Source holds the bundle source; Manifest, its child, the root's file
	// pinned to Source
	Source   string
	Manifest string
}

// DeployTerraform writes a root of a Terraform app in two commits, pushed
// together: first the bundle source in src/<root>/, replacing what was
// there, then the root's file <root>.yaml from d.Manifest, listed in the
// app's kustomization. The root's GitRepository must be pinned to the
// source commit, so that the second commit, and any later commit to the
// repository such as an approval, leaves the revision the controller plans
// unchanged (tofu-controller #1878).
//
// If the source is unchanged, the source commit is the current remote
// commit, which holds the same source.
func (s *Service) DeployTerraform(d TerraformDeploy) (TerraformCommits, error) {
	for _, segment := range []string{d.App, d.Environment, d.Root} {
		if !isSafePathSegment(segment) {
			return TerraformCommits{}, fmt.Errorf("invalid app, environment or root name %q", segment)
		}
	}
	if len(d.Source) == 0 {
		return TerraformCommits{}, fmt.Errorf("the bundle of %s has no files", d.App)
	}
	sourcePath := TerraformSourcePath(d.Environment, d.App, d.Root)
	names := make([]string, 0, len(d.Source))
	for name := range d.Source {
		// Reject entries such as "../x.tf" or "/x.tf" that would escape the
		// source directory
		for _, segment := range strings.Split(name, "/") {
			if !isSafePathSegment(segment) {
				return TerraformCommits{}, fmt.Errorf("invalid bundle path %q", name)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var branch string
	writeSource := func([]string) error {
		var err error
		if branch, err = s.branch(); err != nil {
			return err
		}
		worktree, err := s.repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}

		// Remove the previous source, so files left out of the bundle go
		if _, err := os.Stat(filepath.Join(s.workDir, sourcePath)); err == nil {
			if _, err := worktree.Remove(sourcePath); err != nil {
				return fmt.Errorf("failed to remove the previous source: %w", err)
			}
		}
		for _, name := range names {
			rel := filepath.Join(sourcePath, filepath.FromSlash(name))
			full := filepath.Join(s.workDir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				return fmt.Errorf("failed to create directory for %s: %w", name, err)
			}
			if err := os.WriteFile(full, d.Source[name], 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", name, err)
			}
			// One by one, so a .gitignore in the gitops repo (e.g. of
			// *.tfvars) can't leave files out
			if _, err := worktree.Add(rel); err != nil {
				return fmt.Errorf("failed to add %s to git: %w", name, err)
			}
		}
		return nil
	}

	writeManifest := func(previous []string) error {
		content, err := d.Manifest(branch, previous[0])
		if err != nil {
			return fmt.Errorf("failed to render the %s manifest: %w", d.Root, err)
		}
		worktree, err := s.repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}
		file := d.Root + ".yaml"
		rel := filepath.Join(terraformAppPath(d.Environment, d.App), file)
		if err := os.WriteFile(filepath.Join(s.workDir, rel), content, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file, err)
		}
		if _, err := worktree.Add(rel); err != nil {
			return fmt.Errorf("failed to add %s to git: %w", file, err)
		}
		return s.addTerraformResources(worktree, d.App, d.Environment, []string{file})
	}

	shas, err := s.changeSteps([]step{
		{message: d.SourceMessage, write: writeSource},
		{message: d.ManifestMessage, write: writeManifest},
	})
	if err != nil {
		return TerraformCommits{}, err
	}
	return TerraformCommits{Branch: branch, Source: shas[0], Manifest: shas[1]}, nil
}

// ErrRootNotCurrent is returned by SetTerraformApproval when the root's file
// in the gitops repo is not the deployment's any more (a newer deploy
// rewrote it, or it is gone), or holds the approval of another plan
var ErrRootNotCurrent = errors.New("the terraform root in the gitops repo is not this deployment's")

var approvePlanLine = regexp.MustCompile(`(?m)^([ \t]+approvePlan:)[ \t]*(.*?)[ \t]*$`)

// SetTerraformApproval sets spec.approvePlan of a root's Terraform CR to
// planID, which is an approval, or to "" to reset it after the apply. It
// changes that one line in one commit and nothing else: the root's
// GitRepository is pinned to the source commit, so the revision the
// controller planned stays the same (see DeployTerraform).
//
// It only touches the file of deploymentID (the CR's annotation names it);
// otherwise, or for an approval when another plan is approved already, it
// returns ErrRootNotCurrent. If approvePlan already is planID nothing is
// committed and the current remote SHA is returned, so that the same
// approval or reset can be repeated, e.g. by another smithd replica.
func (s *Service) SetTerraformApproval(appName, environment, root, deploymentID, planID, message string) (string, error) {
	for _, segment := range []string{appName, environment, root} {
		if !isSafePathSegment(segment) {
			return "", fmt.Errorf("invalid app, environment or root name %q", segment)
		}
	}
	if planID != "" && !isSafePathSegment(planID) {
		return "", fmt.Errorf("invalid plan id %q", planID)
	}
	rel := filepath.Join(terraformAppPath(environment, appName), root+".yaml")

	return s.change(message, func() error {
		path := filepath.Join(s.workDir, rel)
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s does not exist", ErrRootNotCurrent, rel)
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", rel, err)
		}
		if !strings.Contains(string(content), "deploysmith.io/deployment-id: "+deploymentID+"\n") {
			return fmt.Errorf("%w: %s belongs to another deployment", ErrRootNotCurrent, rel)
		}

		match := approvePlanLine.FindSubmatch(content)
		if match == nil {
			return fmt.Errorf("%s has no approvePlan", rel)
		}
		current := strings.Trim(string(match[2]), `"'`)
		if planID != "" && current != "" && current != planID {
			return fmt.Errorf("%w: %s approves plan %s", ErrRootNotCurrent, rel, current)
		}
		if current == planID {
			return nil
		}

		value := `""`
		if planID != "" {
			value = `"` + planID + `"`
		}
		updated := approvePlanLine.ReplaceAll(content, []byte("${1} "+value))
		if err := os.WriteFile(path, updated, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", rel, err)
		}
		worktree, err := s.repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}
		if _, err := worktree.Add(rel); err != nil {
			return fmt.Errorf("failed to add %s to git: %w", rel, err)
		}
		return nil
	})
}
