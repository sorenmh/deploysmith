package cmd

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	uploadURLOverride string
)

var uploadCmd = &cobra.Command{
	Use:   "upload [files or directory]",
	Short: "Upload manifest files to S3",
	Long: `Upload manifest YAML files to S3 using the presigned URL from forge init.

You can upload a directory:
  forge upload manifests/

Or specific files:
  forge upload deployment.yaml service.yaml

If version.yml is not present, it will be auto-generated.`,
	RunE: runUpload,
}

func init() {
	rootCmd.AddCommand(uploadCmd)

	uploadCmd.Flags().StringVar(&uploadURLOverride, "upload-url", "", "Override upload URL (otherwise reads from .forge/upload-url)")
}

func runUpload(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no files or directory specified")
	}

	// Get upload URL
	uploadURL := uploadURLOverride
	if uploadURL == "" {
		data, err := os.ReadFile(".forge/upload-url")
		if err != nil {
			return fmt.Errorf("failed to read upload URL from .forge/upload-url: %w\nDid you run 'forge init' first?", err)
		}
		uploadURL = strings.TrimSpace(string(data))
	}

	// Collect files to upload
	files := []string{}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", arg, err)
		}

		if info.IsDir() {
			// Walk directory and find all YAML files
			err := filepath.Walk(arg, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to walk directory %s: %w", arg, err)
			}
		} else {
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		return fmt.Errorf("no YAML files found")
	}

	// Check if version.yml exists in files
	hasVersionYML := false
	for _, f := range files {
		if filepath.Base(f) == "version.yml" {
			hasVersionYML = true
			break
		}
	}

	// Auto-generate version.yml if not present
	var versionYMLContent []byte
	if !hasVersionYML {
		versionInfo, err := loadVersionInfo()
		if err != nil {
			return fmt.Errorf("failed to load version info: %w", err)
		}

		versionData := map[string]interface{}{
			"version": versionInfo["version"],
			"metadata": map[string]interface{}{
				"timestamp": time.Now().Format(time.RFC3339),
			},
		}

		versionYMLContent, err = yaml.Marshal(versionData)
		if err != nil {
			return fmt.Errorf("failed to generate version.yml: %w", err)
		}
	}

	fmt.Println("Uploading manifests...")

	// Validate all files are valid YAML
	for _, file := range files {
		if err := validateYAML(file); err != nil {
			return fmt.Errorf("validation failed for %s: %w", file, err)
		}
	}

	// Upload files
	totalSize := int64(0)
	startTime := time.Now()

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", file, err)
		}

		if err := uploadFile(uploadURL, file); err != nil {
			return fmt.Errorf("failed to upload %s: %w", file, err)
		}

		totalSize += info.Size()
		fmt.Printf("  ✓ %s (%.1f KB)\n", filepath.Base(file), float64(info.Size())/1024)
	}

	// Upload auto-generated version.yml if needed
	if !hasVersionYML && versionYMLContent != nil {
		if err := uploadContent(uploadURL, "version.yml", versionYMLContent); err != nil {
			return fmt.Errorf("failed to upload version.yml: %w", err)
		}
		totalSize += int64(len(versionYMLContent))
		fmt.Printf("  ✓ version.yml (%.1f KB)\n", float64(len(versionYMLContent))/1024)
	}

	duration := time.Since(startTime)
	fileCount := len(files)
	if !hasVersionYML {
		fileCount++
	}

	fmt.Printf("\nUploaded %d files (%.1f KB) in %.1fs\n", fileCount, float64(totalSize)/1024, duration.Seconds())
	return nil
}

func loadVersionInfo() (map[string]string, error) {
	data, err := os.ReadFile(".forge/version-info")
	if err != nil {
		return nil, err
	}

	var info map[string]string
	if err := yaml.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	return info, nil
}

func validateYAML(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var content interface{}
	if err := yaml.Unmarshal(data, &content); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	return nil
}

func uploadFile(presignedURL, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	return uploadContent(presignedURL, filepath.Base(filePath), data)
}

func uploadContent(presignedURL, filename string, content []byte) error {
	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}

	if _, err := part.Write(content); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	// Send request
	req, err := http.NewRequest("POST", presignedURL, &buf)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
