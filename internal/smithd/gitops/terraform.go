package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

		// The app's kustomization lists its files, the environment's lists
		// its apps
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
		for _, name := range names {
			if err := addResource(terraformAppPath(environment, appName), name); err != nil {
				return err
			}
		}
		return addResource(terraformPath(environment), appName)
	})
}
