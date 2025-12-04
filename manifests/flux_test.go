package manifests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

func TestFluxGenerator_Generate(t *testing.T) {
	generator := NewFluxGenerator()

	tests := []struct {
		name          string
		serviceName   string
		componentName string
		component     *models.Component
		shouldContain []string
		shouldError   bool
	}{
		{
			name:          "semver image policy",
			serviceName:   "test-service",
			componentName: "web",
			component: &models.Component{
				Type:  models.ComponentTypeWeb,
				Image: "ghcr.io/user/web:1.0.0",
				ImagePolicy: &models.ImagePolicyConfig{
					SemverRange: ">=1.0.0 <2.0.0",
				},
			},
			shouldContain: []string{
				"kind: ImageRepository",
				"name: test-service-web-repo",
				"namespace: flux-system",
				"image: ghcr.io/user/web",
				"interval: 1m",
				"kind: ImagePolicy",
				"name: test-service-web-policy",
				"imageRepositoryRef:",
				"name: test-service-web-repo",
				"semver:",
				"range: \">=1.0.0 <2.0.0\"",
				"app.kubernetes.io/name: \"web\"",
				"app.kubernetes.io/instance: \"test-service\"",
			},
			shouldError: false,
		},
		{
			name:          "alphabetical policy with pattern",
			serviceName:   "api-service",
			componentName: "api",
			component: &models.Component{
				Type:  models.ComponentTypeAPI,
				Image: "api:latest",
				ImagePolicy: &models.ImagePolicyConfig{
					Pattern: "^main-[a-f0-9]+-(?P<ts>[0-9]+)",
				},
			},
			shouldContain: []string{
				"kind: ImageRepository",
				"name: api-service-api-repo",
				"image: api",
				"kind: ImagePolicy",
				"name: api-service-api-policy",
				"alphabetical:",
				"order: asc",
				"extract: \"^main-[a-f0-9]+-(?P<ts>[0-9]+)\"",
			},
			shouldError: false,
		},
		{
			name:          "alphabetical policy without pattern",
			serviceName:   "simple-service",
			componentName: "app",
			component: &models.Component{
				Type:  models.ComponentTypeWeb,
				Image: "app:v1.0",
				ImagePolicy: &models.ImagePolicyConfig{
					// No semver or pattern - defaults to alphabetical
				},
			},
			shouldContain: []string{
				"kind: ImageRepository",
				"name: simple-service-app-repo",
				"kind: ImagePolicy",
				"name: simple-service-app-policy",
				"alphabetical:",
				"order: asc",
			},
			shouldError: false,
		},
		{
			name:          "component without image policy should error",
			serviceName:   "no-policy-service",
			componentName: "web",
			component: &models.Component{
				Type:  models.ComponentTypeWeb,
				Image: "web:latest",
				// No ImagePolicy
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

			// Should contain both ImageRepository and ImagePolicy
			assert.Contains(t, content, "kind: ImageRepository")
			assert.Contains(t, content, "kind: ImagePolicy")

			// Should have proper YAML document separators
			assert.Contains(t, content, "---")
		})
	}
}

func TestFluxTemplateData_GetPolicyType(t *testing.T) {
	tests := []struct {
		name     string
		data     *FluxTemplateData
		expected string
	}{
		{
			name: "semver policy",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						SemverRange: ">=1.0.0",
					},
				},
			},
			expected: "semver",
		},
		{
			name: "alphabetical policy with pattern",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						Pattern: "^v[0-9]+",
					},
				},
			},
			expected: "alphabetical",
		},
		{
			name: "default alphabetical policy",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						// Empty - defaults to alphabetical
					},
				},
			},
			expected: "alphabetical",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.GetPolicyType()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFluxTemplateData_GetPolicyConfig(t *testing.T) {
	tests := []struct {
		name     string
		data     *FluxTemplateData
		expected map[string]string
	}{
		{
			name: "semver policy config",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						SemverRange: ">=1.0.0 <2.0.0",
					},
				},
			},
			expected: map[string]string{
				"range": ">=1.0.0 <2.0.0",
			},
		},
		{
			name: "alphabetical policy with pattern",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						Pattern: "^main-(?P<timestamp>[0-9]+)",
					},
				},
			},
			expected: map[string]string{
				"order":   "asc",
				"extract": "^main-(?P<timestamp>[0-9]+)",
			},
		},
		{
			name: "default alphabetical policy",
			data: &FluxTemplateData{
				Component: &models.Component{
					ImagePolicy: &models.ImagePolicyConfig{
						// Empty - defaults to alphabetical
					},
				},
			},
			expected: map[string]string{
				"order": "asc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.GetPolicyConfig()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetRepositoryName(t *testing.T) {
	tests := []struct {
		name      string
		imageName string
		expected  string
	}{
		{
			name:      "image with registry host",
			imageName: "ghcr.io/user/app",
			expected:  "user/app",
		},
		{
			name:      "image without registry host",
			imageName: "user/app",
			expected:  "user/app",
		},
		{
			name:      "simple image name",
			imageName: "nginx",
			expected:  "nginx",
		},
		{
			name:      "docker hub image",
			imageName: "library/nginx",
			expected:  "library/nginx",
		},
		{
			name:      "complex registry with port",
			imageName: "registry.example.com:5000/namespace/app",
			expected:  "namespace/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRepositoryName(tt.imageName)
			assert.Equal(t, tt.expected, result)
		})
	}
}