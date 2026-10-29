package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/spf13/cobra"
)

var (
	initApp          string
	initVersion      string
	initGitSHA       string
	initGitBranch    string
	initGitCommitter string
	initBuildNumber  int
	initArtifactType string
	initSourceRepo   string
	initSourceSHA    string
	initPullRequest  int
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new version draft",
	Long: `Initialize a new version draft with smithd and get a presigned URL for uploading manifests.

The presigned URL is saved to .forge/upload-url for use by the upload command.

In GitHub Actions the source repository, commit and pull request are detected
(for pull_request events the PR's head commit, not the merge commit) and sent
along, so smithd can report deployments as check runs on the commit.

A version carries Kubernetes manifests, or with --artifact-type terraform an
OpenTofu bundle, uploaded with 'forge upload --terraform <dir>'.

Examples:
  forge init --app my-app --version v1.0.0 --git-sha abc123 --git-branch main
  forge init --version "$GIT_SHA" --git-sha "$GIT_SHA" --artifact-type terraform`,
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringVar(&initApp, "app", "", "Application name (optional if .deploysmith/app.yaml exists)")
	initCmd.Flags().StringVar(&initVersion, "version", "", "Version identifier (required)")
	initCmd.Flags().StringVar(&initGitSHA, "git-sha", "", "Git commit SHA")
	initCmd.Flags().StringVar(&initGitBranch, "git-branch", "", "Git branch name (detected from CI environment or git if omitted)")
	initCmd.Flags().StringVar(&initGitCommitter, "git-committer", "", "Git committer email")
	initCmd.Flags().IntVar(&initBuildNumber, "build-number", 0, "CI build number")
	initCmd.Flags().StringVar(&initArtifactType, "artifact-type", "manifests", "What the version carries: manifests or terraform")
	initCmd.Flags().StringVar(&initSourceRepo, "source-repo", "", "GitHub repository (owner/name) the version is built from (default $GITHUB_REPOSITORY)")
	initCmd.Flags().StringVar(&initSourceSHA, "source-sha", "", "Full commit SHA to report deployments on (detected in GitHub Actions)")
	initCmd.Flags().IntVar(&initPullRequest, "pr", 0, "Pull request number (detected in GitHub Actions)")

	initCmd.MarkFlagRequired("version")
}

func runInit(cmd *cobra.Command, args []string) error {
	// Validate required config
	if err := ValidateConfig(); err != nil {
		return err
	}

	if initArtifactType != "manifests" && initArtifactType != tfbundle.ArtifactType {
		return fmt.Errorf("unknown --artifact-type %q (use manifests or %s)", initArtifactType, tfbundle.ArtifactType)
	}

	// Resolve app ID
	appID, appName, err := ResolveAppID(initApp)
	if err != nil {
		return err
	}

	source := resolveSource(detectSource(), initSourceRepo, initSourceSHA, initPullRequest)

	// Set defaults for required fields if not provided
	gitSHA := initGitSHA
	if gitSHA == "" && source != nil {
		gitSHA = source.CommitSHA
	}
	if gitSHA == "" {
		gitSHA = "unknown"
	}

	gitBranch := initGitBranch
	if gitBranch == "" {
		gitBranch = detectGitBranch()
	}
	if gitBranch == "" {
		fmt.Fprintln(os.Stderr, "Warning: could not determine git branch; auto-deploy policies will not match this version (pass --git-branch)")
		gitBranch = "unknown"
	}

	// Build request
	metadata := client.VersionMetadata{
		GitSHA:       gitSHA,
		GitBranch:    gitBranch,
		GitCommitter: initGitCommitter,
		Timestamp:    time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}

	if initBuildNumber > 0 {
		metadata.BuildNumber = strconv.Itoa(initBuildNumber)
	}

	req := client.DraftVersionRequest{
		VersionID:    initVersion,
		Metadata:     metadata,
		ArtifactType: initArtifactType,
		Source:       source,
	}

	// Call smithd API
	c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())
	resp, err := c.CreateDraftVersion(appID, req)
	if err != nil {
		return fmt.Errorf("failed to create draft version: %w", err)
	}

	// Create .forge directory
	forgeDir := ".forge"
	if err := os.MkdirAll(forgeDir, 0755); err != nil {
		return fmt.Errorf("failed to create .forge directory: %w", err)
	}

	// Save upload URL to file
	uploadURLFile := filepath.Join(forgeDir, "upload-url")
	if err := os.WriteFile(uploadURLFile, []byte(resp.UploadURL), 0644); err != nil {
		return fmt.Errorf("failed to save upload URL: %w", err)
	}

	// Save version info for later commands
	versionFile := filepath.Join(forgeDir, "version-info")
	versionInfo := VersionInfo{
		App:     appName,
		AppID:   appID,
		Version: initVersion,
	}
	if initArtifactType != "manifests" {
		versionInfo.ArtifactType = initArtifactType
	}
	versionJSON, _ := json.Marshal(versionInfo)
	if err := os.WriteFile(versionFile, versionJSON, 0644); err != nil {
		return fmt.Errorf("failed to save version info: %w", err)
	}

	// Output JSON response
	output := map[string]interface{}{
		"versionId":     resp.VersionID,
		"uploadUrl":     resp.UploadURL,
		"uploadExpires": resp.UploadExpires.Format("2006-01-02T15:04:05Z"),
	}
	outputJSON, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal output: %w", err)
	}

	fmt.Println(string(outputJSON))
	return nil
}
