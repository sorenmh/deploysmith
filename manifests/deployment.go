package manifests

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

// DeploymentGenerator generates Kubernetes Deployment manifests
type DeploymentGenerator struct {
	template *template.Template
}

// NewDeploymentGenerator creates a new deployment generator
func NewDeploymentGenerator() *DeploymentGenerator {
	tmpl := template.Must(template.New("deployment").Funcs(templateFuncs).Parse(deploymentTemplate))
	return &DeploymentGenerator{
		template: tmpl,
	}
}

// Generate creates a Deployment manifest for a component
func (dg *DeploymentGenerator) Generate(serviceName, componentName string, component *models.Component, imagePullSecrets ...string) ([]byte, error) {
	data := DeploymentTemplateData{
		ServiceName:      serviceName,
		ComponentName:    componentName,
		Component:        component,
		Labels:           generateLabels(serviceName, componentName, component),
		ImageName:        models.GetImageName(component.Image),
		ImagePullSecrets: imagePullSecrets,
	}

	var buf bytes.Buffer
	if err := dg.template.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute deployment template: %w", err)
	}

	return buf.Bytes(), nil
}

// DeploymentTemplateData holds the data for the deployment template
type DeploymentTemplateData struct {
	ServiceName      string
	ComponentName    string
	Component        *models.Component
	Labels           map[string]string
	ImageName        string
	ImagePullSecrets []string
}

// generateLabels creates standard labels for Kubernetes resources
func generateLabels(serviceName, componentName string, component *models.Component) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       componentName,
		"app.kubernetes.io/instance":   serviceName,
		"app.kubernetes.io/component":  string(component.Type),
		"app.kubernetes.io/part-of":    serviceName,
		"app.kubernetes.io/managed-by": "service-abstraction-layer",
	}
}

// Template functions for use in templates
var templateFuncs = template.FuncMap{
	"quote": func(s string) string {
		return fmt.Sprintf("%q", s)
	},
	"split": func(sep, s string) []string {
		return strings.Split(s, sep)
	},
	"indent": func(spaces int, text string) string {
		// TODO: Implement proper indentation
		return text
	},
	"imagePolicyAnnotation": func(serviceName, componentName string) string {
		return fmt.Sprintf(`{"$imagepolicy": "flux-system:%s-%s-policy"}`, serviceName, componentName)
	},
}

const deploymentTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .ComponentName }}
  labels:
{{- range $key, $value := .Labels }}
    {{ $key }}: {{ $value | quote }}
{{- end }}
spec:
  replicas: {{ .Component.Replicas }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .ComponentName | quote }}
      app.kubernetes.io/instance: {{ .ServiceName | quote }}
  template:
    metadata:
      labels:
{{- range $key, $value := .Labels }}
        {{ $key }}: {{ $value | quote }}
{{- end }}
    spec:
{{- if .ImagePullSecrets }}
      imagePullSecrets:
{{- range .ImagePullSecrets }}
      - name: {{ . | quote }}
{{- end }}
{{- end }}
      containers:
      - name: {{ .ComponentName }}
        image: {{ .Component.Image }} # {{ imagePolicyAnnotation .ServiceName .ComponentName }}
{{- if .Component.Port }}
        ports:
        - name: http
          containerPort: {{ .Component.Port }}
          protocol: TCP
{{- end }}
{{- if .Component.Env }}
        env:
{{- range .Component.Env }}
        - name: {{ .Name }}
{{- if .Value }}
          value: {{ .Value | quote }}
{{- else if .SecretRef }}
{{- $parts := split "/" .SecretRef }}
          valueFrom:
            secretKeyRef:
              name: {{ index $parts 0 | quote }}
              key: {{ index $parts 1 | quote }}
{{- end }}
{{- end }}
{{- end }}
{{- if .Component.Resources }}
        resources:
{{- if .Component.Resources.Limits }}
          limits:
{{- if .Component.Resources.Limits.Memory }}
            memory: {{ .Component.Resources.Limits.Memory | quote }}
{{- end }}
{{- if .Component.Resources.Limits.CPU }}
            cpu: {{ .Component.Resources.Limits.CPU | quote }}
{{- end }}
{{- end }}
{{- if .Component.Resources.Requests }}
          requests:
{{- if .Component.Resources.Requests.Memory }}
            memory: {{ .Component.Resources.Requests.Memory | quote }}
{{- end }}
{{- if .Component.Resources.Requests.CPU }}
            cpu: {{ .Component.Resources.Requests.CPU | quote }}
{{- end }}
{{- end }}
{{- end }}
`