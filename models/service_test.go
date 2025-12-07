package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		ct       ComponentType
		expected bool
	}{
		{"valid web type", ComponentTypeWeb, true},
		{"valid api type", ComponentTypeAPI, true},
		{"invalid type", ComponentType("invalid"), false},
		{"empty type", ComponentType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ct.IsValid())
		})
	}
}

func TestImagePolicyConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    *ImagePolicyConfig
		wantError bool
	}{
		{
			name:      "valid semver range",
			config:    &ImagePolicyConfig{SemverRange: ">=1.0.0"},
			wantError: false,
		},
		{
			name:      "valid pattern",
			config:    &ImagePolicyConfig{Pattern: "main-.*"},
			wantError: false,
		},
		{
			name:      "empty config",
			config:    &ImagePolicyConfig{},
			wantError: true,
		},
		{
			name:      "both specified",
			config:    &ImagePolicyConfig{SemverRange: ">=1.0.0", Pattern: "main-.*"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEnvVar_Validate(t *testing.T) {
	tests := []struct {
		name      string
		envVar    *EnvVar
		wantError bool
	}{
		{
			name:      "valid value",
			envVar:    &EnvVar{Name: "TEST", Value: "value"},
			wantError: false,
		},
		{
			name:      "valid secret ref",
			envVar:    &EnvVar{Name: "TEST", SecretRef: "secret-name/key"},
			wantError: false,
		},
		{
			name:      "empty config",
			envVar:    &EnvVar{Name: "TEST"},
			wantError: true,
		},
		{
			name:      "both specified",
			envVar:    &EnvVar{Name: "TEST", Value: "value", SecretRef: "secret/key"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.envVar.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestComponent_GetDefaults(t *testing.T) {
	component := &Component{
		Type:  ComponentTypeWeb,
		Image: "test:latest",
		// Replicas not set (should default to 1)
	}

	defaults := component.GetDefaults()

	assert.Equal(t, 1, defaults.Replicas)
	assert.Equal(t, ComponentTypeWeb, defaults.Type)
	assert.Equal(t, "test:latest", defaults.Image)
}

func TestComponent_NeedsService(t *testing.T) {
	tests := []struct {
		name      string
		component *Component
		expected  bool
	}{
		{
			name:      "component with port",
			component: &Component{Port: 8080},
			expected:  true,
		},
		{
			name:      "component without port",
			component: &Component{},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.component.NeedsService())
		})
	}
}

func TestComponent_Validate(t *testing.T) {
	tests := []struct {
		name      string
		component *Component
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid component",
			component: &Component{
				Type:     ComponentTypeWeb,
				Image:    "test:latest",
				Replicas: 1,
				Port:     8080,
				Env: []EnvVar{
					{Name: "TEST", Value: "value"},
				},
			},
			wantError: false,
		},
		{
			name: "invalid type",
			component: &Component{
				Type:  ComponentType("invalid"),
				Image: "test:latest",
			},
			wantError: true,
			errorMsg:  "invalid component type",
		},
		{
			name: "duplicate environment variables",
			component: &Component{
				Type:  ComponentTypeWeb,
				Image: "test:latest",
				Env: []EnvVar{
					{Name: "TEST", Value: "value1"},
					{Name: "TEST", Value: "value2"},
				},
			},
			wantError: true,
			errorMsg:  "duplicate environment variable",
		},
		{
			name: "invalid image policy",
			component: &Component{
				Type:        ComponentTypeWeb,
				Image:       "test:latest",
				ImagePolicy: &ImagePolicyConfig{}, // Empty policy
			},
			wantError: true,
			errorMsg:  "invalid image policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.component.Validate()
			if tt.wantError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestServiceDefinition_Validate(t *testing.T) {
	tests := []struct {
		name        string
		serviceDef  *ServiceDefinition
		wantError   bool
		errorMsg    string
	}{
		{
			name: "valid service definition",
			serviceDef: &ServiceDefinition{
				Name:        "test-service",
				Description: "Test service",
				Components: map[string]*Component{
					"web": {
						Type:     ComponentTypeWeb,
						Image:    "test:latest",
						Replicas: 2,
						Port:     8080,
					},
				},
			},
			wantError: false,
		},
		{
			name: "empty name",
			serviceDef: &ServiceDefinition{
				Components: map[string]*Component{
					"web": {Type: ComponentTypeWeb, Image: "test:latest"},
				},
			},
			wantError: true,
			errorMsg:  "service name is required",
		},
		{
			name: "invalid name",
			serviceDef: &ServiceDefinition{
				Name: "Invalid_Name",
				Components: map[string]*Component{
					"web": {Type: ComponentTypeWeb, Image: "test:latest"},
				},
			},
			wantError: true,
			errorMsg:  "not a valid DNS name",
		},
		{
			name: "no components",
			serviceDef: &ServiceDefinition{
				Name:       "test-service",
				Components: map[string]*Component{},
			},
			wantError: true,
			errorMsg:  "at least one component is required",
		},
		{
			name: "invalid component name",
			serviceDef: &ServiceDefinition{
				Name: "test-service",
				Components: map[string]*Component{
					"Invalid_Component": {Type: ComponentTypeWeb, Image: "test:latest"},
				},
			},
			wantError: true,
			errorMsg:  "not a valid DNS name",
		},
		{
			name: "nil component",
			serviceDef: &ServiceDefinition{
				Name: "test-service",
				Components: map[string]*Component{
					"web": nil,
				},
			},
			wantError: true,
			errorMsg:  "cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.serviceDef.Validate()
			if tt.wantError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetImageName(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		expected string
	}{
		{
			name:     "simple image with tag",
			imageRef: "nginx:1.21",
			expected: "nginx",
		},
		{
			name:     "registry with path and tag",
			imageRef: "ghcr.io/user/app:v1.0.0",
			expected: "ghcr.io/user/app",
		},
		{
			name:     "image without tag",
			imageRef: "nginx",
			expected: "nginx",
		},
		{
			name:     "image with port in registry",
			imageRef: "registry.local:5000/app:latest",
			expected: "registry.local:5000/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetImageName(tt.imageRef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetImageTag(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		expected string
	}{
		{
			name:     "simple image with tag",
			imageRef: "nginx:1.21",
			expected: "1.21",
		},
		{
			name:     "registry with path and tag",
			imageRef: "ghcr.io/user/app:v1.0.0",
			expected: "v1.0.0",
		},
		{
			name:     "image without tag",
			imageRef: "nginx",
			expected: "latest",
		},
		{
			name:     "image with latest tag",
			imageRef: "nginx:latest",
			expected: "latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetImageTag(tt.imageRef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidDNSName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid simple name", "test", true},
		{"valid name with hyphens", "test-service", true},
		{"valid name with numbers", "app123", true},
		{"invalid uppercase", "Test", false},
		{"invalid underscore", "test_service", false},
		{"invalid starting hyphen", "-test", false},
		{"invalid ending hyphen", "test-", false},
		{"invalid empty string", "", false},
		{"invalid too long", "this-is-a-very-long-name-that-exceeds-the-sixty-three-character-dns-limit-for-sure", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidDNSName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}