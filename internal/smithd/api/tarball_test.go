package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestExtractTarball_ValidTarball(t *testing.T) {
	server := &Server{}

	// Create test YAML content
	testFiles := map[string]string{
		"deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
spec:
  replicas: 2`,
		"service.yaml": `apiVersion: v1
kind: Service
metadata:
  name: test-service
spec:
  ports:
  - port: 80`,
		"version.yml": `version: "v1.0.0"
metadata:
  timestamp: "2025-12-10T20:00:00Z"`,
		"config.txt": "not a yaml file",
	}

	// Create tarball
	tarballData := createTestTarball(t, testFiles)

	// Test extraction
	reader := io.NopCloser(bytes.NewReader(tarballData))
	extracted, err := server.extractTarball(reader)
	if err != nil {
		t.Fatalf("Failed to extract tarball: %v", err)
	}

	// Verify extracted files
	if len(extracted) != len(testFiles) {
		t.Errorf("Expected %d files, got %d", len(testFiles), len(extracted))
	}

	for filename, expectedContent := range testFiles {
		if actualContent, exists := extracted[filename]; exists {
			if string(actualContent) != expectedContent {
				t.Errorf("Content mismatch for %s.\nExpected: %s\nActual: %s",
					filename, expectedContent, string(actualContent))
			}
		} else {
			t.Errorf("Expected file %s not found in extracted files", filename)
		}
	}
}

func TestExtractTarball_InvalidGzipData(t *testing.T) {
	server := &Server{}

	// Create invalid gzip data
	invalidData := []byte("this is not gzip data")
	reader := io.NopCloser(bytes.NewReader(invalidData))

	_, err := server.extractTarball(reader)
	if err == nil {
		t.Error("Expected error for invalid gzip data, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create gzip reader") {
		t.Errorf("Expected gzip reader error, got: %v", err)
	}
}

func TestExtractTarball_EmptyTarball(t *testing.T) {
	server := &Server{}

	// Create empty tarball
	tarballData := createTestTarball(t, map[string]string{})

	// Test extraction
	reader := io.NopCloser(bytes.NewReader(tarballData))
	extracted, err := server.extractTarball(reader)
	if err != nil {
		t.Fatalf("Failed to extract empty tarball: %v", err)
	}

	// Verify no files extracted
	if len(extracted) != 0 {
		t.Errorf("Expected 0 files from empty tarball, got %d", len(extracted))
	}
}

func TestExtractTarball_YAMLValidation(t *testing.T) {
	// Test that would be used in the full publish flow
	testYAMLFiles := map[string]string{
		"valid.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config`,
		"invalid.yaml": `this is not valid yaml: [unclosed bracket`,
	}

	tarballData := createTestTarball(t, testYAMLFiles)
	server := &Server{}

	reader := io.NopCloser(bytes.NewReader(tarballData))
	extracted, err := server.extractTarball(reader)
	if err != nil {
		t.Fatalf("Failed to extract tarball: %v", err)
	}

	// Verify extraction worked (validation would happen in publish handler)
	if len(extracted) != 2 {
		t.Errorf("Expected 2 files, got %d", len(extracted))
	}

	// Test individual YAML validation (simulating what happens in handlePublishVersion)
	for filename, content := range extracted {
		if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
			var yamlContent interface{}
			// This is the same validation logic used in handlePublishVersion
			err := yaml.Unmarshal(content, &yamlContent)

			if filename == "valid.yaml" && err != nil {
				t.Errorf("Expected valid.yaml to parse successfully, got error: %v", err)
			}
			if filename == "invalid.yaml" && err == nil {
				t.Error("Expected invalid.yaml to fail parsing, but it succeeded")
			}
		}
	}
}

// Helper function to create test tarballs
func createTestTarball(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	for filename, content := range files {
		header := &tar.Header{
			Name: filename,
			Mode: 0644,
			Size: int64(len(content)),
			ModTime: time.Now(),
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("Failed to write tar header for %s: %v", filename, err)
		}

		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("Failed to write tar content for %s: %v", filename, err)
		}
	}

	if err := tarWriter.Close(); err != nil {
		t.Fatalf("Failed to close tar writer: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatalf("Failed to close gzip writer: %v", err)
	}

	return buf.Bytes()
}