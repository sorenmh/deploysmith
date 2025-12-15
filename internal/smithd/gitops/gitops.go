package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
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

	// Write each manifest file
	for filename, content := range manifests {
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

	return nil
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
