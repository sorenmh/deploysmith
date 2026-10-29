package tfrunnerauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Environment variables the Terraform CR's runnerPodTemplate sets (smithd,
// DEP-18), and the ones this package sets for the runner
const (
	EnvBaoAddr   = "BAO_ADDR"
	EnvBaoToken  = "BAO_TOKEN"
	EnvBaoCACert = "BAO_CACERT"
	// The vault provider reads the VAULT_* names, OpenTofu's openbao key
	// provider the BAO_* names
	EnvVaultAddr   = "VAULT_ADDR"
	EnvVaultToken  = "VAULT_TOKEN"
	EnvVaultCACert = "VAULT_CACERT"

	// OpenBao Kubernetes auth role the runner logs in with; no login if empty
	EnvAuthRole = "SMITH_BAO_AUTH_ROLE"
	// Mount of the Kubernetes auth method, default "kubernetes"
	EnvAuthMount = "SMITH_BAO_AUTH_MOUNT"
	// AWS secrets engine role (assumed_role) of the AWS credentials; the
	// read-only <app>-<env>-readonly for plan-only runs. No AWS
	// credentials if empty.
	EnvAWSRole = "SMITH_BAO_AWS_ROLE"
	// Mount of the AWS secrets engine, default "aws"
	EnvAWSMount = "SMITH_BAO_AWS_MOUNT"
	// Path of the ServiceAccount token, default the pod's mounted token
	EnvJWTPath = "SMITH_BAO_JWT_PATH"

	EnvAWSConfigFile = "AWS_CONFIG_FILE"
)

// Defaults of the settings above
const (
	DefaultAuthMount = "kubernetes"
	DefaultAWSMount  = "aws"
	DefaultJWTPath   = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	// AWSConfigFile is the AWS config baked into the runner image; its
	// default profile runs `tf-runner-auth aws-credentials`
	AWSConfigFile = "/etc/deploysmith/aws-config"
)

// Login is the OpenBao login the runner environment needs
type Login func(ctx context.Context, addr, caCert, mount, role, jwt string) (string, error)

// KubernetesLogin logs in to OpenBao over HTTP
func KubernetesLogin(ctx context.Context, addr, caCert, mount, role, jwt string) (string, error) {
	client, err := NewClient(addr, caCert)
	if err != nil {
		return "", err
	}
	return client.KubernetesLogin(ctx, mount, role, jwt)
}

// RunnerEnv returns the environment to start the runner with: env plus an
// OpenBao token in BAO_TOKEN and VAULT_TOKEN if SMITH_BAO_AUTH_ROLE is set,
// the address and CA certificate under both names, and AWS_CONFIG_FILE
// pointing at the credential_process profile if SMITH_BAO_AWS_ROLE is set.
// tf-runner passes its whole environment to tofu, so the S3 backend, the
// providers and the openbao key provider all see them.
func RunnerEnv(ctx context.Context, env []string, readFile func(string) ([]byte, error), login Login) ([]string, error) {
	vars := envMap(env)
	set := func(name, value string) {
		vars[name] = value
		env = setEnv(env, name, value)
	}

	addr := first(vars[EnvBaoAddr], vars[EnvVaultAddr])
	caCert := first(vars[EnvBaoCACert], vars[EnvVaultCACert])
	for _, pair := range [][2]string{{EnvBaoAddr, addr}, {EnvVaultAddr, addr}, {EnvBaoCACert, caCert}, {EnvVaultCACert, caCert}} {
		if pair[1] != "" && vars[pair[0]] == "" {
			set(pair[0], pair[1])
		}
	}

	if role := vars[EnvAuthRole]; role != "" {
		jwtPath := first(vars[EnvJWTPath], DefaultJWTPath)
		jwt, err := readFile(jwtPath)
		if err != nil {
			return nil, fmt.Errorf("reading the ServiceAccount token: %w", err)
		}
		token, err := login(ctx, addr, caCert, first(vars[EnvAuthMount], DefaultAuthMount), role, strings.TrimSpace(string(jwt)))
		if err != nil {
			return nil, err
		}
		set(EnvBaoToken, token)
		set(EnvVaultToken, token)
	}

	if vars[EnvAWSRole] != "" && vars[EnvAWSConfigFile] == "" {
		set(EnvAWSConfigFile, AWSConfigFile)
	}
	return env, nil
}

// credentialProcessOutput is what an AWS credential_process prints
// (https://docs.aws.amazon.com/sdkref/latest/guide/feature-process-credentials.html)
type credentialProcessOutput struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken,omitempty"`
	Expiration      string `json:"Expiration,omitempty"`
}

// WriteCredentialProcess prints creds in the credential_process format
func WriteCredentialProcess(w io.Writer, creds AWSCredentials) error {
	out := credentialProcessOutput{
		Version:         1,
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}
	if !creds.Expiration.IsZero() {
		out.Expiration = creds.Expiration.UTC().Format(time.RFC3339)
	}
	return json.NewEncoder(w).Encode(out)
}

// CredentialProcess reads the AWS credentials of SMITH_BAO_AWS_ROLE with
// the token RunnerEnv put in the environment and prints them for the AWS
// SDK. Run by the SDK, so it must print nothing else on stdout.
func CredentialProcess(ctx context.Context, env []string, w io.Writer) error {
	vars := envMap(env)
	role := vars[EnvAWSRole]
	if role == "" {
		return errors.New(EnvAWSRole + " is not set")
	}
	token := first(vars[EnvBaoToken], vars[EnvVaultToken])
	if token == "" {
		return errors.New("no OpenBao token: " + EnvAuthRole + " must be set so the runner logs in")
	}
	client, err := NewClient(first(vars[EnvBaoAddr], vars[EnvVaultAddr]), first(vars[EnvBaoCACert], vars[EnvVaultCACert]))
	if err != nil {
		return err
	}
	client.Token = token
	creds, err := client.AWSCredentials(ctx, first(vars[EnvAWSMount], DefaultAWSMount), role)
	if err != nil {
		return err
	}
	return WriteCredentialProcess(w, creds)
}

// envMap indexes an os.Environ-style list; later entries win, as in exec
func envMap(env []string) map[string]string {
	vars := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			vars[name] = value
		}
	}
	return vars
}

// setEnv replaces every entry for name in env, or appends one
func setEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return append(out, name+"="+value)
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
