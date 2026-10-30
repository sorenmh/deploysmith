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
  },
  "artifactType": "manifests"
}
```

`artifactType` is optional: `manifests` (the default) or `terraform` for an
OpenTofu bundle. The upload URL is for `manifests.tar.gz` or
`terraform.tar.gz`. Publishing a terraform version validates the bundle (see
[forge](../forge.md#terraform-bundles)) and returns `400 validation_failed`
listing every problem. Deploying a terraform version commits it for
tofu-controller to plan (see [Deploy Version](#8-deploy-version)). Versions in all responses carry their
`artifactType`.

**Response:** `201 Created`
```json
{
  "versionId": "42540c4-123",
  "uploadUrl": "https://s3.amazonaws.com/bucket/drafts/my-api-service/42540c4-123?X-Amz-Signature=...",
  "uploadExpires": "2025-01-15T10:35:00Z",
  "status": "draft",
  "artifactType": "manifests"
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

**Terraform versions** are committed to
`environments/<env>/terraform/<app>/` in two commits: the bundle source in
`src/main/`, then `main.yaml` with a GitRepository pinned to the source commit
and the `Terraform` CR with `approvePlan: ""` (see
[terraform-controller.md](terraform-controller.md)). The deployment is then
`planning` and carries the plan id the controller will report:

```json
{
  "deploymentId": "deploy-457",
  "versionId": "42540c4-123",
  "environment": "production",
  "status": "planning",
  "gitopsCommitSha": "1c0ffee...",
  "startedAt": "2025-01-15T10:40:00Z",
  "terraform": {
    "root": "main",
    "sourceCommitSha": "8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7",
    "planId": "plan-main-8fa3d36e98"
  }
}
```

Terraform deployments go `pending` → `planning` → `awaiting_approval` →
`applying` → `applied`, or end as `failed`, `stale` or `rejected`. Only one
per app, environment and root is active (`pending` to `applying`). A new deploy
replaces a plan that is `planning` or `awaiting_approval`: that deployment
becomes `stale`, and the rewritten CR makes the controller plan the new
version. While a deployment is `applying` or still `pending`, a new deploy is
refused with `409 terraform_applying` or `409 terraform_deploying`; deploy
again once it has finished. Other refusals are `400`: `terraform_disabled`
(no OpenBao), `terraform_not_enabled`, `terraform_not_provisioned`,
`unsupported_opentofu_version` (no runner image for the bundle's pinned
version) and `validation_failed`. Auto-deploy policies never deploy terraform
versions.

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

### 8a. List Deployments

List deployments, newest first, across all apps or filtered.

**Endpoint:** `GET /deployments`

**Query Parameters:**
- `appId` (optional): Only deployments of this application
- `environment` (optional): Only deployments to this environment
- `status` (optional): `pending`, `success` or `failed`; for terraform deployments also `planning`, `awaiting_approval`, `applying`, `applied`, `stale` or `rejected`
- `limit` (optional, default 50, max 100): Number of results
- `offset` (optional, default 0): Pagination offset

**Response:** `200 OK`
```json
{
  "deployments": [
    {
      "id": "deploy-456",
      "appId": "app-123",
      "appName": "my-api-service",
      "versionId": "42540c4-123",
      "environment": "production",
      "status": "success",
      "triggeredBy": "auto-deploy",
      "policyId": "policy-789",
      "policyName": "main-to-production",
      "gitopsCommitSha": "9f2c1e0",
      "startedAt": "2025-01-15T10:40:00Z",
      "completedAt": "2025-01-15T10:40:05Z"
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

`errorMessage` is included for failed deployments (and says which deployment replaced a `stale` one); `policyId`/`policyName` only for auto-deploys; `terraform` only for terraform deployments.

---

### 8b. Get Deployment

**Endpoint:** `GET /deployments/{deploymentId}`

**Response:** `200 OK` with a single deployment object as in List Deployments.

A terraform deployment that has been planned also has its plan in `terraform.plan`, with the full text (listings leave `terraform.plan` out):

```json
{
  "id": "deploy-789",
  "status": "awaiting_approval",
  "terraform": {
    "root": "main",
    "sourceCommitSha": "8fa3d36e98...",
    "planId": "plan-main-8fa3d36e98",
    "plan": {
      "planId": "plan-main-8fa3d36e98",
      "planHash": "5d41402abc4b2a76b9719d911017c592...",
      "plannedAt": "2025-01-15T10:41:00Z",
      "toAdd": 3,
      "toChange": 1,
      "toDestroy": 2,
      "destroys": true,
      "outputChanges": true,
      "resources": {
        "create": ["terraform_data.new"],
        "update": ["terraform_data.web"],
        "replace": ["terraform_data.rep"],
        "destroy": ["terraform_data.with_secret"]
      },
      "text": "OpenTofu used the selected providers to generate the following execution plan...",
      "appliedPlanHash": "5d41402abc4b2a76b9719d911017c592...",
      "appliedDiffered": false
    }
  }
}
```

A terraform deployment also records who decided on its plan (fields are left out while they are not set): `rollback` (the version is older than the one applied; deploying it is a normal deploy with a new plan), `approvedBy` (GitHub login, `api-key`, or `git` for an approval committed outside smithd), `approvedAt`, `destroyConfirmed`, `approvalResetAt` (when smithd committed `approvePlan: ""` again after the apply), `rejectedBy`, `rejectedAt` and `rejectReason`.

`toAdd`, `toChange` and `toDestroy` are the counts of the plan's `Plan:` line (a replacement counts as an add and a destroy); `destroys` is true if the plan destroys or replaces anything. `text` is `tofu show` of the plan with sensitive values masked by OpenTofu. `appliedPlanHash` and `appliedDiffered` are set after the apply: the controller plans again right before applying, and `appliedDiffered` says that this plan differed from the reviewed one. The deployment's `errorMessage` holds the controller's error for a `failed` run.

Returns `404` if the deployment doesn't exist.

---

### 8c. Approve Terraform Plan

**Endpoint:** `POST /deployments/{deploymentId}/approve`

**Request body (optional):**
```json
{ "confirmDestroy": false }
```

Approves the plan of a terraform deployment that is `awaiting_approval`: smithd commits `approvePlan: "<plan id>"` (and nothing else) to the root's Terraform CR in the gitops repo, and the controller applies. The deployment becomes `applying`; once it is `applied` (or `failed`) smithd commits `approvePlan: ""` again. The approver is recorded from the request's identity (the signed-in GitHub login, or `api-key`).

**Response:** `200 OK` with the deployment as in Get Deployment. Approving a deployment that is already `applying` (or `applied` after an approval) is not an error and commits nothing.

Before committing, smithd checks that the plan is still the one that was reviewed: the CR is the deployment's, `status.plan.pending` is the plan id, the revision and `lastPlannedGeneration` are those of the plan, nobody approved another plan, and the plan in the cluster has the stored `planHash`. The controller makes a new plan when it applies, which only has the same id; if that plan differs from the reviewed one, `terraform.plan.appliedDiffered` says so afterwards.

**Errors:**
- `400 destroy_confirmation_required`: the plan destroys or replaces resources and `confirmDestroy` is not `true`; the message lists them.
- `400 not_terraform`, `400 terraform_disabled`, `400 terraform_unavailable` (smithd is not running in a cluster)
- `404` if the deployment doesn't exist
- `409 not_awaiting_approval`: the deployment is in another state (`planning`, `stale`, `failed`, `rejected`, ...)
- `409 plan_not_current`: the plan changed or was replaced since it was made; deploy again, or wait for the new plan and review that

---

### 8d. Reject Terraform Plan

**Endpoint:** `POST /deployments/{deploymentId}/reject`

**Request body (optional):**
```json
{ "reason": "wrong environment" }
```

Rejects the plan of a terraform deployment that is `awaiting_approval`. The deployment becomes `rejected`, with who, when and the reason (`terraform.rejectedBy`, `rejectedAt`, `rejectReason`; `errorMessage` says `Rejected by <who>: <reason>`). Nothing is committed: the CR stays unapproved, and the next deploy of the root replaces the pending plan. Slack, Web Push and the GitHub check runs report it.

**Response:** `200 OK` with the deployment. Rejecting a `rejected` deployment is not an error.

**Errors:** `400 not_terraform`, `400 invalid_request` (reason over 1000 characters), `404`, `409 not_awaiting_approval`, and `409 conflict` if the plan was approved in the repo and is being applied.
### 8c. Plan a Terraform Version (plan-only run)

Show what a draft or published terraform version would change in an
environment, without deploying it. A plan-only run is not a deployment: it
can't be approved or applied, it doesn't count for what runs in the
environment, and it never blocks or changes a deployment of the same root. See
[Plan-Only Runs](terraform-controller.md#plan-only-runs).

**Endpoint:** `POST /apps/{appId}/versions/{versionId}/plan`

**Request Body:**
```json
{
  "environment": "production"
}
```

**Response:** `202 Accepted`, once the run is committed to the `plans` branch
and waits for the controller:
```json
{
  "id": "6a1f3c1e-5c0b-4c43-a1f4-1f0f8f4e9d10",
  "appId": "app-123",
  "appName": "infra",
  "versionId": "42540c4-123",
  "environment": "production",
  "root": "main",
  "status": "planning",
  "triggeredBy": "ci",
  "startedAt": "2025-01-15T10:40:00Z",
  "sourceCommitSha": "8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7",
  "gitopsCommitSha": "1c0ffee...",
  "planId": "plan-plans-8fa3d36e98"
}
```

A run goes `pending` → `planning` → `planned`, or ends as `failed` or
`superseded`. Only one run per app, environment and root is active: a newer
one replaces it, and the older run becomes `superseded` (and its check run
neutral). The refusals are `400`: `terraform_disabled` (no OpenBao),
`terraform_plans_disabled`, `not_terraform`, `terraform_not_enabled`,
`terraform_not_provisioned`, `unsupported_opentofu_version`, `no_bundle` (a
draft whose bundle isn't uploaded yet) and `validation_failed`; and `404` for
an unknown app or version.

**Endpoint:** `GET /plan-runs/{planRunId}`

**Response:** `200 OK` with the run as above. Once `planned` it has the plan,
with the full text (sensitive values masked by OpenTofu); `failed` runs have
`errorMessage` (the controller's `Ready` message and the events of the CR):
```json
{
  "id": "6a1f3c1e-5c0b-4c43-a1f4-1f0f8f4e9d10",
  "status": "planned",
  "completedAt": "2025-01-15T10:41:30Z",
  "plan": {
    "planId": "plan-plans-8fa3d36e98",
    "plannedAt": "2025-01-15T10:41:20Z",
    "toAdd": 3,
    "toChange": 1,
    "toDestroy": 2,
    "destroys": true,
    "resources": {
      "create": ["aws_s3_bucket.logs"],
      "update": ["aws_lambda_function.api"],
      "replace": ["aws_db_instance.main"],
      "destroy": ["aws_sqs_queue.old"]
    },
    "text": "Terraform will perform the following actions: ..."
  }
}
```

**Endpoint:** `GET /apps/{appId}/versions/{versionId}/plan?environment=production`

**Response:** `200 OK` with the newest run of the version for the environment,
or `404` if there is none. Results are kept for 30 days.

If the version was built from a known repository and commit, the run is also
reported as the check run `deploysmith/plan-<env>` on that commit
([github-checks.md](../github-checks.md)). When smithd publishes a terraform
version it starts a run for every environment with Terraform enabled
(`TERRAFORM_AUTO_PLAN`, on by default).

**Local plans.** A signed-in user (a session from `smithctl login`; API keys get
`403 login_required`) can plan files that are not committed anywhere:

**Endpoint:** `POST /apps/{appId}/terraform/local-plans`

**Request Body:**
```json
{
  "environment": "production",
  "contentHash": "3b4f9c0d1e2a5b6c7d8e9f00112233445566778899aabbccddeeff0011223344"
}
```

`contentHash` is the hex SHA-256 of the bundle's files (forge computes it with
`tfbundle.ContentHash`). smithd makes the temporary version
`local-<user>-<first 12 characters of the hash>`, or, for the same files again,
moves the expiry of the existing one.

**Response:** `201 Created`
```json
{
  "versionId": "local-alice-3b4f9c0d1e2a",
  "uploadUrl": "https://s3.example.com/drafts/viino/local-alice-3b4f9c0d1e2a/terraform.tar.gz?X-Amz-...",
  "uploadExpires": "2025-01-15T10:35:00Z",
  "expiresAt": "2025-01-16T10:30:00Z"
}
```

Upload the bundle (`tfbundle.Pack`) to `uploadUrl`, then start the plan with
`POST /apps/{appId}/versions/{versionId}/plan` as above, as the same user.
`403 not_your_local_plan` for another user's version, `400 bundle_mismatch`
if the uploaded files don't have the hash in the version's id,
`400 local_plan_expired`, `400 terraform_not_enabled`. A newer plan of the
same user for the same app and environment replaces the earlier one; plan-only
runs of other users and of published versions are never replaced by it.

Temporary versions are drafts with `"temporary": true`, `expiresAt` and
`createdBy`. `POST .../versions/{versionId}/publish` and `.../deploy` answer
`409` / `400 temporary_version`. `GET /apps/{appId}/versions` leaves them out
unless `?includeTemporary=true`. smithd deletes them, with their bundle and
plan-only runs, once `expiresAt` has passed (`TERRAFORM_LOCAL_PLAN_TTL`,
default `24h`). No check runs or notifications are sent for them.

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
- [ ] Returns 400 if gitBranchPattern is invalid or targetEnvironment doesn't exist
- [ ] Returns 404 if app doesn't exist
- [ ] Returns 409 if the app already has a policy with the same name
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

### 10a. Update Auto-Deploy Policy

Update a policy, e.g. to enable or disable it. Omitted fields are left unchanged.

**Endpoint:** `PATCH /apps/{appId}/policies/{policyId}`

**Request Body:**
```json
{
  "enabled": false
}
```

Accepted fields: `name`, `gitBranchPattern`, `targetEnvironment`, `enabled`.

**Response:** `200 OK` with the updated policy.

Returns `404` if the policy doesn't exist or belongs to another app, `400` for an
empty name, an invalid branch pattern or an unknown environment, and `409` if the
new name is already used by another policy of the app.

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
