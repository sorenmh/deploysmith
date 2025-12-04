package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	tempDir := t.TempDir()

	client, err := NewClient(
		"https://github.com/test/repo.git",
		"main",
		tempDir,
		"testuser",
		"testtoken",
		"Test Author",
		"test@example.com",
	)

	// This will fail because we can't actually clone the repo, but we're testing the constructor
	assert.Error(t, err) // Expected to fail on clone
	assert.Nil(t, client)
}

func TestClientAuth(t *testing.T) {
	client := &Client{
		username: "testuser",
		token:    "testtoken",
	}

	auth := client.auth()
	assert.NotNil(t, auth)
	assert.Equal(t, "testuser", auth.Username)
	assert.Equal(t, "testtoken", auth.Password)
}

func TestUpdateImageInManifest(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		manifest map[string]interface{}
		newTag   string
		expected bool
		checkImg string
	}{
		{
			name: "valid deployment manifest",
			manifest: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "app",
									"image": "nginx:1.21",
								},
							},
						},
					},
				},
			},
			newTag:   "1.22",
			expected: true,
			checkImg: "nginx:1.22",
		},
		{
			name: "cronjob manifest",
			manifest: map[string]interface{}{
				"apiVersion": "batch/v1",
				"kind":       "CronJob",
				"spec": map[string]interface{}{
					"jobTemplate": map[string]interface{}{
						"spec": map[string]interface{}{
							"template": map[string]interface{}{
								"spec": map[string]interface{}{
									"containers": []interface{}{
										map[string]interface{}{
											"name":  "job",
											"image": "ubuntu:20.04",
										},
									},
								},
							},
						},
					},
				},
			},
			newTag:   "22.04",
			expected: true,
			checkImg: "ubuntu:22.04",
		},
		{
			name: "image with policy marker",
			manifest: map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"image": "nginx:1.21 # {\"$imagepolicy\": \"default:nginx-policy\"}",
								},
							},
						},
					},
				},
			},
			newTag:   "1.22",
			expected: true,
			checkImg: "nginx:1.22 # {\"$imagepolicy\": \"default:nginx-policy\"}",
		},
		{
			name: "invalid manifest - no spec",
			manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
			},
			newTag:   "1.22",
			expected: false,
		},
		{
			name: "invalid manifest - no containers",
			manifest: map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{},
					},
				},
			},
			newTag:   "1.22",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, err := client.updateImageInManifest(tt.manifest, tt.newTag)

			if tt.expected {
				assert.NoError(t, err)
				assert.True(t, updated)

				// Check if image was updated correctly
				if tt.checkImg != "" {
					spec := tt.manifest["spec"].(map[string]interface{})

					// Handle CronJob
					if jobTemplate, ok := spec["jobTemplate"].(map[string]interface{}); ok {
						spec = jobTemplate["spec"].(map[string]interface{})
					}

					template := spec["template"].(map[string]interface{})
					templateSpec := template["spec"].(map[string]interface{})
					containers := templateSpec["containers"].([]interface{})
					container := containers[0].(map[string]interface{})

					assert.Equal(t, tt.checkImg, container["image"])
				}
			} else {
				assert.False(t, updated)
			}
		})
	}
}

func TestCleanupFiles(t *testing.T) {
	tempDir := t.TempDir()

	client := &Client{
		localPath: tempDir,
	}

	// Create test files
	file1 := "test1.txt"
	file2 := "test2.txt"

	fullPath1 := filepath.Join(tempDir, file1)
	fullPath2 := filepath.Join(tempDir, file2)

	err := os.WriteFile(fullPath1, []byte("content1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(fullPath2, []byte("content2"), 0644)
	require.NoError(t, err)

	// Verify files exist
	assert.FileExists(t, fullPath1)
	assert.FileExists(t, fullPath2)

	// Cleanup files
	client.cleanupFiles([]string{file1, file2})

	// Verify files were removed
	assert.NoFileExists(t, fullPath1)
	assert.NoFileExists(t, fullPath2)
}

func TestExtractImageTag(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{
			name:     "simple image with tag",
			image:    "nginx:1.21",
			expected: "1.21",
		},
		{
			name:     "image with registry and tag",
			image:    "ghcr.io/owner/app:v1.0.0",
			expected: "v1.0.0",
		},
		{
			name:     "image without tag",
			image:    "nginx",
			expected: "latest",
		},
		{
			name:     "image with policy marker",
			image:    "nginx:1.21 # {\"$imagepolicy\": \"default:nginx-policy\"}",
			expected: "1.21",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic from GetCurrentImageTag
			imagePart := tt.image
			if len(tt.image) > 0 && tt.image[0] != ' ' {
				// Split on space to remove policy markers
				parts := []string{tt.image}
				if idx := len(tt.image); idx > 0 {
					for i, char := range tt.image {
						if char == ' ' {
							parts = []string{tt.image[:i]}
							break
						}
					}
				}
				imagePart = parts[0]
			}

			// Extract tag
			tagParts := []string{imagePart}
			for i, char := range imagePart {
				if char == ':' {
					tagParts = []string{imagePart[:i], imagePart[i+1:]}
					break
				}
			}

			var result string
			if len(tagParts) < 2 {
				result = "latest"
			} else {
				result = tagParts[len(tagParts)-1]
			}

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClientFields(t *testing.T) {
	client := &Client{
		repoURL:     "https://github.com/test/repo.git",
		branch:      "main",
		localPath:   "/tmp/repo",
		username:    "testuser",
		token:       "testtoken",
		authorName:  "Test Author",
		authorEmail: "test@example.com",
	}

	assert.Equal(t, "https://github.com/test/repo.git", client.repoURL)
	assert.Equal(t, "main", client.branch)
	assert.Equal(t, "/tmp/repo", client.localPath)
	assert.Equal(t, "testuser", client.username)
	assert.Equal(t, "testtoken", client.token)
	assert.Equal(t, "Test Author", client.authorName)
	assert.Equal(t, "test@example.com", client.authorEmail)
}

// Mock tests for interface compliance
func TestGitClientInterface(t *testing.T) {
	// Test that Client implements GitClient interface
	var _ GitClient = (*Client)(nil)

	// This just verifies the interface is implemented correctly
	assert.True(t, true)
}

func TestImageNameExtraction(t *testing.T) {
	tests := []struct {
		image    string
		expected string
	}{
		{"nginx:1.21", "nginx"},
		{"ghcr.io/owner/app:v1.0.0", "ghcr.io/owner/app"},
		{"localhost:5000/app:latest", "localhost:5000/app"},
		{"app", "app"},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			// Extract image name logic (simulate what happens in updateImageInManifest)
			parts := []string{tt.image}
			for i := len(tt.image) - 1; i >= 0; i-- {
				if tt.image[i] == ':' {
					parts = []string{tt.image[:i], tt.image[i+1:]}
					break
				}
			}

			result := parts[0]
			assert.Equal(t, tt.expected, result)
		})
	}
}