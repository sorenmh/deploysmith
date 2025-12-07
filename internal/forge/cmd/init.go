package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deploysmith/deploysmith/internal/forge/client"
	"github.com/spf13/cobra"
)

var (
	initApp          string
	initVersion      string
	initGitSHA       string
	initGitBranch    string
	initGitCommitter string
	initBuildNumber  int
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new version draft",
	Long: `Initialize a new version draft with smithd and get a presigned URL for uploading manifests.

The presigned URL is saved to .forge/upload-url for use by the upload command.

Example:
  forge init --app my-app --version v1.0.0 --git-sha abc123 --git-branch main`,
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringVar(&initApp, "app", "", "Application name (required)")
	initCmd.Flags().StringVar(&initVersion, "version", "", "Version identifier (required)")
	initCmd.Flags().StringVar(&initGitSHA, "git-sha", "", "Git commit SHA")
	initCmd.Flags().StringVar(&initGitBranch, "git-branch", "", "Git branch name")
	initCmd.Flags().StringVar(&initGitCommitter, "git-committer", "", "Git committer email")
	initCmd.Flags().IntVar(&initBuildNumber, "build-number", 0, "CI build number")

	initCmd.MarkFlagRequired("app")
	initCmd.MarkFlagRequired("version")
}

func runInit(cmd *cobra.Command, args []string) error {
	// Validate required config
	if smithdURL == "" {
		return fmt.Errorf("--smithd-url or SMITHD_URL environment variable is required")
	}
	if smithdAPIKey == "" {
		return fmt.Errorf("--smithd-api-key or SMITHD_API_KEY environment variable is required")
	}

	// Build request
	req := client.DraftVersionRequest{
		Version: initVersion,
	}

	if initGitSHA != "" {
		req.GitSHA = &initGitSHA
	}
	if initGitBranch != "" {
		req.GitBranch = &initGitBranch
	}
	if initGitCommitter != "" {
		req.GitCommitter = &initGitCommitter
	}
	if initBuildNumber > 0 {
		req.BuildNumber = &initBuildNumber
	}

	// Call smithd API
	c := client.NewClient(smithdURL, smithdAPIKey)
	resp, err := c.CreateDraftVersion(initApp, req)
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
	versionInfo := map[string]string{
		"app":     initApp,
		"version": initVersion,
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
