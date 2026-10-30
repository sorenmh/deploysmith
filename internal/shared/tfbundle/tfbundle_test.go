package tfbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	versionsTF = `terraform {
  required_version = "1.10.6"

  backend "s3" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
`
	mainTF   = "resource \"aws_s3_bucket\" \"this\" {\n  bucket = var.bucket\n}\n"
	lockFile = "provider \"registry.opentofu.org/hashicorp/aws\" {\n  version = \"5.80.0\"\n}\n"
)

// validFiles returns the files of a valid bundle
func validFiles() map[string][]byte {
	return map[string][]byte{
		"versions.tf":            []byte(versionsTF),
		"main.tf":                []byte(mainTF),
		LockFile:                 []byte(lockFile),
		"env/production.tfvars":  []byte("bucket = \"prod\"\n"),
		"env/staging.tfvars":     []byte("bucket = \"staging\"\n"),
		"modules/bucket/main.tf": []byte("variable \"name\" {}\n"),
		"outputs.tofu":           []byte("output \"bucket\" {\n  value = aws_s3_bucket.this.bucket\n}\n"),
		"templates/policy.tftpl": []byte("{\"Resource\": \"${arn}\"}\n"),
	}
}

func TestValidateValid(t *testing.T) {
	bundle, err := Validate(validFiles())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.OpenTofuVersion != "1.10.6" {
		t.Errorf("OpenTofuVersion = %q, want 1.10.6", bundle.OpenTofuVersion)
	}
	if !slices.Equal(bundle.Environments, []string{"production", "staging"}) {
		t.Errorf("Environments = %v", bundle.Environments)
	}
	if len(bundle.Files) != len(validFiles()) {
		t.Errorf("%d files, want %d", len(bundle.Files), len(validFiles()))
	}
}

func TestValidateVersionPins(t *testing.T) {
	tests := []struct {
		constraint string
		version    string // "" means rejected
	}{
		{"1.10.6", "1.10.6"},
		{"= 1.10.6", "1.10.6"},
		{"=1.11.0-beta1", "1.11.0-beta1"},
		{"v1.9.0", "1.9.0"},
		{">= 1.10.0", ""},
		{"~> 1.10", ""},
		{"1.10", ""},
		{"1.10.6, < 2.0.0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.constraint, func(t *testing.T) {
			files := validFiles()
			files["versions.tf"] = []byte("terraform {\n  required_version = \"" + tt.constraint + "\"\n}\n")

			bundle, err := Validate(files)
			if tt.version == "" {
				requireProblem(t, err, "is not an exact version")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bundle.OpenTofuVersion != tt.version {
				t.Errorf("OpenTofuVersion = %q, want %q", bundle.OpenTofuVersion, tt.version)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(files map[string][]byte)
		problem string
	}{
		{
			name:    "missing lock file",
			mutate:  func(files map[string][]byte) { delete(files, LockFile) },
			problem: ".terraform.lock.hcl is missing",
		},
		{
			name:    "version not pinned",
			mutate:  func(files map[string][]byte) { files["versions.tf"] = []byte("terraform {\n  backend \"s3\" {}\n}\n") },
			problem: "OpenTofu version is not pinned",
		},
		{
			name: "version pinned in a module only",
			mutate: func(files map[string][]byte) {
				files["versions.tf"] = []byte("terraform {}\n")
				files["modules/bucket/versions.tf"] = []byte("terraform {\n  required_version = \"1.10.6\"\n}\n")
			},
			problem: "OpenTofu version is not pinned",
		},
		{
			name: "version pinned twice",
			mutate: func(files map[string][]byte) {
				files["other.tf"] = []byte("terraform {\n  required_version = \"1.10.6\"\n}\n")
			},
			problem: "required_version is set more than once",
		},
		{
			name: "version not a literal",
			mutate: func(files map[string][]byte) {
				files["versions.tf"] = []byte("terraform {\n  required_version = var.v\n}\n")
			},
			problem: "required_version must be a string",
		},
		{
			name: "backend with settings",
			mutate: func(files map[string][]byte) {
				files["versions.tf"] = []byte("terraform {\n  required_version = \"1.10.6\"\n  backend \"s3\" {\n    bucket = \"mine\"\n  }\n}\n")
			},
			problem: `backend "s3" has settings`,
		},
		{
			name: "backend with a nested block",
			mutate: func(files map[string][]byte) {
				files["versions.tf"] = []byte("terraform {\n  required_version = \"1.10.6\"\n  backend \"s3\" {\n    assume_role {}\n  }\n}\n")
			},
			problem: `backend "s3" has settings`,
		},
		{
			name: "cloud block",
			mutate: func(files map[string][]byte) {
				files["versions.tf"] = []byte("terraform {\n  required_version = \"1.10.6\"\n  cloud {}\n}\n")
			},
			problem: "cloud block is not supported",
		},
		{
			name:    ".terraform directory",
			mutate:  func(files map[string][]byte) { files[".terraform/"] = nil },
			problem: "the .terraform directory must not be in the bundle",
		},
		{
			name:    "file in .terraform",
			mutate:  func(files map[string][]byte) { files[".terraform/providers/x.tf"] = []byte("") },
			problem: "the .terraform directory must not be in the bundle",
		},
		{
			name:    "state file",
			mutate:  func(files map[string][]byte) { files["terraform.tfstate"] = []byte("{}") },
			problem: "state files must not be in the bundle",
		},
		{
			name:    "state backup",
			mutate:  func(files map[string][]byte) { files["terraform.tfstate.backup"] = []byte("{}") },
			problem: "state files must not be in the bundle",
		},
		{
			name:    "auto tfvars",
			mutate:  func(files map[string][]byte) { files["secrets.auto.tfvars"] = []byte("password = \"x\"\n") },
			problem: "*.auto.tfvars files are loaded automatically",
		},
		{
			name:    "terraform.tfvars",
			mutate:  func(files map[string][]byte) { files["terraform.tfvars"] = []byte("x = 1\n") },
			problem: "terraform.tfvars is loaded automatically",
		},
		{
			name:    "override.tf",
			mutate:  func(files map[string][]byte) { files["override.tf"] = []byte("") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "backend_override.tf",
			mutate:  func(files map[string][]byte) { files["backend_override.tf"] = []byte("") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "override in a module",
			mutate:  func(files map[string][]byte) { files["modules/bucket/main_override.tf"] = []byte("") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "override.tf.json",
			mutate:  func(files map[string][]byte) { files["override.tf.json"] = []byte("{}") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "backend_override.tf.json",
			mutate:  func(files map[string][]byte) { files["backend_override.tf.json"] = []byte("{}") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "override.tofu",
			mutate:  func(files map[string][]byte) { files["backend_override.tofu"] = []byte("") },
			problem: "override files must not be in the bundle",
		},
		{
			name:    "JSON configuration",
			mutate:  func(files map[string][]byte) { files["main.tf.json"] = []byte("{}") },
			problem: "main.tf.json: JSON configuration is not supported",
		},
		{
			name:    "JSON configuration in a module",
			mutate:  func(files map[string][]byte) { files["modules/bucket/main.tofu.json"] = []byte("{}") },
			problem: "JSON configuration is not supported",
		},
		{
			name: "backend with settings in a .tofu file",
			mutate: func(files map[string][]byte) {
				files["backend.tofu"] = []byte("terraform {\n  backend \"s3\" {\n    bucket = \"mine\"\n  }\n}\n")
			},
			problem: `backend "s3" has settings`,
		},
		{
			name:    "unexpected file",
			mutate:  func(files map[string][]byte) { files["README.md"] = []byte("# hi") },
			problem: "README.md: unexpected file",
		},
		{
			name:    "tfvars outside env",
			mutate:  func(files map[string][]byte) { files["production.tfvars"] = []byte("x = 1\n") },
			problem: "production.tfvars: unexpected file",
		},
		{
			name:    "path escaping the root",
			mutate:  func(files map[string][]byte) { files["../evil.tf"] = []byte("") },
			problem: "path must be relative",
		},
		{
			name:    "absolute path",
			mutate:  func(files map[string][]byte) { files["/etc/evil.tf"] = []byte("") },
			problem: "path must be relative",
		},
		{
			name:    "invalid HCL",
			mutate:  func(files map[string][]byte) { files["main.tf"] = []byte("resource \"x\" {\n") },
			problem: "main.tf:",
		},
		{
			name: "no root configuration",
			mutate: func(files map[string][]byte) {
				delete(files, "main.tf")
				delete(files, "versions.tf")
				delete(files, "outputs.tofu")
			},
			problem: "no *.tf or *.tofu files in the root directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := validFiles()
			tt.mutate(files)
			_, err := Validate(files)
			requireProblem(t, err, tt.problem)
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	files := validFiles()
	delete(files, LockFile)
	files["terraform.tfstate"] = []byte("{}")

	_, err := Validate(files)
	validationErr, ok := AsValidationError(err)
	if !ok {
		t.Fatalf("error = %v, want a validation error", err)
	}
	if len(validationErr.Problems) != 2 {
		t.Errorf("problems = %q, want 2", validationErr.Problems)
	}
}

func TestCollect(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range validFiles() {
		write(name, string(content))
	}
	// Created by tofu init, or not part of a bundle: left out
	write(".terraform/providers/registry.opentofu.org/hashicorp/aws/main.tf", "")
	write(".terraform/terraform.tfstate", "{}")
	write("README.md", "# infra")
	write("env/notes.txt", "")

	files, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(maps.Keys(validFiles()))
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("collected %v, want %v", got, want)
	}

	t.Run("forbidden files", func(t *testing.T) {
		write("terraform.tfstate", "{}")
		write("prod.auto.tfvars", "x = 1\n")
		write("main.tf.json", "{}")
		if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link.tf")); err != nil {
			t.Fatal(err)
		}
		_, err := Collect(dir)
		validationErr, ok := AsValidationError(err)
		if !ok {
			t.Fatalf("error = %v, want a validation error", err)
		}
		if len(validationErr.Problems) != 4 {
			t.Errorf("problems = %q, want 4", validationErr.Problems)
		}
		requireProblem(t, err, "link.tf: symbolic links must not be in the bundle")
	})
}

func TestPackRead(t *testing.T) {
	archive, err := Pack(validFiles())
	if err != nil {
		t.Fatal(err)
	}

	again, err := Pack(validFiles())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archive, again) {
		t.Error("packing the same files twice gave different archives")
	}

	files, err := Read(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if !maps.EqualFunc(files, validFiles(), bytes.Equal) {
		t.Errorf("read back %v", slices.Sorted(maps.Keys(files)))
	}
	if _, err := Validate(files); err != nil {
		t.Errorf("packed bundle is invalid: %v", err)
	}
}

func TestReadRejectsSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "main.tf", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	closeArchive(t, tarWriter, gzWriter)

	_, err := Read(&buf)
	requireProblem(t, err, "only regular files and directories")
}

func TestReadDirectories(t *testing.T) {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./.terraform/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	closeArchive(t, tarWriter, gzWriter)

	files, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[".terraform/"]; !ok {
		t.Fatalf("directory not returned: %v", files)
	}
}

func closeArchive(t *testing.T, tarWriter *tar.Writer, gzWriter *gzip.Writer) {
	t.Helper()
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireProblem(t *testing.T, err error, problem string) {
	t.Helper()
	validationErr, ok := AsValidationError(err)
	if !ok {
		t.Fatalf("error = %v, want a validation error", err)
	}
	for _, p := range validationErr.Problems {
		if strings.Contains(p, problem) {
			return
		}
	}
	t.Fatalf("problems = %q, want one containing %q", validationErr.Problems, problem)
}
