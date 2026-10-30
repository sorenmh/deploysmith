package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

// PlansBranch is the branch of plan-only runs. Flux has its own
// GitRepository and Kustomization for it (docs/terraform-cluster-setup.md),
// so that these short-lived CRs never touch the main branch.
const PlansBranch = "plans"

// terraformPlansPath returns the repo-relative directory holding an
// environment's plan-only runs on the plans branch:
// environments/{environment}/terraform-plans
func terraformPlansPath(environment string) string {
	return filepath.Join("environments", environment, "terraform-plans")
}

// terraformPlanAppPath returns the repo-relative directory of a Terraform
// app's plan-only runs: environments/{environment}/terraform-plans/{app_name}
func terraformPlanAppPath(environment, appName string) string {
	return filepath.Join(terraformPlansPath(environment), appName)
}

// TerraformPlanSourcePath returns the repo-relative directory holding the
// bundle source of a plan-only run:
// environments/{environment}/terraform-plans/{app_name}/{name}/src/{root}
func TerraformPlanSourcePath(environment, appName, name, root string) string {
	return filepath.Join(terraformPlanAppPath(environment, appName), name, "src", root)
}

// TerraformPlanDeploy is a plan-only run of one root of a Terraform app.
// Name identifies it among the app's runs; its files are the directory
// {name}/ with the source, and {name}.yaml.
type TerraformPlanDeploy struct {
	TerraformDeploy
	Name string
}

// DeployTerraformPlan writes a plan-only run to the plans branch in two
// commits, pushed together, like DeployTerraform does for a root: the source,
// then the run's file {name}.yaml with the GitRepository pinned to the source
// commit and the Terraform CR. The service must be a branch service of the
// plans branch.
func (s *Service) DeployTerraformPlan(p TerraformPlanDeploy) (TerraformCommits, error) {
	for _, segment := range []string{p.App, p.Environment, p.Root, p.Name} {
		if !isSafePathSegment(segment) {
			return TerraformCommits{}, fmt.Errorf("invalid app, environment, root or plan name %q", segment)
		}
	}
	return s.deployRoot(p.TerraformDeploy, rootLayout{
		sourcePath: TerraformPlanSourcePath(p.Environment, p.App, p.Name, p.Root),
		file:       p.Name + ".yaml",
		appDir:     terraformPlanAppPath(p.Environment, p.App),
		parentDir:  terraformPlansPath(p.Environment),
	})
}

// PlanEntry identifies a plan-only run on the plans branch
type PlanEntry struct {
	Environment string
	App         string
	Name        string
}

// listPlanEntries returns the plan-only runs in the working copy, sorted
func (s *Service) listPlanEntries() ([]PlanEntry, error) {
	var entries []PlanEntry
	environments, err := os.ReadDir(filepath.Join(s.workDir, "environments"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, environment := range environments {
		apps, err := os.ReadDir(filepath.Join(s.workDir, terraformPlansPath(environment.Name())))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, app := range apps {
			if !app.IsDir() {
				continue
			}
			names := map[string]bool{}
			files, err := os.ReadDir(filepath.Join(s.workDir, terraformPlanAppPath(environment.Name(), app.Name())))
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				switch {
				case f.IsDir():
					names[f.Name()] = true
				case strings.HasSuffix(f.Name(), ".yaml") && !strings.HasPrefix(f.Name(), "kustomization."):
					names[strings.TrimSuffix(f.Name(), ".yaml")] = true
				}
			}
			for name := range names {
				entries = append(entries, PlanEntry{Environment: environment.Name(), App: app.Name(), Name: name})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		return a.Environment+"/"+a.App+"/"+a.Name < b.Environment+"/"+b.App+"/"+b.Name
	})
	return entries, nil
}

// CleanTerraformPlans removes the plan-only runs of the plans branch that
// keep rejects: their source and file, and their place in the kustomizations.
// Flux then prunes the CRs, and the controller garbage-collects their plans.
// All of it is one commit; with nothing to remove, nothing is committed. It
// returns the runs it removed (or found removed already by another smithd).
//
// A run is only removed once its result is stored, so keep must accept every
// run that is still being planned.
func (s *Service) CleanTerraformPlans(message string, keep func(PlanEntry) bool) ([]PlanEntry, error) {
	var removed []PlanEntry
	_, err := s.change(message, func() error {
		removed = nil
		entries, err := s.listPlanEntries()
		if err != nil {
			return fmt.Errorf("failed to list plan-only runs: %w", err)
		}
		worktree, err := s.repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}
		for _, entry := range entries {
			if keep(entry) {
				continue
			}
			if err := s.removePlanEntry(worktree, entry); err != nil {
				return err
			}
			removed = append(removed, entry)
		}
		return s.pruneEmptyPlanApps(worktree, entries, removed)
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// removePlanEntry removes a plan-only run's source and file, and unlists the
// file from the app's kustomization
func (s *Service) removePlanEntry(worktree *git.Worktree, entry PlanEntry) error {
	appDir := terraformPlanAppPath(entry.Environment, entry.App)
	for _, rel := range []string{filepath.Join(appDir, entry.Name), filepath.Join(appDir, entry.Name+".yaml")} {
		if _, err := os.Stat(filepath.Join(s.workDir, rel)); err != nil {
			continue
		}
		if _, err := worktree.Remove(rel); err != nil {
			return fmt.Errorf("failed to remove %s: %w", rel, err)
		}
	}
	return s.unlistResource(worktree, appDir, entry.Name+".yaml")
}

// pruneEmptyPlanApps removes the directory of an app whose runs are all
// removed, and unlists it from the environment's kustomization, which stays
// (with no resources) for the Flux Kustomization on it
func (s *Service) pruneEmptyPlanApps(worktree *git.Worktree, entries, removed []PlanEntry) error {
	type app struct{ environment, name string }
	left := map[app]int{}
	for _, e := range entries {
		left[app{e.Environment, e.App}]++
	}
	for _, e := range removed {
		left[app{e.Environment, e.App}]--
	}
	for a, n := range left {
		if n > 0 {
			continue
		}
		appDir := terraformPlanAppPath(a.environment, a.name)
		for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
			rel := filepath.Join(appDir, name)
			if _, err := os.Stat(filepath.Join(s.workDir, rel)); err == nil {
				if _, err := worktree.Remove(rel); err != nil {
					return fmt.Errorf("failed to remove %s: %w", rel, err)
				}
			}
		}
		if err := s.unlistResource(worktree, terraformPlansPath(a.environment), a.name); err != nil {
			return err
		}
	}
	return nil
}

// unlistResource removes a resource from the kustomization in dir and stages
// the change
func (s *Service) unlistResource(worktree *git.Worktree, dir, resource string) error {
	filename, changed, err := removeKustomizationResource(filepath.Join(s.workDir, dir), resource)
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

// removeKustomizationResource makes sure the kustomization file in dir does
// not list resource, leaving an empty list if it was the last one. It
// returns the kustomization filename and whether the file was modified; a
// missing kustomization is not an error.
func removeKustomizationResource(dir, resource string) (string, bool, error) {
	for _, filename := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return "", false, fmt.Errorf("failed to parse %s: %w", filename, err)
		}
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			return filename, false, nil
		}
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "resources" || root.Content[i+1].Kind != yaml.SequenceNode {
				continue
			}
			resources := root.Content[i+1]
			kept := resources.Content[:0:0]
			for _, item := range resources.Content {
				if filepath.Clean(item.Value) != filepath.Clean(resource) {
					kept = append(kept, item)
				}
			}
			if len(kept) == len(resources.Content) {
				return filename, false, nil
			}
			resources.Content = kept
			if len(kept) == 0 {
				resources.Style |= yaml.FlowStyle
			}

			var buf strings.Builder
			enc := yaml.NewEncoder(&buf)
			enc.SetIndent(2)
			if err := enc.Encode(&doc); err != nil {
				return "", false, err
			}
			if err := enc.Close(); err != nil {
				return "", false, err
			}
			return filename, true, os.WriteFile(path, []byte(buf.String()), 0644)
		}
		return filename, false, nil
	}
	return "", false, nil
}

// SquashTerraformPlans replaces the history of the plans branch with a single
// commit when it has more than maxCommits and no plan-only run is left, so
// that the branch, which gets three commits for every run, doesn't grow
// forever. Nothing but the tree is kept: it holds no run, and the commits
// pinned by finished runs are not needed any more. The push is rejected, and
// the branch left alone, if somebody else pushed meanwhile. It returns
// whether the history was replaced.
func (s *Service) SquashTerraformPlans(maxCommits int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.sync(); err != nil {
		return false, fmt.Errorf("failed to sync gitops repo: %w", err)
	}
	entries, err := s.listPlanEntries()
	if err != nil {
		return false, fmt.Errorf("failed to list plan-only runs: %w", err)
	}
	if len(entries) > 0 {
		return false, nil
	}

	head, err := s.repo.Head()
	if err != nil {
		return false, fmt.Errorf("failed to read HEAD: %w", err)
	}
	log, err := s.repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return false, fmt.Errorf("failed to read the history: %w", err)
	}
	defer log.Close()
	count := 0
	err = log.ForEach(func(*object.Commit) error {
		count++
		if count > maxCommits {
			return errStopLog
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopLog) {
		return false, fmt.Errorf("failed to read the history: %w", err)
	}
	if count <= maxCommits {
		return false, nil
	}

	last, err := s.repo.CommitObject(head.Hash())
	if err != nil {
		return false, fmt.Errorf("failed to read HEAD: %w", err)
	}
	squashed := &object.Commit{
		Author:    object.Signature{Name: "DeploySmith", Email: "deploysmith@system.local", When: time.Now()},
		Committer: object.Signature{Name: "DeploySmith", Email: "deploysmith@system.local", When: time.Now()},
		Message:   "Squash the history of the " + s.branch + " branch",
		TreeHash:  last.TreeHash,
	}
	encoded := s.repo.Storer.NewEncodedObject()
	if err := squashed.Encode(encoded); err != nil {
		return false, fmt.Errorf("failed to create the squash commit: %w", err)
	}
	hash, err := s.repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		return false, fmt.Errorf("failed to create the squash commit: %w", err)
	}

	auth, err := s.getAuth()
	if err != nil {
		return false, fmt.Errorf("failed to get auth: %w", err)
	}
	// Only if the branch still is what was just read; the next sync resets
	// the working copy to the remote either way
	ref := plumbing.NewBranchReferenceName(s.branch)
	if err := s.repo.Storer.SetReference(plumbing.NewHashReference(ref, hash)); err != nil {
		return false, fmt.Errorf("failed to create the squash commit: %w", err)
	}
	err = s.repo.Push(&git.PushOptions{
		RemoteName:     "origin",
		Auth:           auth,
		RefSpecs:       []config.RefSpec{config.RefSpec("+" + ref + ":" + ref)},
		ForceWithLease: &git.ForceWithLease{RefName: ref, Hash: head.Hash()},
	})
	if err != nil {
		return false, fmt.Errorf("failed to push the squashed branch: %w", err)
	}
	return true, nil
}

var errStopLog = errors.New("stop")
