# CI/CD Pipeline

DeploySmith uses Earthly for consistent builds across local development and CI/CD environments.

## Benefits of Using Earthly

1. **Same Build Everywhere**: Run the exact same build commands locally and in CI
2. **Reproducible Builds**: Docker-based builds ensure consistency
3. **Cached Layers**: Fast incremental builds with layer caching
4. **Self-Contained**: No need to install Go, linters, or tools on the host

## Local Development

```bash
# Run all checks (what CI runs)
earthly +test
earthly +lint
earthly +build-smithd
earthly +build-forge

# Or run everything at once
earthly +all

# Build Docker images
earthly +docker-smithd
earthly +docker-forge
```

## GitHub Actions Workflows

### Test Workflow (`.github/workflows/test.yml`)

Runs on every push and PR:
- Uses Earthly for consistent testing
- Runs unit tests with `earthly +test`
- Runs linter with `earthly +lint`
- Builds both binaries with `earthly +build-smithd` and `earthly +build-forge`

### Release Workflows

**smithd**: `.github/workflows/release-smithd.yml`
- Triggered by tags matching `smithd/v*`
- Uses GoReleaser for multi-platform binary releases
- Uses Earthly for Docker image builds
- Pushes to ghcr.io

**forge**: `.github/workflows/release-forge.yml`
- Triggered by tags matching `forge/v*`
- Uses GoReleaser for multi-platform binary releases
- Uses Earthly for Docker image builds
- Pushes to ghcr.io

## Creating a Release

```bash
# For smithd
git tag smithd/v1.0.0
git push origin smithd/v1.0.0

# For forge
git tag forge/v1.0.0
git push origin forge/v1.0.0
```

This will:
1. Run GoReleaser to build binaries for Linux/macOS/Windows (amd64/arm64)
2. Create a GitHub Release with changelogs
3. Build Docker image with Earthly
4. Push Docker image to ghcr.io

## Earthfile Targets

- `+deps` - Download Go dependencies
- `+build-smithd` - Build smithd binary
- `+build-forge` - Build forge binary
- `+test` - Run unit tests
- `+lint` - Run golangci-lint
- `+docker-smithd` - Build smithd Docker image
- `+docker-forge` - Build forge Docker image
- `+all` - Run test, lint, and build all binaries

## Configuration

### Go Version

The project uses Go 1.21 (specified in `go.mod` and Earthfile).

### Linter

golangci-lint runs in the Earthfile with a 5-minute timeout. Warnings are non-blocking.

## Troubleshooting

### "No such tool 'covdata'" error

This happens when using `-race` flag with coverage. The Earthfile runs tests without race detection to avoid this issue.

### Linter version mismatch

The linter runs inside the Earthly container with Go 1.21, so there's no version mismatch between the linter and the Go version.

### Local vs CI differences

There shouldn't be any! That's the whole point of using Earthly. If it works locally with `earthly +test`, it will work in CI.
