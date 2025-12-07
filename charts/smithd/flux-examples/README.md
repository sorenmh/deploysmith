# Flux Examples for smithd Helm Chart

This directory contains example Flux CD manifests for deploying smithd using GitOps.

## Quick Start

### Option 1: Chart from Git Repository (Recommended for Development)

If your chart is in a Git repository:

1. **Create the GitRepository source:**
   ```bash
   kubectl apply -f helmrepository-git.yaml
   ```

2. **Create the namespace:**
   ```bash
   kubectl create namespace deploysmith
   ```

3. **Create the secret with sensitive values:**
   ```bash
   # Edit the secret.yaml file with your actual values first
   kubectl apply -f secret.yaml
   ```

4. **Deploy the HelmRelease:**
   ```bash
   kubectl apply -f helmrelease-git.yaml
   ```

### Option 2: Chart from Helm Repository (Recommended for Production)

If you're publishing the chart to a Helm repository (GitHub Pages, ChartMuseum, Harbor, etc.):

1. **Create the HelmRepository source:**
   ```bash
   kubectl apply -f helmrepository.yaml
   ```

2. **Create the namespace:**
   ```bash
   kubectl create namespace deploysmith
   ```

3. **Create the secret:**
   ```bash
   kubectl apply -f secret.yaml
   ```

4. **Deploy the HelmRelease:**
   ```bash
   kubectl apply -f helmrelease.yaml
   ```

## File Descriptions

### Source Configurations

- **`helmrepository.yaml`** - HelmRepository for chart hosted in a Helm repo
- **`helmrepository-git.yaml`** - GitRepository for chart in a Git repository

### Deployment

- **`helmrelease.yaml`** - HelmRelease using HelmRepository source
- **`helmrelease-git.yaml`** - HelmRelease using GitRepository source
- **`kustomization.yaml`** - Flux Kustomization to deploy all resources

### Secrets Management

- **`secret.yaml`** - Manual Kubernetes Secret (not recommended for production)
- **`external-secrets.yaml`** - External Secrets Operator integration (recommended)

## Secrets Management Options

### Option 1: Manual Secret (Development Only)

Create a Kubernetes Secret manually:

```bash
kubectl create secret generic smithd-secrets \
  --namespace deploysmith \
  --from-literal=values.yaml="$(cat <<EOF
secrets:
  apiKeys: "sk_prod_your_key"
  aws:
    accessKeyId: "YOUR_KEY"
    secretAccessKey: "YOUR_SECRET"
  gitopsSshKey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
EOF
)"
```

### Option 2: Sealed Secrets (Recommended)

Using Bitnami Sealed Secrets:

```bash
# Install kubeseal CLI
brew install kubeseal

# Create a sealed secret
kubectl create secret generic smithd-secrets \
  --namespace deploysmith \
  --from-file=values.yaml=./secret-values.yaml \
  --dry-run=client -o yaml | \
  kubeseal -o yaml > sealed-secret.yaml

# Apply the sealed secret
kubectl apply -f sealed-secret.yaml
```

### Option 3: External Secrets Operator (Recommended for AWS/GCP/Azure)

See `external-secrets.yaml` for a complete example using AWS Secrets Manager.

```bash
# Install External Secrets Operator
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets -n external-secrets-system --create-namespace

# Apply the ExternalSecret and SecretStore
kubectl apply -f external-secrets.yaml
```

### Option 4: SOPS (Mozilla SOPS with Flux)

Using SOPS with Flux for encrypted secrets in Git:

```bash
# Install SOPS
brew install sops

# Create age key
age-keygen -o age.key

# Encrypt the secret
sops --encrypt --age $(cat age.key.pub) secret.yaml > secret.enc.yaml

# Create the age secret in the cluster
cat age.key | kubectl create secret generic sops-age \
  --namespace=flux-system \
  --from-file=age.agekey=/dev/stdin

# Reference the encrypted file in your Kustomization
```

## Directory Structure in Your GitOps Repo

Recommended structure:

```
your-gitops-repo/
├── clusters/
│   └── production/
│       └── smithd/
│           ├── namespace.yaml
│           ├── helmrepository.yaml
│           └── helmrelease.yaml
├── base/
│   └── smithd/
│       ├── helmrelease.yaml
│       └── kustomization.yaml
└── secrets/
    └── production/
        └── smithd-secrets.yaml  # Sealed or encrypted
```

## Monitoring the Deployment

```bash
# Watch the HelmRelease
kubectl get helmrelease -n deploysmith
flux get helmrelease smithd -n deploysmith

# Check the HelmRelease status
kubectl describe helmrelease smithd -n deploysmith

# View Flux logs
flux logs -n deploysmith

# Force reconciliation
flux reconcile helmrelease smithd -n deploysmith

# Suspend reconciliation
flux suspend helmrelease smithd -n deploysmith

# Resume reconciliation
flux resume helmrelease smithd -n deploysmith
```

## Upgrading

Flux will automatically upgrade the chart when:
- A new version matching the semver range is available (for HelmRepository)
- The Git repository is updated (for GitRepository)

To manually trigger an upgrade:

```bash
flux reconcile helmrelease smithd -n deploysmith
```

## Rollback

If a deployment fails, Flux will automatically rollback. To manually rollback:

```bash
# View history
helm history smithd -n deploysmith

# Rollback to previous version
helm rollback smithd -n deploysmith
```

## Complete Example

Here's a complete example of deploying smithd with Flux:

```bash
# 1. Create namespace
kubectl create namespace deploysmith

# 2. Create GitRepository source
cat <<EOF | kubectl apply -f -
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: deploysmith
  namespace: flux-system
spec:
  interval: 5m
  url: https://github.com/your-org/deploysmith
  ref:
    branch: main
EOF

# 3. Create secret with your values
kubectl create secret generic smithd-secrets \
  --namespace deploysmith \
  --from-file=values.yaml=./my-secret-values.yaml

# 4. Create HelmRelease
kubectl apply -f helmrelease-git.yaml

# 5. Wait for deployment
kubectl wait --for=condition=ready helmrelease/smithd -n deploysmith --timeout=5m

# 6. Check status
kubectl get pods -n deploysmith
```

## Troubleshooting

### HelmRelease stuck in "Reconciling"

```bash
# Check events
kubectl describe helmrelease smithd -n deploysmith

# Check Flux logs
flux logs -n deploysmith

# Force reconciliation
flux reconcile helmrelease smithd -n deploysmith --with-source
```

### Chart not found

```bash
# Check the source
flux get sources helm -n flux-system
flux get sources git -n flux-system

# Reconcile the source
flux reconcile source git deploysmith -n flux-system
```

### Values not applied

Check the secret exists and has the correct format:

```bash
kubectl get secret smithd-secrets -n deploysmith -o yaml
kubectl get secret smithd-secrets -n deploysmith -o jsonpath='{.data.values\.yaml}' | base64 -d
```

## Best Practices

1. **Use semver ranges** in HelmRelease to allow automatic updates
2. **Store secrets encrypted** in Git using Sealed Secrets or SOPS
3. **Use health checks** in HelmRelease to ensure proper rollouts
4. **Set resource limits** to prevent resource exhaustion
5. **Enable automatic remediation** for failed deployments
6. **Use dependencies** to ensure correct deployment order
7. **Monitor Flux alerts** for deployment notifications

## References

- [Flux HelmRelease Guide](https://fluxcd.io/docs/components/helm/)
- [Flux GitRepository Guide](https://fluxcd.io/docs/components/source/gitrepositories/)
- [External Secrets Operator](https://external-secrets.io/)
- [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets)
- [SOPS with Flux](https://fluxcd.io/docs/guides/mozilla-sops/)
