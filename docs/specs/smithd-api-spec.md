# smithd API Specification

This document defines the REST API for smithd, the DeploySmith server component.

## Overview

smithd is a REST API server that manages application versions and deployments. It:
- Stores version metadata in a database (SQLite for MVP)
- Manages version artifacts in S3 (drafts and published)
- Writes manifests to a gitops repository for Flux to reconcile
- Supports auto-deployment policies based on git patterns

## API Endpoints

All endpoints require authentication via `X-API-Key` header.

Base URL: `https://smithd.example.com/api/v1`

---

### 1. Register Application

Register a new application to be managed by DeploySmith.

**Endpoint:** `POST /apps`

**Request Body:**
```json
{
  "name": "my-api-service"
}
```

**Response:** `201 Created`
```json
{
  "id": "app-123",
  "name": "my-api-service",
  "createdAt": "2025-01-15T10:30:00Z"
}
```

**Acceptance Test:**
- [ ] Returns 201 when app is successfully registered
- [ ] Returns 400 if name is missing or invalid
- [ ] Returns 409 if app with same name already exists
- [ ] Returns 401 if API key is missing or invalid
- [ ] App is stored in database with correct fields

---

### 2. List Applications

List all registered applications.

**Endpoint:** `GET /apps`

**Query Parameters:**
- `limit` (optional): Max results, default 50, max 100
- `offset` (optional): Pagination offset, default 0

**Response:** `200 OK`
```json
{
  "apps": [
    {
      "id": "app-123",
      "name": "my-api-service",
      "createdAt": "2025-01-15T10:30:00Z"
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

**Acceptance Test:**
- [ ] Returns 200 with list of apps
- [ ] Returns empty array when no apps exist
- [ ] Pagination works correctly with limit/offset
- [ ] Returns 401 if API key is missing or invalid

---

### 3. Get Application

Get details for a specific application.

**Endpoint:** `GET /apps/{appId}`

**Response:** `200 OK`
```json
{
  "id": "app-123",
  "name": "my-api-service",
  "createdAt": "2025-01-15T10:30:00Z",
  "currentVersion": {
    "staging": "42540c4-123",
    "production": "a1b2c3d-120"
  }
}
```

**Acceptance Test:**
- [ ] Returns 200 with app details
- [ ] Returns 404 if app doesn't exist
- [ ] Shows current deployed version per environment
- [ ] Returns 401 if API key is missing or invalid

---

### 4. Draft Version

Create a new draft version and get a pre-signed S3 URL for uploading manifests.

**Endpoint:** `POST /apps/{appId}/versions/draft`

**Request Body:**
```json
{
  "versionId": "42540c4-123",
  "metadata": {
    "gitSha": "42540c4abc123",
    "gitBranch": "main",
    "gitCommitter": "john@example.com",
    "buildNumber": "123",
    "timestamp": "2025-01-15T10:30:00Z"
  }
}
```

**Response:** `201 Created`
```json
{
  "versionId": "42540c4-123",
  "uploadUrl": "https://s3.amazonaws.com/bucket/drafts/my-api-service/42540c4-123?X-Amz-Signature=...",
  "uploadExpires": "2025-01-15T10:35:00Z",
  "status": "draft"
}
```

**Acceptance Test:**
- [x] Returns 201 with pre-signed S3 URL
- [x] Pre-signed URL expires in 5 minutes
- [x] Pre-signed URL allows PUT operations to drafts prefix
- [x] Version is stored in database with status=draft
- [x] Returns 409 if versionId already exists
- [x] Returns 400 if metadata is invalid
- [x] Returns 404 if app doesn't exist
- [x] Returns 401 if API key is missing or invalid

---

### 5. Publish Version

Publish a drafted version, making it immutable and available for deployment.

**Endpoint:** `POST /apps/{appId}/versions/{versionId}/publish`

**Request Body:**
```json
{}
```

**Response:** `200 OK`
```json
{
  "versionId": "42540c4-123",
  "status": "published",
  "publishedAt": "2025-01-15T10:35:00Z",
  "manifestFiles": [
    "deployment.yaml",
    "service.yaml",
    "ingress.yaml",
    "version.yml"
  ]
}
```

**Acceptance Test:**
- [x] Returns 200 when version is successfully published
- [x] Moves files from S3 drafts/ to published/ prefix
- [x] Deletes files from drafts/ after successful move
- [x] Updates version status in database to "published"
- [x] Always validates manifests are valid YAML
- [x] Validates version.yml exists and has required fields (validates all YAML files)
- [x] Returns 404 if app or version doesn't exist
- [x] Returns 409 if version is already published
- [x] Returns 400 if no manifest files uploaded
- [x] Returns 400 if manifest validation fails
- [x] Returns 401 if API key is missing or invalid
- [ ] Triggers auto-deployment if matching policy exists (Phase 1.6)

---

### 6. List Versions

List all versions for an application.

**Endpoint:** `GET /apps/{appId}/versions`

**Query Parameters:**
- `status` (optional): Filter by status (draft, published)
- `limit` (optional): Max results, default 50, max 100
- `offset` (optional): Pagination offset, default 0

**Response:** `200 OK`
```json
{
  "versions": [
    {
      "versionId": "42540c4-123",
      "status": "published",
      "createdAt": "2025-01-15T10:30:00Z",
      "publishedAt": "2025-01-15T10:35:00Z",
      "metadata": {
        "gitSha": "42540c4abc123",
        "gitBranch": "main",
        "gitCommitter": "john@example.com",
        "buildNumber": "123"
      },
      "deployedTo": ["staging"]
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

**Acceptance Test:**
- [x] Returns 200 with list of versions
- [x] Returns empty array when no versions exist
- [x] Pagination works correctly with limit/offset
- [ ] Filter by status works correctly (not implemented yet)
- [x] Shows which environments version is deployed to
- [x] Versions sorted by createdAt descending (newest first)
- [x] Returns 404 if app doesn't exist
- [x] Returns 401 if API key is missing or invalid

---

### 7. Get Version

Get details for a specific version.

**Endpoint:** `GET /apps/{appId}/versions/{versionId}`

**Response:** `200 OK`
```json
{
  "versionId": "42540c4-123",
  "status": "published",
  "createdAt": "2025-01-15T10:30:00Z",
  "publishedAt": "2025-01-15T10:35:00Z",
  "metadata": {
    "gitSha": "42540c4abc123",
    "gitBranch": "main",
    "gitCommitter": "john@example.com",
    "buildNumber": "123",
    "timestamp": "2025-01-15T10:30:00Z"
  },
  "manifestFiles": [
    "deployment.yaml",
    "service.yaml",
    "ingress.yaml",
    "version.yml"
  ],
  "deployedTo": ["staging"]
}
```

**Acceptance Test:**
- [x] Returns 200 with version details
- [x] Returns 404 if app or version doesn't exist
- [x] Shows list of manifest files (for published versions)
- [x] Shows which environments version is deployed to
- [x] Returns 401 if API key is missing or invalid

---

### 8. Deploy Version

Deploy a specific version to an environment.

**Endpoint:** `POST /apps/{appId}/versions/{versionId}/deploy`

**Request Body:**
```json
{
  "environment": "staging"
}
```

**Response:** `202 Accepted`
```json
{
  "deploymentId": "deploy-456",
  "versionId": "42540c4-123",
  "environment": "staging",
  "status": "pending",
  "startedAt": "2025-01-15T10:40:00Z"
}
```

**Acceptance Test:**
- [ ] Returns 202 when deployment is initiated
- [ ] Returns 404 if app or version doesn't exist
- [ ] Returns 400 if version is not published
- [ ] Returns 400 if environment is invalid
- [ ] Fetches manifests from S3 published prefix
- [ ] Writes manifests to gitops repo at correct path
- [ ] Replaces {environment} in gitopsPath with actual environment
- [ ] Commits changes to gitops repo with descriptive message
- [ ] Pushes commit to gitops repo
- [ ] Updates deployment status in database
- [ ] Returns 500 if gitops repo is unreachable
- [ ] Returns 401 if API key is missing or invalid

---

### 9. Create Auto-Deploy Policy

Create an auto-deployment policy for an application.

**Endpoint:** `POST /apps/{appId}/policies`

**Request Body:**
```json
{
  "name": "auto-deploy-main-to-staging",
  "gitBranchPattern": "main",
  "targetEnvironment": "staging",
  "enabled": true
}
```

**Response:** `201 Created`
```json
{
  "id": "policy-789",
  "name": "auto-deploy-main-to-staging",
  "gitBranchPattern": "main",
  "targetEnvironment": "staging",
  "enabled": true,
  "createdAt": "2025-01-15T11:00:00Z"
}
```

**Acceptance Test:**
- [ ] Returns 201 when policy is created
- [ ] Policy is stored in database
- [ ] Returns 400 if required fields are missing
- [ ] Returns 404 if app doesn't exist
- [ ] gitBranchPattern supports wildcards (e.g., "release/*")
- [ ] Returns 401 if API key is missing or invalid

---

### 10. List Auto-Deploy Policies

List all auto-deployment policies for an application.

**Endpoint:** `GET /apps/{appId}/policies`

**Response:** `200 OK`
```json
{
  "policies": [
    {
      "id": "policy-789",
      "name": "auto-deploy-main-to-staging",
      "gitBranchPattern": "main",
      "targetEnvironment": "staging",
      "enabled": true,
      "createdAt": "2025-01-15T11:00:00Z"
    }
  ],
  "total": 1
}
```

**Acceptance Test:**
- [ ] Returns 200 with list of policies
- [ ] Returns empty array when no policies exist
- [ ] Returns 404 if app doesn't exist
- [ ] Returns 401 if API key is missing or invalid

---

### 11. Delete Auto-Deploy Policy

Delete an auto-deployment policy.

**Endpoint:** `DELETE /apps/{appId}/policies/{policyId}`

**Response:** `204 No Content`

**Acceptance Test:**
- [ ] Returns 204 when policy is deleted
- [ ] Policy is removed from database
- [ ] Returns 404 if app or policy doesn't exist
- [ ] Returns 401 if API key is missing or invalid

---

### 12. Health Check

Check if the service is healthy.

**Endpoint:** `GET /health`

**Response:** `200 OK`
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "checks": {
    "database": "ok",
    "s3": "ok",
    "gitops": "ok"
  }
}
```

**Acceptance Test:**
- [ ] Returns 200 when service is healthy
- [ ] Returns 503 if database is unreachable
- [ ] Returns 503 if S3 is unreachable
- [ ] Returns 503 if gitops repo is unreachable
- [ ] Does not require authentication

---

## Error Responses

All error responses follow this format:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "Application name is required"
  }
}
```

**Error Codes:**
- `invalid_request` - 400 Bad Request
- `unauthorized` - 401 Unauthorized
- `not_found` - 404 Not Found
- `conflict` - 409 Conflict
- `internal_error` - 500 Internal Server Error
- `service_unavailable` - 503 Service Unavailable

---

## Authentication

All endpoints except `/health` require the `X-API-Key` header.

```
X-API-Key: sk_live_abc123def456
```

For MVP, any valid API key has full access to all resources.

---

## Configuration

smithd is configured via environment variables:

```bash
# Server
PORT=8080
API_KEYS=sk_live_abc123,sk_live_def456  # Comma-separated list

# Database
DB_TYPE=sqlite
DB_PATH=/data/smithd.db

# S3
S3_BUCKET=deploysmith-versions
S3_REGION=us-east-1
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...

# Gitops (global configuration for all apps)
GITOPS_REPO=git@github.com:org/gitops.git
GITOPS_SSH_KEY_PATH=/secrets/gitops-ssh-key
GITOPS_USER_NAME=smithd
GITOPS_USER_EMAIL=smithd@deploysmith.io
```

**Note:** smithd manages a single gitops repository configured globally. All applications use this repo. Manifests are written to: `environments/{environment}/apps/{app_name}/`

---

## Database Schema

See [smithd-database-schema.md](./smithd-database-schema.md) for complete schema definition.
