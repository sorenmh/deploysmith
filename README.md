# DeploySmith

A GitOps-based deployment controller for Kubernetes applications.

## Project Status

🚧 **MVP Development in Progress**

### Completed ✅

- **Phase 1**: MVP Core Platform (smithd API server)
  - Application Management API
  - Version Management with S3 storage
  - Deployment API with GitOps integration
  - Auto-Deploy Policies
  - Local development environment (Docker Compose)
- **Phase 2**: CI Pipeline Tool (forge CLI)
  - CLI for packaging and publishing from CI/CD
  - Commands: init, upload, publish, version
  - Integration with smithd API

- **Web UI** (served by smithd at `/ui`)
  - Overview of what runs in each environment
  - Deploy and roll back versions, with deployment history and live status
  - Manage auto-deploy policies and environments
  - Sign-in through oauth2-proxy (see [charts/smithd/README.md](charts/smithd/README.md#web-ui-with-oauth2-proxy))

### In Progress 🔨

- Documentation and CI/CD examples

## Architecture

DeploySmith consists of three components:

- **smithd**: Server component that runs in Kubernetes, exposes REST API
- **forge**: CI tool for packaging and publishing versions
- **smithctl**: CLI for developers to manage deployments

## Quick Start

### Option 1: Local Development with Docker Compose (Recommended)

The easiest way to get started is using Docker Compose with MinIO (S3) and Gitea (Git):

```bash
# 1. Set up local environment (creates containers, SSH keys, .env file)
./scripts/setup-local-dev.sh

# 2. Initialize Gitea repository
./scripts/init-gitea.sh

# 3. Build and run smithd
go build -o bin/smithd ./cmd/smithd
source .env && ./bin/smithd

# 4. Build forge (optional, for testing CI workflow)
go build -o bin/forge ./cmd/forge
```

**Services:**

- MinIO Console: http://localhost:9001 (minioadmin / minioadmin123)
- Gitea: http://localhost:3000 (deploysmith / password123)
- smithd API: http://localhost:8080

**Stop services:**

```bash
docker-compose down
```

### Option 2: Production Setup

### Prerequisites

- Go 1.21+
- SQLite3
- AWS S3 bucket
- Git repository for GitOps

### Building

DeploySmith uses a Makefile for common build tasks. Run `make help` to see all available targets.

```bash
# Quick start - build all components for your local platform
make build

# Build individual components
make build-smithd     # Build smithd server (requires CGO for SQLite)
make build-forge      # Build forge CI tool
make build-smithctl   # Build smithctl CLI

# Build smithctl for all platforms (Linux, macOS, Windows)
make build-smithctl-all

# Build Docker images
make docker           # Build all Docker images
make docker-smithd    # Build smithd image
make docker-forge     # Build forge image
make docker-smithctl  # Build smithctl image

# Testing
make test             # Run unit tests
make test-acceptance  # Run acceptance tests
make lint             # Run linter

# Clean build artifacts
make clean

# See all available targets
make help
```

**Direct commands (without Makefile):**

```bash
# Build for your local platform (macOS, Linux, etc.)
go build -o bin/smithd ./cmd/smithd
go build -o bin/forge ./cmd/forge
go build -o bin/smithctl ./cmd/smithctl

# Lint, test and build the Docker images exactly as CI does
make docker-lint
make docker-test
make docker-build
```

### Configuration

Create a `.env` file based on `.env.example`:

```bash
# Server
PORT=8080
API_KEYS=your-api-key-here

# Database
DB_TYPE=sqlite
DB_PATH=./data/smithd.db

# S3
S3_BUCKET=your-bucket
S3_REGION=us-east-1
AWS_ACCESS_KEY_ID=your-key
AWS_SECRET_ACCESS_KEY=your-secret

# Gitops
GITOPS_REPO=git@github.com:org/gitops.git
GITOPS_SSH_KEY_PATH=/path/to/ssh/key

# Web UI (optional). Enables /ui; must only be reachable through an
# authenticating proxy that sets this header with the user's email.
UI_USER_HEADER=X-Auth-Request-Email
UI_ALLOWED_USERS=@example.com
# Serve the UI on its own listener instead of the API port, e.g. on
# localhost behind an oauth2-proxy sidecar (the Helm chart's uiProxy)
UI_LISTEN_ADDR=127.0.0.1:8081

# Login with GitHub (optional), see docs/login-with-github.md. People log
# in to smithctl and the web UI (which then needs no proxy) with GitHub.
GITHUB_CLIENT_ID=Iv23liExample
GITHUB_CLIENT_SECRET=your-client-secret
GITHUB_ALLOWED_ORG=your-org          # and/or GITHUB_ALLOWED_USERS=octocat
SESSION_TTL=12h

# Slack notifications (optional), see below
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
FLUX_KUSTOMIZATION_NAME=apps
UI_PUBLIC_URL=https://smithui.example.com
```

### Login with GitHub

With a GitHub App (or OAuth App) configured, people sign in with their
GitHub account: `smithctl login` uses the device flow, the web UI GitHub's
sign-in page, and smithd records who deployed. smithd checks the user
against `GITHUB_ALLOWED_USERS` and/or membership of `GITHUB_ALLOWED_ORG`
(and `GITHUB_ALLOWED_TEAM`), then issues its own short-lived session. API
keys keep working for CI. See [docs/login-with-github.md](docs/login-with-github.md)
for creating the app and configuring smithd.

### Slack notifications

With `SLACK_WEBHOOK_URL` set to a Slack incoming webhook, smithd posts to
its channel as each deployment progresses:

1. **Deployment requested**: the deployment was created, by a user or an
   auto-deploy policy.
2. **Committed to gitops**: the manifests are pushed to the gitops repo.
3. **Deployed** once Flux has applied a revision containing that commit, or
   **Deployment failed** if Flux reports an error for it or has not applied
   it within 10 minutes. A deployment that fails before reaching the gitops
   repo is reported as failed too.

The last message needs `FLUX_KUSTOMIZATION_NAME`, the Flux Kustomization
that applies `environments/<env>/apps`, and read access to it (the Helm
chart's RBAC). With `UI_PUBLIC_URL`, messages link to the deployment in the
web UI. For the Slack app's icon, use
[docs/brand/deploysmith-slack-icon.png](docs/brand/deploysmith-slack-icon.png).
Note that Flux reports a Kustomization as applied without waiting for
the workloads to become healthy unless the Kustomization sets `wait: true`.

Slack also gets **New version** when a version is published, **New
application registered**, and the same deployment messages worded as
**Rollback started / committed / done / failed** when the deployed version
is older than the one that last succeeded in that environment.

### Push notifications

The web UI can send the same notifications as Web Push, including to an
iPhone (iOS 16.4 or later) where the UI is added to the Home Screen. Generate
a key pair once with `smithd vapid-keys` and set `VAPID_PUBLIC_KEY` and
`VAPID_PRIVATE_KEY` (the chart's `secrets.vapid`); `VAPID_SUBJECT` defaults
to `UI_PUBLIC_URL`. Every signed-in user then gets an **Enable notifications**
button in the UI header, and each device that taps it receives every event
Slack does. On iOS the button only works from the Home Screen app.

### Deploy status on GitHub

With a GitHub App (`GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY`), smithd also
reports each deployment as a `deploysmith/deploy-<env>` check run on the
commit the version was built from, which `forge init` detects in GitHub
Actions. See [docs/github-checks.md](docs/github-checks.md).

### Running

```bash
# Source your config
export $(cat .env | xargs)

# Run smithd
./bin/smithd
```

### Testing

```bash
# Test smithd server
./test-server.sh      # Server health and auth
./test-apps-api.sh    # Application management
./test-versions-api.sh # Version management
./test-deploy-api.sh  # Deployment API
./test-policies-api.sh # Policy management

# Test forge CLI
./test-forge.sh       # forge commands and integration
```

## Using forge in CI/CD

The `forge` CLI tool is designed to be used in CI/CD pipelines to package and publish versions.

### Quick Start

**One-time setup (recommended):**
```bash
# Bind your repository to an application (using binary or container)
forge app-bind --app my-app

# Or using container:
docker run --rm -v $(pwd):/workspace -w /workspace \
  -e SMITHD_URL=https://smithd.example.com \
  -e SMITHD_API_KEY=sk_live_abc123 \
  ghcr.io/sorenmh/forge:latest app-bind --app my-app
```

**CI/CD Pipeline:**
```bash
# 1. Initialize a new version draft (no --app needed after binding!)
forge init --version "${GIT_SHA}-${BUILD_NUMBER}"

# 2. Upload your Kubernetes manifest files
forge upload manifests/

# 3. Publish the version (triggers auto-deploy if policies match)
forge publish --version "${GIT_SHA}-${BUILD_NUMBER}"
```

### Configuration

Set these environment variables in your CI/CD pipeline:

```bash
export SMITHD_URL=https://smithd.example.com
export SMITHD_API_KEY=sk_live_abc123
```

### GitHub Actions Example

```yaml
name: Deploy
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

      - name: Deploy with forge
        run: |
          VERSION="${GITHUB_SHA:0:7}-${GITHUB_RUN_NUMBER}"

          # App binding (run once or commit .deploysmith/app.yaml)
          forge app-bind --app my-app

          # Generate manifests
          helm template my-app ./charts/my-app > manifests/deployment.yaml

          # Deploy
          forge init --version "$VERSION" --git-sha "$GITHUB_SHA"
          forge upload manifests/
          forge publish --version "$VERSION"
```

See [docs/forge.md](docs/forge.md) for complete forge documentation.

## API Documentation

See [docs/specs/smithd-api-spec.md](docs/specs/smithd-api-spec.md) for complete API documentation.

### Example: Register an Application

```bash
curl -X POST http://localhost:8080/api/v1/apps \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-app"}'
```

### Example: List Applications

```bash
curl http://localhost:8080/api/v1/apps \
  -H "Authorization: Bearer your-api-key"
```

## Development

### Project Structure

```
.
├── cmd/
│   ├── smithd/        # Server main
│   ├── forge/         # CI tool
│   └── smithctl/      # CLI tool (planned)
├── internal/
│   ├── smithd/
│   │   ├── api/       # HTTP handlers
│   │   ├── config/    # Configuration
│   │   ├── db/        # Database layer
│   │   ├── gitops/    # GitOps repository integration
│   │   ├── models/    # Data models
│   │   ├── storage/   # S3 storage layer
│   │   └── store/     # Data access layer
│   └── forge/
│       ├── cmd/       # Cobra commands
│       └── client/    # smithd API client
├── docs/specs/        # Specifications
└── tests/             # Tests
```

### Running Tests

```bash
go test ./...
```

### Database Schema

SQLite database with tables:

- `applications` - Registered applications
- `versions` - Application versions
- `deployments` - Deployment history
- `policies` - Auto-deployment policies

See [docs/specs/smithd-database-schema.md](docs/specs/smithd-database-schema.md) for details.

## Documentation

- [forge CLI Tool](docs/forge.md) - **Complete forge documentation with examples**
- [smithd API Spec](docs/specs/smithd-api-spec.md) - REST API documentation
- [Vision](docs/specs/vision.md) - Product vision and requirements
- [Implementation Plan](docs/specs/implementation-plan.md) - Development roadmap
- [Database Schema](docs/specs/smithd-database-schema.md) - Database design
- [smithctl Spec](docs/specs/smithctl-spec.md) - CLI tool specification
- [forge Spec](docs/specs/forge-spec.md) - *Legacy forge specification*

## Progress Tracking

All progress is tracked in:

- [docs/specs/implementation-plan.md](docs/specs/implementation-plan.md) - Detailed milestones with checkboxes
- Git commits with clear messages
- Test scripts validate functionality at each phase

## License

Licensed under the O'Saasy License - see [LICENSE.md](LICENSE.md) for details.

You may freely use, modify, and distribute this software. However, you may not offer it to third parties as a competing hosted, managed, or SaaS product where the primary value is the functionality of DeploySmith itself.
