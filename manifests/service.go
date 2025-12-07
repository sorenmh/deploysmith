package manifests

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

// ServiceGenerator generates Kubernetes Service manifests
type ServiceGenerator struct {
	template *template.Template
}

// NewServiceGenerator creates a new service generator
func NewServiceGenerator() *ServiceGenerator {
	tmpl := template.Must(template.New("service").Funcs(templateFuncs).Parse(serviceTemplate))
	return &ServiceGenerator{
		template: tmpl,
	}
}

// Generate creates a Service manifest for a component
func (sg *ServiceGenerator) Generate(serviceName, componentName string, component *models.Component) ([]byte, error) {
	if !component.NeedsService() {
		return nil, fmt.Errorf("component %q does not need a service (no port specified)", componentName)
	}

	data := ServiceTemplateData{
		ServiceName:   serviceName,
		ComponentName: componentName,
		Component:     component,
		Labels:        generateLabels(serviceName, componentName, component),
	}

	var buf bytes.Buffer
	if err := sg.template.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute service template: %w", err)
	}

	return buf.Bytes(), nil
}

// ServiceTemplateData holds the data for the service template
type ServiceTemplateData struct {
	ServiceName   string
	ComponentName string
	Component     *models.Component
	Labels        map[string]string
}

const serviceTemplate = `apiVersion: v1
kind: Service
metadata:
  name: {{ .ComponentName }}
  labels:
{{- range $key, $value := .Labels }}
    {{ $key }}: {{ $value | quote }}
{{- end }}
spec:
  type: ClusterIP
  ports:
  - port: {{ .Component.Port }}
    targetPort: http
    protocol: TCP
    name: http
  selector:
    app.kubernetes.io/name: {{ .ComponentName | quote }}
    app.kubernetes.io/instance: {{ .ServiceName | quote }}
`