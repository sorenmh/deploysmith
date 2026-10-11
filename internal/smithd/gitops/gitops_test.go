package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureKustomizationResource(t *testing.T) {
	tests := []struct {
		name        string
		existing    *string
		resource    string
		wantChanged bool
		want        string
	}{
		{
			name:        "creates file when missing",
			resource:    "hello-world",
			wantChanged: true,
			want:        "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - hello-world\n",
		},
		{
			name:        "appends and keeps comments",
			existing:    ptr("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n# apps managed by deploysmith\nresources:\n  - api-service # the api\n"),
			resource:    "hello-world",
			wantChanged: true,
			want:        "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n# apps managed by deploysmith\nresources:\n  - api-service # the api\n  - hello-world\n",
		},
		{
			name:        "no change when already listed",
			existing:    ptr("resources:\n  - hello-world\n"),
			resource:    "hello-world",
			wantChanged: false,
			want:        "resources:\n  - hello-world\n",
		},
		{
			name:        "no change when listed with ./ prefix",
			existing:    ptr("resources:\n- ./hello-world/\n"),
			resource:    "hello-world",
			wantChanged: false,
			want:        "resources:\n- ./hello-world/\n",
		},
		{
			name:        "adds resources key when absent",
			existing:    ptr("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n"),
			resource:    "hello-world",
			wantChanged: true,
			want:        "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - hello-world\n",
		},
		{
			name:        "fills empty resources key",
			existing:    ptr("kind: Kustomization\nresources:\n"),
			resource:    "hello-world",
			wantChanged: true,
			want:        "kind: Kustomization\nresources:\n  - hello-world\n",
		},
		{
			name:        "converts empty flow list",
			existing:    ptr("kind: Kustomization\nresources: []\n"),
			resource:    "hello-world",
			wantChanged: true,
			want:        "kind: Kustomization\nresources:\n  - hello-world\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "kustomization.yaml")
			if tt.existing != nil {
				if err := os.WriteFile(path, []byte(*tt.existing), 0644); err != nil {
					t.Fatal(err)
				}
			}

			filename, changed, err := ensureKustomizationResource(dir, tt.resource)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if filename != "kustomization.yaml" {
				t.Errorf("filename = %q, want kustomization.yaml", filename)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("content mismatch\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestEnsureKustomizationResourceRejectsNonList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("resources: foo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensureKustomizationResource(dir, "hello-world"); err == nil {
		t.Fatal("expected error for non-list resources")
	}
}

func ptr(s string) *string { return &s }
