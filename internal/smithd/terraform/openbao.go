package terraform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/openbao"
)

// OpenBao is the part of the OpenBao API the provisioner uses
type OpenBao interface {
	WritePolicy(ctx context.Context, name, policy string) error
	WriteKubernetesRole(ctx context.Context, mount, name string, role openbao.KubernetesRole) error
	WriteAWSRole(ctx context.Context, mount, name string, role openbao.AWSRole) error
	EnsureTransitKey(ctx context.Context, mount, name string) (bool, error)
}

// OpenBaoOptions configure what the provisioner writes to OpenBao
type OpenBaoOptions struct {
	// Mounts of the engines; KubernetesMount is the auth method the
	// runners log in with
	KVMount         string
	TransitMount    string
	AWSMount        string
	KubernetesMount string

	// Audience the runner's ServiceAccount token must have; empty
	// accepts any
	Audience string
	// RunnerTokenTTL is the lifetime of a runner's OpenBao token; it must
	// cover the longest plan or apply
	RunnerTokenTTL time.Duration
	// STSTTL is the lifetime of the AWS credentials a runner gets
	STSTTL time.Duration
	// AWSAccountID is the account of the tf-<app>-<env> IAM roles, unless
	// an environment names its own role
	AWSAccountID string
}

// Provisioner creates an app's OpenBao roles, policies and Transit key
type Provisioner struct {
	Bao  OpenBao
	Opts OpenBaoOptions
}

// RoleARN returns the IAM role OpenBao assumes for an app in an
// environment: override if set, else the tf-<app>-<env> convention. The
// read-only role is the same ARN with a "-readonly" suffix.
func (o OpenBaoOptions) RoleARN(app, env, override string, readOnly bool) (string, error) {
	if override != "" {
		return withReadOnly(override, readOnly), nil
	}
	if o.AWSAccountID == "" {
		return "", fmt.Errorf("no AWS role for %s in %s: set TERRAFORM_AWS_ACCOUNT_ID or the environment's AWS role ARN", app, env)
	}
	return IAMRoleARN(o.AWSAccountID, app, env, readOnly), nil
}

// Provision creates or updates, for an app in an environment, the Transit
// key tf-<app> (only if missing; keys are never changed or deleted), and for
// the apply and the plan-only runner each an ACL policy, a Kubernetes auth
// role bound to the runner's ServiceAccount and an AWS secrets engine role.
// Every step is an upsert, so it is safe to run again; all steps are tried
// and their errors returned together.
func (p *Provisioner) Provision(ctx context.Context, app, env, awsRoleARN string) error {
	var errs []error
	if _, err := p.Bao.EnsureTransitKey(ctx, p.Opts.TransitMount, TransitKey(app)); err != nil {
		errs = append(errs, fmt.Errorf("transit key %s: %w", TransitKey(app), err))
	}

	for _, readOnly := range []bool{false, true} {
		if err := p.Bao.WritePolicy(ctx, PolicyName(app, env, readOnly), p.Policy(app, env, readOnly)); err != nil {
			errs = append(errs, fmt.Errorf("policy %s: %w", PolicyName(app, env, readOnly), err))
		}

		role := openbao.KubernetesRole{
			BoundServiceAccountNames:      []string{RunnerServiceAccount(env, readOnly)},
			BoundServiceAccountNamespaces: []string{Namespace(app)},
			Audience:                      p.Opts.Audience,
			TokenPolicies:                 []string{PolicyName(app, env, readOnly)},
			TokenTTL:                      duration(p.Opts.RunnerTokenTTL),
			TokenMaxTTL:                   duration(p.Opts.RunnerTokenTTL),
		}
		if err := p.Bao.WriteKubernetesRole(ctx, p.Opts.KubernetesMount, AuthRole(app, env, readOnly), role); err != nil {
			errs = append(errs, fmt.Errorf("kubernetes auth role %s: %w", AuthRole(app, env, readOnly), err))
		}

		arn, err := p.Opts.RoleARN(app, env, awsRoleARN, readOnly)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		awsRole := openbao.AWSRole{
			CredentialType: "assumed_role",
			RoleARNs:       []string{arn},
			DefaultSTSTTL:  duration(p.Opts.STSTTL),
			MaxSTSTTL:      duration(p.Opts.STSTTL),
		}
		if err := p.Bao.WriteAWSRole(ctx, p.Opts.AWSMount, AWSRole(app, env, readOnly), awsRole); err != nil {
			errs = append(errs, fmt.Errorf("aws role %s: %w", AWSRole(app, env, readOnly), err))
		}
	}
	return errors.Join(errs...)
}

// Policy returns the ACL policy of a runner: read the app's KV secrets
// (apps/<app>/*), use (not manage) the app's Transit key, and get the AWS
// credentials of its environment. OpenTofu's openbao key provider only
// uses datakey (to encrypt) and decrypt; the plan-only runner gets no
// encrypt and the read-only AWS role, which cannot write state.
func (p *Provisioner) Policy(app, env string, readOnly bool) string {
	key := TransitKey(app)
	awsRole := AWSRole(app, env, readOnly)

	var b strings.Builder
	fmt.Fprintf(&b, "# Runner of the Terraform app %s in %s", app, env)
	if readOnly {
		b.WriteString(" (plan-only)")
	}
	b.WriteString(", managed by smithd\n\n")

	rule := func(path string, capabilities ...string) {
		fmt.Fprintf(&b, "path %q {\n  capabilities = [%s]\n}\n\n", path, quoteList(capabilities))
	}
	rule(fmt.Sprintf("%s/data/apps/%s/*", p.Opts.KVMount, app), "read")
	rule(fmt.Sprintf("%s/metadata/apps/%s/*", p.Opts.KVMount, app), "read", "list")
	if !readOnly {
		rule(fmt.Sprintf("%s/encrypt/%s", p.Opts.TransitMount, key), "update")
	}
	rule(fmt.Sprintf("%s/decrypt/%s", p.Opts.TransitMount, key), "update")
	rule(fmt.Sprintf("%s/datakey/plaintext/%s", p.Opts.TransitMount, key), "update")
	rule(fmt.Sprintf("%s/datakey/wrapped/%s", p.Opts.TransitMount, key), "update")
	rule(fmt.Sprintf("%s/creds/%s", p.Opts.AWSMount, awsRole), "read", "update")
	rule(fmt.Sprintf("%s/sts/%s", p.Opts.AWSMount, awsRole), "read", "update")
	return strings.TrimSuffix(b.String(), "\n")
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}

// duration formats d in seconds, which OpenBao accepts for every TTL field
func duration(d time.Duration) string {
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
