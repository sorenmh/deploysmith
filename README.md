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

```bash
# Build smithd server
go build -o bin/smithd ./cmd/smithd

# Build forge CI tool
go build -o bin/forge ./cmd/forge

# Or use Earthly
earthly +build-smithd
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
```

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

### Basic Workflow

```bash
# 1. Initialize a new version draft
forge init \
  --app my-app \
  --version "${GIT_SHA}-${BUILD_NUMBER}" \
  --git-sha "${GIT_SHA}" \
  --git-branch "${GIT_BRANCH}" \
  --git-committer "${GIT_AUTHOR_EMAIL}" \
  --build-number "${BUILD_NUMBER}"

# 2. Upload your Kubernetes manifest files
forge upload manifests/

# 3. Publish the version (triggers auto-deploy if policies match)
forge publish \
  --app my-app \
  --version "${GIT_SHA}-${BUILD_NUMBER}"
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
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Download forge
        run: |
          curl -L https://github.com/org/deploysmith/releases/download/v1.0.0/forge-linux-amd64 -o forge
          chmod +x forge
          sudo mv forge /usr/local/bin/

      - name: Generate manifests
        run: |
          # Your manifest generation (Helm, Kustomize, etc.)
          helm template my-app ./charts/my-app > manifests/deployment.yaml

      - name: Publish version
        env:
          SMITHD_URL: ${{ secrets.SMITHD_URL }}
          SMITHD_API_KEY: ${{ secrets.SMITHD_API_KEY }}
        run: |
          VERSION="${GITHUB_SHA:0:7}-${GITHUB_RUN_NUMBER}"
          forge init --app my-app --version "$VERSION" \
            --git-sha "$GITHUB_SHA" \
            --git-branch "${GITHUB_REF#refs/heads/}" \
            --git-committer "$GITHUB_ACTOR@users.noreply.github.com" \
            --build-number "$GITHUB_RUN_NUMBER"
          forge upload manifests/
          forge publish --app my-app --version "$VERSION"
```

See [docs/specs/forge-spec.md](docs/specs/forge-spec.md) for complete forge documentation.

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

- [Vision](docs/specs/vision.md) - Product vision and requirements
- [Implementation Plan](docs/specs/implementation-plan.md) - Development roadmap
- [smithd API Spec](docs/specs/smithd-api-spec.md) - REST API documentation
- [Database Schema](docs/specs/smithd-database-schema.md) - Database design
- [forge Spec](docs/specs/forge-spec.md) - CI tool specification
- [smithctl Spec](docs/specs/smithctl-spec.md) - CLI tool specification

## Progress Tracking

All progress is tracked in:
- [docs/specs/implementation-plan.md](docs/specs/implementation-plan.md) - Detailed milestones with checkboxes
- Git commits with clear messages
- Test scripts validate functionality at each phase

## License

TBD - Intended to be open source
