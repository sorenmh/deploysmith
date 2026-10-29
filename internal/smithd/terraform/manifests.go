package terraform

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// NamespaceFile is the file in environments/<env>/terraform/<app>/ that
// holds the runner namespace and its RBAC, NetworkPolicy and ResourceQuota
const NamespaceFile = "namespace.yaml"

// Runner limits for one environment. Every pod in the namespace must set
// requests.cpu, requests.memory and limits.memory, which the Terraform CR's
// runnerPodTemplate does.
const (
	quotaPods           = "4"
	quotaRequestsCPU    = "2"
	quotaRequestsMemory = "4Gi"
	quotaLimitsMemory   = "8Gi"
)

// runnerGRPCPort is the port tofu-controller reaches the runner pod on
const runnerGRPCPort = 30000

// privateCIDRs are never reachable through the internet egress rule: the
// cluster's pods and services (smithd, its database, NATS) and the cloud
// metadata endpoint, which would hand out the node's credentials
var (
	privateCIDRs   = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	privateCIDRsV6 = []string{"fc00::/7", "fe80::/10"}
)

// ManifestOptions configure the runner namespace
type ManifestOptions struct {
	// SmithdNamespace and SmithdServiceAccount identify smithd, which may
	// read the readable plans and events in the namespace
	SmithdNamespace      string
	SmithdServiceAccount string
	// ControllerNamespace is where tofu-controller runs; it connects to the
	// runner over gRPC
	ControllerNamespace string
	// OpenBaoNamespace and OpenBaoPort are where the runner reaches OpenBao
	OpenBaoNamespace string
	OpenBaoPort      int
	// KubeAPICIDRs are the Kubernetes API server addresses; the runner
	// stores plans as Secrets and ConfigMaps. If empty, the private ranges
	// are allowed on ports 443 and 6443 instead.
	KubeAPICIDRs []string
}

type object struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
	RoleRef    *roleRef `yaml:"roleRef,omitempty"`
	Subjects   []any    `yaml:"subjects,omitempty"`
	Spec       any      `yaml:"spec,omitempty"`
}

type metadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type roleRef struct {
	APIGroup string `yaml:"apiGroup"`
	Kind     string `yaml:"kind"`
	Name     string `yaml:"name"`
}

type subject struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type networkPolicySpec struct {
	PodSelector map[string]any      `yaml:"podSelector"`
	PolicyTypes []string            `yaml:"policyTypes"`
	Ingress     []networkPolicyRule `yaml:"ingress"`
	Egress      []networkPolicyRule `yaml:"egress"`
}

type networkPolicyRule struct {
	From  []peer `yaml:"from,omitempty"`
	To    []peer `yaml:"to,omitempty"`
	Ports []port `yaml:"ports"`
}

type peer struct {
	NamespaceSelector *selector `yaml:"namespaceSelector,omitempty"`
	PodSelector       *selector `yaml:"podSelector,omitempty"`
	IPBlock           *ipBlock  `yaml:"ipBlock,omitempty"`
}

type selector struct {
	MatchLabels map[string]string `yaml:"matchLabels,omitempty"`
}

type ipBlock struct {
	CIDR   string   `yaml:"cidr"`
	Except []string `yaml:"except,omitempty"`
}

type port struct {
	Protocol string `yaml:"protocol"`
	Port     int    `yaml:"port"`
}

// NamespaceManifest returns namespace.yaml for an app in an environment:
// the Namespace tf-<app>, the environment's runner ServiceAccounts, their
// RoleBindings, a NetworkPolicy and a ResourceQuota
func NamespaceManifest(app, env string, opts ManifestOptions) ([]byte, error) {
	namespace := Namespace(app)
	runner := RunnerServiceAccount(env, false)
	readOnly := RunnerServiceAccount(env, true)
	labels := map[string]string{"deploysmith.io/terraform-app": app, "deploysmith.io/environment": env}

	objects := []object{
		{
			APIVersion: "v1",
			Kind:       "Namespace",
			Metadata: metadata{
				Name:   namespace,
				Labels: map[string]string{"deploysmith.io/terraform-app": app},
				// Every environment of the app lists the Namespace: create it
				// once, never overwrite it from another environment's tree,
				// and keep it (and the other environments' runners) when one
				// environment is removed
				Annotations: map[string]string{
					"kustomize.toolkit.fluxcd.io/ssa":   "IfNotPresent",
					"kustomize.toolkit.fluxcd.io/prune": "disabled",
				},
			},
		},
		{APIVersion: "v1", Kind: "ServiceAccount", Metadata: metadata{Name: runner, Namespace: namespace, Labels: labels}},
		{APIVersion: "v1", Kind: "ServiceAccount", Metadata: metadata{Name: readOnly, Namespace: namespace, Labels: labels}},
		{
			// The runner stores plans as Secrets/ConfigMaps and uses Leases
			// in its own namespace only
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "RoleBinding",
			Metadata:   metadata{Name: runner, Namespace: namespace, Labels: labels},
			RoleRef:    &roleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "tf-runner-role"},
			Subjects: []any{
				subject{Kind: "ServiceAccount", Name: runner, Namespace: namespace},
				subject{Kind: "ServiceAccount", Name: readOnly, Namespace: namespace},
			},
		},
		{
			// Lets smithd read the readable plans and events in this namespace
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "RoleBinding",
			Metadata:   metadata{Name: "smithd-terraform-reader-" + env, Namespace: namespace, Labels: labels},
			RoleRef:    &roleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "smithd-terraform-reader"},
			Subjects:   []any{subject{Kind: "ServiceAccount", Name: opts.SmithdServiceAccount, Namespace: opts.SmithdNamespace}},
		},
		{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
			Metadata:   metadata{Name: runner, Namespace: namespace, Labels: labels},
			Spec:       networkPolicy(opts),
		},
		{
			APIVersion: "v1",
			Kind:       "ResourceQuota",
			Metadata:   metadata{Name: runner, Namespace: namespace, Labels: labels},
			Spec: map[string]any{"hard": map[string]string{
				"pods":            quotaPods,
				"requests.cpu":    quotaRequestsCPU,
				"requests.memory": quotaRequestsMemory,
				"limits.memory":   quotaLimitsMemory,
			}},
		},
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Runner namespace of the Terraform app %s in %s, managed by smithd\n", app, env)
	for _, obj := range objects {
		buf.WriteString("---\n")
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(obj); err != nil {
			return nil, fmt.Errorf("failed to encode %s %s: %w", obj.Kind, obj.Metadata.Name, err)
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// networkPolicy applies to every pod in the namespace (only runners run
// there), so a runner whose labels are unknown is still isolated. It allows
// tofu-controller in, and DNS, OpenBao, the Kubernetes API and HTTPS to the
// internet (AWS, provider registries) out. Nothing else in the cluster is
// reachable: not smithd, its database or NATS.
func networkPolicy(opts ManifestOptions) networkPolicySpec {
	namespaceSelector := func(name string) *selector {
		return &selector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": name}}
	}

	apiCIDRs := opts.KubeAPICIDRs
	if len(apiCIDRs) == 0 {
		apiCIDRs = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	}
	var apiPeers []peer
	for _, cidr := range apiCIDRs {
		apiPeers = append(apiPeers, peer{IPBlock: &ipBlock{CIDR: cidr}})
	}

	return networkPolicySpec{
		PodSelector: map[string]any{},
		PolicyTypes: []string{"Ingress", "Egress"},
		Ingress: []networkPolicyRule{{
			From:  []peer{{NamespaceSelector: namespaceSelector(opts.ControllerNamespace)}},
			Ports: []port{{Protocol: "TCP", Port: runnerGRPCPort}},
		}},
		Egress: []networkPolicyRule{
			{
				To:    []peer{{NamespaceSelector: &selector{}, PodSelector: &selector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}},
				Ports: []port{{Protocol: "UDP", Port: 53}, {Protocol: "TCP", Port: 53}},
			},
			{
				To:    []peer{{NamespaceSelector: namespaceSelector(opts.OpenBaoNamespace)}},
				Ports: []port{{Protocol: "TCP", Port: opts.OpenBaoPort}},
			},
			{
				To:    apiPeers,
				Ports: []port{{Protocol: "TCP", Port: 443}, {Protocol: "TCP", Port: 6443}},
			},
			{
				To: []peer{
					{IPBlock: &ipBlock{CIDR: "0.0.0.0/0", Except: privateCIDRs}},
					{IPBlock: &ipBlock{CIDR: "::/0", Except: privateCIDRsV6}},
				},
				Ports: []port{{Protocol: "TCP", Port: 443}},
			},
		},
	}
}
