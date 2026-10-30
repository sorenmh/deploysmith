package gitops

import (
	"fmt"
	"os"
	"path/filepath"
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

		return s.addTerraformResources(worktree, terraformAppPath(environment, appName), terraformPath(environment), names...)
	})
}

// addTerraformResources lists files in the kustomization of a Terraform
// app's directory, and the directory in the kustomization of its parent
// (the environment's terraform directory)
func (s *Service) addTerraformResources(worktree *git.Worktree, appDir, parentDir string, files ...string) error {
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
		if err := addResource(appDir, name); err != nil {
			return err
		}
	}
	return addResource(parentDir, filepath.Base(appDir))
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

// rootLayout is where a Terraform deploy puts its files in the repository
// (all repo-relative): the source, the root's file, the kustomization that
// lists the file, and the one that lists that directory
type rootLayout struct {
	sourcePath string
	file       string
	appDir     string
	parentDir  string
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
	return s.deployRoot(d, rootLayout{
		sourcePath: TerraformSourcePath(d.Environment, d.App, d.Root),
		file:       d.Root + ".yaml",
		appDir:     terraformAppPath(d.Environment, d.App),
		parentDir:  terraformPath(d.Environment),
	})
}

// deployRoot writes the source and the root's file as DeployTerraform does,
// at the places of layout
func (s *Service) deployRoot(d TerraformDeploy, layout rootLayout) (TerraformCommits, error) {
	if len(d.Source) == 0 {
		return TerraformCommits{}, fmt.Errorf("the bundle of %s has no files", d.App)
	}
	sourcePath := layout.sourcePath
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
		if branch, err = s.currentBranch(); err != nil {
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
		rel := filepath.Join(layout.appDir, layout.file)
		if err := os.WriteFile(filepath.Join(s.workDir, rel), content, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", layout.file, err)
		}
		if _, err := worktree.Add(rel); err != nil {
			return fmt.Errorf("failed to add %s to git: %w", layout.file, err)
		}
		return s.addTerraformResources(worktree, layout.appDir, layout.parentDir, layout.file)
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
