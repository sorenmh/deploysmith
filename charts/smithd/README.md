# smithd Helm Chart

A Helm chart for deploying DeploySmith server (smithd) - a GitOps-based deployment controller for Kubernetes applications.

## Overview

This chart deploys smithd to your Kubernetes cluster with the following components:

- **Deployment**: Runs the smithd server
- **Service**: Exposes the smithd API
- **PersistentVolumeClaim**: Stores the SQLite database (skipped if using PostgreSQL — see [Database](#database-sqlite-vs-postgresql))
- **ConfigMap**: Non-sensitive configuration
- **Secret**: API keys, AWS credentials, and SSH keys
- **ServiceAccount**: For pod identity
- **Ingress** (optional): External access to the API

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- S3-compatible storage (AWS S3, MinIO, etc.)
- Git repository for GitOps (GitHub, GitLab, Gitea, etc.)
- SSH key pair for Git authentication

## Installation

### Quick Start

1. Create a values file with your configuration:

```yaml
# my-values.yaml
config:
  s3:
    bucket: my-deploysmith-versions
    region: us-east-1
  gitops:
    repo: git@github.com:myorg/gitops.git
    userName: smithd
    userEmail: smithd@mycompany.com

secrets:
  apiKeys: "sk_prod_replace_with_secure_key"
  aws:
    accessKeyId: "your-aws-access-key"
    secretAccessKey: "your-aws-secret-key"
  gitopsSshKey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    your-ssh-private-key-here
    -----END OPENSSH PRIVATE KEY-----

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: smithd.example.com
      paths:
        - path: /
          pathType: Prefix
```

2. Install the chart:

```bash
helm install smithd ./charts/smithd -f my-values.yaml
```

### With Existing Secrets

If you prefer to manage secrets separately:

```bash
# Create secret manually
kubectl create secret generic smithd-secrets \
  --from-literal=api-keys="sk_prod_your_key" \
  --from-literal=aws-access-key-id="your_key" \
  --from-literal=aws-secret-access-key="your_secret" \
  --from-file=gitops-ssh-key=/path/to/id_rsa

# Install with existing secret
helm install smithd ./charts/smithd \
  --set secrets.existingSecret=smithd-secrets \
  --set config.s3.bucket=my-bucket \
  --set config.gitops.repo=git@github.com:org/gitops.git
```

## Configuration

### Required Configuration

These values must be set for smithd to function:

| Parameter                     | Description                 | Example                                    |
| ----------------------------- | --------------------------- | ------------------------------------------ |
| `secrets.apiKeys`             | API keys for authentication | `sk_prod_abc123`                           |
| `secrets.aws.accessKeyId`     | AWS access key              | `AKIAIOSFODNN7EXAMPLE`                     |
| `secrets.aws.secretAccessKey` | AWS secret key              | `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY` |
| `secrets.gitopsSshKey`        | SSH private key for Git     | See example above                          |
| `config.s3.bucket`            | S3 bucket name              | `deploysmith-versions`                     |
| `config.gitops.repo`          | Git repository URL          | `git@github.com:org/gitops.git`            |

### Common Configuration Options

#### Image Configuration

```yaml
image:
  repository: ghcr.io/your-org/smithd
  tag: "v0.1.1"
  pullPolicy: IfNotPresent
```

#### Resource Limits

```yaml
resources:
  limits:
    cpu: 1000m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

#### Persistence

```yaml
persistence:
  enabled: true
  size: 10Gi
  storageClass: "gp3"
  # Or use existing PVC
  # existingClaim: my-pvc
```

#### Database (SQLite vs PostgreSQL)

```yaml
config:
  database:
    driver: postgres

secrets:
  # Kept in the Secret rather than the ConfigMap, since it holds a password
  databaseDsn: "postgres://user:pass@host:5432/smithd?sslmode=require"

# No local file needed with postgres — turn off the PVC
persistence:
  enabled: false
```

Schema migrations run automatically on startup for either driver. With SQLite (the default), leave `persistence.enabled: true` so `/data/smithd.db` survives pod restarts.

**Migrating from SQLite to PostgreSQL:** use `smithctl db export`/`db import` to move data between instances — no external tool (e.g. pgloader) is needed.

```bash
# 1. Export from the running SQLite-backed smithd
smithctl db export dump.json

# 2. Point a fresh smithd instance at an empty PostgreSQL database
#    (schema migrations run automatically on startup) — see driver/dsn above

# 3. Import into the new instance
smithctl db import dump.json
```

Cut-over with minimal downtime: export, then switch the release to PostgreSQL (the new pod starts with an empty database), then import. Deployments requested in between are lost, so do it when nothing is deploying. Keep the old PVC until you have checked the result; setting `persistence.enabled: false` does not delete an existing claim.

Only applications, environments, versions, policies and deployments are moved. Push notification subscriptions are not: re-enable notifications once on each device. PostgreSQL enforces foreign keys, which SQLite (as smithd uses it) does not, so a dump containing rows that point at something deleted is refused, and nothing is written. Fix the rows or delete the orphans from the dump file and import again.

Import requires an empty target database (a freshly-migrated schema with no rows) — it refuses with a clear error otherwise, and does not partially write on failure.

#### Ingress with TLS

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  hosts:
    - host: smithd.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: smithd-tls
      hosts:
        - smithd.example.com
```

#### Web UI with oauth2-proxy

smithd can serve a web UI for viewing what runs where, deploying and rolling
back versions, and managing auto-deploy policies and environments. smithd does
not handle logins itself: it trusts a header naming the user, set by an
authenticating proxy, [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/).
The chart supports two setups.

##### Sidecar on its own host (any ingress controller, recommended)

`uiProxy` runs oauth2-proxy as a sidecar in the smithd pod and serves the UI
on its own host. smithd then serves the UI on localhost only, so signing in
through the sidecar is the only way to reach it: the user header cannot be
forged from outside the cluster or from other pods.

1. Create an OAuth app. With GitHub, a personal one is enough (Settings →
   Developer settings → OAuth Apps; no organization needed), with
   homepage `https://smithui.example.com` and callback URL
   `https://smithui.example.com/oauth2/callback`.

2. Put its credentials in smithd's secret, as `oauth2-proxy-client-id`,
   `oauth2-proxy-client-secret` and `oauth2-proxy-cookie-secret` (from
   `openssl rand -base64 32 | tr -- '+/' '-_'`), or set
   `secrets.oauth2Proxy.*` when the chart manages the secret.

3. Enable it:

   ```yaml
   config:
     ui:
       allowedUsers: octocat            # optional second check in smithd

   uiProxy:
     enabled: true
     host: smithui.example.com
     githubUsers: octocat               # and/or githubOrg / githubTeam
     ingress:
       annotations:
         cert-manager.io/cluster-issuer: letsencrypt-prod
       tlsSecretName: smithui-tls
   ```

Users are identified by `X-Forwarded-User`, their GitHub login, which is also
recorded on deployments they start. For another provider set
`uiProxy.provider`, pass its flags in `uiProxy.extraArgs`, and consider
`config.ui.userHeader: X-Forwarded-Email`.

##### ingress-nginx external authentication

Alternatively, with ingress-nginx, `uiIngress` serves `/ui` on the main
ingress hosts and has nginx check every request with an oauth2-proxy you
deploy yourself.

1. Deploy oauth2-proxy on the same host as smithd under `/oauth2`, for example
   with its Helm chart (`oauth2-proxy/oauth2-proxy`). It must return the user's
   email in a response header, which `--set-xauthrequest` enables:

   ```yaml
   # oauth2-proxy values
   config:
     clientID: <OAuth client ID>
     clientSecret: <OAuth client secret>
     cookieSecret: <openssl rand -base64 32 | head -c 32>
   extraArgs:
     provider: github            # or google, oidc, ...
     email-domain: "*"           # narrow down with smithd's allowedUsers
     set-xauthrequest: true
     reverse-proxy: true
     upstream: static://202
   ingress:
     enabled: true
     className: nginx
     path: /oauth2
     hosts:
       - smithd.example.com
   ```

2. Enable the UI and its protected ingress in smithd's values:

   ```yaml
   config:
     ui:
       userHeader: X-Auth-Request-Email
       allowedUsers: "@example.com,contractor@example.org"   # optional

   uiIngress:
     enabled: true
     oauth2ProxyUrl: https://smithd.example.com/oauth2
   ```

   `uiIngress` routes `/ui` on the main ingress hosts through oauth2-proxy.
   nginx sets `X-Auth-Request-Email` from oauth2-proxy's answer, replacing any
   value the browser sent, so the header cannot be forged.

3. Open `https://smithd.example.com/ui`. Deployments started from the UI are
   recorded with the signed-in user's email.

The API under `/api/v1` keeps using API keys and is unaffected.

Because smithd trusts the header, `/ui` must never be reachable without the
proxy. The chart therefore refuses to enable the UI on the main ingress
without `uiProxy` or `uiIngress`. If you protect `/ui` with a different proxy, set
`config.ui.protectedExternally: true`. Consider a NetworkPolicy so that only
the ingress controller can reach smithd's service.

#### Deploy status on GitHub

smithd can report each deployment as a check run
(`deploysmith/deploy-<env>`) on the commit the version was built from, with
a GitHub App that has **Checks: Read and write** and is installed on the
repositories (step by step in
[docs/github-checks.md](../../docs/github-checks.md)):

```yaml
config:
  ui:
    publicUrl: https://smithd.example.com   # for the Details link
  github:
    appId: "123456"

secrets:
  github:
    privateKey: |   # or github-app-private-key in existingSecret
      -----BEGIN RSA PRIVATE KEY-----
      ...
      -----END RSA PRIVATE KEY-----
```

#### Using MinIO Instead of AWS S3

```yaml
config:
  s3:
    bucket: deploysmith-versions
    region: us-east-1
    endpoint: http://minio.minio.svc.cluster.local:9000

secrets:
  aws:
    accessKeyId: minioadmin
    secretAccessKey: minioadmin123
```

### All Configuration Options

See [values.yaml](values.yaml) for all available configuration options.

## Usage

### Accessing the API

After installation, get the URL:

```bash
# If using Ingress
echo https://smithd.example.com

# If using LoadBalancer
kubectl get svc smithd -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# If using ClusterIP (port-forward)
kubectl port-forward svc/smithd 8080:8080
curl http://localhost:8080/health
```

### Testing the Deployment

```bash
# Check pod status
kubectl get pods -l app.kubernetes.io/name=smithd

# View logs
kubectl logs -l app.kubernetes.io/name=smithd -f

# Test health endpoint
kubectl run curl --rm -it --image=curlimages/curl -- \
  curl http://smithd:8080/health

# Test API with authentication
export API_KEY="your-api-key"
kubectl run curl --rm -it --image=curlimages/curl -- \
  curl -H "Authorization: Bearer $API_KEY" \
  http://smithd:8080/api/v1/apps
```

### Using with CI/CD

Configure your CI/CD pipeline to use the `forge` CLI:

```bash
# Set environment variables
export SMITHD_URL=https://smithd.example.com
export SMITHD_API_KEY=sk_prod_your_key

# In your CI pipeline
forge init --app my-app --version $VERSION --git-sha $GIT_SHA
forge upload manifests/
forge publish --app my-app --version $VERSION
```

## Upgrading

```bash
# Upgrade with new values
helm upgrade smithd ./charts/smithd -f my-values.yaml

# Upgrade just the image
helm upgrade smithd ./charts/smithd \
  --reuse-values \
  --set image.tag=v1.1.0
```

## Uninstalling

```bash
# Uninstall the release
helm uninstall smithd

# Optionally, delete the PVC
kubectl delete pvc smithd
```

## Troubleshooting

### Pod not starting

```bash
# Check pod events
kubectl describe pod -l app.kubernetes.io/name=smithd

# Check logs
kubectl logs -l app.kubernetes.io/name=smithd
```

### Common Issues

**Issue**: Pod crashes with "permission denied" on database

**Solution**: Check PVC permissions and pod security context:

```yaml
podSecurityContext:
  fsGroup: 1000
  runAsUser: 1000
```

**Issue**: Cannot connect to S3

**Solution**: Verify AWS credentials and S3 endpoint:

```bash
kubectl exec -it <pod-name> -- env | grep AWS
```

**Issue**: GitOps commits failing

**Solution**: Check SSH key permissions and Git repository access:

```bash
# SSH key should be mode 0600
kubectl exec -it <pod-name> -- ls -la /app/.ssh/
```

## Security Considerations

1. **API Keys**: Use strong, randomly generated keys
2. **Secrets Management**: Consider using external secret managers (AWS Secrets Manager, HashiCorp Vault)
3. **Network Policies**: Restrict network access to smithd
4. **Pod Security**: Run as non-root user (default)
5. **TLS**: Always use TLS in production (enable Ingress with cert-manager)

## Development

### Testing Locally

```bash
# Lint the chart
helm lint ./charts/smithd

# Test template rendering
helm template smithd ./charts/smithd -f my-values.yaml

# Dry-run installation
helm install smithd ./charts/smithd -f my-values.yaml --dry-run --debug

# Install to kind cluster
kind create cluster
helm install smithd ./charts/smithd -f my-values.yaml
```

## Support

For issues and questions:

- GitHub Issues: https://github.com/sorenmh/deploysmith/issues
- Documentation: See the [main README](../../README.md)

## License

Licensed under the O'Saasy License - see [LICENSE.md](../../LICENSE.md) for details.

You may freely use, modify, and distribute this software. However, you may not offer it to third parties as a competing hosted, managed, or SaaS product where the primary value is the functionality of DeploySmith itself.
