# forge YAML Specification

This document defines the YAML format that `forge` uses to generate Kubernetes manifests.

## Overview

The forge YAML format is designed to be simple and opinionated, abstracting away Kubernetes complexity while still providing the essential configuration needed for most applications.

## Structure

### Top-level fields

```yaml
version: "1.0"        # Required: Spec version
app: {...}            # Required: Application metadata
components: [...]     # Required: List of components to deploy
config: {...}         # Optional: Global configuration
```

### `app` section

Defines application-level metadata.

```yaml
app:
  name: my-service          # Required: Application name (used for resource names)
  namespace: production     # Required: Kubernetes namespace
```

### `components` section

List of components to deploy. Each component becomes a Kubernetes resource.

#### Component types

- `deployment` - Creates a Deployment + Service (+ optionally Ingress)
- `job` - Creates a Job (runs once)
- `cronjob` - Creates a CronJob (scheduled tasks)

#### Common component fields

```yaml
components:
  - name: api                                    # Required: Component name
    type: deployment                             # Required: deployment | job | cronjob
    image: ghcr.io/org/app:{{.Version}}         # Required: Container image (can use {{.Version}} template)
    replicas: 3                                  # Optional: Number of replicas (default: 1, only for deployment)
    port: 8080                                   # Optional: Container port to expose (only for deployment)
    command: ["./app"]                           # Optional: Override container command
    args: ["--verbose"]                          # Optional: Container arguments
```

#### Resources

Define CPU and memory limits:

```yaml
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 512Mi
```

Defaults if not specified:
- requests: `cpu: 50m, memory: 64Mi`
- limits: `cpu: 200m, memory: 256Mi`

#### Environment variables

```yaml
    env:
      # Simple value
      - name: LOG_LEVEL
        value: info

      # From secret
      - name: DATABASE_URL
        valueFrom:
          secretKeyRef:
            name: db-credentials
            key: url

      # From configmap
      - name: CONFIG_FILE
        valueFrom:
          configMapKeyRef:
            name: app-config
            key: config.json
```

#### Health checks (deployment only)

```yaml
    healthcheck:
      liveness:
        path: /health/live              # HTTP GET path
        port: 8080                      # Port to probe
        initialDelaySeconds: 30         # Wait before first probe
        periodSeconds: 10               # How often to probe
        timeoutSeconds: 5               # Probe timeout
        failureThreshold: 3             # Failures before restart

      readiness:
        path: /health/ready
        port: 8080
        initialDelaySeconds: 10
        periodSeconds: 5
```

Defaults if not specified:
- `initialDelaySeconds: 10`
- `periodSeconds: 10`
- `timeoutSeconds: 3`
- `failureThreshold: 3`

#### Ingress configuration (deployment only)

```yaml
    ingress:
      enabled: true                     # Enable ingress
      hostname: api.example.com         # Required if enabled
      path: /                           # Optional: Path prefix (default: /)
      pathType: Prefix                  # Optional: Prefix | Exact (default: Prefix)

      tls:
        enabled: true                   # Enable TLS
        secretName: api-tls-cert        # Secret containing TLS cert

      annotations:                      # Optional: Ingress annotations
        cert-manager.io/cluster-issuer: letsencrypt-prod
        traefik.ingress.kubernetes.io/router.middlewares: default-rate-limit@kubernetescrd
```

#### Job-specific fields

```yaml
    type: job
    restartPolicy: OnFailure            # Never | OnFailure (default: OnFailure)
    backoffLimit: 3                     # Max retries (default: 3)
    ttlSecondsAfterFinished: 86400      # Cleanup after completion (default: 86400 = 1 day)
```

#### CronJob-specific fields

```yaml
    type: cronjob
    schedule: "0 2 * * *"               # Required: Cron schedule
    restartPolicy: OnFailure            # Never | OnFailure (default: OnFailure)
    concurrencyPolicy: Forbid           # Allow | Forbid | Replace (default: Forbid)
    successfulJobsHistoryLimit: 3       # Keep last N successful (default: 3)
    failedJobsHistoryLimit: 1           # Keep last N failed (default: 1)
```

### `config` section

Global configuration applied to all components.

```yaml
config:
  # Labels added to all resources
  labels:
    team: platform
    app: my-service
    environment: production

  # Annotations added to all resources
  annotations:
    deploysmith.io/managed: "true"
    deploysmith.io/version: "{{.Version}}"

  # Image pull secrets
  imagePullSecrets:
    - name: ghcr-credentials
    - name: dockerhub-credentials

  # Service account configuration
  serviceAccount:
    name: my-service-sa
    create: false                       # Whether to create the SA (default: false)
```

## Template variables

The following template variables can be used in YAML values:

- `{{.Version}}` - The version ID passed to forge
- `{{.GitSHA}}` - Git commit SHA
- `{{.GitBranch}}` - Git branch name
- `{{.BuildNumber}}` - CI pipeline build number

Example:
```yaml
image: ghcr.io/myorg/app:{{.Version}}
env:
  - name: GIT_SHA
    value: "{{.GitSHA}}"
```

## Opinionated defaults

forge makes the following opinionated decisions:

1. **Service type**: Always `ClusterIP` for deployments
2. **Rolling update strategy**: `maxSurge: 1, maxUnavailable: 0` for zero-downtime deploys
3. **Ingress class**: Uses cluster default (typically Traefik)
4. **DNS policy**: `ClusterFirst`
5. **Security context**: Non-root user (UID 1000) for all containers
6. **Restart policy**: `Always` for deployments, `OnFailure` for jobs

## Generated manifest structure

For a component named `api` in app `my-service`, forge generates:

### For `deployment` type:
- `Deployment`: `my-service-api`
- `Service`: `my-service-api`
- `Ingress` (if enabled): `my-service-api`

### For `job` type:
- `Job`: `my-service-{component}-{version-hash}`

### For `cronjob` type:
- `CronJob`: `my-service-{component}`

## Validation rules

forge validates the YAML and will fail if:

1. Required fields are missing
2. `replicas < 1` or `replicas > 100`
3. Invalid resource formats (CPU, memory)
4. Invalid cron schedule format
5. Image doesn't contain a registry (must be fully qualified)
6. Hostname is not a valid DNS name
7. Port is not in range 1-65535

## Examples

See:
- [forge-yaml-simple-example.yaml](./forge-yaml-simple-example.yaml) - Basic web service
- [forge-yaml-example.yaml](./forge-yaml-example.yaml) - Complex multi-component app

## Future enhancements (out of MVP scope)

- ConfigMap/Secret generation
- Persistent volume support
- HorizontalPodAutoscaler
- PodDisruptionBudget
- Multiple ingress hosts
- TCP/UDP services (non-HTTP)
- InitContainers and sidecars
