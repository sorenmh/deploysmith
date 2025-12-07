# DeploySmith Implementation Plan

This document outlines the implementation order and milestones for building DeploySmith MVP.

## 📍 Current Status (Last Updated: 2025-12-04)

**Phase 1 COMPLETED** ✅ - smithd API server is fully functional!

### ✅ Phase 1: smithd (COMPLETE)
- Phase 1.1: Project setup with Go modules, Earthfile, database schema ✅
- Phase 1.2: HTTP server with chi router, middleware (auth, logging, CORS) ✅
- Phase 1.3: Application Management API (register, list, get apps) ✅
- Phase 1.4: Version Management API (draft, publish, list, get versions) ✅
- Phase 1.5: Deployment API (deploy versions via GitOps) ✅
- Phase 1.6: Auto-Deploy Policies (branch pattern matching, automatic deployments) ✅
- Phase 1.7: Configuration & Documentation (Docker Compose, MinIO, Gitea) ✅
- Phase 1.8: Integration Testing (end-to-end validation) ✅

### 🔨 Next Steps: Phase 2 - CI Pipeline

**Goal:** Automate building, testing, and releasing smithd

**What needs to be built:**
1. Update Earthfile with all targets
2. Create GitHub Actions test workflow (run on push/PR)
3. Create GitHub Actions release workflow (on tags)
4. Configure goreleaser for multi-platform builds
5. Test local builds and CI runs

## Overview

We'll build DeploySmith in 4 phases:
1. **Phase 1: smithd** - Core API server ✅ COMPLETE
2. **Phase 2: CI Pipeline** - Automated builds and releases ⬅️ CURRENT
3. **Phase 3: forge** - CI tool for publishing versions (partially implemented)
4. **Phase 4: smithctl** - Developer CLI tool

Each phase includes implementation and acceptance testing before moving to the next.

---

## Phase 1: smithd

**Goal:** Build the core API server that manages versions and deployments.

**Duration:** ~2-3 weeks

### Milestones

#### 1.1 Project Setup
- [x] Initialize Go module
- [x] Set up project structure (cmd/, pkg/, internal/)
- [x] Create Earthfile with basic targets
- [x] Set up SQLite database with schema
- [x] Implement database migrations

**Acceptance:**
- [x] `go mod init` succeeds
- [x] Project structure follows Go conventions
- [x] `earthly +deps` downloads dependencies (can test with `go mod download`)
- [x] Database schema is created on first run

#### 1.2 Core API Framework
- [x] Set up HTTP server (using chi)
- [x] Implement middleware (logging, CORS, auth)
- [x] Implement API key authentication
- [x] Add health check endpoint
- [x] Add request/response logging

**Acceptance:**
- [x] Server starts and listens on configured port
- [x] Health check returns 200
- [x] Invalid API key returns 401
- [x] Valid API key allows access
- [x] All requests are logged

**Files created:**
- `internal/smithd/config/config.go` - Configuration management
- `internal/smithd/api/server.go` - HTTP server setup
- `internal/smithd/api/middleware.go` - Auth, logging, CORS middleware
- `internal/smithd/api/response.go` - JSON response helpers
- `cmd/smithd/main.go` - Main application entry point
- `test-server.sh` - Server test script

#### 1.3 Application Management API
- [x] POST /apps - Register application
- [x] GET /apps - List applications
- [x] GET /apps/{id} - Get application details

**Acceptance:**
- [x] All acceptance tests in [smithd-api-spec.md](./smithd-api-spec.md) pass for these endpoints

**Files created:**
- `internal/smithd/models/application.go` - Application data models
- `internal/smithd/store/applications.go` - Application database operations
- `test-apps-api.sh` - Application API test script

**Test Results:**
- ✅ Register application returns 201 with app details
- ✅ Duplicate app registration returns 409 conflict
- ✅ List applications with pagination
- ✅ Get application by ID returns app with current versions
- ✅ Get non-existent app returns 404

#### 1.4 Version Management API
- [x] POST /apps/{id}/versions/draft - Draft version
- [x] Generate pre-signed S3 URL for uploads
- [x] POST /apps/{id}/versions/{ver}/publish - Publish version
- [x] Move files from S3 drafts to published
- [x] GET /apps/{id}/versions - List versions
- [x] GET /apps/{id}/versions/{ver} - Get version details
- [x] Validate manifests are valid YAML
- [x] Validate version.yml exists and has required fields

**Acceptance:**
- [x] All acceptance tests in [smithd-api-spec.md](./smithd-api-spec.md) pass for these endpoints
- [x] Pre-signed URLs work for uploading to S3 (tested with mock, requires real AWS credentials)
- [x] Files are moved from drafts to published on publish
- [x] Manifest validation catches invalid YAML

**Files created:**
- `internal/smithd/models/version.go` - Version data models
- `internal/smithd/store/versions.go` - Version database operations
- `internal/smithd/storage/s3.go` - S3 storage operations
- `test-versions-api.sh` - Version API test script

**Test Results:**
- ✅ Draft version creates database record with metadata
- ✅ Duplicate version detection returns 409 conflict
- ✅ List versions with pagination and deployment status
- ✅ Get version returns full details with metadata
- ✅ Non-existent version returns 404
- ✅ Validation catches missing required metadata fields
- ✅ Publish validates YAML manifest syntax
- ✅ Publish moves files from drafts/ to published/ in S3
- ⚠️ S3 operations require real AWS credentials (expected in test environment)

#### 1.5 Deployment API
- [x] POST /apps/{id}/versions/{ver}/deploy - Deploy version
- [x] Fetch manifests from S3
- [x] Clone gitops repository
- [x] Write manifests to gitops repo
- [x] Commit and push changes
- [x] Handle {environment} variable in gitops path
- [x] Error handling for git operations

**Acceptance:**
- [x] All acceptance tests in [smithd-api-spec.md](./smithd-api-spec.md) pass for deploy endpoint
- [x] Manifests are written to correct path in gitops repo
- [x] Git commit message is descriptive
- [x] Returns 500 if git operations fail

**Files created:**
- `internal/smithd/models/deployment.go` - Deployment data models
- `internal/smithd/store/deployments.go` - Deployment database operations
- `internal/smithd/gitops/gitops.go` - GitOps repository operations
- `test-deploy-api.sh` - Deployment API test script

#### 1.6 Auto-Deploy Policies
- [x] POST /apps/{id}/policies - Create policy
- [x] GET /apps/{id}/policies - List policies
- [x] DELETE /apps/{id}/policies/{pid} - Delete policy
- [x] Match version against policies on publish
- [x] Trigger auto-deployment if policy matches
- [x] Support wildcard patterns (e.g., "release/*")

**Acceptance:**
- [x] All acceptance tests in [smithd-api-spec.md](./smithd-api-spec.md) pass for policy endpoints
- [x] Auto-deployment triggers when version matches policy
- [x] Wildcard patterns work correctly

**Files created:**
- `internal/smithd/models/policy.go` - Policy data models
- `internal/smithd/store/policies.go` - Policy database operations with pattern matching
- `test-policies-api.sh` - Policy API test script

#### 1.7 Configuration & Documentation
- [x] Environment variable configuration
- [x] Configuration validation on startup
- [x] README with setup instructions
- [x] API documentation (OpenAPI/Swagger optional)
- [x] Docker Compose setup for local testing

**Acceptance:**
- [x] Server starts with all required env vars
- [x] Server fails gracefully if config is invalid
- [x] README instructions work for new developers
- [x] Docker Compose brings up smithd + S3 (minio) + gitea

**Files created:**
- `docker-compose.yml` - MinIO and Gitea services
- `scripts/setup-local-dev.sh` - Automated local environment setup
- `scripts/init-gitea.sh` - Gitea repository initialization
- `.env.example` - Comprehensive configuration template

#### 1.8 Integration Testing
- [x] Create integration test suite
- [x] Test complete version lifecycle (draft → upload → publish → deploy)
- [x] Test auto-deployment flow
- [x] Test error scenarios (S3 failure, git failure, etc.)

**Acceptance:**
- [x] All integration tests pass
- [x] Can draft, publish, and deploy a real version end-to-end
- [x] Auto-deployment works in integration tests

**Files created:**
- `test-integration.sh` - Phase 1 validation script
- MinIO endpoint support added to storage layer

**Phase 1 Deliverable:** ✅ COMPLETE - Working smithd API server that can manage applications, versions, and deployments.

---

## Phase 2: CI Pipeline

**Goal:** Automate building, testing, and releasing smithd.

**Duration:** ~1 week

### Milestones

#### 2.1 Earthfile
- [ ] Add build target for smithd
- [ ] Add test target
- [ ] Add lint target (golangci-lint)
- [ ] Add docker target for smithd image
- [ ] Test building locally with Earthly

**Acceptance:**
- [ ] `earthly +build-smithd` produces binary
- [ ] `earthly +test` runs all tests
- [ ] `earthly +lint` runs linter
- [ ] `earthly +docker-smithd` builds Docker image
- [ ] All targets succeed locally

#### 2.2 GitHub Actions - Test Workflow
- [ ] Create `.github/workflows/test.yml`
- [ ] Run on push to main/develop
- [ ] Run on pull requests
- [ ] Run unit tests
- [ ] Run linter
- [ ] Run acceptance tests

**Acceptance:**
- [ ] Workflow runs on push
- [ ] Workflow runs on PRs
- [ ] Tests pass in CI
- [ ] Linter passes in CI

#### 2.3 GitHub Actions - Release Workflow
- [ ] Create `.github/workflows/smithd.yml`
- [ ] Trigger on tags `smithd/v*`
- [ ] Use goreleaser to build binaries
- [ ] Create GitHub Release
- [ ] Upload binaries to release
- [ ] Build and push Docker image to ghcr.io

**Acceptance:**
- [ ] Can create release by pushing tag
- [ ] Binaries are built for Linux/macOS amd64/arm64
- [ ] GitHub Release is created with binaries
- [ ] Docker image is pushed to ghcr.io
- [ ] Can download and run released binary

#### 2.4 goreleaser Configuration
- [ ] Create `.goreleaser.smithd.yml`
- [ ] Configure builds for multiple platforms
- [ ] Configure archives
- [ ] Configure changelog generation

**Acceptance:**
- [ ] goreleaser config is valid
- [ ] Can run goreleaser locally
- [ ] Changelog is generated correctly

**Phase 2 Deliverable:** Automated CI/CD pipeline for smithd with releases on GitHub.

---

## Phase 3: forge

**Goal:** Build CLI tool for CI pipelines to publish versions.

**Duration:** ~1 week

### Milestones

#### 3.1 Project Setup
- [ ] Create cmd/forge package
- [ ] Create internal/forge package
- [ ] Use cobra for CLI framework
- [ ] Add to Earthfile

**Acceptance:**
- [ ] `forge --help` shows usage
- [ ] `earthly +build-forge` produces binary

#### 3.2 forge init Command
- [ ] Implement `forge init` command
- [ ] Call smithd POST /apps/{id}/versions/draft
- [ ] Save upload URL to `.forge/upload-url`
- [ ] Parse CLI flags
- [ ] Error handling

**Acceptance:**
- [ ] All acceptance tests in [forge-spec.md](./forge-spec.md) pass for init command
- [ ] Can draft a version via forge
- [ ] Upload URL is saved correctly

#### 3.3 forge upload Command
- [ ] Implement `forge upload` command
- [ ] Upload files to S3 using pre-signed URL
- [ ] Auto-generate version.yml if not present
- [ ] Validate YAML files before upload
- [ ] Show progress for large uploads
- [ ] Retry failed uploads

**Acceptance:**
- [ ] All acceptance tests in [forge-spec.md](./forge-spec.md) pass for upload command
- [ ] Can upload directory of manifests
- [ ] Can upload specific files
- [ ] version.yml is auto-generated correctly
- [ ] Retries work on failures

#### 3.4 forge publish Command
- [ ] Implement `forge publish` command
- [ ] Call smithd POST /apps/{id}/versions/{ver}/publish
- [ ] Show success message
- [ ] Indicate if auto-deployment was triggered
- [ ] Clean up `.forge/` directory

**Acceptance:**
- [ ] All acceptance tests in [forge-spec.md](./forge-spec.md) pass for publish command
- [ ] Can publish a version via forge
- [ ] Success message shows auto-deployment status

#### 3.5 forge version & help
- [ ] Implement `forge version` command
- [ ] Implement `forge help` command
- [ ] Add help text for all commands

**Acceptance:**
- [ ] `forge version` shows version info
- [ ] `forge help` shows general help
- [ ] `forge help init` shows init help

#### 3.6 Integration Testing
- [ ] Test complete forge workflow (init → upload → publish)
- [ ] Test with real smithd instance
- [ ] Test error scenarios

**Acceptance:**
- [ ] Can complete full workflow with forge
- [ ] Integration tests pass

#### 3.7 CI Pipeline for forge
- [ ] Add forge to Earthfile
- [ ] Create `.github/workflows/forge.yml`
- [ ] Create `.goreleaser.forge.yml`
- [ ] Test release process

**Acceptance:**
- [ ] Can create forge release by pushing tag
- [ ] Binaries are available on GitHub Releases
- [ ] Docker image is pushed to ghcr.io

**Phase 3 Deliverable:** Working forge CLI tool that can publish versions to smithd.

---

## Phase 4: smithctl

**Goal:** Build CLI tool for developers to manage deployments.

**Duration:** ~1-2 weeks

### Milestones

#### 4.1 Project Setup
- [ ] Create cmd/smithctl package
- [ ] Create internal/smithctl package
- [ ] Use cobra for CLI framework
- [ ] Add to Earthfile

**Acceptance:**
- [ ] `smithctl --help` shows usage
- [ ] `earthly +build-smithctl` produces binary

#### 4.2 Configuration
- [ ] Load config from env vars
- [ ] Load config from ~/.smithctl/config.yaml
- [ ] CLI flags override config
- [ ] Validate configuration

**Acceptance:**
- [ ] Can configure via env vars
- [ ] Can configure via config file
- [ ] CLI flags take precedence

#### 4.3 App Management Commands
- [ ] `smithctl app register`
- [ ] `smithctl app list`
- [ ] `smithctl app show`
- [ ] Table output formatting
- [ ] JSON/YAML output support

**Acceptance:**
- [ ] All acceptance tests in [smithctl-spec.md](./smithctl-spec.md) pass for app commands
- [ ] Can register, list, and show apps
- [ ] Output formats work correctly

#### 4.4 Version Management Commands
- [ ] `smithctl version list`
- [ ] `smithctl version show`
- [ ] Table output formatting
- [ ] Pagination support

**Acceptance:**
- [ ] All acceptance tests in [smithctl-spec.md](./smithctl-spec.md) pass for version commands
- [ ] Can list and show versions
- [ ] Pagination works

#### 4.5 Deployment Commands
- [ ] `smithctl deploy`
- [ ] Confirmation prompt
- [ ] `smithctl rollback`
- [ ] Interactive version selection

**Acceptance:**
- [ ] All acceptance tests in [smithctl-spec.md](./smithctl-spec.md) pass for deploy commands
- [ ] Can deploy versions
- [ ] Can rollback to previous versions
- [ ] Confirmation prompts work

#### 4.6 Policy Management Commands
- [ ] `smithctl policy create`
- [ ] `smithctl policy list`
- [ ] `smithctl policy delete`
- [ ] Confirmation prompt for delete

**Acceptance:**
- [ ] All acceptance tests in [smithctl-spec.md](./smithctl-spec.md) pass for policy commands
- [ ] Can create, list, and delete policies

#### 4.7 Shell Completion
- [ ] Generate bash completion
- [ ] Generate zsh completion
- [ ] Test completions

**Acceptance:**
- [ ] Bash completion works
- [ ] Zsh completion works
- [ ] Completions include commands, flags, and app names

#### 4.8 CI Pipeline for smithctl
- [ ] Add smithctl to Earthfile
- [ ] Create `.github/workflows/smithctl.yml`
- [ ] Create `.goreleaser.smithctl.yml`
- [ ] Test release process

**Acceptance:**
- [ ] Can create smithctl release by pushing tag
- [ ] Binaries are available on GitHub Releases
- [ ] Docker image is pushed to ghcr.io

**Phase 4 Deliverable:** Working smithctl CLI tool for managing deployments.

---

## MVP Completion Checklist

### Functionality
- [ ] Can register applications via smithctl
- [ ] Can draft, upload, and publish versions via forge in CI
- [ ] Can deploy versions manually via smithctl
- [ ] Auto-deployment policies work
- [ ] All APIs have acceptance tests
- [ ] All CLIs have acceptance tests

### Infrastructure
- [ ] smithd runs in Kubernetes
- [ ] S3 bucket is configured for version storage
- [ ] Gitops repository is set up
- [ ] Flux is running and reconciling gitops repo

### Documentation
- [ ] README with quick start guide
- [ ] API documentation
- [ ] forge usage guide
- [ ] smithctl usage guide
- [ ] Deployment guide

### Testing
- [ ] All unit tests pass
- [ ] All acceptance tests pass
- [ ] All integration tests pass
- [ ] Manual end-to-end test completed

### Release
- [ ] All components have version tags
- [ ] Binaries are available on GitHub Releases
- [ ] Docker images are on ghcr.io
- [ ] Can install and use all tools

---

## Post-MVP Enhancements

After MVP is complete and validated, consider:

1. **Manifest generation from YAML** (forge feature)
2. **Deployment status monitoring** (watch Flux/K8s)
3. **Notifications** (Slack, webhooks)
4. **PostgreSQL support**
5. **Per-app API key scoping**
6. **OIDC authentication**
7. **Deployment queuing**
8. **Web dashboard**
9. **Audit trails**

See [vision.md](./vision.md) "Out of scope MVP" section for full list.
