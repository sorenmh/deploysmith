package terraform

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var testManifestOptions = ManifestOptions{
	SmithdNamespace:      "deploysmith",
	SmithdServiceAccount: "smithd",
	ControllerNamespace:  "flux-system",
	OpenBaoNamespace:     "openbao",
	OpenBaoPort:          8200,
	KubeAPICIDRs:         []string{"172.18.0.2/32"},
}

const wantNamespaceManifest = `# Runner namespace of the Terraform app viino in dev, managed by smithd
---
apiVersion: v1
kind: Namespace
metadata:
  name: tf-viino
  labels:
    deploysmith.io/terraform-app: viino
  annotations:
    kustomize.toolkit.fluxcd.io/prune: disabled
    kustomize.toolkit.fluxcd.io/ssa: IfNotPresent
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tf-runner-dev
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tf-runner-dev-readonly
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tf-runner-dev
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tf-runner-role
subjects:
  - kind: ServiceAccount
    name: tf-runner-dev
    namespace: tf-viino
  - kind: ServiceAccount
    name: tf-runner-dev-readonly
    namespace: tf-viino
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: smithd-terraform-reader-dev
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: smithd-terraform-reader
subjects:
  - kind: ServiceAccount
    name: smithd
    namespace: deploysmith
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: tf-runner-dev
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
spec:
  podSelector: {}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: flux-system
      ports:
        - protocol: TCP
          port: 30000
  egress:
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: openbao
      ports:
        - protocol: TCP
          port: 8200
    - to:
        - ipBlock:
            cidr: 172.18.0.2/32
      ports:
        - protocol: TCP
          port: 443
        - protocol: TCP
          port: 6443
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 10.0.0.0/8
              - 172.16.0.0/12
              - 192.168.0.0/16
              - 100.64.0.0/10
              - 169.254.0.0/16
        - ipBlock:
            cidr: ::/0
            except:
              - fc00::/7
              - fe80::/10
      ports:
        - protocol: TCP
          port: 443
---
apiVersion: v1
kind: ResourceQuota
metadata:
  name: tf-runner-dev
  namespace: tf-viino
  labels:
    deploysmith.io/environment: dev
    deploysmith.io/terraform-app: viino
spec:
  hard:
    limits.memory: 8Gi
    pods: "4"
    requests.cpu: "2"
    requests.memory: 4Gi
`

func TestNamespaceManifest(t *testing.T) {
	got, err := NamespaceManifest("viino", "dev", testManifestOptions)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantNamespaceManifest {
		t.Errorf("manifest mismatch\ngot:\n%s\nwant:\n%s", got, wantNamespaceManifest)
	}

	// Every document is valid YAML with a kind and a name
	dec := yaml.NewDecoder(bytes.NewReader(got))
	var kinds []string
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if doc.Metadata.Name == "" {
			t.Errorf("%s without a name", doc.Kind)
		}
		kinds = append(kinds, doc.Kind)
	}
	want := "Namespace ServiceAccount ServiceAccount RoleBinding RoleBinding NetworkPolicy ResourceQuota"
	if strings.Join(kinds, " ") != want {
		t.Errorf("kinds = %v, want %s", kinds, want)
	}
}

func TestNamespaceManifestWithoutAPICIDRs(t *testing.T) {
	opts := testManifestOptions
	opts.KubeAPICIDRs = nil
	got, err := NamespaceManifest("viino", "dev", opts)
	if err != nil {
		t.Fatal(err)
	}
	// Falls back to the private ranges, on the API server ports only
	for _, cidr := range []string{"cidr: 10.0.0.0/8", "cidr: 172.16.0.0/12", "cidr: 192.168.0.0/16"} {
		if !strings.Contains(string(got), cidr) {
			t.Errorf("manifest does not allow %s:\n%s", cidr, got)
		}
	}
}

func TestEnvironmentsDoNotShareObjects(t *testing.T) {
	// Two environments in one cluster write the same Namespace, which Flux
	// creates once and never prunes; every other object is per environment
	names := func(env string) map[string]bool {
		got, err := NamespaceManifest("viino", env, testManifestOptions)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		dec := yaml.NewDecoder(bytes.NewReader(got))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if err := dec.Decode(&doc); err != nil {
				break
			}
			seen[doc.Kind+"/"+doc.Metadata.Name] = true
		}
		return seen
	}
	dev, prod := names("dev"), names("prod")
	for name := range dev {
		if prod[name] && name != "Namespace/tf-viino" {
			t.Errorf("%s is written by both environments", name)
		}
	}
}

func TestValidateNames(t *testing.T) {
	tests := []struct {
		app, env string
		ok       bool
	}{
		{"viino", "prod", true},
		{"viino-backend", "dev-2", true},
		{"Viino", "prod", false},
		{"viino_backend", "prod", false},
		{"viino.api", "prod", false},
		{"viino", "-prod", false},
		{strings.Repeat("a", 61), "p", false},                     // too long
		{strings.Repeat("a", 40), strings.Repeat("b", 11), true},  // IAM role tf-<app>-<env>-readonly at 64
		{strings.Repeat("a", 40), strings.Repeat("b", 12), false}, // IAM role over 64
	}
	for _, tt := range tests {
		err := ValidateNames(tt.app, tt.env)
		if (err == nil) != tt.ok {
			t.Errorf("ValidateNames(%q, %q) = %v, want ok=%v", tt.app, tt.env, err, tt.ok)
		}
	}
}
