package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeServer is a minimal OpenBao: Kubernetes auth login, ACL policies,
// auth roles, AWS roles and Transit keys, all kept in memory by path
type fakeServer struct {
	mu       sync.Mutex
	data     map[string]map[string]any // path → last written body
	logins   int
	requests []string // "METHOD path token"
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	f := &fakeServer{data: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	token := r.Header.Get("X-Vault-Token")
	f.requests = append(f.requests, r.Method+" "+path+" "+token)

	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) // empty for GET
	}

	if path == "auth/kubernetes/login" {
		if body["role"] != "smithd" || body["jwt"] != "sa-jwt" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":["invalid role or jwt"]}`))
			return
		}
		f.logins++
		_, _ = w.Write([]byte(`{"auth":{"client_token":"login-token","lease_duration":3600}}`))
		return
	}

	if token != "static-token" && token != "login-token" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
		return
	}

	switch r.Method {
	case http.MethodGet:
		if _, ok := f.data[path]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": f.data[path]})
	case http.MethodPut, http.MethodPost:
		f.data[path] = body
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestStaticToken(t *testing.T) {
	f, srv := newFakeServer(t)
	c, err := New(Config{Addr: srv.URL + "/", Token: "static-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := c.WritePolicy(ctx, "tf-viino-prod", `path "x" {}`); err != nil {
		t.Fatal(err)
	}
	if got := f.data["sys/policies/acl/tf-viino-prod"]["policy"]; got != `path "x" {}` {
		t.Errorf("policy = %v", got)
	}

	role := KubernetesRole{
		BoundServiceAccountNames:      []string{"tf-runner-prod"},
		BoundServiceAccountNamespaces: []string{"tf-viino"},
		TokenPolicies:                 []string{"tf-viino-prod"},
		TokenTTL:                      "7200s",
		TokenMaxTTL:                   "7200s",
	}
	if err := c.WriteKubernetesRole(ctx, "kubernetes", "tf-viino-prod", role); err != nil {
		t.Fatal(err)
	}
	written := f.data["auth/kubernetes/role/tf-viino-prod"]
	if written["token_ttl"] != "7200s" || written["bound_service_account_namespaces"].([]any)[0] != "tf-viino" {
		t.Errorf("role = %v", written)
	}
	if _, ok := written["audience"]; ok {
		t.Errorf("empty audience sent: %v", written)
	}

	awsRole := AWSRole{CredentialType: "assumed_role", RoleARNs: []string{"arn:aws:iam::1:role/tf-viino-prod"}, DefaultSTSTTL: "3600s", MaxSTSTTL: "3600s"}
	if err := c.WriteAWSRole(ctx, "aws-prod", "viino-prod", awsRole); err != nil {
		t.Fatal(err)
	}
	if got := f.data["aws-prod/roles/viino-prod"]["credential_type"]; got != "assumed_role" {
		t.Errorf("aws role = %v", f.data["aws-prod/roles/viino-prod"])
	}
}

func TestEnsureTransitKey(t *testing.T) {
	f, srv := newFakeServer(t)
	c, err := New(Config{Addr: srv.URL, Token: "static-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	created, err := c.EnsureTransitKey(ctx, "transit", "tf-viino")
	if err != nil || !created {
		t.Fatalf("first EnsureTransitKey = %v, %v; want created", created, err)
	}
	if got := f.data["transit/keys/tf-viino"]["type"]; got != "aes256-gcm96" {
		t.Errorf("key type = %v", got)
	}

	// An existing key is not written again
	f.data["transit/keys/tf-viino"] = map[string]any{"type": "chacha20-poly1305"}
	created, err = c.EnsureTransitKey(ctx, "transit", "tf-viino")
	if err != nil || created {
		t.Fatalf("second EnsureTransitKey = %v, %v; want existing", created, err)
	}
	if got := f.data["transit/keys/tf-viino"]["type"]; got != "chacha20-poly1305" {
		t.Errorf("existing key was overwritten: %v", got)
	}
}

func TestKubernetesLogin(t *testing.T) {
	f, srv := newFakeServer(t)
	jwtPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwtPath, []byte("sa-jwt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{Addr: srv.URL, AuthMount: "kubernetes", AuthRole: "smithd", JWTPath: jwtPath})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := c.WritePolicy(context.Background(), "p", "x"); err != nil {
			t.Fatal(err)
		}
	}
	// The token is reused until it nears expiry
	if f.logins != 1 {
		t.Errorf("logins = %d, want 1", f.logins)
	}
	if last := f.requests[len(f.requests)-1]; last != "PUT sys/policies/acl/p login-token" {
		t.Errorf("last request = %q", last)
	}
}

func TestErrors(t *testing.T) {
	_, srv := newFakeServer(t)

	c, err := New(Config{Addr: srv.URL, Token: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.WritePolicy(context.Background(), "p", "x")
	if err == nil || err.Error() != "openbao PUT sys/policies/acl/p: 403 permission denied" {
		t.Errorf("err = %v", err)
	}

	jwtPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwtPath, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = New(Config{Addr: srv.URL, AuthMount: "kubernetes", AuthRole: "smithd", JWTPath: jwtPath})
	if err != nil {
		t.Fatal(err)
	}
	err = c.WritePolicy(context.Background(), "p", "x")
	if err == nil || !strings.Contains(err.Error(), "openbao login failed") || !strings.Contains(err.Error(), "invalid role or jwt") {
		t.Errorf("err = %v", err)
	}

	if _, err := New(Config{Addr: srv.URL}); err == nil {
		t.Error("New without token or Kubernetes auth succeeded")
	}
}
