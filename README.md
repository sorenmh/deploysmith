# Deployment Manager

A lightweight REST API service for managing Kubernetes deployments via GitOps. Works with Flux CD by manipulating Git manifests and letting Flux handle cluster reconciliation.

## Features

- ✅ List services and available versions from container registry
- ✅ Deploy specific versions
- ✅ Rollback to previous versions
- ✅ Deployment history tracking
- ✅ Webhook endpoint for CI/CD integration
- ✅ SQLite database for deployment records
- ✅ Git repository manipulation
- ✅ Container registry integration
- ✅ API key authentication

## Architecture

```
CI/CD Pipeline → Deployment API → Git Repository → Flux CD → Kubernetes Cluster
```

The Deployment API:

1. Receives deployment requests (API or webhook)
2. Validates version exists in container registry
3. Updates image tag in Git manifest
4. Commits and pushes to Git
5. Flux automatically reconciles changes to cluster

## Building

### Local Development

```bash
# Install dependencies
go mod download

# Run locally
go run main.go -config config.example.yaml
```

### Docker Build

```bash
docker build -t deployment-api:latest .
```

### Multi-arch Build

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/myuser/deployment-api:latest --push .
```

## Configuration

Create a `config.yaml` file:

```yaml
server:
  port: 8080
  api_keys:
    - name: "ci-pipeline"
      key: "your-secret-key-here"

git:
  repository_url: "https://github.com/user/k8s-gitops.git"
  branch: "main"
  username: "git-username"
  token: "github-personal-access-token"
  local_path: "/data/gitops-repo"
  author_name: "Deployment API"
  author_email: "deploy-api@example.com"

services:
  - name: "webapp"
    namespace: "default"
    manifest_path: "clusters/production/apps/webapp/deployment.yaml"
    image_repository: "ghcr.io/myuser/webapp"
    workload_type: "deployment"

database:
  path: "/data/deployments.db"

logging:
  level: "info"
  format: "json"
```

Environment variables are expanded automatically using `${VAR}` syntax.

## API Endpoints

### Authentication

All API endpoints (except `/health`) require Bearer token authentication:

```
Authorization: Bearer <your-api-key>
```

### Endpoints

#### Health Check

```
GET /health
```

Response:

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "git_repo_accessible": true,
  "database_accessible": true
}
```

#### List Services

```
GET /api/v1/services
```

Response:

```json
{
  "services": [
    {
      "name": "webapp",
      "namespace": "default",
      "current_version": "1.2.3",
      "manifest_path": "clusters/production/apps/webapp/deployment.yaml",
      "image_repository": "ghcr.io/myuser/webapp"
    }
  ]
}
```

#### List Available Versions

```
GET /api/v1/services/{service}/versions?limit=10
```

Response:

```json
{
  "service": "webapp",
  "versions": [
    {
      "tag": "1.2.3",
      "digest": "sha256:abc123...",
      "created_at": "2025-01-15T10:30:00Z",
      "deployed": true
    },
    {
      "tag": "1.2.2",
      "digest": "sha256:def456...",
      "created_at": "2025-01-14T08:20:00Z",
      "deployed": false
    }
  ]
}
```

#### Get Current Deployment

```
GET /api/v1/services/{service}/current
```

Response:

```json
{
  "id": "dep_abc123",
  "service": "webapp",
  "version": "1.2.3",
  "deployed_at": "2025-01-15T10:35:00Z",
  "deployed_by": "ci-pipeline",
  "git_commit": "a1b2c3d",
  "status": "success",
  "type": "deploy"
}
```

#### Deploy Version

```
POST /api/v1/services/{service}/deploy
```

Request:

```json
{
  "version": "1.2.3",
  "deployed_by": "user@example.com",
  "message": "Deploy new feature X"
}
```

Response:

```json
{
  "id": "dep_abc123",
  "service": "webapp",
  "version": "1.2.3",
  "deployed_at": "2025-01-15T11:00:00Z",
  "deployed_by": "user@example.com",
  "git_commit": "e4f5g6h",
  "status": "success",
  "type": "deploy",
  "message": "Deploy new feature X"
}
```

#### Rollback

```
POST /api/v1/services/{service}/rollback
```

Request:

```json
{
  "version": "1.2.2", // Optional, defaults to previous version
  "deployed_by": "user@example.com"
}
```

#### Deployment History

```
GET /api/v1/services/{service}/deployments?limit=20&offset=0
```

Response:

```json
{
  "service": "webapp",
  "deployments": [...],
  "total": 45,
  "limit": 20,
  "offset": 0
}
```

#### Webhook (CI Integration)

```
POST /api/v1/webhook/build
```

Request:

```json
{
  "service": "webapp",
  "version": "1.2.4",
  "image": "ghcr.io/myuser/webapp:1.2.4",
  "git_sha": "abc123def",
  "auto_deploy": true
}
```

## Kubernetes Deployment

See `../k8s/deployment-api/` for complete Kubernetes manifests.

### Quick Deploy

```bash
# Create namespace
kubectl apply -f k8s/deployment-api/namespace.yaml

# Create secrets (use Sealed Secrets in production!)
kubectl create secret generic deployment-api-secrets \
  -n deployment-api \
  --from-literal=ci-api-key="$(openssl rand -base64 32)" \
  --from-literal=admin-api-key="$(openssl rand -base64 32)" \
  --from-literal=git-token="ghp_your_token"

# Apply manifests
kubectl apply -k k8s/deployment-api/

# Check status
kubectl get pods -n deployment-api
kubectl logs -n deployment-api -l app=deployment-api
```

### Using with Flux

Copy the manifests to your GitOps repository:

```bash
cp -r k8s/deployment-api/ /path/to/gitops-repo/clusters/production/infrastructure/
```

## Usage Examples

### Deploy via curl

```bash
curl -X POST https://deploy.example.com/api/v1/services/webapp/deploy \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "version": "1.2.3",
    "deployed_by": "ops-team",
    "message": "Deploy new feature"
  }'
```

### Rollback via curl

```bash
curl -X POST https://deploy.example.com/api/v1/services/webapp/rollback \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "deployed_by": "ops-team"
  }'
```

### CI/CD Integration (GitHub Actions)

```yaml
- name: Notify Deployment API
  run: |
    curl -X POST https://deploy.example.com/api/v1/webhook/build \
      -H "Authorization: Bearer ${{ secrets.DEPLOY_API_KEY }}" \
      -H "Content-Type: application/json" \
      -d '{
        "service": "webapp",
        "version": "${{ github.sha }}",
        "image": "ghcr.io/myuser/webapp:${{ github.sha }}",
        "git_sha": "${{ github.sha }}",
        "auto_deploy": true
      }'
```

## Security

### API Keys

Generate strong API keys:

```bash
openssl rand -base64 32
```

Store in Kubernetes Secrets (use Sealed Secrets for GitOps):

```bash
kubectl create secret generic deployment-api-secrets \
  --dry-run=client \
  --from-literal=ci-api-key="$(openssl rand -base64 32)" \
  --from-literal=admin-api-key="$(openssl rand -base64 32)" \
  --from-literal=git-token="ghp_your_token" \
  -o yaml | kubeseal -o yaml > secrets-sealed.yaml
```

### GitHub Token Permissions

The GitHub Personal Access Token needs:

- `repo` scope (full repository access)
- Or fine-grained token with:
  - Repository permissions: Contents (Read and write)

### Network Security

- Use HTTPS/TLS for all API communication
- Restrict API access with rate limiting (Traefik middleware)
- Use network policies to limit pod-to-pod communication

## Monitoring

### Metrics

The API exposes health check endpoint for monitoring:

```bash
curl https://deploy.example.com/health
```

### Logs

```bash
# View logs
kubectl logs -n deployment-api -l app=deployment-api -f

# View specific deployment logs
kubectl logs -n deployment-api deployment/deployment-api
```

### Database

Access deployment history:

```bash
# Exec into pod
kubectl exec -it -n deployment-api deployment/deployment-api -- sh

# Query database
sqlite3 /data/deployments.db "SELECT * FROM deployments ORDER BY deployed_at DESC LIMIT 10;"
```

## Development

### Running Tests

```bash
go test ./...
```

### Adding a New Service

1. Update `config.yaml`:

```yaml
services:
  - name: "new-service"
    namespace: "default"
    manifest_path: "clusters/production/apps/new-service/deployment.yaml"
    image_repository: "ghcr.io/myuser/new-service"
    workload_type: "deployment"
```

2. Restart the API:

```bash
kubectl rollout restart deployment/deployment-api -n deployment-api
```

## Troubleshooting

### Git Push Fails

Check Git credentials:

```bash
kubectl get secret deployment-api-secrets -n deployment-api -o jsonpath='{.data.git-token}' | base64 -d
```

Verify repository access:

```bash
kubectl exec -it -n deployment-api deployment/deployment-api -- sh
cd /data/gitops-repo
git status
git pull
```

### Registry Authentication Fails

Check registry credentials in secrets.

For public registries, omit `registry_auth` in config.

### Image Not Found

Verify the image exists:

```bash
docker pull ghcr.io/myuser/webapp:1.2.3
```

Check image repository URL in config.

## License

MIT License - See LICENSE file for details
