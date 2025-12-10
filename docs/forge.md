# forge CLI Tool

forge is a command-line tool for CI/CD pipelines that packages and publishes application versions to DeploySmith.

## Overview

forge enables you to:
- Package Kubernetes manifests and publish them to smithd
- Integrate seamlessly with CI/CD pipelines
- Manage application versions with proper metadata tracking
- Bind repositories to applications for streamlined workflows

## Installation

### Download Binary

```bash
# Download from GitHub Releases (replace with actual release URL)
curl -L https://github.com/sorenmh/deploysmith/releases/download/v1.0.0/forge-linux-amd64 -o forge
chmod +x forge
sudo mv forge /usr/local/bin/
```

### Build from Source

```bash
git clone https://github.com/sorenmh/deploysmith.git
cd deploysmith
make build-forge
# Binary will be in bin/forge
```

### Container Images

forge is available as a container image for use in CI/CD pipelines:

```bash
# Run forge in a container
docker run --rm -v $(pwd):/workspace -w /workspace \
  ghcr.io/sorenmh/forge:latest version

# Use in CI pipelines
docker run --rm -v $(pwd):/workspace -w /workspace \
  -e SMITHD_URL=https://smithd.example.com \
  -e SMITHD_API_KEY=sk_live_abc123 \
  ghcr.io/sorenmh/forge:latest init --version v1.0.0
```

**Available tags:**
- `latest` - Latest stable release
- `v1.0.0` - Specific version tags (e.g., v1.0.0, v1.1.0)

**Image details:**
- **Base**: Alpine Linux 3.19 (minimal, security-focused)
- **Size**: ~15MB (small footprint for CI)
- **User**: Runs as non-root user `forge` (UID 1000)
- **Working dir**: `/workspace` (mount your project here)
- **Platforms**: linux/amd64, linux/arm64

**Building the image:**
```bash
# Using Docker
docker build -f Dockerfile.forge -t forge:local .

# Using Earthly (recommended)
earthly +docker-forge
# Creates forge:latest locally
```

## Configuration

forge can be configured in three ways:

### 1. Environment Variables

```bash
export SMITHD_URL=https://smithd.example.com
export SMITHD_API_KEY=sk_live_abc123
```

**Note:** forge only needs access to smithd - it does NOT require AWS credentials since it uses presigned URLs.

### 2. Configuration File

Create `~/.deploysmith/config.yaml`:

```yaml
url: https://smithd.example.com
apikey: sk_live_abc123
```

### 3. Command Line Flags

```bash
forge init --url https://smithd.example.com --api-key sk_live_abc123 --version v1.0.0
```

## Commands

### `forge configure`

Interactively configure forge settings.

```bash
forge configure
```

This will prompt for:
- smithd URL
- API key

The configuration is saved to `~/.deploysmith/config.yaml`.

### `forge app-bind`

Bind the current repository/directory to a DeploySmith application. This creates a `.deploysmith/app.yaml` config file that allows other forge commands to work without specifying `--app`.

```bash
forge app-bind --app my-api-service
```

**Output:**
```
Repository bound to application 'my-api-service' (ID: 23c6b83c-da01-47d4-a2dd-55877dd6f569)
Config saved to .deploysmith/app.yaml
You can now run forge commands without specifying --app
```

**Requirements:**
- Application must already exist in smithd
- Run this once per repository

**Creates:**
`.deploysmith/app.yaml`:
```yaml
appId: 23c6b83c-da01-47d4-a2dd-55877dd6f569
appName: my-api-service
```

### `forge init`

Initialize a new version draft with smithd.

```bash
# With app binding (recommended)
forge init --version v1.2.3

# Without app binding
forge init --app my-api-service --version v1.2.3

# Full example with metadata
forge init \
  --version "${GIT_SHA}-${BUILD_NUMBER}" \
  --git-sha "${GIT_SHA}" \
  --git-branch "${GIT_BRANCH}" \
  --git-committer "${GIT_AUTHOR_EMAIL}" \
  --build-number "${BUILD_NUMBER}"
```

**Flags:**
- `--app` (optional if app is bound): Application name
- `--version` (required): Version identifier
- `--git-sha` (optional): Git commit SHA
- `--git-branch` (optional): Git branch name
- `--git-committer` (optional): Git committer email
- `--build-number` (optional): CI build number

**Output:**
```json
{
  "versionId": "v1.2.3",
  "uploadUrl": "https://s3.amazonaws.com/bucket/path...",
  "uploadExpires": "2025-12-08T20:00:00Z"
}
```

**What it does:**
1. Creates a draft version in smithd
2. Generates a presigned S3 URL for manifest upload
3. Saves upload URL to `.forge/upload-url`
4. Saves version info to `.forge/version-info`

**Required metadata:** If not provided, git-sha and git-branch default to "unknown".

### `forge upload`

Upload manifest files as a tar.gz archive to S3.

```bash
# Upload directory of YAML files
forge upload manifests/

# Upload specific files
forge upload deployment.yaml service.yaml

# Override upload URL
forge upload manifests/ --upload-url "https://custom-url"
```

**What it does:**
1. Validates all YAML files for syntax errors
2. Creates a tar.gz archive containing all files
3. Auto-generates `version.yml` if not present
4. Uploads archive to S3 using presigned URL from `forge init`

**Auto-generated version.yml:**
```yaml
version: "v1.2.3"
metadata:
  timestamp: "2025-12-08T19:30:00Z"
```

**Output:**
```
Creating manifest archive...
  ✓ deployment.yaml (1.2 KB)
  ✓ service.yaml (0.5 KB)
  ✓ version.yml (0.1 KB)
Uploading manifest archive...

Uploaded 3 files (1.8 KB) as archive (0.9 KB) in 0.5s
```

### `forge publish`

Publish a draft version to make it deployable.

```bash
# With app binding
forge publish --version v1.2.3

# Without app binding
forge publish --app my-api-service --version v1.2.3
```

**Flags:**
- `--app` (optional if app is bound): Application name
- `--version` (required): Version identifier

**What it does:**
1. Validates all uploaded manifests
2. Moves files from drafts to published in S3
3. Updates version status to "published"
4. Triggers auto-deployments if policies match

**Output:**
```
Publishing version v1.2.3...
✓ Version published
✓ Auto-deployment triggered for staging

Version v1.2.3 is now live
```

### `forge version`

Show forge version information.

```bash
forge version
```

**Output:**
```
forge version dev
commit: c0ae8e3
built: 2025-12-08T19:30:00Z
```

## Complete Workflow

### Option 1: With App Binding (Recommended)

**One-time setup:**
```bash
# Bind repository to app
forge app-bind --app my-api-service
```

**CI/CD Pipeline:**
```bash
#!/bin/bash
set -e

# Generate manifests (using Helm, Kustomize, etc.)
helm template my-app ./charts/my-app > manifests/deployment.yaml

# Package and publish (no --app needed!)
VERSION="${GIT_SHA}-${BUILD_NUMBER}"
forge init --version "$VERSION" \
  --git-sha "$GIT_SHA" \
  --git-branch "$GIT_BRANCH" \
  --git-committer "$GIT_AUTHOR_EMAIL" \
  --build-number "$BUILD_NUMBER"

forge upload manifests/
forge publish --version "$VERSION"

echo "✅ Version $VERSION deployed successfully"
```

### Option 2: Without App Binding

```bash
#!/bin/bash
set -e

APP_NAME="my-api-service"
VERSION="${GIT_SHA}-${BUILD_NUMBER}"

# Generate manifests
helm template my-app ./charts/my-app > manifests/deployment.yaml

# Package and publish
forge init --app "$APP_NAME" --version "$VERSION" \
  --git-sha "$GIT_SHA" \
  --git-branch "$GIT_BRANCH" \
  --git-committer "$GIT_AUTHOR_EMAIL" \
  --build-number "$BUILD_NUMBER"

forge upload manifests/
forge publish --app "$APP_NAME" --version "$VERSION"
```

## CI/CD Examples

### GitHub Actions (Binary)

```yaml
name: Deploy with forge binary
on:
  push:
    branches: [master]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup forge
        run: |
          # Download forge binary (update URL for actual releases)
          curl -L https://github.com/sorenmh/deploysmith/releases/download/v1.0.0/forge-linux-amd64 -o forge
          chmod +x forge
          sudo mv forge /usr/local/bin/

      - name: Configure forge
        run: |
          mkdir -p ~/.deploysmith
          cat > ~/.deploysmith/config.yaml << EOF
          url: ${{ secrets.SMITHD_URL }}
          apikey: ${{ secrets.SMITHD_API_KEY }}
          EOF

      - name: Bind app (one-time setup - can be done locally)
        run: forge app-bind --app my-api-service

      - name: Generate manifests
        run: |
          # Your manifest generation here
          mkdir -p manifests
          helm template my-app ./charts/my-app > manifests/deployment.yaml

      - name: Deploy with forge
        run: |
          VERSION="${GITHUB_SHA:0:7}-${GITHUB_RUN_NUMBER}"

          forge init --version "$VERSION" \
            --git-sha "$GITHUB_SHA" \
            --git-branch "${GITHUB_REF_NAME}" \
            --git-committer "${{ github.actor }}@users.noreply.github.com" \
            --build-number "$GITHUB_RUN_NUMBER"

          forge upload manifests/
          forge publish --version "$VERSION"
```

### GitHub Actions (Container)

```yaml
name: Deploy with forge container
on:
  push:
    branches: [master]

jobs:
  deploy:
    runs-on: ubuntu-latest
    container: ghcr.io/sorenmh/forge:latest
    env:
      SMITHD_URL: ${{ secrets.SMITHD_URL }}
      SMITHD_API_KEY: ${{ secrets.SMITHD_API_KEY }}

    steps:
      - uses: actions/checkout@v4

      - name: Bind app (one-time setup - can be done locally)
        run: forge app-bind --app my-api-service

      - name: Generate manifests
        run: |
          # Your manifest generation here
          mkdir -p manifests
          helm template my-app ./charts/my-app > manifests/deployment.yaml

      - name: Deploy with forge
        run: |
          VERSION="${GITHUB_SHA:0:7}-${GITHUB_RUN_NUMBER}"

          forge init --version "$VERSION" \
            --git-sha "$GITHUB_SHA" \
            --git-branch "${GITHUB_REF_NAME}" \
            --git-committer "${{ github.actor }}@users.noreply.github.com" \
            --build-number "$GITHUB_RUN_NUMBER"

          forge upload manifests/
          forge publish --version "$VERSION"
```

### GitLab CI

```yaml
deploy:
  stage: deploy
  image: ghcr.io/sorenmh/forge:latest
  variables:
    SMITHD_URL: $SMITHD_URL
    SMITHD_API_KEY: $SMITHD_API_KEY
  script:
    - forge app-bind --app my-api-service
    - mkdir -p manifests
    - helm template my-app ./charts/my-app > manifests/deployment.yaml
    - VERSION="${CI_COMMIT_SHORT_SHA}-${CI_PIPELINE_ID}"
    - forge init --version "$VERSION" --git-sha "$CI_COMMIT_SHA" --git-branch "$CI_COMMIT_REF_NAME"
    - forge upload manifests/
    - forge publish --version "$VERSION"
  only:
    - master
```

### Jenkins Pipeline

```groovy
pipeline {
    agent {
        docker {
            image 'ghcr.io/sorenmh/forge:latest'
            args '-v $PWD:/workspace -w /workspace'
        }
    }

    environment {
        SMITHD_URL = credentials('smithd-url')
        SMITHD_API_KEY = credentials('smithd-api-key')
    }

    stages {
        stage('Bind App') {
            steps {
                sh 'forge app-bind --app my-api-service'
            }
        }

        stage('Generate Manifests') {
            steps {
                sh '''
                mkdir -p manifests
                helm template my-app ./charts/my-app > manifests/deployment.yaml
                '''
            }
        }

        stage('Deploy') {
            steps {
                sh '''
                VERSION="${GIT_COMMIT:0:7}-${BUILD_NUMBER}"
                forge init --version "$VERSION" --git-sha "$GIT_COMMIT" --git-branch "$GIT_BRANCH"
                forge upload manifests/
                forge publish --version "$VERSION"
                '''
            }
        }
    }
}
```

## Error Handling

forge provides clear error messages for common issues:

### Configuration Errors
```bash
$ forge init --version v1.0.0
Error: smithd URL and API key must be configured. Run 'forge configure' first.
```

### App Not Bound
```bash
$ forge init --version v1.0.0
Error: app config file not found (run 'forge app-bind' or specify --app)
```

### Missing Arguments
```bash
$ forge init --app my-app
Error: --version is required
```

### Upload Issues
```bash
$ forge upload manifests/
Error: validation failed for deployment.yaml: invalid YAML: line 5: syntax error
```

### API Errors
```bash
$ forge init --app nonexistent --version v1.0.0
Error: failed to resolve app 'nonexistent': application 'nonexistent' not found
```

## File Structure

After running forge commands, you'll see these files:

```
.deploysmith/
  app.yaml           # App binding (from app-bind)
.forge/
  upload-url         # S3 upload URL (from init)
  version-info       # Version metadata (from init)
```

These files are temporary and can be ignored in version control:

**.gitignore:**
```
.forge/
```

**Note:** `.deploysmith/app.yaml` should be committed as it contains the app binding.

## Advanced Usage

### Custom Upload URL
```bash
# Use a different upload URL
forge upload manifests/ --upload-url "https://custom-s3-url"
```

### Configuration Override
```bash
# Override config for one command
forge init --url https://staging.smithd.example.com --api-key sk_test_123 --version v1.0.0
```

### Debugging
```bash
# See what forge is doing
FORGE_DEBUG=1 forge init --version v1.0.0
```

## Troubleshooting

### Common Issues

**1. S3 Upload Errors (403/404)**
- Check that S3 bucket exists and is accessible
- Verify AWS credentials have proper permissions
- Ensure smithd is configured with correct S3 settings

**2. App Not Found**
- Verify app name spelling
- Check that app exists: `curl -H "X-API-Key: $API_KEY" $SMITHD_URL/api/v1/apps`
- Try running `forge app-bind --app correct-name`

**3. Upload URL Expired**
- Upload URLs expire in 5 minutes
- Run `forge init` again to get a fresh URL
- Ensure upload happens quickly after init

**4. YAML Validation Errors**
- Check YAML syntax with a validator
- Ensure files are valid Kubernetes manifests
- Avoid Helm/Go template syntax in raw YAML files

### Getting Help

```bash
# General help
forge help

# Command-specific help
forge help init
forge help upload
forge help publish
```

For more help, see the [smithd API documentation](smithd-api-spec.md) or check the project repository.

## DeploySmith Container Ecosystem

forge is part of the complete DeploySmith container ecosystem:

### Available Images

| Component | Image | Purpose | Size |
|-----------|-------|---------|------|
| **forge** | `ghcr.io/sorenmh/forge:latest` | CI/CD tool for packaging and publishing | ~15MB |
| **smithd** | `ghcr.io/sorenmh/smithd:latest` | API server and deployment controller | ~25MB |
| **smithctl** | `ghcr.io/sorenmh/smithctl:latest` | Developer CLI for managing deployments | ~15MB |

### Image Features

**Common features across all images:**
- **Base**: Alpine Linux 3.19 (security-focused, minimal)
- **Security**: Run as non-root users
- **Platforms**: linux/amd64, linux/arm64
- **Dependencies**: Only essential runtime dependencies included

**forge-specific features:**
- **User**: `forge` (UID 1000)
- **Working directory**: `/workspace`
- **Dependencies**: CA certificates for HTTPS API calls
- **Size optimization**: No unnecessary build tools or dependencies

### Registry and Tags

**Image naming:**
```
ghcr.io/sorenmh/forge:latest      # Latest stable release
ghcr.io/sorenmh/forge:v1.0.0      # Specific version release
```

**Note:** Images are only built for tagged releases (e.g., v1.0.0). There are no development or branch-based images.

**Multi-platform images:**
All images are built for multiple platforms and can be used on:
- Linux AMD64 (x86_64)
- Linux ARM64 (aarch64) - for ARM servers and Apple Silicon

**Building custom images:**
```bash
# Build all DeploySmith images
make docker

# Build specific images
make docker-forge
make docker-smithd
make docker-smithctl

# Using Earthly (cross-platform)
earthly +docker-forge    # Builds for current platform
```