package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	uploadURLOverride string
	uploadTerraform   string
)

var uploadCmd = &cobra.Command{
	Use:   "upload [files or directory]",
	Short: "Upload manifest files or a terraform bundle to S3",
	Long: `Upload manifest YAML files to S3 using the presigned URL from forge init.

You can upload a directory:
  forge upload manifests/

Or specific files:
  forge upload deployment.yaml service.yaml

If version.yml is not present, it will be auto-generated.

For a version initialized with --artifact-type terraform, upload the
OpenTofu root module instead. forge packs its *.tf files, the
.terraform.lock.hcl lock file and env/<env>.tfvars, after checking that
the lock file is there, the OpenTofu version is pinned with an exact
required_version, the backend block is empty and there are no state files
or automatically loaded tfvars:
  forge upload --terraform infra/`,
	RunE: runUpload,
}

func init() {
	rootCmd.AddCommand(uploadCmd)

	uploadCmd.Flags().StringVar(&uploadURLOverride, "upload-url", "", "Override upload URL (otherwise reads from .forge/upload-url)")
	uploadCmd.Flags().StringVar(&uploadTerraform, "terraform", "", "Upload the OpenTofu root module in this directory as a terraform bundle")
}

func runUpload(cmd *cobra.Command, args []string) error {
	if uploadTerraform != "" {
		if len(args) > 0 {
			return fmt.Errorf("--terraform uploads one directory; do not pass files as well")
		}
		return runUploadTerraform(uploadTerraform)
	}

	if len(args) == 0 {
		return fmt.Errorf("no files or directory specified")
	}

	uploadURL, err := resolveUploadURL()
	if err != nil {
		return err
	}
	if err := checkArtifactType("manifests"); err != nil {
		return err
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
		versionInfo, err := LoadVersionInfo()
		if err != nil {
			return fmt.Errorf("failed to load version info: %w", err)
		}

		versionData := map[string]interface{}{
			"version": versionInfo.Version,
			"metadata": map[string]interface{}{
				"timestamp": time.Now().Format(time.RFC3339),
			},
		}

		versionYMLContent, err = yaml.Marshal(versionData)
		if err != nil {
			return fmt.Errorf("failed to generate version.yml: %w", err)
		}
	}

	fmt.Println("Creating manifest archive...")

	// Validate all files are valid YAML
	for _, file := range files {
		if err := validateYAML(file); err != nil {
			return fmt.Errorf("validation failed for %s: %w", file, err)
		}
	}

	// Create tar.gz archive
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	totalSize := int64(0)
	startTime := time.Now()

	// Add all files to archive
	for _, file := range files {
		if err := addFileToArchive(tarWriter, file); err != nil {
			return fmt.Errorf("failed to add %s to archive: %w", file, err)
		}

		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", file, err)
		}
		totalSize += info.Size()
		fmt.Printf("  ✓ %s (%.1f KB)\n", filepath.Base(file), float64(info.Size())/1024)
	}

	// Add auto-generated version.yml if needed
	if !hasVersionYML && versionYMLContent != nil {
		if err := addContentToArchive(tarWriter, "version.yml", versionYMLContent); err != nil {
			return fmt.Errorf("failed to add version.yml to archive: %w", err)
		}
		totalSize += int64(len(versionYMLContent))
		fmt.Printf("  ✓ version.yml (%.1f KB)\n", float64(len(versionYMLContent))/1024)
	}

	// Close archive
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	// Upload archive
	fmt.Println("Uploading manifest archive...")
	if err := uploadContent(uploadURL, "manifests.tar.gz", buf.Bytes()); err != nil {
		return fmt.Errorf("failed to upload archive: %w", err)
	}

	duration := time.Since(startTime)
	fileCount := len(files)
	if !hasVersionYML {
		fileCount++
	}
	archiveSize := len(buf.Bytes())

	fmt.Printf("\nUploaded %d files (%.1f KB) as archive (%.1f KB) in %.1fs\n", fileCount, float64(totalSize)/1024, float64(archiveSize)/1024, duration.Seconds())
	return nil
}

// runUploadTerraform validates, packs and uploads the bundle rooted at dir
func runUploadTerraform(dir string) error {
	uploadURL, err := resolveUploadURL()
	if err != nil {
		return err
	}
	if err := checkArtifactType(tfbundle.ArtifactType); err != nil {
		return err
	}

	fmt.Printf("Creating terraform bundle from %s...\n", dir)
	startTime := time.Now()

	files, err := tfbundle.Collect(dir)
	if err != nil {
		return err
	}
	bundle, err := tfbundle.Validate(files)
	if err != nil {
		return err
	}

	totalSize := 0
	for _, name := range slices.Sorted(maps.Keys(bundle.Files)) {
		size := len(bundle.Files[name])
		totalSize += size
		fmt.Printf("  ✓ %s (%.1f KB)\n", name, float64(size)/1024)
	}
	environments := "none"
	if len(bundle.Environments) > 0 {
		environments = strings.Join(bundle.Environments, ", ")
	}
	fmt.Printf("OpenTofu %s, environments: %s\n", bundle.OpenTofuVersion, environments)

	archive, err := tfbundle.Pack(bundle.Files)
	if err != nil {
		return err
	}

	fmt.Println("Uploading terraform bundle...")
	if err := uploadContent(uploadURL, tfbundle.ArchiveName, archive); err != nil {
		return fmt.Errorf("failed to upload bundle: %w", err)
	}

	fmt.Printf("\nUploaded %d files (%.1f KB) as archive (%.1f KB) in %.1fs\n", len(bundle.Files), float64(totalSize)/1024, float64(len(archive))/1024, time.Since(startTime).Seconds())
	return nil
}

// resolveUploadURL returns the --upload-url flag or the URL saved by forge init
func resolveUploadURL() (string, error) {
	if uploadURLOverride != "" {
		return uploadURLOverride, nil
	}
	data, err := os.ReadFile(".forge/upload-url")
	if err != nil {
		return "", fmt.Errorf("failed to read upload URL from .forge/upload-url: %w\nDid you run 'forge init' first?", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// checkArtifactType fails if forge init created the draft for a different
// artifact type than the one being uploaded. Without version info (e.g. with
// --upload-url) there is nothing to check; smithd validates on publish.
func checkArtifactType(uploading string) error {
	versionInfo, err := LoadVersionInfo()
	if err != nil {
		return nil
	}
	initialized := versionInfo.ArtifactType
	if initialized == "" {
		initialized = "manifests"
	}
	switch initialized {
	case uploading:
		return nil
	case tfbundle.ArtifactType:
		return fmt.Errorf("version %s was initialized for a terraform bundle; upload it with 'forge upload --terraform <dir>'", versionInfo.Version)
	default:
		return fmt.Errorf("version %s was initialized for %s; run 'forge init --artifact-type %s' to upload a terraform bundle", versionInfo.Version, initialized, uploading)
	}
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

func addFileToArchive(tarWriter *tar.Writer, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    filepath.Base(filePath),
		Mode:    int64(info.Mode()),
		Size:    int64(len(data)),
		ModTime: info.ModTime(),
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}

	_, err = tarWriter.Write(data)
	return err
}

func addContentToArchive(tarWriter *tar.Writer, filename string, content []byte) error {
	header := &tar.Header{
		Name:    filename,
		Mode:    0644,
		Size:    int64(len(content)),
		ModTime: time.Now(),
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}

	_, err := tarWriter.Write(content)
	return err
}

func uploadContent(presignedURL, filename string, content []byte) error {
	// For S3 presigned URLs, send the content directly as PUT request body
	req, err := http.NewRequest("PUT", presignedURL, bytes.NewReader(content))
	if err != nil {
		return err
	}

	// Set appropriate content type based on file extension
	contentType := "application/octet-stream"
	if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
		contentType = "application/x-yaml"
	} else if strings.HasSuffix(filename, ".tar.gz") {
		contentType = "application/gzip"
	}
	req.Header.Set("Content-Type", contentType)

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
