# smithctl Specification

CLI tool for developers to interact with DeploySmith.

## Overview

smithctl is a command-line tool for developers to:
1. Register new applications
2. List available versions
3. Deploy specific versions
4. Manage auto-deployment policies
5. View deployment history

## Installation

```bash
# Download from GitHub Releases
curl -L https://github.com/org/deploysmith/releases/download/v1.0.0/smithctl-linux-amd64 -o smithctl
chmod +x smithctl
sudo mv smithctl /usr/local/bin/
```

Or via Homebrew (macOS):
```bash
brew install org/tap/smithctl
```

## Configuration

smithctl is configured via environment variables, config file, or CLI flags.

**Environment variables:**
```bash
SMITHD_URL=https://smithd.example.com
SMITHD_API_KEY=sk_live_abc123
```

**Config file** (`~/.smithctl/config.yaml`):
```yaml
url: https://smithd.example.com
apiKey: sk_live_abc123
```

**CLI flags** (override env vars and config):
```bash
smithctl --url https://smithd.example.com --api-key sk_live_abc123 ...
```

---

## Commands

### `smithctl app register`

Register a new application with DeploySmith.

**Usage:**
```bash
smithctl app register my-api-service
```

Or with explicit flag:
```bash
smithctl app register --name my-api-service
```

**Flags:**
- `--name` (optional): Application name (can also be provided as positional argument)

**Output:**
```
✓ Application registered successfully

  Name: my-api-service
  ID:   app-123
  Path: environments/{environment}/apps/my-api-service
```

**Exit codes:**
- `0`: Success
- `1`: API error
- `2`: Invalid arguments

**Acceptance Test:**
- [ ] Calls smithd POST /apps API
- [ ] Shows success message with app details
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if app already exists
- [ ] Returns exit code 2 if app name is missing
- [ ] Accepts app name as positional argument or --name flag

---

### `smithctl app list`

List all registered applications.

**Usage:**
```bash
smithctl app list
```

**Output:**
```
NAME              ID         CREATED
my-api-service    app-123    2025-01-15 10:30:00
hello-world       app-456    2025-01-14 09:15:00
```

**Flags:**
- `--output` (optional): Output format (table, json, yaml), default: table

**Acceptance Test:**
- [ ] Calls smithd GET /apps API
- [ ] Displays apps in table format by default
- [ ] Supports JSON output with --output json
- [ ] Supports YAML output with --output yaml
- [ ] Shows "No applications found" if list is empty
- [ ] Returns exit code 0

---

### `smithctl app show`

Show details for a specific application.

**Usage:**
```bash
smithctl app show my-api-service
```

**Output:**
```
Application: my-api-service

  ID:      app-123
  Path:    environments/{environment}/apps/my-api-service
  Created: 2025-01-15 10:30:00

Current Deployments:
  staging:     42540c4-123 (deployed 2 hours ago)
  production:  a1b2c3d-120 (deployed 1 day ago)
```

**Acceptance Test:**
- [ ] Calls smithd GET /apps/{appId} API
- [ ] Shows app details and current deployments
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if app not found
- [ ] Supports --output json/yaml

---

### `smithctl version list`

List all versions for an application.

**Usage:**
```bash
smithctl version list my-api-service
```

**Output:**
```
VERSION        STATUS      BRANCH    DEPLOYED TO       CREATED
42540c4-123    published   main      staging           2025-01-15 10:30
a1b2c3d-120    published   main      production        2025-01-14 15:20
xyz789-119     published   feature   -                 2025-01-14 12:10
```

**Flags:**
- `--status` (optional): Filter by status (draft, published)
- `--limit` (optional): Max results, default 20
- `--output` (optional): Output format (table, json, yaml)

**Acceptance Test:**
- [ ] Calls smithd GET /apps/{appId}/versions API
- [ ] Displays versions in table format sorted by created date
- [ ] Shows which environments each version is deployed to
- [ ] Filter by status works correctly
- [ ] Pagination works with --limit
- [ ] Returns exit code 0
- [ ] Returns exit code 1 if app not found

---

### `smithctl version show`

Show details for a specific version.

**Usage:**
```bash
smithctl version show my-api-service 42540c4-123
```

**Output:**
```
Version: 42540c4-123

  Status:      published
  Git SHA:     42540c4abc123
  Git Branch:  main
  Committer:   john@example.com
  Build:       #123
  Created:     2025-01-15 10:30:00
  Published:   2025-01-15 10:35:00

Manifest Files:
  - deployment.yaml
  - service.yaml
  - ingress.yaml
  - version.yml

Deployed To:
  staging (2 hours ago)
```

**Acceptance Test:**
- [ ] Calls smithd GET /apps/{appId}/versions/{versionId} API
- [ ] Shows version details, manifest files, and deployment status
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if app or version not found
- [ ] Supports --output json/yaml

---

### `smithctl deploy`

Deploy a specific version to an environment.

**Usage:**
```bash
smithctl deploy my-api-service 42540c4-123 --env staging
```

**Flags:**
- `--env` (required): Target environment
- `--confirm` (optional): Skip confirmation prompt

**Output:**
```
You are about to deploy:

  App:         my-api-service
  Version:     42540c4-123
  Environment: staging

This will update the gitops repository and Flux will apply the changes.

Continue? (y/n): y

✓ Deployment initiated
  Deployment ID: deploy-789

Monitor progress with:
  smithctl deployment show deploy-789
```

**Acceptance Test:**
- [ ] Calls smithd POST /apps/{appId}/versions/{versionId}/deploy API
- [ ] Shows confirmation prompt unless --confirm is used
- [ ] Shows deployment ID on success
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if app/version not found or API error
- [ ] Returns exit code 2 if user cancels confirmation

---

### `smithctl rollback`

Rollback to a previous version (alias for deploy).

**Usage:**
```bash
smithctl rollback my-api-service --env staging
```

**Output:**
```
Current version in staging: 42540c4-123

Recent versions:
  1. a1b2c3d-120 (deployed 1 day ago)
  2. xyz789-119 (deployed 2 days ago)

Select version to rollback to (1-2): 1

✓ Rolling back to version a1b2c3d-120...
✓ Deployment initiated
```

**Acceptance Test:**
- [ ] Shows current deployed version
- [ ] Lists recent versions for the environment
- [ ] Prompts user to select version
- [ ] Calls deploy API with selected version
- [ ] Returns exit code 0 on success

---

### `smithctl policy create`

Create an auto-deployment policy.

**Usage:**
```bash
smithctl policy create my-api-service \
  --name auto-deploy-main \
  --branch main \
  --env staging
```

**Flags:**
- `--name` (required): Policy name
- `--branch` (required): Git branch pattern
- `--env` (required): Target environment
- `--disabled` (optional): Create policy in disabled state

**Output:**
```
✓ Auto-deploy policy created

  Name:        auto-deploy-main
  Branch:      main
  Environment: staging
  Status:      enabled
```

**Acceptance Test:**
- [ ] Calls smithd POST /apps/{appId}/policies API
- [ ] Shows success message with policy details
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if app not found or policy already exists
- [ ] Branch pattern supports wildcards (e.g., "release/*")

---

### `smithctl policy list`

List all auto-deployment policies for an application.

**Usage:**
```bash
smithctl policy list my-api-service
```

**Output:**
```
NAME                  BRANCH       ENVIRONMENT    STATUS
auto-deploy-main      main         staging        enabled
auto-deploy-release   release/*    production     enabled
```

**Acceptance Test:**
- [ ] Calls smithd GET /apps/{appId}/policies API
- [ ] Displays policies in table format
- [ ] Shows "No policies found" if list is empty
- [ ] Returns exit code 0
- [ ] Supports --output json/yaml

---

### `smithctl policy delete`

Delete an auto-deployment policy.

**Usage:**
```bash
smithctl policy delete my-api-service auto-deploy-main
```

**Output:**
```
✓ Policy deleted
```

**Acceptance Test:**
- [ ] Calls smithd DELETE /apps/{appId}/policies/{policyId} API
- [ ] Shows confirmation prompt before deletion
- [ ] Shows success message on completion
- [ ] Returns exit code 0 on success
- [ ] Returns exit code 1 if policy not found

---

### `smithctl version`

Show the smithctl version.

**Usage:**
```bash
smithctl version
```

**Output:**
```
smithctl version 1.0.0
commit: abc123
built: 2025-01-15T10:00:00Z
```

**Acceptance Test:**
- [ ] Prints version, commit SHA, and build time
- [ ] Returns exit code 0

---

### `smithctl help`

Show help information.

**Usage:**
```bash
smithctl help
smithctl help deploy
```

**Acceptance Test:**
- [ ] Shows general help when called without arguments
- [ ] Shows command-specific help when called with command name
- [ ] Returns exit code 0

---

## Interactive Features

### Auto-completion

smithctl supports shell auto-completion:

```bash
# Bash
smithctl completion bash > /etc/bash_completion.d/smithctl

# Zsh
smithctl completion zsh > /usr/local/share/zsh/site-functions/_smithctl
```

**Acceptance Test:**
- [ ] Generates bash completion script
- [ ] Generates zsh completion script
- [ ] Completion includes command names, flags, and app names

---

## Output Formats

smithctl supports multiple output formats:

**Table (default):**
```
NAME              STATUS      CREATED
my-api-service    active      2025-01-15
```

**JSON:**
```json
{
  "apps": [
    {
      "name": "my-api-service",
      "status": "active",
      "createdAt": "2025-01-15T10:30:00Z"
    }
  ]
}
```

**YAML:**
```yaml
apps:
  - name: my-api-service
    status: active
    createdAt: 2025-01-15T10:30:00Z
```

---

## Error Handling

smithctl provides clear error messages:

```bash
$ smithctl app register --name my-app
Error: --gitops-repo is required

$ smithctl deploy my-app 123 --env staging
Error: Version 123 not found

$ smithctl policy create my-app --name test --branch main
Error: --env is required
```

---

## Future Enhancements (Post-MVP)

- `smithctl deployment show` - Show deployment details and logs
- `smithctl deployment list` - List deployment history
- `smithctl diff` - Compare two versions
- `smithctl logs` - Stream logs from deployed app (via kubectl integration)
- `smithctl dashboard` - Open web dashboard
- TUI (Terminal UI) for interactive exploration
- Watch mode for real-time deployment status
