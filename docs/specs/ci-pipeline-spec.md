# CI/CD Pipeline Specification

This document defines the CI/CD pipeline for building, testing, and releasing DeploySmith components.

## Overview

DeploySmith uses:
- **GitHub Actions** for CI/CD
- **Earthly** for reproducible builds
- **goreleaser** for creating releases
- **GitHub Releases** for artifact distribution

## Repository Structure

```
.
├── .github/
│   └── workflows/
│       ├── smithd.yml           # smithd CI/CD
│       ├── forge.yml            # forge CI/CD
│       ├── smithctl.yml         # smithctl CI/CD
│       └── test.yml             # Run all tests
├── Earthfile                    # Earthly build definitions
├── .goreleaser.yml              # goreleaser configuration
├── cmd/
│   ├── smithd/                  # smithd entrypoint
│   ├── forge/                   # forge entrypoint
│   └── smithctl/                # smithctl entrypoint
├── pkg/                         # Shared packages
├── internal/                    # Internal packages
└── tests/
    ├── acceptance/              # Acceptance tests
    └── integration/             # Integration tests
```

---

## Build Order

1. **smithd** - Server component (can be built and tested independently)
2. **CI Pipeline** - Set up automated builds and releases
3. **forge** - Depends on smithd being available
4. **smithctl** - Depends on smithd being available

---

## Earthfile

Earthly provides reproducible builds across all platforms.

### Targets

```dockerfile
# Earthfile

VERSION 0.8

FROM golang:1.21-alpine
WORKDIR /workspace

deps:
    # Install dependencies
    COPY go.mod go.sum ./
    RUN go mod download
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum

build-smithd:
    FROM +deps
    COPY cmd/smithd ./cmd/smithd
    COPY pkg ./pkg
    COPY internal/smithd ./internal/smithd
    RUN CGO_ENABLED=1 go build -o bin/smithd ./cmd/smithd
    SAVE ARTIFACT bin/smithd AS LOCAL bin/smithd

build-forge:
    FROM +deps
    COPY cmd/forge ./cmd/forge
    COPY pkg ./pkg
    COPY internal/forge ./internal/forge
    RUN CGO_ENABLED=0 go build -o bin/forge ./cmd/forge
    SAVE ARTIFACT bin/forge AS LOCAL bin/forge

build-smithctl:
    FROM +deps
    COPY cmd/smithctl ./cmd/smithctl
    COPY pkg ./pkg
    COPY internal/smithctl ./internal/smithctl
    RUN CGO_ENABLED=0 go build -o bin/smithctl ./cmd/smithctl
    SAVE ARTIFACT bin/smithctl AS LOCAL bin/smithctl

test:
    FROM +deps
    COPY . .
    RUN go test -v ./...

test-acceptance:
    FROM +deps
    COPY . .
    RUN go test -v ./tests/acceptance/...

lint:
    FROM +deps
    COPY . .
    RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    RUN golangci-lint run

docker-smithd:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates git openssh-client
    COPY +build-smithd/smithd /usr/local/bin/smithd
    ENTRYPOINT ["/usr/local/bin/smithd"]
    SAVE IMAGE --push ghcr.io/org/smithd:latest

docker-forge:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates
    COPY +build-forge/forge /usr/local/bin/forge
    ENTRYPOINT ["/usr/local/bin/forge"]
    SAVE IMAGE --push ghcr.io/org/forge:latest

docker-smithctl:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates
    COPY +build-smithctl/smithctl /usr/local/bin/smithctl
    ENTRYPOINT ["/usr/local/bin/smithctl"]
    SAVE IMAGE --push ghcr.io/org/smithctl:latest

all:
    BUILD +build-smithd
    BUILD +build-forge
    BUILD +build-smithctl
    BUILD +test
    BUILD +lint
```

---

## GitHub Actions Workflows

### `test.yml` - Run Tests

Runs on every push and pull request.

```yaml
name: Test

on:
  push:
    branches: [ main, develop ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Earthly
        uses: earthly/actions-setup@v1

      - name: Run unit tests
        run: earthly +test

      - name: Run linter
        run: earthly +lint

  acceptance:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v4

      - name: Set up Earthly
        uses: earthly/actions-setup@v1

      - name: Run acceptance tests
        run: earthly +test-acceptance
```

**Acceptance Test:**
- [ ] Workflow runs on push to main/develop
- [ ] Workflow runs on pull requests
- [ ] Unit tests pass
- [ ] Linter passes
- [ ] Acceptance tests pass
- [ ] Build fails if any test fails

---

### `smithd.yml` - Build and Release smithd

Builds and releases smithd on version tags.

```yaml
name: smithd Release

on:
  push:
    tags:
      - 'smithd/v*'

permissions:
  contents: write
  packages: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.21'

      - name: Set up Earthly
        uses: earthly/actions-setup@v1

      - name: Build binaries
        run: earthly +build-smithd

      - name: Run goreleaser
        uses: goreleaser/goreleaser-action@v5
        with:
          version: latest
          args: release --config .goreleaser.smithd.yml
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push Docker image
        run: |
          echo ${{ secrets.GITHUB_TOKEN }} | docker login ghcr.io -u ${{ github.actor }} --password-stdin
          earthly +docker-smithd --push
        env:
          TAG: ${{ github.ref_name }}
```

**Acceptance Test:**
- [ ] Workflow runs on tags matching `smithd/v*`
- [ ] Builds smithd binary for multiple platforms
- [ ] Creates GitHub Release with binaries
- [ ] Builds and pushes Docker image to ghcr.io
- [ ] Docker image is tagged with version
- [ ] Docker image includes latest tag on main branch
- [ ] Release notes are auto-generated from commits

---

### `forge.yml` - Build and Release forge

Similar to smithd.yml but for forge.

```yaml
name: forge Release

on:
  push:
    tags:
      - 'forge/v*'

# Similar structure to smithd.yml
```

**Acceptance Test:**
- [ ] Workflow runs on tags matching `forge/v*`
- [ ] Builds forge binary for multiple platforms
- [ ] Creates GitHub Release with binaries
- [ ] Builds and pushes Docker image

---

### `smithctl.yml` - Build and Release smithctl

Similar to smithd.yml but for smithctl.

```yaml
name: smithctl Release

on:
  push:
    tags:
      - 'smithctl/v*'

# Similar structure to smithd.yml
```

**Acceptance Test:**
- [ ] Workflow runs on tags matching `smithctl/v*`
- [ ] Builds smithctl binary for multiple platforms
- [ ] Creates GitHub Release with binaries
- [ ] Builds and pushes Docker image

---

## goreleaser Configuration

### `.goreleaser.smithd.yml`

```yaml
project_name: smithd

before:
  hooks:
    - go mod tidy

builds:
  - id: smithd
    main: ./cmd/smithd
    binary: smithd
    env:
      - CGO_ENABLED=1
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.Commit}}
      - -X main.date={{.Date}}

archives:
  - id: smithd
    format: tar.gz
    name_template: >-
      {{ .ProjectName }}_
      {{- .Version }}_
      {{- .Os }}_
      {{- .Arch }}

checksum:
  name_template: 'checksums.txt'

changelog:
  sort: asc
  filters:
    exclude:
      - '^docs:'
      - '^test:'
      - '^chore:'

release:
  github:
    owner: org
    name: deploysmith
  draft: false
  prerelease: auto
  name_template: "smithd {{.Version}}"
```

Similar configs for `.goreleaser.forge.yml` and `.goreleaser.smithctl.yml`.

**Acceptance Test:**
- [ ] Builds for Linux amd64/arm64
- [ ] Builds for macOS amd64/arm64
- [ ] Creates tar.gz archives
- [ ] Generates checksums file
- [ ] Creates GitHub Release
- [ ] Release name includes component and version
- [ ] Changelog excludes docs/test/chore commits

---

## Release Process

### Creating a Release

1. **Ensure all tests pass:**
   ```bash
   earthly +test
   earthly +test-acceptance
   ```

2. **Create and push a tag:**
   ```bash
   # For smithd
   git tag smithd/v1.0.0
   git push origin smithd/v1.0.0

   # For forge
   git tag forge/v1.0.0
   git push origin forge/v1.0.0

   # For smithctl
   git tag smithctl/v1.0.0
   git push origin smithctl/v1.0.0
   ```

3. **GitHub Actions automatically:**
   - Builds binaries for all platforms
   - Creates GitHub Release
   - Uploads binaries to release
   - Builds and pushes Docker images
   - Generates changelog

### Manual Release (if needed)

```bash
# Build locally
earthly +build-smithd
earthly +build-forge
earthly +build-smithctl

# Run goreleaser locally (requires GITHUB_TOKEN)
goreleaser release --config .goreleaser.smithd.yml
```

---

## Docker Images

### Registry

All images are published to GitHub Container Registry:
- `ghcr.io/org/smithd:latest`
- `ghcr.io/org/smithd:v1.0.0`
- `ghcr.io/org/forge:latest`
- `ghcr.io/org/forge:v1.0.0`
- `ghcr.io/org/smithctl:latest`
- `ghcr.io/org/smithctl:v1.0.0`

### Tags

- `latest` - Latest release on main branch
- `vX.Y.Z` - Specific version
- `main` - Latest commit on main branch
- `<commit-sha>` - Specific commit

**Acceptance Test:**
- [ ] Images are published to ghcr.io
- [ ] Images are tagged with version
- [ ] Images include latest tag
- [ ] Images are multi-arch (amd64, arm64)
- [ ] Images can be pulled and run successfully

---

## Acceptance Testing in CI

Acceptance tests run against real API endpoints.

```go
// tests/acceptance/smithd_test.go
package acceptance

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestSmithdAPI(t *testing.T) {
    // Start smithd in test mode
    server := StartTestServer(t)
    defer server.Stop()

    // Test app registration
    t.Run("RegisterApp", func(t *testing.T) {
        resp := server.POST("/api/v1/apps", map[string]interface{}{
            "name": "test-app",
            "gitopsRepo": "git@github.com:test/repo.git",
            "gitopsPath": "apps/test-app",
        })
        assert.Equal(t, 201, resp.StatusCode)
    })

    // More tests...
}
```

---

## Local Development

### Build all components

```bash
earthly +all
```

### Run tests

```bash
earthly +test
earthly +test-acceptance
```

### Build specific component

```bash
earthly +build-smithd
earthly +build-forge
earthly +build-smithctl
```

### Build Docker images

```bash
earthly +docker-smithd
earthly +docker-forge
earthly +docker-smithctl
```
