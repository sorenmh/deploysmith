package gitops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// Service handles gitops repository operations
type Service struct {
	repoURL    string
	sshKeyPath string
	workDir    string
	repo       *git.Repository
}

// NewService creates a new gitops service
func NewService(repoURL, sshKeyPath string) *Service {
	return &Service{
		repoURL:    repoURL,
		sshKeyPath: sshKeyPath,
		workDir:    "/tmp/deploysmith-gitops", // Could be configurable
	}
}

// Clone clones the gitops repository or pulls if it already exists
func (s *Service) Clone() error {
	// Check if repo already exists
	if _, err := os.Stat(filepath.Join(s.workDir, ".git")); err == nil {
		// Repo exists, try to open and pull
		repo, err := git.PlainOpen(s.workDir)
		if err != nil {
			return fmt.Errorf("failed to open existing repo: %w", err)
		}
		s.repo = repo

		// Pull latest changes
		worktree, err := repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}

		auth, err := s.getAuth()
		if err != nil {
			return fmt.Errorf("failed to get auth: %w", err)
		}

		err = worktree.Pull(&git.PullOptions{
			RemoteName: "origin",
			Auth:       auth,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("failed to pull: %w", err)
		}

		return nil
	}

	// Clone fresh
	auth, err := s.getAuth()
	if err != nil {
		return fmt.Errorf("failed to get auth: %w", err)
	}

	// Remove work dir if it exists but isn't a git repo
	os.RemoveAll(s.workDir)

	// Clone the repository
	repo, err := git.PlainClone(s.workDir, false, &git.CloneOptions{
		URL:      s.repoURL,
		Auth:     auth,
		Progress: nil, // Could add progress tracking
	})
	if err != nil {
		return fmt.Errorf("failed to clone repo: %w", err)
	}

	s.repo = repo
	return nil
}

// WriteManifests writes manifest files to the gitops repo
func (s *Service) WriteManifests(appName, environment, versionID string, manifests map[string][]byte) error {
	if s.repo == nil {
		return fmt.Errorf("repository not initialized, call Clone() first")
	}

	// Create directory structure: environments/{environment}/apps/{app_name}/
	appDir := filepath.Join(s.workDir, "environments", environment, "apps", appName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	// Process manifest files, extracting tarballs if present
	processedManifests := make(map[string][]byte)

	for filename, content := range manifests {
		if filename == "manifests.tar.gz" {
			// Extract tarball contents
			extractedFiles, err := s.extractTarball(content)
			if err != nil {
				return fmt.Errorf("failed to extract tarball %s: %w", filename, err)
			}

			// Add extracted files to processed manifests
			for extractedFilename, extractedContent := range extractedFiles {
				// Only include YAML files
				if strings.HasSuffix(extractedFilename, ".yaml") || strings.HasSuffix(extractedFilename, ".yml") {
					processedManifests[extractedFilename] = extractedContent
				}
			}
		} else {
			// Regular file, add as-is
			processedManifests[filename] = content
		}
	}

	// Write each processed manifest file
	for filename, content := range processedManifests {
		filePath := filepath.Join(appDir, filename)
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			return fmt.Errorf("failed to write manifest %s: %w", filename, err)
		}
	}

	// Add files to git
	worktree, err := s.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Add the entire app directory
	relativePath := filepath.Join("environments", environment, "apps", appName)
	if err := worktree.AddGlob(relativePath + "/*"); err != nil {
		return fmt.Errorf("failed to add files to git: %w", err)
	}

	// Make sure the app is referenced from the environment's apps kustomization
	appsDir := filepath.Join(s.workDir, "environments", environment, "apps")
	kustomizationFile, changed, err := ensureKustomizationResource(appsDir, appName)
	if err != nil {
		return fmt.Errorf("failed to update apps kustomization: %w", err)
	}
	if changed {
		relativeKustomization := filepath.Join("environments", environment, "apps", kustomizationFile)
		if _, err := worktree.Add(relativeKustomization); err != nil {
			return fmt.Errorf("failed to add %s to git: %w", relativeKustomization, err)
		}
	}

	return nil
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

// getAuth returns SSH authentication
func (s *Service) getAuth() (*ssh.PublicKeys, error) {
	if s.sshKeyPath == "" {
		return nil, fmt.Errorf("SSH key path not configured")
	}

	auth, err := ssh.NewPublicKeysFromFile("git", s.sshKeyPath, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH auth: %w", err)
	}

	// Disable host key verification to avoid known_hosts issues
	auth.HostKeyCallback = cryptossh.InsecureIgnoreHostKey()

	return auth, nil
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
