// Package terraform sets up what a Terraform app needs to run isolated
// from other apps: its runner namespace in the gitops repository and its
// OpenBao roles, policies and Transit key. See
// docs/specs/terraform-controller.md for the layout.
//
// One namespace tf-<app> per app holds the runners of all its
// environments. Everything else is per environment, so environments that
// share a cluster each get their own ServiceAccounts, OpenBao roles and
// AWS credentials, and their gitops trees never write the same object
// (except the Namespace, which is created once and never pruned).
package terraform

import (
	"fmt"
	"regexp"
)

// labelPattern is a Kubernetes DNS label (RFC 1123)
var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateNames checks that an app and environment name can be used in the
// names below: as Kubernetes namespace and ServiceAccount names (DNS labels
// of at most 63 characters) and as IAM role names (at most 64 characters).
func ValidateNames(app, env string) error {
	for _, name := range []string{app, env} {
		if !labelPattern.MatchString(name) {
			return fmt.Errorf("'%s' must contain only lowercase letters, digits and '-' to be used for Terraform", name)
		}
	}
	if len(Namespace(app)) > 63 {
		return fmt.Errorf("app name '%s' is too long for Terraform (namespace %s is over 63 characters)", app, Namespace(app))
	}
	if len(IAMRoleName(app, env, true)) > 64 {
		return fmt.Errorf("app and environment names '%s', '%s' are too long for Terraform (IAM role %s is over 64 characters)", app, env, IAMRoleName(app, env, true))
	}
	if len(RunnerServiceAccount(env, true)) > 63 {
		return fmt.Errorf("environment name '%s' is too long for Terraform", env)
	}
	return nil
}

// Namespace is the runner namespace of an app
func Namespace(app string) string { return "tf-" + app }

// TransitKey is the OpenBao Transit key that encrypts an app's state and
// plans
func TransitKey(app string) string { return "tf-" + app }

// RunnerServiceAccount is the ServiceAccount the runner of an environment
// runs as; readOnly is the one for plan-only runs
func RunnerServiceAccount(env string, readOnly bool) string {
	return withReadOnly("tf-runner-"+env, readOnly)
}

// AuthRole is the OpenBao Kubernetes auth role a runner logs in with
// (SMITH_BAO_AUTH_ROLE). The ACL policy of the role has the same name.
func AuthRole(app, env string, readOnly bool) string {
	return withReadOnly("tf-"+app+"-"+env, readOnly)
}

// PolicyName is the OpenBao ACL policy of an AuthRole
func PolicyName(app, env string, readOnly bool) string { return AuthRole(app, env, readOnly) }

// AWSRole is the OpenBao AWS secrets engine role a runner gets its AWS
// credentials from (SMITH_BAO_AWS_ROLE)
func AWSRole(app, env string, readOnly bool) string {
	return withReadOnly(app+"-"+env, readOnly)
}

// IAMRoleName is the IAM role OpenBao assumes for AWSRole by convention,
// created by the platform app's tf_app Terraform module
func IAMRoleName(app, env string, readOnly bool) string {
	return withReadOnly("tf-"+app+"-"+env, readOnly)
}

// IAMRoleARN is the ARN of IAMRoleName in an AWS account
func IAMRoleARN(accountID, app, env string, readOnly bool) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, IAMRoleName(app, env, readOnly))
}

func withReadOnly(name string, readOnly bool) string {
	if readOnly {
		return name + "-readonly"
	}
	return name
}
