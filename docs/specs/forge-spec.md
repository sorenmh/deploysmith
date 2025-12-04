# forge Specification

CLI tool for CI pipelines to package and publish versions to smithd.

## Overview

forge is a command-line tool used in CI pipelines to:
1. Draft new versions with smithd
2. Upload manifest files to S3
3. Publish versions to make them deployable
4. Generate and validate version.yml metadata

## Installation

```bash
# Download from GitHub Releases
curl -L https://github.com/org/deploysmith/releases/download/v1.0.0/forge-linux-amd64 -o forge
chmod +x forge
sudo mv forge /usr/local/bin/
```

Or via Docker:
```bash
docker run ghcr.io/org/forge:latest --help
```

## Configuration

forge is configured via environment variables or CLI flags.

```bash
# smithd API endpoint
SMITHD_URL=https://smithd.example.com
SMITHD_API_KEY=sk_live_abc123

# Optional: AWS credentials (if not using instance profile)
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
```

## Commands

### `forge init`

Initialize a new version draft.

**Usage:**
```bash
forge init \
  --app my-api-service \
  --version 42540c4-123 \
  --git-sha 42540c4abc123 \
  --git-branch main \
  --git-committer john@example.com \
  --build-number 123
```

**Flags:**
- `--app` (required): Application name
- `--version` (required): Version identifier
- `--git-sha` (optional): Git commit SHA
- `--git-branch` (optional): Git branch name
- `--git-committer` (optional): Git committer email
- `--build-number` (optional): CI build number

**Output:**
```json
{
  "versionId": "42540c4-123",
  "uploadUrl": "https://s3.amazonaws.com/...",
  "uploadExpires": "2025-01-15T10:35:00Z"
}
```

**Exit codes:**
- `0`: Success
- `1`: API error (see stderr for details)
- `2`: Invalid arguments

**Acceptance Test:**
- [ ] Calls smithd POST /apps/{app}/versions/draft API
- [ ] Returns upload URL from smithd response
- [ ] Saves upload URL to `.forge/upload-url` for next command
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if smithd returns error
- [ ] Returns exit code 2 if required flags are missing
- [ ] Prints error message to stderr on failure

---

### `forge upload`

Upload manifest files to S3 using the pre-signed URL from `forge init`.

**Usage:**
```bash
forge upload manifests/
```

Or upload specific files:
```bash
forge upload deployment.yaml service.yaml ingress.yaml
```

**Flags:**
- `--upload-url` (optional): Override URL (otherwise reads from `.forge/upload-url`)

**Output:**
```
Uploading manifests...
  ✓ deployment.yaml (1.2 KB)
  ✓ service.yaml (0.5 KB)
  ✓ ingress.yaml (0.8 KB)
  ✓ version.yml (0.3 KB)

Uploaded 4 files (2.8 KB) in 1.2s
```

**Exit codes:**
- `0`: Success
- `1`: Upload error
- `2`: Invalid arguments

**Acceptance Test:**
- [ ] Uploads all YAML files in directory if directory is provided
- [ ] Uploads specific files if file paths are provided
- [ ] Auto-generates version.yml if not present in upload
- [ ] Uses pre-signed URL from `.forge/upload-url` if not specified
- [ ] Validates files are valid YAML before uploading
- [ ] Shows progress bar for large uploads
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if upload fails
- [ ] Returns exit code 2 if no files found or upload URL missing
- [ ] Retries failed uploads up to 3 times with exponential backoff

---

### `forge publish`

Publish the version to make it available for deployment.

**Usage:**
```bash
forge publish \
  --app my-api-service \
  --version 42540c4-123
```

**Flags:**
- `--app` (required): Application name
- `--version` (required): Version identifier
- `--no-validate` (optional): Skip manifest validation

**Output:**
```
Publishing version 42540c4-123...
  ✓ Version published
  ✓ Auto-deployment triggered for staging

Version 42540c4-123 is now live
```

**Exit codes:**
- `0`: Success
- `1`: API error
- `2`: Invalid arguments

**Acceptance Test:**
- [ ] Calls smithd POST /apps/{app}/versions/{version}/publish API
- [ ] Shows success message on completion
- [ ] Indicates if auto-deployment was triggered
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if smithd returns error
- [ ] Returns exit code 2 if required flags are missing
- [ ] Cleans up `.forge/` directory on success

---

### `forge version`

Show the forge version.

**Usage:**
```bash
forge version
```

**Output:**
```
forge version 1.0.0
commit: abc123
built: 2025-01-15T10:00:00Z
```

**Acceptance Test:**
- [ ] Prints version, commit SHA, and build time
- [ ] Returns exit code 0

---

### `forge help`

Show help information.

**Usage:**
```bash
forge help
forge help init
```

**Acceptance Test:**
- [ ] Shows general help when called without arguments
- [ ] Shows command-specific help when called with command name
- [ ] Returns exit code 0

---

## Complete Workflow Example

Typical usage in a CI pipeline:

```bash
#!/bin/bash
set -e

# Build your manifests (using Helm, Kustomize, or plain YAML)
helm template my-app ./charts/my-app > manifests/deployment.yaml

# Initialize version draft
forge init \
  --app my-api-service \
  --version "${GIT_SHA}-${BUILD_NUMBER}" \
  --git-sha "${GIT_SHA}" \
  --git-branch "${GIT_BRANCH}" \
  --git-committer "${GIT_AUTHOR_EMAIL}" \
  --build-number "${BUILD_NUMBER}"

# Upload manifests
forge upload manifests/

# Publish version
forge publish \
  --app my-api-service \
  --version "${GIT_SHA}-${BUILD_NUMBER}"

echo "✅ Version deployed successfully"
```

---

## version.yml Format

forge auto-generates this file if not provided:

```yaml
version: "42540c4-123"
metadata:
  gitSha: "42540c4abc123"
  gitBranch: "main"
  gitCommitter: "john@example.com"
  buildNumber: "123"
  timestamp: "2025-01-15T10:30:00Z"
```

---

## Error Handling

forge provides clear error messages:

```bash
$ forge init --app my-app
Error: --version is required

$ forge upload manifests/
Error: No YAML files found in manifests/

$ forge publish --app my-app --version 123
Error: Version 123 not found or not in draft status
```

---

## Future Enhancements (Post-MVP)

- `forge build` - Generate manifests from simplified YAML
- `forge validate` - Validate manifests without uploading
- `forge list` - List versions for an app
- `forge rollback` - Rollback to previous version
- Interactive mode for local development
- Support for manifest templates with variable substitution
