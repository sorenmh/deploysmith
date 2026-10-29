// Package tfrunnerauth gives a tofu-controller runner pod its credentials
// before the runner starts: an OpenBao token from Kubernetes auth with the
// pod's ServiceAccount, and AWS credentials from the OpenBao AWS secrets
// engine through credential_process. See docs/terraform-cluster-setup.md.
package tfrunnerauth

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to the OpenBao HTTP API, only as far as the runner needs it
type Client struct {
	Addr  string
	Token string
	HTTP  *http.Client
	// Attempts per request; network errors and 5xx responses are retried,
	// since OpenBao can be briefly unavailable while the pod starts
	Attempts int
	// Backoff before the second attempt, doubled for each further one
	Backoff time.Duration
}

// NewClient returns a client for addr, trusting the CA certificate in the
// PEM file caCert if set, otherwise the system roots
func NewClient(addr, caCert string) (*Client, error) {
	if addr == "" {
		return nil, errors.New("BAO_ADDR is not set")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caCert != "" {
		pem, err := os.ReadFile(caCert)
		if err != nil {
			return nil, fmt.Errorf("reading OpenBao CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificate found in %s", caCert)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Client{
		Addr:     strings.TrimSuffix(addr, "/"),
		HTTP:     &http.Client{Transport: transport, Timeout: 30 * time.Second},
		Attempts: 4,
		Backoff:  time.Second,
	}, nil
}

// response is the envelope of every OpenBao API response
type response struct {
	Errors        []string        `json:"errors"`
	Data          json.RawMessage `json:"data"`
	LeaseDuration int             `json:"lease_duration"`
	Auth          *struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int    `json:"lease_duration"`
	} `json:"auth"`
}

// KubernetesLogin logs in at auth/<mount>/login with a ServiceAccount
// token and returns the OpenBao token
func (c *Client) KubernetesLogin(ctx context.Context, mount, role, jwt string) (string, error) {
	body, err := json.Marshal(map[string]string{"role": role, "jwt": jwt})
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, http.MethodPost, "auth/"+mount+"/login", body)
	if err != nil {
		return "", fmt.Errorf("OpenBao Kubernetes login with role %s: %w", role, err)
	}
	if resp.Auth == nil || resp.Auth.ClientToken == "" {
		return "", fmt.Errorf("OpenBao Kubernetes login with role %s: no token in the response", role)
	}
	return resp.Auth.ClientToken, nil
}

// AWSCredentials are temporary credentials from the AWS secrets engine
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	// Expiration is when the credentials stop working; zero if OpenBao
	// returned no lease duration
	Expiration time.Time
}

// AWSCredentials reads <mount>/creds/<role>. With credential_type
// assumed_role (DEP-17) these are STS credentials that last the role's
// default_sts_ttl.
func (c *Client) AWSCredentials(ctx context.Context, mount, role string) (AWSCredentials, error) {
	requested := time.Now()
	resp, err := c.do(ctx, http.MethodGet, mount+"/creds/"+url.PathEscape(role), nil)
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("reading AWS credentials for role %s: %w", role, err)
	}
	var data struct {
		AccessKey     string `json:"access_key"`
		SecretKey     string `json:"secret_key"`
		SessionToken  string `json:"session_token"`
		SecurityToken string `json:"security_token"` // older name of session_token
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return AWSCredentials{}, fmt.Errorf("reading AWS credentials for role %s: %w", role, err)
	}
	if data.AccessKey == "" || data.SecretKey == "" {
		return AWSCredentials{}, fmt.Errorf("reading AWS credentials for role %s: no access key in the response", role)
	}
	creds := AWSCredentials{
		AccessKeyID:     data.AccessKey,
		SecretAccessKey: data.SecretKey,
		SessionToken:    data.SessionToken,
	}
	if creds.SessionToken == "" {
		creds.SessionToken = data.SecurityToken
	}
	// The lease lasts as long as the STS credentials. Counted from before
	// the request, so the SDK refreshes a bit early rather than late.
	if resp.LeaseDuration > 0 {
		creds.Expiration = requested.Add(time.Duration(resp.LeaseDuration) * time.Second).UTC()
	}
	return creds, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*response, error) {
	attempts := max(c.Attempts, 1)
	backoff := c.Backoff
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, errors.Join(lastErr, ctx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		resp, retry, err := c.once(ctx, method, path, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

// once makes one request and reports whether a failure is worth retrying
func (c *Client) once(ctx context.Context, method, path string, body []byte) (*response, bool, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Addr+"/v1/"+path, reader)
	if err != nil {
		return nil, false, err
	}
	if c.Token != "" {
		req.Header.Set("X-Vault-Token", c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, true, err
	}

	var parsed response
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil && res.StatusCode < 300 {
			return nil, false, fmt.Errorf("invalid response from OpenBao: %w", err)
		}
	}
	if res.StatusCode >= 300 {
		msg := strings.Join(parsed.Errors, "; ")
		if msg == "" {
			msg = http.StatusText(res.StatusCode)
		}
		return nil, res.StatusCode >= 500, fmt.Errorf("OpenBao returned %d: %s", res.StatusCode, msg)
	}
	return &parsed, false, nil
}
