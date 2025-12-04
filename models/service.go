package models

import (
	"fmt"
	"strings"
)

// ServiceDefinition represents the complete service configuration
// as defined in service.yaml
type ServiceDefinition struct {
	Name        string                `yaml:"name" json:"name" validate:"required,dns1123"`
	Description string                `yaml:"description,omitempty" json:"description,omitempty" validate:"max=256"`
	Components  map[string]*Component `yaml:"components" json:"components" validate:"required,min=1,dive"`
}

// Component represents a single deployable component within a service
type Component struct {
	Type        ComponentType          `yaml:"type" json:"type" validate:"required"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty" validate:"max=256"`
	Image       string                 `yaml:"image" json:"image" validate:"required,image_ref"`
	ImagePolicy *ImagePolicyConfig     `yaml:"image_policy,omitempty" json:"image_policy,omitempty"`
	Replicas    int                    `yaml:"replicas,omitempty" json:"replicas,omitempty" validate:"omitempty,min=1,max=100"`
	Port        int                    `yaml:"port,omitempty" json:"port,omitempty" validate:"omitempty,min=1,max=65535"`
	Env         []EnvVar               `yaml:"env,omitempty" json:"env,omitempty"`
	Resources   *ResourceRequirements  `yaml:"resources,omitempty" json:"resources,omitempty"`
}

// ComponentType defines the supported component types
type ComponentType string

const (
	ComponentTypeWeb ComponentType = "web"
	ComponentTypeAPI ComponentType = "api"
)

// IsValid checks if the component type is supported
func (ct ComponentType) IsValid() bool {
	switch ct {
	case ComponentTypeWeb, ComponentTypeAPI:
		return true
	default:
		return false
	}
}

// String returns the string representation of ComponentType
func (ct ComponentType) String() string {
	return string(ct)
}

// ImagePolicyConfig defines how Flux should handle image updates
type ImagePolicyConfig struct {
	SemverRange string `yaml:"semver_range,omitempty" json:"semver_range,omitempty"`
	Pattern     string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
}

// Validate ensures the image policy has exactly one configuration method
func (ipc *ImagePolicyConfig) Validate() error {
	hasRange := ipc.SemverRange != ""
	hasPattern := ipc.Pattern != ""

	if !hasRange && !hasPattern {
		return fmt.Errorf("image policy must specify either semver_range or pattern")
	}

	if hasRange && hasPattern {
		return fmt.Errorf("image policy cannot specify both semver_range and pattern")
	}

	return nil
}

// EnvVar represents an environment variable
type EnvVar struct {
	Name      string `yaml:"name" json:"name" validate:"required,env_var_name"`
	Value     string `yaml:"value,omitempty" json:"value,omitempty"`
	SecretRef string `yaml:"secretRef,omitempty" json:"secretRef,omitempty" validate:"omitempty,secret_ref"`
}

// Validate ensures the environment variable has exactly one value source
func (ev *EnvVar) Validate() error {
	hasValue := ev.Value != ""
	hasSecretRef := ev.SecretRef != ""

	if !hasValue && !hasSecretRef {
		return fmt.Errorf("environment variable %q must specify either value or secretRef", ev.Name)
	}

	if hasValue && hasSecretRef {
		return fmt.Errorf("environment variable %q cannot specify both value and secretRef", ev.Name)
	}

	return nil
}

// ResourceRequirements defines CPU and memory constraints
type ResourceRequirements struct {
	Limits   *ResourceList `yaml:"limits,omitempty" json:"limits,omitempty"`
	Requests *ResourceList `yaml:"requests,omitempty" json:"requests,omitempty"`
}

// ResourceList defines specific resource amounts
type ResourceList struct {
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty" validate:"omitempty,k8s_quantity"`
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty" validate:"omitempty,k8s_quantity"`
}

// GetDefaults returns a component with default values applied
func (c *Component) GetDefaults() *Component {
	defaults := *c // Copy the component

	// Apply default values
	if defaults.Replicas == 0 {
		defaults.Replicas = 1
	}

	return &defaults
}

// NeedsService returns true if the component should have a Kubernetes Service
func (c *Component) NeedsService() bool {
	return c.Port > 0
}

// ValidateComponent validates a component configuration
func (c *Component) Validate() error {
	if !c.Type.IsValid() {
		return fmt.Errorf("invalid component type: %q", c.Type)
	}

	if c.ImagePolicy != nil {
		if err := c.ImagePolicy.Validate(); err != nil {
			return fmt.Errorf("invalid image policy: %w", err)
		}
	}

	// Validate environment variables
	envNames := make(map[string]bool)
	for _, env := range c.Env {
		if err := env.Validate(); err != nil {
			return err
		}

		// Check for duplicate names
		if envNames[env.Name] {
			return fmt.Errorf("duplicate environment variable: %q", env.Name)
		}
		envNames[env.Name] = true
	}

	return nil
}

// ValidateServiceDefinition validates the entire service definition
func (sd *ServiceDefinition) Validate() error {
	if sd.Name == "" {
		return fmt.Errorf("service name is required")
	}

	if !isValidDNSName(sd.Name) {
		return fmt.Errorf("service name %q is not a valid DNS name", sd.Name)
	}

	if len(sd.Components) == 0 {
		return fmt.Errorf("at least one component is required")
	}

	// Validate each component
	for name, component := range sd.Components {
		if component == nil {
			return fmt.Errorf("component %q cannot be nil", name)
		}

		if !isValidDNSName(name) {
			return fmt.Errorf("component name %q is not a valid DNS name", name)
		}

		if err := component.Validate(); err != nil {
			return fmt.Errorf("component %q: %w", name, err)
		}
	}

	return nil
}

// GetImageName extracts the image name (without tag) from an image reference
func GetImageName(imageRef string) string {
	// Split on the last ':' to separate image from tag
	parts := strings.Split(imageRef, ":")
	if len(parts) < 2 {
		return imageRef
	}

	// Join all parts except the last one (which is the tag)
	return strings.Join(parts[:len(parts)-1], ":")
}

// GetImageTag extracts the tag from an image reference
func GetImageTag(imageRef string) string {
	parts := strings.Split(imageRef, ":")
	if len(parts) < 2 {
		return "latest"
	}

	return parts[len(parts)-1]
}

// isValidDNSName checks if a string is a valid DNS-1123 subdomain name
func isValidDNSName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}

	// Must start and end with alphanumeric
	if !isAlphaNumeric(name[0]) || !isAlphaNumeric(name[len(name)-1]) {
		return false
	}

	// Check each character
	for _, char := range name {
		if !isAlphaNumeric(byte(char)) && char != '-' {
			return false
		}
	}

	return true
}

// isAlphaNumeric checks if a byte is alphanumeric
func isAlphaNumeric(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}