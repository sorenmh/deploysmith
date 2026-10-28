package terraform

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/openbao"
)

// fakeOpenBao records what is written, keyed by mount and name
type fakeOpenBao struct {
	mu       sync.Mutex
	policies map[string]string
	k8sRoles map[string]openbao.KubernetesRole
	awsRoles map[string]openbao.AWSRole
	keys     map[string]bool
	err      error // returned by WriteAWSRole
}

func newFakeOpenBao() *fakeOpenBao {
	return &fakeOpenBao{
		policies: map[string]string{},
		k8sRoles: map[string]openbao.KubernetesRole{},
		awsRoles: map[string]openbao.AWSRole{},
		keys:     map[string]bool{},
	}
}

func (f *fakeOpenBao) WritePolicy(_ context.Context, name, policy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies[name] = policy
	return nil
}

func (f *fakeOpenBao) WriteKubernetesRole(_ context.Context, mount, name string, role openbao.KubernetesRole) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.k8sRoles[mount+"/"+name] = role
	return nil
}

func (f *fakeOpenBao) WriteAWSRole(_ context.Context, mount, name string, role openbao.AWSRole) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.awsRoles[mount+"/"+name] = role
	return nil
}

func (f *fakeOpenBao) EnsureTransitKey(_ context.Context, mount, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys[mount+"/"+name] {
		return false, nil
	}
	f.keys[mount+"/"+name] = true
	return true, nil
}

var testOpenBaoOptions = OpenBaoOptions{
	KVMount:         "secret",
	TransitMount:    "transit",
	AWSMount:        "aws",
	KubernetesMount: "kubernetes",
	RunnerTokenTTL:  2 * time.Hour,
	STSTTL:          time.Hour,
	AWSAccountID:    "123456789012",
}

const wantApplyPolicy = `# Runner of the Terraform app viino in prod, managed by smithd

path "secret/data/apps/viino/*" {
  capabilities = ["read"]
}

path "secret/metadata/apps/viino/*" {
  capabilities = ["read", "list"]
}

path "transit/encrypt/tf-viino" {
  capabilities = ["update"]
}

path "transit/decrypt/tf-viino" {
  capabilities = ["update"]
}

path "transit/datakey/plaintext/tf-viino" {
  capabilities = ["update"]
}

path "transit/datakey/wrapped/tf-viino" {
  capabilities = ["update"]
}

path "aws/creds/viino-prod" {
  capabilities = ["read", "update"]
}

path "aws/sts/viino-prod" {
  capabilities = ["read", "update"]
}
`

func TestProvision(t *testing.T) {
	bao := newFakeOpenBao()
	p := &Provisioner{Bao: bao, Opts: testOpenBaoOptions}

	// Running twice gives the same result
	for i := 0; i < 2; i++ {
		if err := p.Provision(context.Background(), "viino", "prod", ""); err != nil {
			t.Fatal(err)
		}
	}

	if !bao.keys["transit/tf-viino"] || len(bao.keys) != 1 {
		t.Errorf("transit keys = %v", bao.keys)
	}

	if got := bao.policies["tf-viino-prod"]; got != wantApplyPolicy {
		t.Errorf("apply policy mismatch\ngot:\n%s\nwant:\n%s", got, wantApplyPolicy)
	}
	readOnly := bao.policies["tf-viino-prod-readonly"]
	if strings.Contains(readOnly, "encrypt/") || !strings.Contains(readOnly, `"aws/creds/viino-prod-readonly"`) || !strings.Contains(readOnly, `"transit/decrypt/tf-viino"`) {
		t.Errorf("unexpected read-only policy:\n%s", readOnly)
	}
	if len(bao.policies) != 2 {
		t.Errorf("policies = %v", bao.policies)
	}

	wantRoles := map[string]openbao.KubernetesRole{
		"kubernetes/tf-viino-prod": {
			BoundServiceAccountNames:      []string{"tf-runner-prod"},
			BoundServiceAccountNamespaces: []string{"tf-viino"},
			TokenPolicies:                 []string{"tf-viino-prod"},
			TokenTTL:                      "7200s",
			TokenMaxTTL:                   "7200s",
		},
		"kubernetes/tf-viino-prod-readonly": {
			BoundServiceAccountNames:      []string{"tf-runner-prod-readonly"},
			BoundServiceAccountNamespaces: []string{"tf-viino"},
			TokenPolicies:                 []string{"tf-viino-prod-readonly"},
			TokenTTL:                      "7200s",
			TokenMaxTTL:                   "7200s",
		},
	}
	for name, want := range wantRoles {
		if got := bao.k8sRoles[name]; !equalRole(got, want) {
			t.Errorf("role %s = %+v, want %+v", name, got, want)
		}
	}

	wantAWS := map[string]string{
		"aws/viino-prod":          "arn:aws:iam::123456789012:role/tf-viino-prod",
		"aws/viino-prod-readonly": "arn:aws:iam::123456789012:role/tf-viino-prod-readonly",
	}
	for name, arn := range wantAWS {
		got := bao.awsRoles[name]
		if got.CredentialType != "assumed_role" || len(got.RoleARNs) != 1 || got.RoleARNs[0] != arn || got.DefaultSTSTTL != "3600s" {
			t.Errorf("aws role %s = %+v, want role %s", name, got, arn)
		}
	}
}

func equalRole(a, b openbao.KubernetesRole) bool {
	return strings.Join(a.BoundServiceAccountNames, ",") == strings.Join(b.BoundServiceAccountNames, ",") &&
		strings.Join(a.BoundServiceAccountNamespaces, ",") == strings.Join(b.BoundServiceAccountNamespaces, ",") &&
		strings.Join(a.TokenPolicies, ",") == strings.Join(b.TokenPolicies, ",") &&
		a.TokenTTL == b.TokenTTL && a.TokenMaxTTL == b.TokenMaxTTL && a.Audience == b.Audience
}

func TestProvisionRoleARNOverride(t *testing.T) {
	bao := newFakeOpenBao()
	opts := testOpenBaoOptions
	opts.AWSAccountID = ""
	p := &Provisioner{Bao: bao, Opts: opts}

	if err := p.Provision(context.Background(), "legacy", "prod", "arn:aws:iam::999999999999:role/legacy-terraform"); err != nil {
		t.Fatal(err)
	}
	if got := bao.awsRoles["aws/legacy-prod-readonly"].RoleARNs; len(got) != 1 || got[0] != "arn:aws:iam::999999999999:role/legacy-terraform-readonly" {
		t.Errorf("read-only role ARNs = %v", got)
	}
}

func TestProvisionReportsAllErrors(t *testing.T) {
	bao := newFakeOpenBao()
	opts := testOpenBaoOptions
	opts.AWSAccountID = ""
	p := &Provisioner{Bao: bao, Opts: opts}

	err := p.Provision(context.Background(), "viino", "prod", "")
	if err == nil || !strings.Contains(err.Error(), "TERRAFORM_AWS_ACCOUNT_ID") {
		t.Fatalf("err = %v, want missing account", err)
	}
	// Everything else is still written
	if len(bao.policies) != 2 || len(bao.k8sRoles) != 2 || !bao.keys["transit/tf-viino"] {
		t.Errorf("not everything written: policies %d, roles %d, keys %v", len(bao.policies), len(bao.k8sRoles), bao.keys)
	}

	bao.err = errors.New("permission denied")
	p.Opts.AWSAccountID = "123456789012"
	if err := p.Provision(context.Background(), "viino", "prod", ""); err == nil || !strings.Contains(err.Error(), "aws role viino-prod: permission denied") {
		t.Errorf("err = %v", err)
	}
}
