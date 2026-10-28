// Package tfbundle packs and validates OpenTofu bundles, the artifact of
// versions of type "terraform". forge checks a bundle before uploading it
// and smithd checks it again on publish, so both share this code.
//
// A bundle is one OpenTofu root module:
//
//	*.tf                  the configuration, local modules in subdirectories
//	.terraform.lock.hcl   the provider lock file (required)
//	env/<env>.tfvars      non-secret variables per environment (optional)
//
// The OpenTofu version is pinned with an exact required_version in the
// root module's terraform block, which OpenTofu itself enforces. smithd sets
// the backend, so a bundle may only declare an empty backend block.
package tfbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const (
	// ArtifactType is the version artifact type of a bundle
	ArtifactType = "terraform"
	// ArchiveName is the name of the uploaded bundle archive
	ArchiveName = "terraform.tar.gz"
	// LockFile is the provider lock file every bundle must have
	LockFile = ".terraform.lock.hcl"
	// EnvDir holds the per-environment tfvars files
	EnvDir = "env"

	// maxSize limits the uncompressed size of a bundle read from an archive
	maxSize = 32 << 20
)

// Bundle is a validated bundle
type Bundle struct {
	// Files maps slash-separated paths relative to the root to contents
	Files map[string][]byte
	// OpenTofuVersion is the version pinned by required_version
	OpenTofuVersion string
	// Environments are the environments with an env/<env>.tfvars file
	Environments []string
}

// ValidationError lists everything wrong with a bundle
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid terraform bundle:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// AsValidationError returns the *ValidationError in err's chain, if any
func AsValidationError(err error) (*ValidationError, bool) {
	var e *ValidationError
	ok := errors.As(err, &e)
	return e, ok
}

// Collect reads the files of the bundle rooted at dir. It skips files that
// are not part of a bundle, and hidden directories such as the .terraform
// directory that tofu init creates, but fails on files that must not be
// there at all, such as state files.
func Collect(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	var problems []string

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if problem := forbidden(rel); problem != "" {
			problems = append(problems, problem)
			return nil
		}
		if !included(rel) {
			return nil
		}

		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", dir, err)
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return files, nil
}

// Validate checks a bundle's files. Paths are slash-separated and relative
// to the root; paths ending in "/" are directories (from Read). All
// problems found are returned together in a *ValidationError.
func Validate(files map[string][]byte) (*Bundle, error) {
	bundle := &Bundle{Files: map[string][]byte{}}
	var problems []string
	var pins []pin
	rootConfigs := 0

	for _, name := range sortedNames(files) {
		content := files[name]

		if !safePath(name) {
			problems = append(problems, fmt.Sprintf("%s: path must be relative and must not contain \"..\"", name))
			continue
		}
		if problem := forbidden(name); problem != "" {
			problems = append(problems, problem)
			continue
		}
		if strings.HasSuffix(name, "/") {
			// Other directories carry no content
			continue
		}
		if !included(name) {
			problems = append(problems, fmt.Sprintf("%s: unexpected file; a bundle holds only *.tf files, %s and %s/<env>.tfvars", name, LockFile, EnvDir))
			continue
		}

		file, diags := hclsyntax.ParseConfig(content, name, hcl.InitialPos)
		if diags.HasErrors() {
			problems = append(problems, diags.Error())
			continue
		}
		bundle.Files[name] = content

		switch {
		case strings.HasSuffix(name, ".tfvars"):
			bundle.Environments = append(bundle.Environments, strings.TrimSuffix(path.Base(name), ".tfvars"))
		case strings.HasSuffix(name, ".tf") && !strings.Contains(name, "/"):
			// Only the root module's settings apply
			rootConfigs++
			filePins, fileProblems := checkTerraformBlocks(file.Body.(*hclsyntax.Body))
			pins = append(pins, filePins...)
			problems = append(problems, fileProblems...)
		}
	}

	if _, ok := files[LockFile]; !ok {
		problems = append(problems, fmt.Sprintf("%s is missing: run tofu init and commit the lock file", LockFile))
	}
	if rootConfigs == 0 {
		problems = append(problems, "no *.tf files in the root directory")
	}

	switch {
	case len(pins) == 0:
		problems = append(problems, `OpenTofu version is not pinned: set an exact required_version (e.g. required_version = "1.10.6") in the terraform block`)
	case len(pins) > 1:
		var where []string
		for _, p := range pins {
			where = append(where, p.where)
		}
		problems = append(problems, "required_version is set more than once ("+strings.Join(where, ", ")+"); set it in one place")
	default:
		bundle.OpenTofuVersion = pins[0].version
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return bundle, nil
}

// pin is an exact required_version found in a file
type pin struct {
	version string
	where   string
}

// exactVersion matches a single exact version constraint, e.g. "1.10.6" or
// "= 1.11.0-beta1"
var exactVersion = regexp.MustCompile(`^=?\s*v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$`)

// checkTerraformBlocks checks the terraform blocks of a root module file:
// required_version must be exact, and a backend must be empty and is the
// only way to configure state
func checkTerraformBlocks(body *hclsyntax.Body) ([]pin, []string) {
	var pins []pin
	var problems []string

	for _, block := range body.Blocks {
		if block.Type != "terraform" {
			continue
		}

		if attr, ok := block.Body.Attributes["required_version"]; ok {
			where := attr.SrcRange.String()
			value, diags := attr.Expr.Value(nil)
			if diags.HasErrors() || value.Type() != cty.String || value.IsNull() {
				problems = append(problems, where+": required_version must be a string")
			} else if m := exactVersion.FindStringSubmatch(strings.TrimSpace(value.AsString())); m == nil {
				problems = append(problems, fmt.Sprintf("%s: required_version %q is not an exact version; pin one version, e.g. \"1.10.6\"", where, value.AsString()))
			} else {
				pins = append(pins, pin{version: m[1], where: where})
			}
		}

		for _, inner := range block.Body.Blocks {
			where := inner.DefRange().String()
			switch inner.Type {
			case "backend":
				if len(inner.Body.Attributes) > 0 || len(inner.Body.Blocks) > 0 {
					problems = append(problems, fmt.Sprintf("%s: backend %q has settings; leave it empty (backend %q {}), smithd sets the backend configuration", where, strings.Join(inner.Labels, " "), strings.Join(inner.Labels, " ")))
				}
			case "cloud":
				problems = append(problems, where+": cloud block is not supported; smithd sets the backend configuration")
			}
		}
	}

	return pins, problems
}

// forbidden returns why a path must not be in a bundle, or "" if it may be
func forbidden(name string) string {
	base := path.Base(name)
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == ".terraform" {
			return fmt.Sprintf("%s: the .terraform directory must not be in the bundle", name)
		}
	}
	switch {
	case strings.HasSuffix(name, "/"):
		return ""
	case strings.Contains(base, ".tfstate"):
		return fmt.Sprintf("%s: state files must not be in the bundle; state lives in the backend smithd configures", name)
	case strings.HasSuffix(base, ".auto.tfvars") || strings.HasSuffix(base, ".auto.tfvars.json"):
		return fmt.Sprintf("%s: *.auto.tfvars files are loaded automatically and must not be in the bundle; use %s/<env>.tfvars for non-secret variables", name, EnvDir)
	case base == "terraform.tfvars" || base == "terraform.tfvars.json":
		return fmt.Sprintf("%s: %s is loaded automatically and must not be in the bundle; use %s/<env>.tfvars for non-secret variables", name, base, EnvDir)
	case isOverride(base):
		return fmt.Sprintf("%s: override files must not be in the bundle; the controller writes backend_override.tf with smithd's backend configuration", name)
	}
	return ""
}

// isOverride reports whether a file name is an OpenTofu override file
// (override.tf, *_override.tf and their .tf.json variants), which could
// replace the backend configuration smithd sets
func isOverride(base string) bool {
	for _, ext := range []string{".tf", ".tf.json"} {
		if stem, ok := strings.CutSuffix(base, ext); ok && (stem == "override" || strings.HasSuffix(stem, "_override")) {
			return true
		}
	}
	return false
}

// included reports whether a file is part of a bundle
func included(name string) bool {
	switch {
	case name == LockFile:
		return true
	case strings.HasSuffix(name, ".tf"):
		return true
	case path.Dir(name) == EnvDir && strings.HasSuffix(name, ".tfvars") && path.Base(name) != ".tfvars":
		return true
	}
	return false
}

// safePath reports whether a path stays inside the bundle root
func safePath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

// Pack creates the gzipped tarball of a bundle's files. The archive is the
// same for the same files.
func Pack(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	for _, name := range sortedNames(files) {
		content := files[name]
		header := &tar.Header{
			Name:    name,
			Mode:    0644,
			Size:    int64(len(content)),
			ModTime: time.Unix(0, 0),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("failed to add %s to archive: %w", name, err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			return nil, fmt.Errorf("failed to add %s to archive: %w", name, err)
		}
	}

	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// Read extracts the files of a gzipped bundle tarball for Validate.
// Directories are returned with a trailing "/" and no content.
func Read(r io.Reader) (map[string][]byte, error) {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// Guard against archives that unpack to far more than they weigh
	limited := &io.LimitedReader{R: gzReader, N: maxSize + 1}
	tarReader := tar.NewReader(limited)
	files := map[string][]byte{}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		name := strings.TrimPrefix(header.Name, "./")
		switch header.Typeflag {
		case tar.TypeDir:
			files[strings.TrimSuffix(name, "/")+"/"] = nil
		case tar.TypeReg:
			content, err := io.ReadAll(tarReader)
			if err != nil && limited.N > 0 {
				return nil, fmt.Errorf("failed to read file %s: %w", name, err)
			}
			files[name] = content
		default:
			return nil, &ValidationError{Problems: []string{fmt.Sprintf("%s: only regular files and directories may be in the bundle", name)}}
		}

		if limited.N <= 0 {
			return nil, fmt.Errorf("bundle is larger than %d MiB uncompressed", maxSize>>20)
		}
	}

	return files, nil
}

func sortedNames(files map[string][]byte) []string {
	return slices.Sorted(maps.Keys(files))
}
