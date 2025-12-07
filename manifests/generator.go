package manifests

import (
	"fmt"
	"strings"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

// ManifestGenerator is the interface for generating Kubernetes manifests
type ManifestGenerator interface {
	GenerateManifests(serviceName string, serviceDef *models.ServiceDefinition) (map[string][]byte, error)
}

// RegistryConfig contains registry configuration for manifest generation
type RegistryConfig struct {
	Type                string
	ImagePullSecretName string
}

// Generator implements the ManifestGenerator interface
type Generator struct {
	deploymentGenerator *DeploymentGenerator
	serviceGenerator    *ServiceGenerator
	fluxGenerator       *FluxGenerator
	registryConfig      *RegistryConfig
}

// NewGenerator creates a new manifest generator
func NewGenerator() *Generator {
	return &Generator{
		deploymentGenerator: NewDeploymentGenerator(),
		serviceGenerator:    NewServiceGenerator(),
		fluxGenerator:       NewFluxGenerator(),
	}
}

// NewGeneratorWithConfig creates a new manifest generator with registry configuration
func NewGeneratorWithConfig(registryConfig *RegistryConfig) *Generator {
	return &Generator{
		deploymentGenerator: NewDeploymentGenerator(),
		serviceGenerator:    NewServiceGenerator(),
		fluxGenerator:       NewFluxGenerator(),
		registryConfig:      registryConfig,
	}
}

// GenerateManifests generates all Kubernetes manifests for a service definition
func (g *Generator) GenerateManifests(serviceName string, serviceDef *models.ServiceDefinition) (map[string][]byte, error) {
	manifests := make(map[string][]byte)

	// Validate service definition first
	if err := serviceDef.Validate(); err != nil {
		return nil, fmt.Errorf("invalid service definition: %w", err)
	}

	// Generate manifests for each component
	for componentName, component := range serviceDef.Components {
		// Apply defaults
		component = component.GetDefaults()

		// Generate Deployment manifest
		var imagePullSecrets []string
		if g.needsImagePullSecret(component.Image) {
			imagePullSecrets = append(imagePullSecrets, g.getImagePullSecretName())
		}

		deploymentYAML, err := g.deploymentGenerator.Generate(serviceName, componentName, component, imagePullSecrets...)
		if err != nil {
			return nil, fmt.Errorf("failed to generate deployment for component %q: %w", componentName, err)
		}
		manifests[fmt.Sprintf("%s-deployment.yaml", componentName)] = deploymentYAML

		// Generate Service manifest if the component has a port
		if component.NeedsService() {
			serviceYAML, err := g.serviceGenerator.Generate(serviceName, componentName, component)
			if err != nil {
				return nil, fmt.Errorf("failed to generate service for component %q: %w", componentName, err)
			}
			manifests[fmt.Sprintf("%s-service.yaml", componentName)] = serviceYAML
		}

		// Generate Flux manifests if the component has an image policy
		if component.ImagePolicy != nil {
			fluxYAML, err := g.fluxGenerator.Generate(serviceName, componentName, component)
			if err != nil {
				return nil, fmt.Errorf("failed to generate flux manifests for component %q: %w", componentName, err)
			}
			manifests[fmt.Sprintf("%s-flux.yaml", componentName)] = fluxYAML
		}
	}

	return manifests, nil
}

// GenerateManifestsForComponent generates manifests for a specific component only
func (g *Generator) GenerateManifestsForComponent(serviceName, componentName string, component *models.Component) (map[string][]byte, error) {
	manifests := make(map[string][]byte)

	// Validate component
	if err := component.Validate(); err != nil {
		return nil, fmt.Errorf("invalid component: %w", err)
	}

	// Apply defaults
	component = component.GetDefaults()

	// Generate Deployment manifest
	var imagePullSecrets []string
	if g.needsImagePullSecret(component.Image) {
		imagePullSecrets = append(imagePullSecrets, g.getImagePullSecretName())
	}

	deploymentYAML, err := g.deploymentGenerator.Generate(serviceName, componentName, component, imagePullSecrets...)
	if err != nil {
		return nil, fmt.Errorf("failed to generate deployment: %w", err)
	}
	manifests[fmt.Sprintf("%s-deployment.yaml", componentName)] = deploymentYAML

	// Generate Service manifest if needed
	if component.NeedsService() {
		serviceYAML, err := g.serviceGenerator.Generate(serviceName, componentName, component)
		if err != nil {
			return nil, fmt.Errorf("failed to generate service: %w", err)
		}
		manifests[fmt.Sprintf("%s-service.yaml", componentName)] = serviceYAML
	}

	// Generate Flux manifests if the component has an image policy
	if component.ImagePolicy != nil {
		fluxYAML, err := g.fluxGenerator.Generate(serviceName, componentName, component)
		if err != nil {
			return nil, fmt.Errorf("failed to generate flux manifests: %w", err)
		}
		manifests[fmt.Sprintf("%s-flux.yaml", componentName)] = fluxYAML
	}

	return manifests, nil
}

// ValidateManifests validates that generated manifests are valid YAML
func (g *Generator) ValidateManifests(manifests map[string][]byte) error {
	// Basic YAML validation - ensure each manifest is parseable
	for filename, content := range manifests {
		if len(content) == 0 {
			return fmt.Errorf("manifest %q is empty", filename)
		}

		// TODO: Add more sophisticated validation
		// - YAML parsing
		// - Kubernetes object validation
		// - Schema validation
	}

	return nil
}

// needsImagePullSecret determines if an image requires a pull secret
func (g *Generator) needsImagePullSecret(image string) bool {
	if g.registryConfig == nil {
		return false
	}

	// Check if it's an ECR image
	if g.registryConfig.Type == "ecr" {
		return strings.Contains(image, ".dkr.ecr.") && strings.Contains(image, ".amazonaws.com")
	}

	// For other private registries, you could add logic here
	// For now, assume any non-public registry needs a pull secret
	return !strings.HasPrefix(image, "docker.io/") &&
		   !strings.Contains(image, "docker.io/") &&
		   strings.Contains(image, "/")
}

// getImagePullSecretName returns the name of the image pull secret to use
func (g *Generator) getImagePullSecretName() string {
	if g.registryConfig != nil && g.registryConfig.ImagePullSecretName != "" {
		return g.registryConfig.ImagePullSecretName
	}
	return "registry-credentials"
}