package terraform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/openbao"
)

// TestProvisionAgainstOpenBao provisions two apps in a real OpenBao and
// checks that each runner policy reaches only its own app. It needs
// OPENBAO_TEST_ADDR, OPENBAO_TEST_TOKEN (a token with smithd's policy from
// the chart README) and OPENBAO_TEST_ROOT_TOKEN, with KV v2 at secret/,
// Transit at transit/, the AWS secrets engine at aws/ and Kubernetes auth
// at kubernetes/, e.g. `bao server -dev`.
func TestProvisionAgainstOpenBao(t *testing.T) {
	addr, token, root := os.Getenv("OPENBAO_TEST_ADDR"), os.Getenv("OPENBAO_TEST_TOKEN"), os.Getenv("OPENBAO_TEST_ROOT_TOKEN")
	if addr == "" || token == "" || root == "" {
		t.Skip("OPENBAO_TEST_ADDR, OPENBAO_TEST_TOKEN and OPENBAO_TEST_ROOT_TOKEN not set")
	}
	ctx := context.Background()

	client, err := openbao.New(openbao.Config{Addr: addr, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	p := &Provisioner{Bao: client, Opts: testOpenBaoOptions}
	for _, app := range []string{"appa", "appb"} {
		// Twice: provisioning again must succeed and change nothing
		for i := 0; i < 2; i++ {
			if err := p.Provision(ctx, app, "prod", ""); err != nil {
				t.Fatalf("provision %s: %v", app, err)
			}
		}
	}

	rootClient := &rawClient{addr: addr, token: root}
	for _, app := range []string{"appa", "appb"} {
		rootClient.must(t, http.MethodPost, "secret/data/apps/"+app+"/db", `{"data":{"password":"`+app+`"}}`)
	}
	role := rootClient.must(t, http.MethodGet, "auth/kubernetes/role/tf-appa-prod", "")
	if !strings.Contains(role, `"bound_service_account_namespaces":["tf-appa"]`) || !strings.Contains(role, `"token_ttl":7200`) {
		t.Errorf("unexpected role: %s", role)
	}

	// A token of appa's apply runner
	var created struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal([]byte(rootClient.must(t, http.MethodPost, "auth/token/create", `{"policies":["tf-appa-prod"],"no_default_policy":true,"ttl":"10m"}`)), &created); err != nil {
		t.Fatal(err)
	}
	runner := &rawClient{addr: addr, token: created.Auth.ClientToken}

	allowed := []struct{ method, path, body string }{
		{http.MethodGet, "secret/data/apps/appa/db", ""},
		{http.MethodPost, "transit/datakey/plaintext/tf-appa", `{"bits":256}`},
		{http.MethodPost, "transit/encrypt/tf-appa", `{"plaintext":"` + base64.StdEncoding.EncodeToString([]byte("x")) + `"}`},
	}
	for _, req := range allowed {
		if _, err := runner.do(req.method, req.path, req.body); err != nil {
			t.Errorf("appa runner %s %s: %v", req.method, req.path, err)
		}
	}
	denied := []struct{ method, path, body string }{
		{http.MethodGet, "secret/data/apps/appb/db", ""},
		{http.MethodPost, "transit/datakey/plaintext/tf-appb", `{"bits":256}`},
		{http.MethodPost, "transit/decrypt/tf-appb", `{"ciphertext":"vault:v1:x"}`},
		{http.MethodGet, "aws/creds/appb-prod", ""},
		{http.MethodGet, "transit/keys/tf-appa", ""},
		{http.MethodDelete, "transit/keys/tf-appa", ""},
	}
	for _, req := range denied {
		if _, err := runner.do(req.method, req.path, req.body); !isForbidden(err) {
			t.Errorf("appa runner %s %s: err = %v, want 403", req.method, req.path, err)
		}
	}

	// smithd may create keys but not configure, trim or delete them
	smithd := &rawClient{addr: addr, token: token}
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "transit/keys/tf-appa/config", `{"deletion_allowed":true}`},
		{http.MethodPost, "transit/keys/tf-appa/trim", `{"min_available_version":1}`},
		{http.MethodPost, "transit/keys/tf-appa/rotate", ""},
		{http.MethodDelete, "transit/keys/tf-appa", ""},
		{http.MethodPost, "sys/policies/acl/admin", `{"policy":"path \"*\" {capabilities=[\"sudo\"]}"}`},
	} {
		if _, err := smithd.do(req.method, req.path, req.body); !isForbidden(err) {
			t.Errorf("smithd %s %s: err = %v, want 403", req.method, req.path, err)
		}
	}
}

type rawClient struct{ addr, token string }

func (c *rawClient) do(method, path, body string) (string, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.addr, "/")+"/v1/"+path, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return string(data), &openbao.APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Errors: []string{string(data)}}
	}
	return string(data), nil
}

func (c *rawClient) must(t *testing.T, method, path, body string) string {
	t.Helper()
	out, err := c.do(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func isForbidden(err error) bool {
	var apiErr *openbao.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden
}
