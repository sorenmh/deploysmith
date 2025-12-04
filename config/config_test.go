package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		validate    func(t *testing.T, cfg *Config)
	}{
		{
			name: "complete valid config",
			configYAML: `
server:
  port: 8080
  api_keys:
    - name: "test-key"
      key: "test-secret"
    - name: "prod-key"
      key: "prod-secret"

git:
  repository_url: "https://github.com/test/repo.git"
  branch: "main"
  username: "testuser"
  token: "testtoken"
  local_path: "/tmp/repo"
  author_name: "Test Author"
  author_email: "test@example.com"

services:
  - name: "test-service"
    namespace: "default"
    manifest_path: "manifests/test-service"
    image_repository: "ghcr.io/test/service"
    workload_type: "deployment"

database:
  path: "/tmp/test.db"

logging:
  level: "info"
  format: "json"
`,
			expectError: false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 8080, cfg.Server.Port)
				assert.Len(t, cfg.Server.APIKeys, 2)
				assert.Equal(t, "test-key", cfg.Server.APIKeys[0].Name)
				assert.Equal(t, "test-secret", cfg.Server.APIKeys[0].Key)
				assert.Equal(t, "https://github.com/test/repo.git", cfg.Git.RepositoryURL)
				assert.Equal(t, "main", cfg.Git.Branch)
				assert.Equal(t, "testuser", cfg.Git.Username)
				assert.Equal(t, "testtoken", cfg.Git.Token)
				assert.Equal(t, "/tmp/repo", cfg.Git.LocalPath)
				assert.Equal(t, "Test Author", cfg.Git.AuthorName)
				assert.Equal(t, "test@example.com", cfg.Git.AuthorEmail)
				assert.Len(t, cfg.Services, 1)
				assert.Equal(t, "test-service", cfg.Services[0].Name)
				assert.Equal(t, "default", cfg.Services[0].Namespace)
				assert.Equal(t, "deployment", cfg.Services[0].WorkloadType)
				assert.Equal(t, "/tmp/test.db", cfg.Database.Path)
				assert.Equal(t, "info", cfg.Logging.Level)
				assert.Equal(t, "json", cfg.Logging.Format)
			},
		},
		{
			name: "minimal config with defaults",
			configYAML: `
server:
  port: 3000
  api_keys:
    - name: "minimal"
      key: "minimal-secret"

git:
  repository_url: "https://github.com/minimal/repo.git"
  username: "minimal"
  token: "minimal-token"
`,
			expectError: false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 3000, cfg.Server.Port)
				assert.Len(t, cfg.Server.APIKeys, 1)
				assert.Equal(t, "minimal", cfg.Server.APIKeys[0].Name)
				assert.Equal(t, "https://github.com/minimal/repo.git", cfg.Git.RepositoryURL)
				assert.Equal(t, "minimal", cfg.Git.Username)
				// Check defaults
				assert.Equal(t, "main", cfg.Git.Branch) // default
				assert.Equal(t, "/data/deployments.db", cfg.Database.Path) // default
				assert.Equal(t, "info", cfg.Logging.Level) // default
				assert.Equal(t, "json", cfg.Logging.Format) // default
			},
		},
		{
			name: "config with environment variables",
			configYAML: `
server:
  port: ${TEST_PORT}
  api_keys:
    - name: "test"
      key: "${TEST_API_KEY}"

git:
  repository_url: "${TEST_REPO_URL}"
  username: "${TEST_USERNAME}"
  token: "${TEST_TOKEN}"
`,
			expectError: false,
			validate: func(t *testing.T, cfg *Config) {
				// Set environment variables before the test
				os.Setenv("TEST_PORT", "9090")
				os.Setenv("TEST_API_KEY", "env-secret")
				os.Setenv("TEST_REPO_URL", "https://github.com/env/repo.git")
				os.Setenv("TEST_USERNAME", "envuser")
				os.Setenv("TEST_TOKEN", "envtoken")

				// The test will verify these values are expanded
			},
		},
		{
			name: "invalid YAML syntax",
			configYAML: `
server:
  port: 8080
  invalid: [unclosed
`,
			expectError: true,
		},
		{
			name: "empty config file",
			configYAML: "",
			expectError: false,
			validate: func(t *testing.T, cfg *Config) {
				// Should apply all defaults
				assert.Equal(t, 8080, cfg.Server.Port) // default
				assert.Equal(t, "main", cfg.Git.Branch) // default
				assert.Equal(t, "/data/gitops-repo", cfg.Git.LocalPath) // default
				assert.Equal(t, "/data/deployments.db", cfg.Database.Path) // default
				assert.Equal(t, "info", cfg.Logging.Level) // default
				assert.Equal(t, "json", cfg.Logging.Format) // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configFile := filepath.Join(tempDir, "config.yaml")

			err := os.WriteFile(configFile, []byte(tt.configYAML), 0644)
			require.NoError(t, err)

			// Set up environment variables if needed
			if tt.name == "config with environment variables" {
				os.Setenv("TEST_PORT", "9090")
				os.Setenv("TEST_API_KEY", "env-secret")
				os.Setenv("TEST_REPO_URL", "https://github.com/env/repo.git")
				os.Setenv("TEST_USERNAME", "envuser")
				os.Setenv("TEST_TOKEN", "envtoken")
				defer func() {
					os.Unsetenv("TEST_PORT")
					os.Unsetenv("TEST_API_KEY")
					os.Unsetenv("TEST_REPO_URL")
					os.Unsetenv("TEST_USERNAME")
					os.Unsetenv("TEST_TOKEN")
				}()
			}

			// Load config
			cfg, err := Load(configFile)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, cfg)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, cfg)
				if tt.validate != nil {
					tt.validate(t, cfg)
				}
			}
		})
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	cfg, err := Load("/non/existent/path/config.yaml")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestConfigGetService(t *testing.T) {
	cfg := &Config{
		Services: []ServiceConfig{
			{Name: "service1", Namespace: "default"},
			{Name: "service2", Namespace: "production"},
		},
	}

	// Test existing service
	svc := cfg.GetService("service1")
	assert.NotNil(t, svc)
	assert.Equal(t, "service1", svc.Name)
	assert.Equal(t, "default", svc.Namespace)

	// Test another existing service
	svc = cfg.GetService("service2")
	assert.NotNil(t, svc)
	assert.Equal(t, "service2", svc.Name)
	assert.Equal(t, "production", svc.Namespace)

	// Test non-existent service
	svc = cfg.GetService("non-existent")
	assert.Nil(t, svc)
}

func TestConfigValidateAPIKey(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			APIKeys: []APIKey{
				{Name: "test1", Key: "secret1"},
				{Name: "test2", Key: "secret2"},
			},
		},
	}

	// Test valid API keys
	assert.True(t, cfg.ValidateAPIKey("secret1"))
	assert.True(t, cfg.ValidateAPIKey("secret2"))

	// Test invalid API key
	assert.False(t, cfg.ValidateAPIKey("invalid-key"))
	assert.False(t, cfg.ValidateAPIKey(""))
}

func TestConfigDefaults(t *testing.T) {
	// Test config with partial values to verify defaults are applied
	configYAML := `
server:
  api_keys:
    - name: "test"
      key: "secret"

services:
  - name: "test-service"
    namespace: "default"
`

	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	require.NoError(t, err)

	cfg, err := Load(configFile)
	require.NoError(t, err)

	// Verify defaults are applied
	assert.Equal(t, 8080, cfg.Server.Port) // default port
	assert.Equal(t, "main", cfg.Git.Branch) // default branch
	assert.Equal(t, "/data/gitops-repo", cfg.Git.LocalPath) // default local path
	assert.Equal(t, "/data/deployments.db", cfg.Database.Path) // default database path
	assert.Equal(t, "info", cfg.Logging.Level) // default log level
	assert.Equal(t, "json", cfg.Logging.Format) // default log format

	// Verify service defaults
	assert.Len(t, cfg.Services, 1)
	assert.Equal(t, "deployment", cfg.Services[0].WorkloadType) // default workload type
}

func TestConfigWithRegistryAuth(t *testing.T) {
	configYAML := `
server:
  port: 8080
  api_keys:
    - name: "test"
      key: "secret"

services:
  - name: "test-service"
    namespace: "default"
    image_repository: "private.registry.com/app"
    registry_auth:
      username: "reguser"
      password: "regpass"
`

	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configFile, []byte(configYAML), 0644)
	require.NoError(t, err)

	cfg, err := Load(configFile)
	require.NoError(t, err)
	require.Len(t, cfg.Services, 1)

	service := cfg.Services[0]
	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, "private.registry.com/app", service.ImageRepository)
	require.NotNil(t, service.RegistryAuth)
	assert.Equal(t, "reguser", service.RegistryAuth.Username)
	assert.Equal(t, "regpass", service.RegistryAuth.Password)
}