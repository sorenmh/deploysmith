package gitops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// Service handles gitops repository operations.
//
// All operations go through Deploy, which serializes access to the local
// working copy and resets it to the remote before every attempt, so a failed
// or concurrent deploy can never leave state behind for the next one.
type Service struct {
	repoURL    string
	sshKeyPath string
	workDir    string
	// branch is the branch to work on, created if the remote has none; the
	// remote's default branch if empty
	branch string

	mu   sync.Mutex
	repo *git.Repository

	pushAttempts int
	retryDelay   time.Duration

	// beforePush is a test hook invoked right before each push attempt
	beforePush func()
}

// NewService creates a new gitops service
func NewService(repoURL, sshKeyPath string) *Service {
	return &Service{
		repoURL:      repoURL,
		sshKeyPath:   sshKeyPath,
		workDir:      "/tmp/deploysmith-gitops", // Could be configurable
		pushAttempts: 3,
		retryDelay:   time.Second,
	}
}

// NewBranchService creates a gitops service for another branch of the
// repository than the default one, with a working copy of its own. The
// branch is created, with no history in common with the others, if the
// remote does not have it yet.
func NewBranchService(repoURL, sshKeyPath, branch string) *Service {
	s := NewService(repoURL, sshKeyPath)
	s.branch = branch
	s.workDir += "-" + branch
	return s
}

// isSafePathSegment reports whether name can be used as a single directory
// name without escaping its parent
func isSafePathSegment(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// appsPath returns the repo-relative directory holding an environment's apps
// kustomization: environments/{environment}/apps
func appsPath(environment string) string {
	return filepath.Join("environments", environment, "apps")
}

// appPath returns the repo-relative directory for an app's manifests:
// environments/{environment}/apps/{app_name}
func appPath(environment, appName string) string {
	return filepath.Join(appsPath(environment), appName)
}

// Deploy writes an app's manifests for an environment, commits and pushes
// them, and returns the resulting commit SHA. If the manifests are already
// up to date, nothing is committed and the current remote SHA is returned.
//
// When a push is rejected (e.g. because the remote moved), the working copy
// is reset to the new remote state and the change is re-applied.
func (s *Service) Deploy(appName, environment, message string, manifests map[string][]byte) (string, error) {
	for _, segment := range []string{appName, environment} {
		if !isSafePathSegment(segment) {
			return "", fmt.Errorf("invalid app or environment name %q", segment)
		}
	}

	files, err := s.processManifests(manifests)
	if err != nil {
		return "", err
	}

	return s.change(message, func() error {
		if err := s.writeManifests(appName, environment, files); err != nil {
			return fmt.Errorf("failed to write manifests: %w", err)
		}
		return nil
	})
}

// change applies write to a fresh copy of the remote, commits and pushes
// it, and returns the resulting commit SHA. If nothing changed, nothing is
// committed and the current remote SHA is returned. When a push is
// rejected, the working copy is reset to the new remote state and write is
// applied again.
func (s *Service) change(message string, write func() error) (string, error) {
	shas, err := s.changeSteps([]step{{message: message, write: func([]string) error { return write() }}})
	if err != nil {
		return "", err
	}
	return shas[0], nil
}

// step is one commit of changeSteps. write gets the commit SHAs of the
// steps before it.
type step struct {
	message string
	write   func(previous []string) error
}

// changeSteps is change for several commits pushed together: each step's
// write is applied and committed in turn, and all of them are pushed at
// once, so the remote gets either all commits or none. It returns each
// step's commit SHA; a step that changed nothing gets the SHA of the commit
// before it. When the push is rejected, all steps are applied again to the
// new remote state.
func (s *Service) changeSteps(steps []step) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastErr error
	for attempt := 1; attempt <= s.pushAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(s.retryDelay * time.Duration(attempt-1))
		}

		if err := s.sync(); err != nil {
			lastErr = fmt.Errorf("failed to sync gitops repo: %w", err)
			continue
		}

		shas := make([]string, 0, len(steps))
		anyChanged := false
		for _, st := range steps {
			if err := st.write(shas); err != nil {
				return nil, err
			}
			sha, changed, err := s.commit(st.message)
			if err != nil {
				return nil, fmt.Errorf("failed to commit: %w", err)
			}
			shas = append(shas, sha)
			anyChanged = anyChanged || changed
		}
		if !anyChanged {
			return shas, nil
		}

		if s.beforePush != nil {
			s.beforePush()
		}
		if err := s.push(); err != nil {
			lastErr = fmt.Errorf("failed to push: %w", err)
			continue
		}

		return shas, nil
	}

	return nil, fmt.Errorf("gitops deploy failed after %d attempts: %w", s.pushAttempts, lastErr)
}

// currentBranch returns the branch the working copy is on
func (s *Service) currentBranch() (string, error) {
	head, err := s.repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD: %w", err)
	}
	if !head.Target().IsBranch() {
		return "", fmt.Errorf("HEAD is not on a branch")
	}
	return head.Target().Short(), nil
}

// Contains reports whether revision includes commit, i.e. is the commit or
// one of its descendants. It fetches first if revision is not known yet.
func (s *Service) Contains(revision, commit string) (bool, error) {
	if revision == commit {
		return true, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo == nil || !s.hasCommit(revision) {
		if err := s.sync(); err != nil {
			return false, fmt.Errorf("failed to sync gitops repo: %w", err)
		}
	}

	descendant, err := s.repo.CommitObject(plumbing.NewHash(revision))
	if err != nil {
		return false, fmt.Errorf("failed to find commit %s: %w", revision, err)
	}
	ancestor, err := s.repo.CommitObject(plumbing.NewHash(commit))
	if err != nil {
		return false, fmt.Errorf("failed to find commit %s: %w", commit, err)
	}
	return ancestor.IsAncestor(descendant)
}

func (s *Service) hasCommit(sha string) bool {
	_, err := s.repo.CommitObject(plumbing.NewHash(sha))
	return err == nil
}

// sync makes the local working copy an exact copy of the remote branch,
// cloning it if needed. Unpushed commits, staged changes and untracked files
// from earlier attempts are discarded.
func (s *Service) sync() error {
	auth, err := s.getAuth()
	if err != nil {
		return fmt.Errorf("failed to get auth: %w", err)
	}

	if s.repo == nil {
		if repo, err := git.PlainOpen(s.workDir); err == nil {
			s.repo = repo
		} else {
			// Remove work dir if it exists but isn't a usable git repo
			os.RemoveAll(s.workDir)
			repo, err := s.clone(auth)
			if err != nil {
				return err
			}
			s.repo = repo
			return nil
		}
	}

	err = s.repo.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Auth:       auth,
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	head, err := s.repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return fmt.Errorf("failed to read HEAD: %w", err)
	}
	branch := head.Target()
	if !branch.IsBranch() {
		return fmt.Errorf("HEAD is not on a branch")
	}

	remoteRef, err := s.repo.Reference(plumbing.NewRemoteReferenceName("origin", branch.Short()), true)
	if err != nil {
		return fmt.Errorf("failed to resolve origin/%s: %w", branch.Short(), err)
	}

	worktree, err := s.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}
	if err := worktree.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: remoteRef.Hash()}); err != nil {
		return fmt.Errorf("failed to reset to origin/%s: %w", branch.Short(), err)
	}
	if err := worktree.Clean(&git.CleanOptions{Dir: true}); err != nil {
		return fmt.Errorf("failed to clean worktree: %w", err)
	}

	return nil
}

// clone clones the remote into the working directory, on the service's
// branch if it has one
func (s *Service) clone(auth transport.AuthMethod) (*git.Repository, error) {
	options := &git.CloneOptions{URL: s.repoURL, Auth: auth}
	if s.branch != "" {
		options.ReferenceName = plumbing.NewBranchReferenceName(s.branch)
		options.SingleBranch = true
	}
	var noRef git.NoMatchingRefSpecError
	repo, err := git.PlainClone(s.workDir, false, options)
	if s.branch != "" && errors.As(err, &noRef) {
		os.RemoveAll(s.workDir)
		return s.createBranch(auth)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to clone repo: %w", err)
	}
	return repo, nil
}

// createBranch creates the service's branch on the remote, with a single
// commit that has no parent, so that it never shares history with, or
// merges into, the repository's other branches
func (s *Service) createBranch(auth transport.AuthMethod) (*git.Repository, error) {
	fail := func(err error) (*git.Repository, error) {
		return nil, fmt.Errorf("failed to create branch %s: %w", s.branch, err)
	}
	repo, err := git.PlainInit(s.workDir, false)
	if err != nil {
		return fail(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(s.branch))); err != nil {
		return fail(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{s.repoURL}}); err != nil {
		return fail(err)
	}
	readme := fmt.Sprintf("# %s\n\nWritten by DeploySmith (smithd), do not edit. This branch holds the plan-only runs of Terraform apps while they run and is cleaned up afterwards. Its history is rewritten from time to time.\n", s.branch)
	if err := os.WriteFile(filepath.Join(s.workDir, "README.md"), []byte(readme), 0644); err != nil {
		return fail(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return fail(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		return fail(err)
	}
	created := &Service{repo: repo, sshKeyPath: s.sshKeyPath}
	if _, _, err := created.commit("Create the " + s.branch + " branch"); err != nil {
		return fail(err)
	}
	if err := created.push(); err != nil {
		return fail(err)
	}
	return repo, nil
}

// processManifests flattens the uploaded files, extracting tarballs if present
func (s *Service) processManifests(manifests map[string][]byte) (map[string][]byte, error) {
	processedManifests := make(map[string][]byte)

	for filename, content := range manifests {
		if filename == "manifests.tar.gz" {
			extractedFiles, err := s.extractTarball(content)
			if err != nil {
				return nil, fmt.Errorf("failed to extract tarball %s: %w", filename, err)
			}

			for extractedFilename, extractedContent := range extractedFiles {
				// Only include YAML files
				if strings.HasSuffix(extractedFilename, ".yaml") || strings.HasSuffix(extractedFilename, ".yml") {
					processedManifests[extractedFilename] = extractedContent
				}
			}
		} else {
			processedManifests[filename] = content
		}
	}

	return processedManifests, nil
}

// writeManifests writes the app's files into the working copy, registers the
// app in the environment's apps kustomization, and stages everything
func (s *Service) writeManifests(appName, environment string, files map[string][]byte) error {
	appDir := filepath.Join(s.workDir, appPath(environment, appName))
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	for filename, content := range files {
		filePath := filepath.Join(appDir, filename)
		// Reject entries such as "../x.yaml" that would escape the app directory
		if rel, err := filepath.Rel(appDir, filePath); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("invalid manifest path %q", filename)
		}
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", filename, err)
		}
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			return fmt.Errorf("failed to write manifest %s: %w", filename, err)
		}
	}

	worktree, err := s.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	if err := worktree.AddGlob(appPath(environment, appName) + "/*"); err != nil {
		return fmt.Errorf("failed to add files to git: %w", err)
	}

	// Make sure the app is referenced from the environment's apps kustomization
	kustomizationFile, changed, err := ensureKustomizationResource(filepath.Join(s.workDir, appsPath(environment)), appName)
	if err != nil {
		return fmt.Errorf("failed to update apps kustomization: %w", err)
	}
	if changed {
		relativeKustomization := filepath.Join(appsPath(environment), kustomizationFile)
		if _, err := worktree.Add(relativeKustomization); err != nil {
			return fmt.Errorf("failed to add %s to git: %w", relativeKustomization, err)
		}
	}

	return nil
}

// commit commits the staged changes. It returns the commit SHA and whether a
// new commit was created; with nothing to commit it returns the HEAD SHA.
func (s *Service) commit(message string) (string, bool, error) {
	worktree, err := s.repo.Worktree()
	if err != nil {
		return "", false, fmt.Errorf("failed to get worktree: %w", err)
	}

	commitHash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "DeploySmith",
			Email: "deploysmith@system.local",
			When:  time.Now(),
		},
	})
	if errors.Is(err, git.ErrEmptyCommit) {
		head, err := s.repo.Head()
		if err != nil {
			return "", false, fmt.Errorf("failed to read HEAD: %w", err)
		}
		return head.Hash().String(), false, nil
	}
	if err != nil {
		return "", false, err
	}

	return commitHash.String(), true, nil
}

// push pushes the current branch to the remote
func (s *Service) push() error {
	auth, err := s.getAuth()
	if err != nil {
		return fmt.Errorf("failed to get auth: %w", err)
	}

	err = s.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return err
	}

	return nil
}

// getAuth returns SSH authentication, or nil when no SSH key is configured
// (e.g. for HTTPS URLs with embedded credentials or local repositories)
func (s *Service) getAuth() (transport.AuthMethod, error) {
	if s.sshKeyPath == "" {
		return nil, nil
	}

	auth, err := ssh.NewPublicKeysFromFile("git", s.sshKeyPath, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH auth: %w", err)
	}

	// Disable host key verification to avoid known_hosts issues
	auth.HostKeyCallback = cryptossh.InsecureIgnoreHostKey()

	return auth, nil
}

// ensureKustomizationResource makes sure the kustomization file in dir lists
// resource. If no kustomization file exists, one is created. It returns the
// kustomization filename and whether the file was modified.
func ensureKustomizationResource(dir, resource string) (string, bool, error) {
	filename := "kustomization.yaml"
	for _, candidate := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			filename = candidate
			break
		}
	}
	path := filepath.Join(dir, filename)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content := fmt.Sprintf("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - %s\n", resource)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", false, err
		}
		return filename, true, nil
	}
	if err != nil {
		return "", false, err
	}

	// Work on the node tree so comments and ordering are preserved
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("failed to parse %s: %w", filename, err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", false, fmt.Errorf("%s is not a YAML mapping", filename)
	}

	var resources *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "resources" {
			resources = root.Content[i+1]
			break
		}
	}
	if resources == nil || resources.Tag == "!!null" {
		if resources == nil {
			resources = &yaml.Node{}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "resources"}, resources)
		}
		resources.Kind = yaml.SequenceNode
		resources.Tag = "!!seq"
		resources.Value = ""
	}
	if resources.Kind != yaml.SequenceNode {
		return "", false, fmt.Errorf("resources in %s is not a list", filename)
	}

	for _, item := range resources.Content {
		if filepath.Clean(item.Value) == filepath.Clean(resource) {
			return filename, false, nil
		}
	}
	resources.Style &^= yaml.FlowStyle
	resources.Content = append(resources.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: resource})

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", false, err
	}
	if err := enc.Close(); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return "", false, err
	}
	return filename, true, nil
}

// Commit commits the changes and returns the commit SHA
func (s *Service) Commit(message string) (string, error) {
	if s.repo == nil {
		return "", fmt.Errorf("repository not initialized, call Clone() first")
	}

	worktree, err := s.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Create commit
	commitHash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "DeploySmith",
			Email: "deploysmith@system.local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	return commitHash.String(), nil
}

// Push pushes the commits to the remote repository
func (s *Service) Push() error {
	if s.repo == nil {
		return fmt.Errorf("repository not initialized, call Clone() first")
	}

	auth, err := s.getAuth()
	if err != nil {
		return fmt.Errorf("failed to get auth: %w", err)
	}

	err = s.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       auth,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// Cleanup removes the working directory
func (s *Service) Cleanup() error {
	if s.workDir != "" {
		return os.RemoveAll(s.workDir)
	}
	return nil
}

// extractTarball extracts files from a gzipped tarball
func (s *Service) extractTarball(data []byte) (map[string][]byte, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	files := make(map[string][]byte)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		// Skip directories
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Read file content
		content, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		files[header.Name] = content
	}

	return files, nil
}
