package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"gopkg.in/yaml.v3"
)

// GitClient interface for git operations
type GitClient interface {
	UpdateImageTag(manifestPath, newTag string) (string, error)
	GetCurrentImageTag(manifestPath string) (string, error)
	WriteServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error)
	UpdateServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error)
	DeleteServiceManifests(serviceName, targetDirectory string) (string, error)
	CheckHealth() error
}

type Client struct {
	repoURL     string
	branch      string
	localPath   string
	username    string
	token       string
	authorName  string
	authorEmail string
	repo        *git.Repository
}

func NewClient(repoURL, branch, localPath, username, token, authorName, authorEmail string) (*Client, error) {
	c := &Client{
		repoURL:     repoURL,
		branch:      branch,
		localPath:   localPath,
		username:    username,
		token:       token,
		authorName:  authorName,
		authorEmail: authorEmail,
	}

	if err := c.ensureRepo(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) ensureRepo() error {
	// Check if repo already exists
	if _, err := os.Stat(filepath.Join(c.localPath, ".git")); err == nil {
		// Open existing repo
		repo, err := git.PlainOpen(c.localPath)
		if err != nil {
			return fmt.Errorf("failed to open repository: %w", err)
		}
		c.repo = repo
		return c.pull()
	}

	// Clone repo
	repo, err := git.PlainClone(c.localPath, false, &git.CloneOptions{
		URL:           c.repoURL,
		Auth:          c.auth(),
		ReferenceName: plumbing.NewBranchReferenceName(c.branch),
		SingleBranch:  true,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	c.repo = repo
	return nil
}

func (c *Client) auth() *http.BasicAuth {
	return &http.BasicAuth{
		Username: c.username,
		Password: c.token,
	}
}

func (c *Client) pull() error {
	w, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	err = w.Pull(&git.PullOptions{
		Auth:          c.auth(),
		ReferenceName: plumbing.NewBranchReferenceName(c.branch),
		SingleBranch:  true,
	})

	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to pull: %w", err)
	}

	return nil
}

func (c *Client) UpdateImageTag(manifestPath, newTag string) (string, error) {
	// Ensure we have latest
	if err := c.pull(); err != nil {
		return "", fmt.Errorf("failed to pull latest: %w", err)
	}

	fullPath := filepath.Join(c.localPath, manifestPath)

	// Read manifest
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read manifest: %w", err)
	}

	// Parse YAML
	var manifest map[string]interface{}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Update image tag based on workload type
	updated, err := c.updateImageInManifest(manifest, newTag)
	if err != nil {
		return "", err
	}

	if !updated {
		return "", fmt.Errorf("failed to find image field in manifest")
	}

	// Write back
	newData, err := yaml.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(fullPath, newData, 0644); err != nil {
		return "", fmt.Errorf("failed to write manifest: %w", err)
	}

	return c.commitAndPush(manifestPath, newTag)
}

func (c *Client) updateImageInManifest(manifest map[string]interface{}, newTag string) (bool, error) {
	// Navigate through the YAML structure to find the image field
	// Support Deployment, StatefulSet, CronJob

	spec, ok := manifest["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	var template map[string]interface{}

	// Check if it's a CronJob
	if jobTemplate, ok := spec["jobTemplate"].(map[string]interface{}); ok {
		spec = jobTemplate["spec"].(map[string]interface{})
	}

	// Get template
	template, ok = spec["template"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	templateSpec, ok := template["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	containers, ok := templateSpec["containers"].([]interface{})
	if !ok || len(containers) == 0 {
		return false, nil
	}

	// Update first container's image
	container, ok := containers[0].(map[string]interface{})
	if !ok {
		return false, nil
	}

	currentImage, ok := container["image"].(string)
	if !ok {
		return false, nil
	}

	// Extract image name (everything before the tag)
	imageParts := strings.Split(currentImage, ":")
	if len(imageParts) == 0 {
		return false, fmt.Errorf("invalid image format")
	}

	newImage := imageParts[0] + ":" + newTag

	// Preserve image policy marker if it exists
	if strings.Contains(currentImage, "# {\"$imagepolicy\"") {
		lines := strings.Split(currentImage, "#")
		if len(lines) > 1 {
			newImage = newImage + " # " + strings.TrimSpace(lines[1])
		}
	}

	container["image"] = newImage
	return true, nil
}

func (c *Client) commitAndPush(manifestPath, version string) (string, error) {
	w, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Stage file
	if _, err := w.Add(manifestPath); err != nil {
		return "", fmt.Errorf("failed to stage file: %w", err)
	}

	// Commit
	message := fmt.Sprintf("Deploy version %s\n\nUpdated: %s\nDeployed at: %s",
		version, manifestPath, time.Now().Format(time.RFC3339))

	commit, err := w.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.authorName,
			Email: c.authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	// Push
	err = c.repo.Push(&git.PushOptions{
		Auth: c.auth(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to push: %w", err)
	}

	return commit.String(), nil
}

func (c *Client) GetCurrentImageTag(manifestPath string) (string, error) {
	if err := c.pull(); err != nil {
		return "", fmt.Errorf("failed to pull latest: %w", err)
	}

	fullPath := filepath.Join(c.localPath, manifestPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest map[string]interface{}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Navigate to image field
	spec, ok := manifest["spec"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid manifest structure")
	}

	// Handle CronJob
	if jobTemplate, ok := spec["jobTemplate"].(map[string]interface{}); ok {
		spec = jobTemplate["spec"].(map[string]interface{})
	}

	template, ok := spec["template"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid manifest structure")
	}

	templateSpec, ok := template["spec"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid manifest structure")
	}

	containers, ok := templateSpec["containers"].([]interface{})
	if !ok || len(containers) == 0 {
		return "", fmt.Errorf("no containers found")
	}

	container, ok := containers[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid container structure")
	}

	image, ok := container["image"].(string)
	if !ok {
		return "", fmt.Errorf("no image found")
	}

	// Extract tag
	parts := strings.Split(strings.Split(image, " ")[0], ":")
	if len(parts) < 2 {
		return "latest", nil
	}

	return parts[1], nil
}

// Service Abstraction Layer Git Operations

// WriteServiceManifests writes multiple manifests for a service to the git repository atomically
func (c *Client) WriteServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error) {
	// Ensure we have latest
	if err := c.pull(); err != nil {
		return "", fmt.Errorf("failed to pull latest: %w", err)
	}

	// Create target directory if it doesn't exist
	fullTargetPath := filepath.Join(c.localPath, targetDirectory)
	if err := os.MkdirAll(fullTargetPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	// Write all manifests
	var writtenFiles []string
	for filename, content := range manifests {
		fullPath := filepath.Join(fullTargetPath, filename)

		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			// Clean up any files we've written on error
			c.cleanupFiles(writtenFiles)
			return "", fmt.Errorf("failed to write manifest %s: %w", filename, err)
		}

		// Store relative path for git operations
		relativePath := filepath.Join(targetDirectory, filename)
		writtenFiles = append(writtenFiles, relativePath)
	}

	// Commit and push all files atomically
	return c.commitAndPushMultiple(serviceName, targetDirectory, writtenFiles)
}

// cleanupFiles removes files that were written during a failed operation
func (c *Client) cleanupFiles(filePaths []string) {
	for _, filePath := range filePaths {
		fullPath := filepath.Join(c.localPath, filePath)
		os.Remove(fullPath) // Best effort cleanup, ignore errors
	}
}

// commitAndPushMultiple commits multiple files atomically
func (c *Client) commitAndPushMultiple(serviceName, targetDirectory string, filePaths []string) (string, error) {
	w, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Stage all files
	for _, filePath := range filePaths {
		if _, err := w.Add(filePath); err != nil {
			return "", fmt.Errorf("failed to stage file %s: %w", filePath, err)
		}
	}

	// Create commit message
	message := fmt.Sprintf("Deploy service %s\n\nGenerated manifests:\n", serviceName)
	for _, filePath := range filePaths {
		message += fmt.Sprintf("  - %s\n", filePath)
	}
	message += fmt.Sprintf("\nTarget directory: %s\nDeployed at: %s",
		targetDirectory, time.Now().Format(time.RFC3339))

	// Commit
	commit, err := w.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.authorName,
			Email: c.authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	// Push
	err = c.repo.Push(&git.PushOptions{
		Auth: c.auth(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to push: %w", err)
	}

	return commit.String(), nil
}

// UpdateServiceManifests updates existing service manifests in the git repository
func (c *Client) UpdateServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error) {
	return c.WriteServiceManifests(serviceName, targetDirectory, manifests)
}

// DeleteServiceManifests removes all manifests for a service from the git repository
func (c *Client) DeleteServiceManifests(serviceName, targetDirectory string) (string, error) {
	// Ensure we have latest
	if err := c.pull(); err != nil {
		return "", fmt.Errorf("failed to pull latest: %w", err)
	}

	fullTargetPath := filepath.Join(c.localPath, targetDirectory)

	// Check if directory exists
	if _, err := os.Stat(fullTargetPath); os.IsNotExist(err) {
		return "", fmt.Errorf("service directory does not exist: %s", targetDirectory)
	}

	// Remove directory and all contents
	if err := os.RemoveAll(fullTargetPath); err != nil {
		return "", fmt.Errorf("failed to remove service directory: %w", err)
	}

	// Commit and push the deletion
	w, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Stage the directory removal
	if _, err := w.Add(targetDirectory); err != nil {
		return "", fmt.Errorf("failed to stage directory removal: %w", err)
	}

	// Create commit message
	message := fmt.Sprintf("Remove service %s\n\nDeleted directory: %s\nRemoved at: %s",
		serviceName, targetDirectory, time.Now().Format(time.RFC3339))

	// Commit
	commit, err := w.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.authorName,
			Email: c.authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	// Push
	err = c.repo.Push(&git.PushOptions{
		Auth: c.auth(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to push: %w", err)
	}

	return commit.String(), nil
}

func (c *Client) CheckHealth() error {
	// Attempt to pull to check connectivity and authentication
	err := c.pull()
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("git client health check failed: %w", err)
	}
	return nil
}
