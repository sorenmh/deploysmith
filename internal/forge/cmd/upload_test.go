package cmd

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
)

// writeFiles writes files, by slash-separated path, below dir
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// uploadTarget is a stand-in for the presigned S3 URL that records uploads
func uploadTarget(t *testing.T) (*httptest.Server, *[][]byte) {
	t.Helper()
	var uploads [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		uploads = append(uploads, body)
	}))
	t.Cleanup(server.Close)
	return server, &uploads
}

func TestUploadTerraform(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, "infra", map[string]string{
		"versions.tf":          "terraform {\n  required_version = \"1.10.6\"\n  backend \"s3\" {}\n}\n",
		"main.tf":              "resource \"null_resource\" \"this\" {}\n",
		tfbundle.LockFile:      "provider \"registry.opentofu.org/hashicorp/null\" {\n  version = \"3.2.3\"\n}\n",
		"env/staging.tfvars":   "name = \"staging\"\n",
		".terraform/x/main.tf": "",
		"README.md":            "# infra",
	})

	server, uploads := uploadTarget(t)
	uploadURLOverride = server.URL
	t.Cleanup(func() { uploadURLOverride = "" })

	if err := runUploadTerraform("infra"); err != nil {
		t.Fatal(err)
	}
	if len(*uploads) != 1 {
		t.Fatalf("%d uploads, want 1", len(*uploads))
	}

	files, err := tfbundle.Read(bytes.NewReader((*uploads)[0]))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{tfbundle.LockFile, "env/staging.tfvars", "main.tf", "versions.tf"}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("uploaded %v, want %v", got, want)
	}

	t.Run("invalid bundle is not uploaded", func(t *testing.T) {
		writeFiles(t, "infra", map[string]string{"terraform.tfstate": "{}"})
		err := runUploadTerraform("infra")
		if err == nil || !strings.Contains(err.Error(), "state files must not be in the bundle") {
			t.Errorf("error = %v", err)
		}
		if len(*uploads) != 1 {
			t.Errorf("invalid bundle was uploaded")
		}
	})
}

func TestUploadChecksArtifactType(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFiles(t, ".forge", map[string]string{
		"upload-url":   "http://example.invalid/upload",
		"version-info": `{"app":"infra","appId":"1","version":"abc123"}`,
	})

	err := runUploadTerraform("infra")
	if err == nil || !strings.Contains(err.Error(), "forge init --artifact-type terraform") {
		t.Errorf("terraform upload to a manifests draft: error = %v", err)
	}

	writeFiles(t, ".forge", map[string]string{
		"version-info": `{"app":"infra","appId":"1","version":"abc123","artifactType":"terraform"}`,
	})
	err = runUpload(nil, []string{"manifests"})
	if err == nil || !strings.Contains(err.Error(), "forge upload --terraform") {
		t.Errorf("manifests upload to a terraform draft: error = %v", err)
	}
}
