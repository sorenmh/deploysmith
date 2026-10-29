// Package openbao is a small client for the parts of the OpenBao HTTP API
// (Vault-compatible) that smithd uses to set up Terraform apps: ACL
// policies, Kubernetes auth roles, Transit keys and AWS secrets engine
// roles. Every write is an idempotent upsert; nothing is ever deleted.
package openbao

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
	"os"
	"strings"
	"sync"
	"time"
)

// Config configures a Client. Token, if set, is used as is (for
// development); otherwise the client logs in with Kubernetes auth using the
// pod's ServiceAccount token.
type Config struct {
	Addr   string
	Token  string
	CACert string // path to a PEM file; empty uses the system roots

	AuthMount string // Kubernetes auth mount smithd logs in with, e.g. "kubernetes"
	AuthRole  string // smithd's own role in that mount
	JWTPath   string // ServiceAccount token file
}

// Client talks to the OpenBao HTTP API
type Client struct {
	cfg  Config
	http *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time // zero for a static token
}

// APIError is an error response from OpenBao
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Errors     []string
}

func (e *APIError) Error() string {
	msg := strings.Join(e.Errors, "; ")
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("openbao %s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from OpenBao
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// New creates a Client
func New(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("openbao address is required")
	}
	if cfg.Token == "" && (cfg.AuthMount == "" || cfg.AuthRole == "" || cfg.JWTPath == "") {
		return nil, fmt.Errorf("openbao needs a token or a Kubernetes auth mount, role and token path")
	}
	cfg.Addr = strings.TrimRight(cfg.Addr, "/")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read openbao CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", cfg.CACert)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}

	return &Client{
		cfg:   cfg,
		http:  &http.Client{Timeout: 30 * time.Second, Transport: transport},
		token: cfg.Token,
	}, nil
}

// WritePolicy creates or replaces an ACL policy
func (c *Client) WritePolicy(ctx context.Context, name, policy string) error {
	return c.do(ctx, http.MethodPut, "sys/policies/acl/"+name, map[string]string{"policy": policy}, nil)
}

// KubernetesRole is a role of the Kubernetes auth method
type KubernetesRole struct {
	BoundServiceAccountNames      []string `json:"bound_service_account_names"`
	BoundServiceAccountNamespaces []string `json:"bound_service_account_namespaces"`
	Audience                      string   `json:"audience,omitempty"`
	TokenPolicies                 []string `json:"token_policies"`
	TokenTTL                      string   `json:"token_ttl"`
	TokenMaxTTL                   string   `json:"token_max_ttl"`
}

// WriteKubernetesRole creates or replaces a role of the Kubernetes auth
// method mounted at mount
func (c *Client) WriteKubernetesRole(ctx context.Context, mount, name string, role KubernetesRole) error {
	return c.do(ctx, http.MethodPost, "auth/"+mount+"/role/"+name, role, nil)
}

// AWSRole is a role of the AWS secrets engine
type AWSRole struct {
	CredentialType string   `json:"credential_type"` // "assumed_role"
	RoleARNs       []string `json:"role_arns"`
	DefaultSTSTTL  string   `json:"default_sts_ttl"`
	MaxSTSTTL      string   `json:"max_sts_ttl"`
}

// WriteAWSRole creates or replaces a role of the AWS secrets engine mounted
// at mount
func (c *Client) WriteAWSRole(ctx context.Context, mount, name string, role AWSRole) error {
	return c.do(ctx, http.MethodPost, mount+"/roles/"+name, role, nil)
}

// EnsureTransitKey creates the Transit key name (aes256-gcm96) unless it
// exists, and reports whether it was created. An existing key is left as
// it is.
func (c *Client) EnsureTransitKey(ctx context.Context, mount, name string) (bool, error) {
	err := c.do(ctx, http.MethodGet, mount+"/keys/"+name, nil, nil)
	if err == nil {
		return false, nil
	}
	if !IsNotFound(err) {
		return false, err
	}
	if err := c.do(ctx, http.MethodPost, mount+"/keys/"+name, map[string]string{"type": "aes256-gcm96"}, nil); err != nil {
		return false, err
	}
	return true, nil
}

// do sends a request with the client token and decodes the response's JSON
// into out, if given
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}
	return c.send(ctx, method, path, token, body, out)
}

func (c *Client) send(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode openbao request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.cfg.Addr+"/v1/"+path, reader)
	if err != nil {
		return fmt.Errorf("failed to create openbao request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openbao %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("openbao %s %s: failed to read response: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		apiErr := &APIError{Method: method, Path: path, StatusCode: resp.StatusCode}
		var errResp struct {
			Errors []string `json:"errors"`
		}
		if json.Unmarshal(data, &errResp) == nil {
			apiErr.Errors = errResp.Errors
		}
		return apiErr
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("openbao %s %s: failed to decode response: %w", method, path, err)
		}
	}
	return nil
}

// getToken returns the static token, or a token from a Kubernetes auth
// login that is renewed by logging in again shortly before it expires
func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg.Token != "" {
		return c.cfg.Token, nil
	}
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	jwt, err := os.ReadFile(c.cfg.JWTPath)
	if err != nil {
		return "", fmt.Errorf("failed to read ServiceAccount token for openbao login: %w", err)
	}
	var resp struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	login := map[string]string{"role": c.cfg.AuthRole, "jwt": strings.TrimSpace(string(jwt))}
	if err := c.send(ctx, http.MethodPost, "auth/"+c.cfg.AuthMount+"/login", "", login, &resp); err != nil {
		return "", fmt.Errorf("openbao login failed: %w", err)
	}
	if resp.Auth.ClientToken == "" {
		return "", fmt.Errorf("openbao login returned no token")
	}

	// Log in again at 80% of the lease, so a token never expires mid-run
	lease := time.Duration(resp.Auth.LeaseDuration) * time.Second
	c.token = resp.Auth.ClientToken
	c.expires = time.Now().Add(lease * 4 / 5)
	return c.token, nil
}
