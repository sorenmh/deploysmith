package manifests

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

// FluxGenerator generates Flux Image Automation manifests
type FluxGenerator struct {
	template *template.Template
}

// NewFluxGenerator creates a new flux generator
func NewFluxGenerator() *FluxGenerator {
	funcMap := template.FuncMap{}
	for k, v := range templateFuncs {
		funcMap[k] = v
	}

	// Add flux-specific template functions
	funcMap["getPolicyType"] = func(data FluxTemplateData) string {
		return data.GetPolicyType()
	}
	funcMap["getPolicyConfig"] = func(data FluxTemplateData) map[string]string {
		return data.GetPolicyConfig()
	}

	tmpl := template.Must(template.New("flux").Funcs(funcMap).Parse(fluxTemplate))
	return &FluxGenerator{
		template: tmpl,
	}
}

// Generate creates Flux ImageRepository and ImagePolicy manifests for a component
func (fg *FluxGenerator) Generate(serviceName, componentName string, component *models.Component) ([]byte, error) {
	if component.ImagePolicy == nil {
		return nil, fmt.Errorf("component %q has no image policy defined", componentName)
	}

	data := FluxTemplateData{
		ServiceName:   serviceName,
		ComponentName: componentName,
		Component:     component,
		ImageName:     models.GetImageName(component.Image),
		Labels:        generateLabels(serviceName, componentName, component),
	}

	var buf bytes.Buffer
	if err := fg.template.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute flux template: %w", err)
	}

	return buf.Bytes(), nil
}

// FluxTemplateData holds the data for the flux template
type FluxTemplateData struct {
	ServiceName   string
	ComponentName string
	Component     *models.Component
	ImageName     string
	Labels        map[string]string
}

// GetPolicyType returns the policy type (semver or alphabetical)
func (f *FluxTemplateData) GetPolicyType() string {
	if f.Component.ImagePolicy.SemverRange != "" {
		return "semver"
	}
	return "alphabetical"
}

// GetPolicyConfig returns the policy configuration based on type
func (f *FluxTemplateData) GetPolicyConfig() map[string]string {
	if f.Component.ImagePolicy.SemverRange != "" {
		return map[string]string{
			"range": f.Component.ImagePolicy.SemverRange,
		}
	}

	if f.Component.ImagePolicy.Pattern != "" {
		return map[string]string{
			"order": "asc",
			"extract": f.Component.ImagePolicy.Pattern,
		}
	}

	// Default alphabetical policy
	return map[string]string{
		"order": "asc",
	}
}

// GetRepositoryName extracts the repository name without registry host
func GetRepositoryName(imageName string) string {
	// Remove registry host if present (e.g., ghcr.io/user/app -> user/app)
	parts := strings.SplitN(imageName, "/", 2)
	if len(parts) == 2 && strings.Contains(parts[0], ".") {
		return parts[1]
	}
	return imageName
}

const fluxTemplate = `---
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImageRepository
metadata:
  name: {{ .ServiceName }}-{{ .ComponentName }}-repo
  namespace: flux-system
  labels:
{{- range $key, $value := .Labels }}
    {{ $key }}: {{ $value | quote }}
{{- end }}
spec:
  image: {{ .ImageName }}
  interval: 1m
---
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImagePolicy
metadata:
  name: {{ .ServiceName }}-{{ .ComponentName }}-policy
  namespace: flux-system
  labels:
{{- range $key, $value := .Labels }}
    {{ $key }}: {{ $value | quote }}
{{- end }}
spec:
  imageRepositoryRef:
    name: {{ .ServiceName }}-{{ .ComponentName }}-repo
  policy:
{{- if eq (getPolicyType .) "semver" }}
    semver:
      range: {{ .Component.ImagePolicy.SemverRange | quote }}
{{- else }}
    alphabetical:
      order: asc
{{- if .Component.ImagePolicy.Pattern }}
      extract: {{ .Component.ImagePolicy.Pattern | quote }}
{{- end }}
{{- end }}
`