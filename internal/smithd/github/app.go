// Package github reports deployments (and, later, Terraform plans) to GitHub
// as check runs on the commit a version was built from. It authenticates as
// a GitHub App: a JWT signed with the app's private key buys a short-lived
// installation token for one repository.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// jwtLifetime is how long an app JWT is valid; GitHub allows at most 10
	// minutes
	jwtLifetime = 9 * time.Minute
	// jwtClockSkew backdates a JWT against clocks that are slightly ahead
	jwtClockSkew = time.Minute
	// tokenRefreshMargin renews installation tokens (valid for an hour)
	// this long before they expire
	tokenRefreshMargin = 5 * time.Minute
)

// errNotInstalled means the app is not installed on a repository
var errNotInstalled = errors.New("the GitHub App is not installed on the repository")

// App is a GitHub App. It is safe for concurrent use.
type App struct {
	id     string
	key    *rsa.PrivateKey
	apiURL string
	client *http.Client
	now    func() time.Time

	mu            sync.Mutex
	installations map[string]int64 // by repository (owner/name)
	tokens        map[string]installationToken
}

type installationToken struct {
	token     string
	expiresAt time.Time
}

// NewApp returns an App for the app id (or client id) and its PEM private
// key, talking to the API at apiURL (https://api.github.com)
func NewApp(id, privateKeyPEM, apiURL string) (*App, error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &App{
		id:            id,
		key:           key,
		apiURL:        strings.TrimRight(apiURL, "/"),
		client:        &http.Client{Timeout: 10 * time.Second},
		now:           time.Now,
		installations: map[string]int64{},
		tokens:        map[string]installationToken{},
	}, nil
}

// parsePrivateKey reads an RSA key in PKCS#1 (what GitHub hands out) or
// PKCS#8 PEM
func parsePrivateKey(data string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(data)))
	if block == nil {
		return nil, errors.New("GitHub App private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub App private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App private key is not an RSA key")
	}
	return key, nil
}

// jwt returns a JWT (RS256) that authenticates as the app itself. Only
// RS256 is needed, so a few lines of standard library replace a JWT library.
func (a *App) jwt() (string, error) {
	now := a.now()
	// GitHub takes the app id as a number, or the client id as a string
	var issuer any = a.id
	if n, err := strconv.ParseInt(a.id, 10, 64); err == nil {
		issuer = n
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-jwtClockSkew).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": issuer,
	})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signed := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign GitHub App JWT: %w", err)
	}
	return signed + "." + enc.EncodeToString(signature), nil
}

// Token returns an installation token for a repository (owner/name),
// limited to that repository. Tokens are cached until shortly before they
// expire.
func (a *App) Token(ctx context.Context, repo string) (string, error) {
	a.mu.Lock()
	cached, ok := a.tokens[repo]
	installation, known := a.installations[repo]
	a.mu.Unlock()
	if ok && a.now().Add(tokenRefreshMargin).Before(cached.expiresAt) {
		return cached.token, nil
	}

	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	if !known {
		if installation, err = a.findInstallation(ctx, jwt, repo); err != nil {
			return "", err
		}
	}

	_, name, _ := strings.Cut(repo, "/")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = a.do(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installation), jwt,
		map[string]any{"repositories": []string{name}}, &resp)
	if err != nil {
		// The app may have been reinstalled under a new installation id
		a.mu.Lock()
		delete(a.installations, repo)
		a.mu.Unlock()
		return "", fmt.Errorf("failed to create installation token for %s: %w", repo, err)
	}

	a.mu.Lock()
	a.installations[repo] = installation
	a.tokens[repo] = installationToken{token: resp.Token, expiresAt: resp.ExpiresAt}
	a.mu.Unlock()
	return resp.Token, nil
}

// findInstallation looks up the app's installation that covers a repository
func (a *App) findInstallation(ctx context.Context, jwt, repo string) (int64, error) {
	var resp struct {
		ID int64 `json:"id"`
	}
	err := a.do(ctx, http.MethodGet, "/repos/"+repo+"/installation", jwt, nil, &resp)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return 0, fmt.Errorf("%w: %s", errNotInstalled, repo)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to find GitHub App installation for %s: %w", repo, err)
	}
	return resp.ID, nil
}

// APIError is a non-2xx response from the GitHub API
type APIError struct {
	Method  string
	Path    string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// do sends a request to the API, authenticated with token (a JWT or an
// installation token), and decodes the JSON response into out if not nil
func (a *App) do(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.apiURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "deploysmith")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var msg struct {
			Message string `json:"message"`
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(data, &msg) != nil || msg.Message == "" {
			msg.Message = strings.TrimSpace(string(data))
		}
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: msg.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode GitHub API response: %w", err)
	}
	return nil
}
