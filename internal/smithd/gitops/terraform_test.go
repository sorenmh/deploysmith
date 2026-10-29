package gitops

import (
	"strings"
	"testing"
)

func TestWriteTerraformFiles(t *testing.T) {
	remote := newTestRemote(t)
	s := newTestService(t, remote)
	files := map[string][]byte{"namespace.yaml": []byte("kind: Namespace\n")}

	first, err := s.WriteTerraformFiles("viino", "prod", "set up viino", files)
	if err != nil {
		t.Fatal(err)
	}

	if got := readRemote(t, remote, "environments/prod/terraform/viino/namespace.yaml"); got != "kind: Namespace\n" {
		t.Errorf("namespace.yaml = %q", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/kustomization.yaml"); !strings.Contains(got, "- namespace.yaml") {
		t.Errorf("app kustomization does not list namespace.yaml:\n%s", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/kustomization.yaml"); !strings.Contains(got, "- viino") {
		t.Errorf("terraform kustomization does not list the app:\n%s", got)
	}
	// Apps' manifests are elsewhere
	if got := readRemote(t, remote, "environments/prod/apps/kustomization.yaml"); got != "" {
		t.Errorf("apps kustomization written:\n%s", got)
	}

	// Writing the same again commits nothing
	second, err := s.WriteTerraformFiles("viino", "prod", "set up viino", files)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("unchanged write made commit %s after %s", second, first)
	}

	// Files written by others (the Terraform CRs of DEP-18) are kept
	pushFromOtherClone(t, remote, "environments/prod/terraform/viino/prod-main.yaml", "kind: Terraform\n")
	if _, err := s.WriteTerraformFiles("viino", "prod", "update viino", map[string][]byte{"namespace.yaml": []byte("kind: Namespace # v2\n")}); err != nil {
		t.Fatal(err)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/prod-main.yaml"); got != "kind: Terraform\n" {
		t.Errorf("other file = %q", got)
	}
	if got := readRemote(t, remote, "environments/prod/terraform/viino/namespace.yaml"); got != "kind: Namespace # v2\n" {
		t.Errorf("namespace.yaml = %q", got)
	}
}

func TestWriteTerraformFilesRejectsUnsafeNames(t *testing.T) {
	s := newTestService(t, newTestRemote(t))
	for _, tc := range []struct{ app, env, file string }{
		{"../x", "prod", "namespace.yaml"},
		{"viino", "..", "namespace.yaml"},
		{"viino", "prod", "../namespace.yaml"},
	} {
		if _, err := s.WriteTerraformFiles(tc.app, tc.env, "x", map[string][]byte{tc.file: nil}); err == nil {
			t.Errorf("WriteTerraformFiles(%q, %q, %q) succeeded", tc.app, tc.env, tc.file)
		}
	}
}
