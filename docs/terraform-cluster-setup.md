# Terraform Cluster Setup

One-time platform setup every Terraform app relies on (DEP-16, epic DEP-13): the runner image, tofu-controller, the state backend and OpenTofu state encryption. The CR shape, status fields and the reasons behind each setting are in [specs/terraform-controller.md](specs/terraform-controller.md). The per-app parts (namespace, ServiceAccounts, OpenBao roles and policies, Transit key, IAM roles) are created by smithd and the `tf-app` module (DEP-17, chart README "Terraform apps"). Moving existing apps over: [terraform-migration.md](terraform-migration.md).

## Checklist

What has to exist before the first Terraform app can deploy:

1. **Runner image** for each OpenTofu version in use, built by CI: `ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu<version>` ([Runner Image](#runner-image)).
2. **tofu-controller** 0.16.6 installed by Flux from [examples/terraform/tofu-controller.yaml](examples/terraform/tofu-controller.yaml) ([tofu-controller](#tofu-controller)).
3. **Flux Kustomizations** `terraform-<env>` and `terraform-plans-<env>`, the `plans` branch and the read-only deploy key `gitops-readonly` ([Gitops Repository](#gitops-repository)).
4. **Shared state backend** (S3 bucket + DynamoDB table) from `terraform/tf_state.tf` in the platform app ([State Backend](#state-backend)).
5. **OpenBao**: KV v2 at `secret`, Transit at `transit`, the AWS secrets engine plugin at `aws` with OpenBao's own AWS identity, Kubernetes auth, and smithd's policy and role (chart README, "Terraform apps").
6. **smithd** with `config.openbao.*` and `config.terraform.*` set and `rbac.create: true` ([smithd RBAC](#smithd-rbac)).
7. Per app: one `tf-app` module block in the platform app and `smithctl app terraform enable <app> --env <env>`.

Then check the setup with the hand-written CR in [examples/terraform/terraform-cr.yaml](examples/terraform/terraform-cr.yaml): it must plan, wait for approval, and apply after `approvePlan` is set to `status.plan.pending`, with the state in S3 encrypted (the object has `encrypted_data` and no plaintext resources).

## Runner Image

`Dockerfile.tf-runner` builds one image per OpenTofu version. tofu-controller talks to the runner over gRPC, so the runner must have the controller's version.

| Layer | From |
|---|---|
| Base: `tf-runner` without OpenTofu, user 65532, `tini` | `ghcr.io/flux-iac/tf-runner:v0.16.6-base` (pinned by digest), the same base upstream builds `tf-runner:v0.16.6` from |
| `/usr/local/bin/tofu` | `ghcr.io/opentofu/opentofu:<TOFU_VERSION>-minimal`; the build fails if `tofu version` is not exactly `TOFU_VERSION` |
| `/usr/local/bin/tf-runner-auth` | `cmd/tf-runner-auth`, static Go binary |
| `/etc/deploysmith/aws-config` | `docker/tf-runner/aws-config`: default profile with `credential_process = tf-runner-auth aws-credentials` |

```bash
make docker-build-tf-runner TOFU_VERSION=1.12.9   # → ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.12.9
```

CI builds the supported versions (`tofu` matrix of the `tf-runner` job in `.github/workflows/ci.yml`, now 1.12.9 and 1.13.2) and pushes from master. Add a version there to support it. smithd maps a bundle's pinned OpenTofu version to the tag and pins the image by digest in the CR.

The entrypoint is `tini -- tf-runner-auth exec tf-runner`; the controller only sets args, which are appended. Before it execs `tf-runner` unchanged, `tf-runner-auth`:

1. logs in to OpenBao at `auth/<SMITH_BAO_AUTH_MOUNT>/login` with role `SMITH_BAO_AUTH_ROLE` and the pod's ServiceAccount token. Network errors and 5xx are retried 3 times. A failed login stops the pod, so the run fails with the error instead of running without credentials;
2. sets `BAO_TOKEN` and `VAULT_TOKEN` to the token (for OpenTofu's `openbao` key provider and the `vault` provider), and the address and CA certificate under both the `BAO_*` and `VAULT_*` names;
3. sets `AWS_CONFIG_FILE=/etc/deploysmith/aws-config` if `SMITH_BAO_AWS_ROLE` is set.

tf-runner passes its whole environment to tofu. Whenever the S3 backend or the `aws` provider need credentials, the AWS SDK runs `tf-runner-auth aws-credentials`. It reads `<SMITH_BAO_AWS_MOUNT>/creds/<SMITH_BAO_AWS_ROLE>` with the token and prints the `credential_process` JSON, with `Expiration` from the lease, so the SDK fetches new credentials before the old ones expire. The backend thus has credentials before any provider runs, and nothing is stored on disk. Each process (backend, each `aws` provider) gets its own short-lived STS credentials from OpenBao.

Why Go and not a shell script: the credential helper has to turn OpenBao's JSON into a different JSON shape, which in shell needs `bao` and `jq` in the image (the `bao` binary alone is larger than the runner). In Go it is a small static binary with unit tests (`internal/tfrunnerauth`) and no new dependencies.

### Runner Environment

What smithd puts in `runnerPodTemplate.spec.env` (DEP-18):

| Variable | Value | Notes |
|---|---|---|
| `BAO_ADDR` | e.g. `https://openbao.openbao.svc:8200` (`config.openbao.addr`) | `VAULT_ADDR` is set from it unless given |
| `BAO_CACERT` | path to OpenBao's CA certificate, if not publicly trusted | optional; mounted through `runnerPodTemplate.spec.volumes`; `VAULT_CACERT` is set from it |
| `SMITH_BAO_AUTH_ROLE` | `tf-<app>-<env>`, `tf-<app>-<env>-readonly` for plan-only runs | no login if empty |
| `SMITH_BAO_AUTH_MOUNT` | `config.openbao.runnerAuthMount` | optional, default `kubernetes` |
| `SMITH_BAO_AWS_ROLE` | `<app>-<env>`, `<app>-<env>-readonly` for plan-only runs | no AWS credentials if empty |
| `SMITH_BAO_AWS_MOUNT` | `config.openbao.awsMount` | optional, default `aws` |
| `SMITH_BAO_JWT_PATH` | path of a projected ServiceAccount token | optional, default `/var/run/secrets/kubernetes.io/serviceaccount/token`; use a projected token with audience `config.openbao.runnerAudience` if that is set |
| `TF_ENCRYPTION` | see [State Encryption](#state-encryption) | |

The runner pod's ServiceAccount is `tf-runner-<env>` (or `tf-runner-<env>-readonly`) in `tf-<app>`, so the OpenBao role only accepts that pod. The token lives as long as the role's `token_ttl` (`config.terraform.runnerTokenTtl`, 2h). It is not renewed, and AWS credentials can only be refreshed while it is valid, so it must cover the longest plan or apply. A runner pod serves one reconcile and logs in again on the next one.

## tofu-controller

[examples/terraform/tofu-controller.yaml](examples/terraform/tofu-controller.yaml) has the `HelmRepository` and `HelmRelease` for the gitops repo (e.g. next to Flux's other infrastructure). The settings that matter:

| Value | Why |
|---|---|
| `version: "0.16.6"` (exact) | the runner image is built for it; upgrade both together after re-running the checks in the spec |
| `allowCrossNamespaceRefs: true` | GitRepositories in `flux-system`, CRs in `tf-<app>` |
| `runner.serviceAccount.create: false` | smithd commits the runner ServiceAccounts per app (DEP-17) |
| `runner.serviceAccount.namespacedRoleBindings: true` | `tf-runner-role` can read and write Secrets; the default ClusterRoleBinding would let one app's bundle code read every Secret in the cluster. smithd commits a RoleBinding per `tf-<app>` |
| `awsPackage.install: false` | no AWS primitive modules |
| `runner.image` | our runner, as the default for CRs that set no image |
| `concurrency: 4`, requests 50m / 128Mi, limit 512Mi | enough for a few apps on a small cluster; runner pods have their own resources from the CR |

## Gitops Repository

[examples/terraform/kustomizations.yaml](examples/terraform/kustomizations.yaml), per environment:

- `Kustomization` `terraform-<env>` on `./environments/<env>/terraform`, `prune: true`, **no `wait` or `healthChecks`**: a CR waiting for approval is `Ready=Unknown`. smithd maintains `environments/<env>/terraform/kustomization.yaml`.
- `GitRepository` `gitops-plans` on the branch `plans` and a `Kustomization` `terraform-plans-<env>` on `./environments/<env>/terraform-plans`, for plan-only runs (DEP-22). Create the branch with an `environments/<env>/terraform-plans/kustomization.yaml` holding `resources: []`, otherwise the Kustomization fails until the first plan-only run.
- A read-only deploy key for the per-root GitRepositories, so tofu-controller never gets smithd's write key:

  ```bash
  flux create secret git gitops-readonly -n flux-system \
    --url=ssh://git@github.com/sorenmh/gitops.git --private-key-file=./gitops-readonly
  ```

  Add the public key to the gitops repo as a deploy key without write access.

## State Backend

Each app/env has its backend recorded in smithd (`smithctl app terraform enable`). smithd writes it into the CR's `backendConfig.customConfiguration`, which the controller writes as `backend_override.tf`; a bundle never sets it.

**New apps** use the shared bucket and lock table from `terraform/tf_state.tf` in the platform app (defaults `deploysmith-tfstate` and `deploysmith-tflock`, in the provider's region): a versioned bucket with SSE-S3, public access blocked, TLS-only bucket policy and old versions expiring after 90 days, and a pay-per-request DynamoDB table with hash key `LockID` and point-in-time recovery. Keys are `<app>/<env>/<root>/terraform.tfstate`:

```hcl
backend "s3" {
  bucket         = "deploysmith-tfstate"
  key            = "viino/dev/main/terraform.tfstate"
  region         = "eu-central-1"
  dynamodb_table = "deploysmith-tflock"
  encrypt        = true
}
```

**Existing apps** keep their bucket, key and table.

Isolation is by IAM, not by naming: the `tf-app` module (DEP-17, `terraform/modules/tf-app`) gives each `tf-<app>-<env>` role only `arn:aws:s3:::<bucket>/<app>/<env>/*` and the lock items whose `LockID` starts with `<bucket>/<app>/<env>/` (`dynamodb:LeadingKeys`; the S3 backend writes `<bucket>/<key>` for the lock and `<bucket>/<key>-md5` for the digest). For an existing backend it gets that bucket and table.

OpenTofu 1.10+ can also lock with a lock file in S3 (`use_lockfile = true`) instead of DynamoDB. We keep DynamoDB for now because the existing apps use it and the IAM module is written for it.

## State Encryption

State and saved plans are encrypted by OpenTofu with the app's OpenBao Transit key `tf-<app>` (one per app, created by smithd, DEP-17). Enable the engine once:

```bash
bao secrets enable transit
```

smithd puts the configuration in the runner's `TF_ENCRYPTION`, which overrides any `encryption` block in the bundle, so a bundle can't switch encryption off. It holds no secret: the `openbao` key provider takes the address and token from `BAO_ADDR` and `BAO_TOKEN`, which `tf-runner-auth` sets.

```hcl
key_provider "openbao" "app" {
  key_name            = "tf-viino"
  transit_engine_path = "/transit"     # config.openbao.transitMount
}
method "aes_gcm" "app" {
  keys = key_provider.openbao.app
}
state {
  method   = method.aes_gcm.app
  enforced = true
}
plan {
  method   = method.aes_gcm.app
  enforced = true
}
```

For each state or plan write, the key provider asks OpenBao for a new data key (`transit/datakey/plaintext/tf-<app>`) and stores the wrapped key next to the ciphertext. Reading unwraps it with `transit/decrypt/tf-<app>`. The runner policy allows exactly these on its own key, so a runner can't decrypt another app's state. The plan-only role also needs both: decrypt to read the state, a data key to write the encrypted plan file.

**Existing plaintext state** is encrypted on its first apply through a `fallback`, see [terraform-migration.md](terraform-migration.md#4-turn-on-encryption-with-a-fallback). With `enforced = true` and no fallback, plaintext state is refused (`encountered unencrypted payload without unencrypted method configured`), so an app whose state was never migrated fails safely instead of being overwritten.

Remote state of other apps (`terraform_remote_state`) is encrypted with their key and can't be read. Use data sources, or outputs published to OpenBao KV.

## smithd RBAC

smithd reads, and never writes, the cluster ([spec, RBAC](specs/terraform-controller.md#rbac)):

- cluster-wide `get`/`list`/`watch` on `terraforms.infra.contrib.fluxcd.io` and `get`/`list` on `gitrepositories.source.toolkit.fluxcd.io`, to poll CR status (DEP-18);
- `get`/`list` on ConfigMaps and Events only in `tf-*` namespaces, through the ClusterRole `smithd-terraform-reader` that the chart creates with `rbac.create: true` (DEP-17) and the RoleBinding smithd commits into each runner namespace;
- no Secrets, Pods or logs. The binary plan Secret never leaves the cluster.

## Verified

Checked by hand on 2026-10-29 with the runner image built from this repository (`v0.16.6-tofu1.13.2`), OpenBao 2.5.5 in dev mode and a fake S3 (gofakes3) that logs the access key of each request:

- The image's entrypoint starts `tf-runner` through `tini` and `tf-runner-auth`.
- `tf-runner-auth exec tofu …` logged in to OpenBao and got a token, then `tofu init`/`apply` with the S3 backend and **no AWS credentials in the environment**. Every S3 request was signed with the access key from `aws/creds/<role>` and carried a session token, so `credential_process` worked. OpenBao's JWT auth method was mounted at `kubernetes` because it has the same login API, and a KV mount at `aws` returned `creds/<role>` in the AWS engine's shape. Kubernetes token review and the real AWS engine are not covered.
- The encryption config above with the `tf-demo` key: the state object in S3 held only `encrypted_data` plus the Transit-wrapped key, and the saved plan file was encrypted too.
- Migration: plaintext state was refused by the enforced config, read and written back encrypted by the fallback config, then planned with no changes by the enforced config. `tofu init -migrate-state` moved the encrypted state to another key.
- Isolation: the key provider was denied for another app's key (`tf-other`), `credential_process` was denied for another app's AWS role (403), and login with another app's role was refused.

Not verified here: tofu-controller running the image in a cluster (the spike ran upstream's runner, see the spec), real Kubernetes auth, the AWS secrets engine plugin, DynamoDB locking with the `tf-app` IAM roles, and `remediation: {retries: 1}` (no automatic re-apply after a failed apply). These are part of the "done when" check with the hand-written CR, against the shared backend and one existing app's backend.
