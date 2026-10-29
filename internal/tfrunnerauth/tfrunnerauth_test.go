package tfrunnerauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBao serves the two OpenBao endpoints the runner uses; the first
// failLogins logins fail with 503
func fakeBao(t *testing.T, failLogins *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/kubernetes/login", func(w http.ResponseWriter, r *http.Request) {
		if failLogins.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"errors":["temporarily unavailable"]}`))
			return
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if body["role"] != "tf-viino-dev" || body["jwt"] != "sa-jwt" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"auth":{"client_token":"s.runner","lease_duration":7200}}`))
	})
	mux.HandleFunc("GET /v1/aws/creds/{role}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "s.runner" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		if r.PathValue("role") != "viino-dev" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"lease_duration":3600,"data":{"access_key":"ASIAEXAMPLE","secret_key":"secret","session_token":"session","arn":"arn:aws:sts::123456789012:assumed-role/tf-viino-dev/x"}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func readJWT(path string) ([]byte, error) {
	if path != DefaultJWTPath {
		return nil, errors.New("unexpected token path " + path)
	}
	return []byte("sa-jwt\n"), nil
}

func lookup(env []string, name string) string {
	return envMap(env)[name]
}

func TestRunnerEnvLogsIn(t *testing.T) {
	server := fakeBao(t, &atomic.Int32{})
	env := []string{
		"PATH=/usr/bin",
		"BAO_ADDR=" + server.URL,
		"SMITH_BAO_AUTH_ROLE=tf-viino-dev",
		"SMITH_BAO_AWS_ROLE=viino-dev",
		"BAO_TOKEN=stale",
	}

	out, err := RunnerEnv(context.Background(), env, readJWT, KubernetesLogin)
	require.NoError(t, err)

	assert.Equal(t, "s.runner", lookup(out, EnvBaoToken))
	assert.Equal(t, "s.runner", lookup(out, EnvVaultToken))
	assert.Equal(t, server.URL, lookup(out, EnvVaultAddr))
	assert.Equal(t, AWSConfigFile, lookup(out, EnvAWSConfigFile))
	assert.Equal(t, "/usr/bin", lookup(out, "PATH"))

	// The stale token is replaced, not duplicated
	count := 0
	for _, kv := range out {
		if strings.HasPrefix(kv, "BAO_TOKEN=") {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestRunnerEnvKeepsExplicitSettings(t *testing.T) {
	env := []string{
		"VAULT_ADDR=https://openbao:8200",
		"VAULT_CACERT=/etc/ca.pem",
		"SMITH_BAO_AWS_ROLE=viino-dev",
		"AWS_CONFIG_FILE=/custom/config",
	}
	// No SMITH_BAO_AUTH_ROLE: no login
	login := func(context.Context, string, string, string, string, string) (string, error) {
		t.Fatal("login without SMITH_BAO_AUTH_ROLE")
		return "", nil
	}

	out, err := RunnerEnv(context.Background(), env, readJWT, login)
	require.NoError(t, err)
	assert.Equal(t, "https://openbao:8200", lookup(out, EnvBaoAddr))
	assert.Equal(t, "/etc/ca.pem", lookup(out, EnvBaoCACert))
	assert.Equal(t, "/custom/config", lookup(out, EnvAWSConfigFile))
	assert.Empty(t, lookup(out, EnvBaoToken))
}

func TestRunnerEnvUsesMountAndTokenPath(t *testing.T) {
	env := []string{
		"BAO_ADDR=https://openbao:8200",
		"SMITH_BAO_AUTH_ROLE=tf-viino-dev",
		"SMITH_BAO_AUTH_MOUNT=kubernetes-prod",
		"SMITH_BAO_JWT_PATH=/var/run/secrets/openbao/token",
	}
	var gotMount, gotJWT string
	login := func(_ context.Context, _, _, mount, _, jwt string) (string, error) {
		gotMount, gotJWT = mount, jwt
		return "s.token", nil
	}
	readFile := func(path string) ([]byte, error) {
		assert.Equal(t, "/var/run/secrets/openbao/token", path)
		return []byte("projected"), nil
	}

	out, err := RunnerEnv(context.Background(), env, readFile, login)
	require.NoError(t, err)
	assert.Equal(t, "kubernetes-prod", gotMount)
	assert.Equal(t, "projected", gotJWT)
	assert.Equal(t, "s.token", lookup(out, EnvBaoToken))
	// No AWS role: no credential_process profile
	assert.Empty(t, lookup(out, EnvAWSConfigFile))
}

func TestRunnerEnvFailsClosed(t *testing.T) {
	server := fakeBao(t, &atomic.Int32{})

	_, err := RunnerEnv(context.Background(), []string{"BAO_ADDR=" + server.URL, "SMITH_BAO_AUTH_ROLE=tf-other-dev"}, readJWT, KubernetesLogin)
	require.ErrorContains(t, err, "permission denied")

	_, err = RunnerEnv(context.Background(), []string{"SMITH_BAO_AUTH_ROLE=tf-viino-dev"}, readJWT, KubernetesLogin)
	require.ErrorContains(t, err, "BAO_ADDR is not set")

	noToken := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	_, err = RunnerEnv(context.Background(), []string{"BAO_ADDR=" + server.URL, "SMITH_BAO_AUTH_ROLE=tf-viino-dev"}, noToken, KubernetesLogin)
	require.ErrorContains(t, err, "ServiceAccount token")
}

func TestLoginRetriesServerErrors(t *testing.T) {
	failLogins := &atomic.Int32{}
	failLogins.Store(3)
	server := fakeBao(t, failLogins)

	client, err := NewClient(server.URL, "")
	require.NoError(t, err)
	client.Backoff = time.Millisecond
	client.Attempts = 3
	_, err = client.KubernetesLogin(context.Background(), "kubernetes", "tf-viino-dev", "sa-jwt")
	require.ErrorContains(t, err, "503")

	// Succeeds once OpenBao is back
	failLogins.Store(2)
	token, err := client.KubernetesLogin(context.Background(), "kubernetes", "tf-viino-dev", "sa-jwt")
	require.NoError(t, err)
	assert.Equal(t, "s.runner", token)
}

func TestCredentialProcess(t *testing.T) {
	server := fakeBao(t, &atomic.Int32{})
	env := []string{"VAULT_ADDR=" + server.URL, "VAULT_TOKEN=s.runner", "SMITH_BAO_AWS_ROLE=viino-dev"}

	before := time.Now()
	var buf bytes.Buffer
	require.NoError(t, CredentialProcess(context.Background(), env, &buf))

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, float64(1), out["Version"])
	assert.Equal(t, "ASIAEXAMPLE", out["AccessKeyId"])
	assert.Equal(t, "secret", out["SecretAccessKey"])
	assert.Equal(t, "session", out["SessionToken"])
	expiration, err := time.Parse(time.RFC3339, out["Expiration"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, before.Add(time.Hour), expiration, 5*time.Second)
	// Nothing but the JSON document on stdout
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
}

func TestCredentialProcessErrors(t *testing.T) {
	server := fakeBao(t, &atomic.Int32{})

	err := CredentialProcess(context.Background(), []string{"BAO_ADDR=" + server.URL, "BAO_TOKEN=s.runner"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "SMITH_BAO_AWS_ROLE is not set")

	err = CredentialProcess(context.Background(), []string{"BAO_ADDR=" + server.URL, "SMITH_BAO_AWS_ROLE=viino-dev"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "no OpenBao token")

	// Another app's role is denied by the policy, and not retried
	err = CredentialProcess(context.Background(), []string{"BAO_ADDR=" + server.URL, "BAO_TOKEN=s.runner", "SMITH_BAO_AWS_ROLE=other-dev"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "403")
}

func TestAWSCredentialsSecurityToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/aws-prod/creds/viino-prod", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":{"access_key":"A","secret_key":"S","security_token":"T"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/", "")
	require.NoError(t, err)
	creds, err := client.AWSCredentials(context.Background(), "aws-prod", "viino-prod")
	require.NoError(t, err)
	assert.Equal(t, "T", creds.SessionToken)
	assert.True(t, creds.Expiration.IsZero())

	var buf bytes.Buffer
	require.NoError(t, WriteCredentialProcess(&buf, creds))
	assert.NotContains(t, buf.String(), "Expiration")
}

func TestNewClientCACert(t *testing.T) {
	_, err := NewClient("https://openbao:8200", "/does/not/exist.pem")
	require.ErrorContains(t, err, "CA certificate")
}
