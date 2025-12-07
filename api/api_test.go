package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorenmh/infrastructure-shared/deployment-api/config"
	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

func TestHandleGenerateManifests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a mock config
	cfg := &config.Config{
		Server: config.ServerConfig{
			APIKeys: []config.APIKey{
				{Name: "test", Key: "test-api-key"},
			},
		},
	}

	server := &Server{
		config: cfg,
		router: gin.New(),
		manifestGenerator: &mockManifestGenerator{},
	}

	server.setupRoutes()

	tests := []struct {
		name           string
		requestBody    interface{}
		expectedStatus int
		expectedError  string
	}{
		{
			name: "valid service definition",
			requestBody: models.GenerateManifestsRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name:        "test-service",
					Description: "Test service",
					Components: map[string]*models.Component{
						"web": {
							Type:     models.ComponentTypeWeb,
							Image:    "nginx:latest",
							Port:     80,
							Replicas: 1,
						},
					},
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "invalid service definition",
			requestBody: models.GenerateManifestsRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name: "", // Invalid empty name
					Components: map[string]*models.Component{
						"web": {
							Type:  models.ComponentTypeWeb,
							Image: "nginx:latest",
						},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Failed to generate manifests",
		},
		{
			name:           "missing request body",
			requestBody:    map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.requestBody)
			req, _ := http.NewRequest("POST", "/api/v1/manifests/generate", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-api-key")

			w := httptest.NewRecorder()
			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var response models.GenerateManifestsResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)

				assert.Equal(t, "test-service", response.ServiceName)
				assert.Contains(t, response.Manifests, "web-deployment.yaml")
				assert.NotEmpty(t, response.GeneratedAt)
			} else if tt.expectedError != "" {
				var response models.ErrorResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response.Error, tt.expectedError)
			}
		})
	}
}

func TestHandleValidateService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{
			APIKeys: []config.APIKey{
				{Name: "test", Key: "test-api-key"},
			},
		},
	}

	server := &Server{
		config: cfg,
		router: gin.New(),
	}

	server.setupRoutes()

	tests := []struct {
		name           string
		requestBody    interface{}
		expectedStatus int
		expectValid    bool
		expectWarnings bool
	}{
		{
			name: "valid service definition",
			requestBody: models.ValidateServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name:        "test-service",
					Description: "Test service",
					Components: map[string]*models.Component{
						"web": {
							Type:     models.ComponentTypeWeb,
							Image:    "nginx:latest",
							Port:     80,
							Replicas: 1,
							ImagePolicy: &models.ImagePolicyConfig{
								SemverRange: ">=1.0.0",
							},
						},
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectValid:    true,
			expectWarnings: false,
		},
		{
			name: "valid service with warnings",
			requestBody: models.ValidateServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name:        "test-service",
					Description: "Test service",
					Components: map[string]*models.Component{
						"web": {
							Type:     models.ComponentTypeWeb,
							Image:    "nginx:latest",
							Replicas: 1,
							// No port - should generate warning for web component
							// No image policy - should generate warning
						},
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectValid:    true,
			expectWarnings: true,
		},
		{
			name: "invalid service definition",
			requestBody: models.ValidateServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name: "", // Invalid empty name
					Components: map[string]*models.Component{
						"web": {
							Type:  models.ComponentTypeWeb,
							Image: "nginx:latest",
						},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectValid:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.requestBody)
			req, _ := http.NewRequest("POST", "/api/v1/manifests/validate", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-api-key")

			w := httptest.NewRecorder()
			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response models.ValidateServiceResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectValid, response.Valid)

			if tt.expectWarnings {
				assert.Greater(t, len(response.Warnings), 0)
			}

			assert.NotEmpty(t, response.Validated)
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{
			APIKeys: []config.APIKey{
				{Name: "test", Key: "test-api-key"},
			},
		},
	}

	server := &Server{
		config: cfg,
		router: gin.New(),
	}

	server.setupRoutes()

	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "valid API key",
			authHeader:     "Bearer test-api-key",
			expectedStatus: http.StatusBadRequest, // Bad request because empty body, but auth passed
		},
		{
			name:           "invalid API key",
			authHeader:     "Bearer invalid-api-key",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "missing authorization header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "malformed authorization header",
			authHeader:     "Invalid format",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/api/v1/manifests/generate", bytes.NewBuffer([]byte("{}")))
			req.Header.Set("Content-Type", "application/json")

			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestHandleDeployService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{
			APIKeys: []config.APIKey{
				{Name: "test", Key: "test-api-key"},
			},
		},
	}

	server := &Server{
		config:           cfg,
		router:           gin.New(),
		manifestGenerator: &mockManifestGenerator{},
		gitClient:        &mockGitClient{},
	}

	server.setupRoutes()

	tests := []struct {
		name           string
		requestBody    interface{}
		expectedStatus int
		expectedError  string
	}{
		{
			name: "successful deployment",
			requestBody: models.DeployServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name:        "test-service",
					Description: "Test service",
					Components: map[string]*models.Component{
						"web": {
							Type:     models.ComponentTypeWeb,
							Image:    "nginx:latest",
							Port:     80,
							Replicas: 1,
						},
					},
				},
				DeployedBy: "test-user",
				Message:    "Test deployment",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "deployment with custom target directory",
			requestBody: models.DeployServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name:        "custom-service",
					Description: "Custom service",
					Components: map[string]*models.Component{
						"api": {
							Type:     models.ComponentTypeAPI,
							Image:    "api:latest",
							Replicas: 1, // Add required replicas
						},
					},
				},
				TargetDirectory: "custom/path",
				DeployedBy:      "test-user",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "invalid service definition",
			requestBody: models.DeployServiceRequest{
				ServiceDefinition: &models.ServiceDefinition{
					Name: "", // Invalid empty name
					Components: map[string]*models.Component{
						"web": {
							Type:  models.ComponentTypeWeb,
							Image: "nginx:latest",
						},
					},
				},
				DeployedBy: "test-user",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Service definition validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.requestBody)
			req, _ := http.NewRequest("POST", "/api/v1/manifests/deploy", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-api-key")

			w := httptest.NewRecorder()
			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var response models.DeployServiceResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)

				assert.NotEmpty(t, response.ServiceName)
				assert.NotEmpty(t, response.TargetDirectory)
				assert.NotEmpty(t, response.GitCommit)
				assert.Greater(t, len(response.Manifests), 0)
				assert.NotEmpty(t, response.DeployedAt)
				assert.NotEmpty(t, response.DeployedBy)
			} else if tt.expectedError != "" {
				var response models.ErrorResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response.Error, tt.expectedError)
			}
		})
	}
}

// Mock implementations for testing
type mockManifestGenerator struct{}

func (m *mockManifestGenerator) GenerateManifests(serviceName string, serviceDef *models.ServiceDefinition) (map[string][]byte, error) {
	if serviceDef.Name == "" {
		return nil, fmt.Errorf("service name cannot be empty")
	}

	manifests := make(map[string][]byte)
	manifests["web-deployment.yaml"] = []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web")

	if len(serviceDef.Components) > 0 {
		for componentName, component := range serviceDef.Components {
			if component.Port > 0 {
				manifests[componentName+"-service.yaml"] = []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: " + componentName)
			}
		}
	}

	return manifests, nil
}

type mockGitClient struct{}

func (m *mockGitClient) WriteServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error) {
	// Mock successful git operation
	return "abc123def456", nil
}

func (m *mockGitClient) UpdateServiceManifests(serviceName, targetDirectory string, manifests map[string][]byte) (string, error) {
	// Mock successful git operation
	return "abc123def456", nil
}

func (m *mockGitClient) DeleteServiceManifests(serviceName, targetDirectory string) (string, error) {
	// Mock successful git operation
	return "abc123def456", nil
}

func (m *mockGitClient) UpdateImageTag(manifestPath, newTag string) (string, error) {
	return "abc123def456", nil
}

func (m *mockGitClient) GetCurrentImageTag(manifestPath string) (string, error) {
	return "latest", nil
}

func (m *mockGitClient) CheckHealth() error {
	return nil
}