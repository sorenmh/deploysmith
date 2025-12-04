package manifests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

func TestGenerator_GenerateManifests(t *testing.T) {
	generator := NewGenerator()

	tests := []struct {
		name           string
		serviceName    string
		serviceDef     *models.ServiceDefinition
		expectedFiles  []string
		shouldError    bool
	}{
		{
			name:        "simple web component",
			serviceName: "test-service",
			serviceDef: &models.ServiceDefinition{
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
			expectedFiles: []string{
				"web-deployment.yaml",
				"web-service.yaml",
			},
			shouldError: false,
		},
		{
			name:        "api component without port",
			serviceName: "api-service",
			serviceDef: &models.ServiceDefinition{
				Name: "api-service",
				Components: map[string]*models.Component{
					"api": {
						Type:  models.ComponentTypeAPI,
						Image: "api:latest",
						// No port - should not generate service
					},
				},
			},
			expectedFiles: []string{
				"api-deployment.yaml",
				// No service file expected
			},
			shouldError: false,
		},
		{
			name:        "multi-component service",
			serviceName: "multi-service",
			serviceDef: &models.ServiceDefinition{
				Name: "multi-service",
				Components: map[string]*models.Component{
					"web": {
						Type: models.ComponentTypeWeb,
						Image: "web:latest",
						Port: 3000,
					},
					"api": {
						Type: models.ComponentTypeAPI,
						Image: "api:latest",
						Port: 8080,
					},
				},
			},
			expectedFiles: []string{
				"web-deployment.yaml",
				"web-service.yaml",
				"api-deployment.yaml",
				"api-service.yaml",
			},
			shouldError: false,
		},
		{
			name:        "component with flux image automation",
			serviceName: "flux-service",
			serviceDef: &models.ServiceDefinition{
				Name: "flux-service",
				Components: map[string]*models.Component{
					"web": {
						Type:  models.ComponentTypeWeb,
						Image: "ghcr.io/user/web:v1.0.0",
						Port:  8080,
						ImagePolicy: &models.ImagePolicyConfig{
							SemverRange: ">=1.0.0 <2.0.0",
						},
					},
					"api": {
						Type:  models.ComponentTypeAPI,
						Image: "api:latest",
						ImagePolicy: &models.ImagePolicyConfig{
							Pattern: "^main-[a-f0-9]+-(?P<ts>[0-9]+)",
						},
					},
				},
			},
			expectedFiles: []string{
				"web-deployment.yaml",
				"web-service.yaml",
				"web-flux.yaml",
				"api-deployment.yaml",
				"api-flux.yaml",
			},
			shouldError: false,
		},
		{
			name:        "invalid service definition",
			serviceName: "invalid-service",
			serviceDef: &models.ServiceDefinition{
				Name:       "", // Invalid empty name
				Components: map[string]*models.Component{},
			},
			expectedFiles: nil,
			shouldError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifests, err := generator.GenerateManifests(tt.serviceName, tt.serviceDef)

			if tt.shouldError {
				assert.Error(t, err)
				assert.Nil(t, manifests)
				return
			}

			require.NoError(t, err)
			assert.Len(t, manifests, len(tt.expectedFiles))

			for _, expectedFile := range tt.expectedFiles {
				content, exists := manifests[expectedFile]
				assert.True(t, exists, "Expected file %s not found", expectedFile)
				assert.Greater(t, len(content), 0, "File %s is empty", expectedFile)

				// Validate that it's valid YAML
				var parsed interface{}
				err := yaml.Unmarshal(content, &parsed)
				assert.NoError(t, err, "File %s is not valid YAML", expectedFile)
			}
		})
	}
}

func TestDeploymentGenerator_Generate(t *testing.T) {
	generator := NewDeploymentGenerator()

	tests := []struct {
		name          string
		serviceName   string
		componentName string
		component     *models.Component
		shouldContain []string
		shouldError   bool
	}{
		{
			name:          "basic web deployment",
			serviceName:   "test-service",
			componentName: "web",
			component: &models.Component{
				Type:     models.ComponentTypeWeb,
				Image:    "nginx:1.21",
				Port:     80,
				Replicas: 2,
				Env: []models.EnvVar{
					{Name: "ENV", Value: "production"},
				},
			},
			shouldContain: []string{
				"kind: Deployment",
				"name: web",
				"image: nginx:1.21",
				"replicas: 2",
				"containerPort: 80",
				"name: ENV",
				"value: \"production\"",
				"app.kubernetes.io/name: \"web\"",
				"app.kubernetes.io/instance: \"test-service\"",
				"flux-system:test-service-web-policy",
			},
			shouldError: false,
		},
		{
			name:          "component with secret reference",
			serviceName:   "secret-service",
			componentName: "api",
			component: &models.Component{
				Type:  models.ComponentTypeAPI,
				Image: "api:latest",
				Env: []models.EnvVar{
					{Name: "DATABASE_URL", SecretRef: "api-secrets/database-url"},
				},
			},
			shouldContain: []string{
				"kind: Deployment",
				"name: DATABASE_URL",
				"secretKeyRef:",
				"name: \"api-secrets\"",
				"key: \"database-url\"",
			},
			shouldError: false,
		},
		{
			name:          "component with resources",
			serviceName:   "resource-service",
			componentName: "app",
			component: &models.Component{
				Type:  models.ComponentTypeWeb,
				Image: "app:latest",
				Resources: &models.ResourceRequirements{
					Limits: &models.ResourceList{
						Memory: "256Mi",
						CPU:    "200m",
					},
					Requests: &models.ResourceList{
						Memory: "128Mi",
						CPU:    "100m",
					},
				},
			},
			shouldContain: []string{
				"resources:",
				"limits:",
				"memory: \"256Mi\"",
				"cpu: \"200m\"",
				"requests:",
				"memory: \"128Mi\"",
				"cpu: \"100m\"",
			},
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generator.Generate(tt.serviceName, tt.componentName, tt.component)

			if tt.shouldError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			content := string(result)

			// Validate YAML structure
			var parsed interface{}
			err = yaml.Unmarshal(result, &parsed)
			require.NoError(t, err, "Generated manifest is not valid YAML")

			// Check for expected content
			for _, expected := range tt.shouldContain {
				assert.Contains(t, content, expected, "Generated manifest should contain %q", expected)
			}

			// Verify it starts with proper YAML structure
			assert.True(t, strings.HasPrefix(content, "apiVersion: apps/v1"))
		})
	}
}

func TestServiceGenerator_Generate(t *testing.T) {
	generator := NewServiceGenerator()

	tests := []struct {
		name          string
		serviceName   string
		componentName string
		component     *models.Component
		shouldContain []string
		shouldError   bool
	}{
		{
			name:          "basic service",
			serviceName:   "test-service",
			componentName: "web",
			component: &models.Component{
				Type: models.ComponentTypeWeb,
				Port: 8080,
			},
			shouldContain: []string{
				"kind: Service",
				"name: web",
				"port: 8080",
				"targetPort: http",
				"app.kubernetes.io/name: \"web\"",
				"app.kubernetes.io/instance: \"test-service\"",
			},
			shouldError: false,
		},
		{
			name:          "component without port should error",
			serviceName:   "no-port-service",
			componentName: "worker",
			component: &models.Component{
				Type: models.ComponentTypeAPI,
				// No port specified
			},
			shouldContain: nil,
			shouldError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generator.Generate(tt.serviceName, tt.componentName, tt.component)

			if tt.shouldError {
				assert.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			content := string(result)

			// Validate YAML structure
			var parsed interface{}
			err = yaml.Unmarshal(result, &parsed)
			require.NoError(t, err, "Generated manifest is not valid YAML")

			// Check for expected content
			for _, expected := range tt.shouldContain {
				assert.Contains(t, content, expected, "Generated manifest should contain %q", expected)
			}

			// Verify it starts with proper YAML structure
			assert.True(t, strings.HasPrefix(content, "apiVersion: v1"))
		})
	}
}

func TestGenerateLabels(t *testing.T) {
	component := &models.Component{
		Type: models.ComponentTypeWeb,
	}

	labels := generateLabels("test-service", "web-app", component)

	expected := map[string]string{
		"app.kubernetes.io/name":       "web-app",
		"app.kubernetes.io/instance":   "test-service",
		"app.kubernetes.io/component":  "web",
		"app.kubernetes.io/part-of":    "test-service",
		"app.kubernetes.io/managed-by": "service-abstraction-layer",
	}

	assert.Equal(t, expected, labels)
}

func TestTemplateFunctions(t *testing.T) {
	t.Run("quote function", func(t *testing.T) {
		result := templateFuncs["quote"].(func(string) string)("test")
		assert.Equal(t, `"test"`, result)
	})

	t.Run("split function", func(t *testing.T) {
		result := templateFuncs["split"].(func(string, string) []string)("/", "secret/key")
		assert.Equal(t, []string{"secret", "key"}, result)
	})

	t.Run("imagePolicyAnnotation function", func(t *testing.T) {
		result := templateFuncs["imagePolicyAnnotation"].(func(string, string) string)("service", "component")
		expected := `{"$imagepolicy": "flux-system:service-component-policy"}`
		assert.Equal(t, expected, result)
	})
}