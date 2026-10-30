# Terraform Controller

Which controller reconciles the `Terraform` custom resources smithd commits to the gitops repo, and the exact shape smithd writes and reads. Result of the spike DEP-14 for the epic DEP-13 (Terraform support).

> **Status.** This is the design from the spike; the implementation follows it with these differences, and the examples below are marked where they were written before them. For the finished behaviour read [terraform.md](../terraform.md) and the [chart README](../../charts/smithd/README.md#terraform-apps).
>
> - ServiceAccounts, RoleBindings and OpenBao roles are **per environment**: `tf-runner-<env>` and `tf-runner-<env>-readonly` (not `tf-runner`), RoleBinding `tf-runner-<env>`, and `smithd-terraform-reader-<env>`. Two environments sharing a cluster would otherwise share objects, and a dev runner could log in as prod.
> - The variables file of an environment is `env/<env>.tfvars` in the bundle (not `tfvars/<env>.tfvars`).
> - Plans are read from the ConfigMap only; smithd has no access to Secrets.
> - Approval, stale handling and plan-only runs are implemented as described under [Approval](#approval) and [Plan-Only Runs](#plan-only-runs), plus the checks listed in [terraform.md](../terraform.md#the-model).

## Decision

**Use tofu-controller** (`github.com/flux-iac/tofu-controller`, CRD `terraforms.infra.contrib.fluxcd.io/v1alpha2`), pinned to **v0.16.6**. We do not build our own controller.

Reasons:

- **Maintained**: 7 releases in 2026 (v0.16.0 on 2026-01-27 to v0.16.6 on 2026-10-26), commits almost every day in the last week of October 2026, several active maintainers and outside contributors. Built against Flux `source-controller/api` v1.9.x and OpenTofu 1.12.1. It ran on Flux v2.9.6 in the hands-on test.
- **Covers what DEP-13 needs**: manual approval with a plan id in status (`approvePlan`, `status.plan.pending`), `planOnly`, readable plans (`storeReadablePlan: human`), per-CR `serviceAccountName`, `runnerPodTemplate` (image, env, volumes), custom backend (`backendConfig.customConfiguration`), `tfVarsFiles`, runner pods in the CR's own namespace, Flux-style status conditions.
- **OpenTofu state encryption works** through `TF_ENCRYPTION` in `runnerPodTemplate.spec.env`. The state and the saved binary plan were both encrypted in the test.
- The problems found (below) can be worked around in what smithd generates. None of them needs a fork.

There are two gaps. smithd's CR shape is designed around both:

1. **The plan id is derived from the source revision only** (`plan-<branch>-<first 10 chars of commit SHA>`), not from the plan content or the CR spec. Before applying an approved plan, the controller re-plans and applies the new plan if its id still matches. So:
   - If the source is the same gitops repo branch that smithd commits the approval to, the approval commit changes the revision and the approval never matches (upstream issue [#1878](https://github.com/flux-iac/tofu-controller/issues/1878), fix in open PR [#1890](https://github.com/flux-iac/tofu-controller/pull/1890)). Any unrelated commit to the gitops repo also throws away a pending plan. **Fix: smithd pins each root's `GitRepository` to the commit that holds the bundle source** (`ref.commit`). Commits that only change the CR then don't move the revision.
   - If `approvePlan` stays set to the id of the current revision, the next plan of that revision is applied without a new approval: a spec-only change, a drift plan, or a re-plan after a failed apply (upstream issue [#1129](https://github.com/flux-iac/tofu-controller/issues/1129), confirmed hands-on). **Fix: every commit smithd makes to the CR, except the approval itself, sets `approvePlan: ""`, and smithd commits `approvePlan: ""` again as soon as it sees the apply finish.**
2. **What gets applied is a fresh plan taken at approval time, not the stored plan the user reviewed.** The two only share the same id. With a pinned source and an unchanged spec they differ only if the infrastructure or the data sources changed in between. smithd detects this afterwards by comparing the readable plan hash (see [Approval](#approval)). Blocking it beforehand is possible with a post-planning webhook, but that is not part of the first version.

## Overview

```
smithd ── commit ──► gitops repo
                       ├─ environments/<env>/terraform/<app>/src/<root>/   bundle source (commit A)
                       └─ environments/<env>/terraform/<app>/*.yaml        Namespace, SA, RBAC,
                                                                            GitRepository (ref.commit = A),
                                                                            Terraform CR (commit B)
Flux kustomize-controller ── applies the YAML (Kustomization "terraform-<env>", no health checks)
Flux source-controller    ── fetches the pinned commit A → artifact main@sha1:A
tofu-controller           ── starts runner pod "<cr>-tf-runner" in tf-<app> with the app's SA
runner pod                ── OpenBao login (Kubernetes auth) → BAO_TOKEN; AWS creds via credential_process
                          ── tofu init/plan → binary plan Secret + readable plan ConfigMap in tf-<app>
smithd ── polls the Terraform CR + reads the readable plan ConfigMap ── deployment record
user approves ── smithd commits approvePlan: plan-main-<A[:10]> ── controller re-plans + applies
```

## Gitops Layout

Terraform objects live in their own tree, separate from `environments/<env>/apps/`. Two reasons. A Kustomization with `wait: true` would health-check the `Terraform` CR and fail while a plan waits for approval (`Ready=Unknown`). And the bundle source must not mix with an app's manifests.

```
environments/<env>/terraform/
├── kustomization.yaml                 # resources: [<app>, ...]   (smithd maintains it, like apps/)
└── <app>/
    ├── kustomization.yaml             # resources: [namespace.yaml, <root>.yaml]
    ├── namespace.yaml                 # Namespace, ServiceAccounts, RoleBindings, NetworkPolicy, ResourceQuota
    ├── <root>.yaml                    # GitRepository + Terraform for this env/root
    └── src/<root>/                    # unpacked bundle: *.tf, *.tofu, *.tftpl, .terraform.lock.hcl, env/<env>.tfvars
```

DEP-16 adds one Flux `Kustomization` per environment: `terraform-<env>`, path `./environments/<env>/terraform`, `prune: true`, **no `wait`/`healthChecks`**.

Each deploy pushes **two commits**:

1. Commit A: write `src/<root>/` (replace the directory with the bundle contents).
2. Commit B: write `<root>.yaml` with `GitRepository.spec.ref.commit: <A>` and `Terraform.spec.approvePlan: ""`.

The plan id for the deploy is then `plan-<branch>-<A[:10]>`, e.g. `plan-main-8fa3d36e98`. smithd knows it before the plan exists.

## Resources smithd Generates

Names: namespace `tf-<app>`, CR `<env>-<root>` (e.g. `dev-main`), GitRepository `tf-<app>-<env>-<root>` in the Flux namespace (e.g. `tf-viino-dev-main`). The runner pod is named `<cr>-tf-runner`, so keep CR names short.

### Namespace and RBAC (`namespace.yaml`)

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: tf-viino
  labels:
    deploysmith.io/terraform-app: viino
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tf-runner-dev          # used by apply runs; OpenBao role tf-viino-dev
  namespace: tf-viino
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tf-runner-dev-readonly  # used by plan-only runs (DEP-22); read-only OpenBao/IAM role
  namespace: tf-viino
---
# The runner stores plans as Secrets/ConfigMaps and uses Leases in its own namespace.
# Bind the chart's ClusterRole per namespace; never use the chart's default ClusterRoleBinding.
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tf-runner-dev
  namespace: tf-viino
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tf-runner-role
subjects:
  - {kind: ServiceAccount, name: tf-runner-dev, namespace: tf-viino}
  - {kind: ServiceAccount, name: tf-runner-dev-readonly, namespace: tf-viino}
---
# Lets smithd read the readable plan and the events in this namespace only
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: smithd-terraform-reader-dev
  namespace: tf-viino
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: smithd-terraform-reader
subjects:
  - {kind: ServiceAccount, name: smithd, namespace: deploysmith}
# + NetworkPolicy and ResourceQuota (DEP-17)
```

Committing these to gitops, instead of creating them through the Kubernetes API, means smithd needs no `create namespace` or `bind` rights. Flux applies them. The OpenBao parts (role, policy, Transit key) are still created by smithd through the OpenBao API (DEP-17).

### GitRepository and Terraform (`<root>.yaml`)

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: tf-viino-dev-main
  namespace: flux-system          # keeps the gitops repo credentials out of tf-<app>; needs allowCrossNamespaceRefs
spec:
  interval: 10m
  url: ssh://git@github.com/sorenmh/gitops.git
  secretRef: {name: gitops-readonly}   # read-only deploy key, not smithd's write key
  ref:
    branch: main
    commit: 8fa3d36e98b07c3fa4aa308bfd6992f75e1f38f7   # commit A: the bundle source
  ignore: |
    /*
    !/environments/dev/terraform/viino/src/main/
---
apiVersion: infra.contrib.fluxcd.io/v1alpha2
kind: Terraform
metadata:
  name: dev-main
  namespace: tf-viino
  labels:
    deploysmith.io/app: viino
    deploysmith.io/environment: dev
    deploysmith.io/root: main
  annotations:
    deploysmith.io/version: 42540c4-123        # bundle version id
    deploysmith.io/deployment-id: dep-789
spec:
  interval: 6h                     # plans with no changes every interval; approval/deploy commits trigger at once
  retryInterval: 5m
  remediation: {retries: 1}        # one attempt per generation/revision, no automatic re-apply (read in code, verify in DEP-16)
  sourceRef:
    kind: GitRepository
    name: tf-viino-dev-main
    namespace: flux-system
  path: ./environments/dev/terraform/viino/src/main
  tfVarsFiles:                     # relative to the repo root, not to path
    - environments/dev/terraform/viino/src/main/env/dev.tfvars
  approvePlan: ""                  # smithd sets the full plan id on approval, then "" again after apply
  storeReadablePlan: human
  serviceAccountName: tf-runner-dev
  disableDriftDetection: true      # see Known Limits
  destroyResourcesOnDeletion: false
  alwaysCleanupRunnerPod: true
  backendConfig:
    customConfiguration: |
      backend "s3" {
        bucket         = "deploysmith-tfstate"
        key            = "viino/dev/main/terraform.tfstate"
        region         = "eu-north-1"
        dynamodb_table = "deploysmith-tflock"
        encrypt        = true
      }
  runnerPodTemplate:
    spec:
      image: ghcr.io/sorenmh/deploysmith-tf-runner:v0.16.6-tofu1.12.9@sha256:...
      env:
        - name: BAO_ADDR
          value: https://openbao.openbao.svc:8200
        - name: VAULT_ADDR             # for the vault provider in the root
          value: https://openbao.openbao.svc:8200
        - name: SMITH_BAO_AUTH_ROLE    # OpenBao Kubernetes auth role
          value: tf-viino-dev
        - name: SMITH_BAO_AWS_ROLE     # OpenBao AWS secrets engine role (assumed_role)
          value: viino-dev
        - name: AWS_CONFIG_FILE
          value: /etc/deploysmith/aws-config
        - name: TF_ENCRYPTION
          value: |
            key_provider "openbao" "app" {
              key_name            = "tf-viino"
              transit_engine_path = "/transit"
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
      resources:
        requests: {cpu: 100m, memory: 256Mi}
        limits: {memory: 1Gi}
```

Field notes:

- `backendConfig.customConfiguration` is wrapped by the controller in `terraform { ... }` and written as `backend_override.tf` in the root. It replaces any backend in the bundle. **A bundle could still win with its own `*_override.tf` file that sorts after ours.** So forge/smithd must reject bundles that contain `override.tf`, `*_override.tf`, `*.tf.json` overrides, or a `backend`/`cloud` block (DEP-15/DEP-18).
- `TF_ENCRYPTION` in the environment overrides any `encryption` block in the bundle, so a bundle can't switch encryption off. The config holds no secret: the token comes from `BAO_TOKEN`, which the runner image's entrypoint sets.
- Migrating existing plaintext state (DEP-16): add `method "unencrypted" "migrate" {}` and `fallback { method = method.unencrypted.migrate }` to `state`, set `enforced = false`, apply once, then go back to the block above.
- `tfVarsFiles` only takes non-secret values from the bundle. Secrets are read in the root through the `vault` provider (which reads `VAULT_ADDR`/`VAULT_TOKEN`). We don't use `varsFrom` for now. It is available if smithd ever needs to pass values from a ConfigMap.
- `approvePlan` also accepts a **prefix** of the pending id, plus the special values `auto`, `disable` and `replan…`. smithd must only ever write `""` or the full pending id.

### Runner Image

The upstream runner `ghcr.io/flux-iac/tf-runner:v0.16.6` ships one OpenTofu version (1.12.1; the latest OpenTofu releases are 1.13.2 and 1.12.9). We build our own runner images. One tag per supported OpenTofu version, always on the same tofu-controller version as the controller (the runner speaks gRPC with the controller):

```dockerfile
# Sketch; the real image is built in DEP-16
ARG TOFU_VERSION
FROM ghcr.io/opentofu/opentofu:${TOFU_VERSION}-minimal AS tofu
FROM ghcr.io/openbao/openbao:2.x AS bao
FROM ghcr.io/flux-iac/tf-runner:v0.16.6
COPY --from=tofu /usr/local/bin/tofu /usr/local/bin/tofu
COPY --from=bao  /bin/bao /usr/local/bin/bao
COPY smith-entrypoint smith-aws-creds /usr/local/bin/
COPY aws-config /etc/deploysmith/aws-config
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/smith-entrypoint"]
```

The controller sets only `args` on the runner container, so the image `ENTRYPOINT` runs:

```sh
#!/bin/sh
# smith-entrypoint: log in to OpenBao with the pod's ServiceAccount token, then start the runner
set -eu
jwt=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
BAO_TOKEN=$(bao write -field=token auth/kubernetes/login role="$SMITH_BAO_AUTH_ROLE" jwt="$jwt")
export BAO_TOKEN VAULT_TOKEN="$BAO_TOKEN"
exec tf-runner "$@"
```

```ini
# /etc/deploysmith/aws-config: used by both the S3 backend and the aws provider; refreshed on expiry
[default]
credential_process = /usr/local/bin/smith-aws-creds
```

`smith-aws-creds` runs `bao read -format=json aws/creds/$SMITH_BAO_AWS_ROLE` (OpenBao AWS secrets engine, `assumed_role`) and prints the AWS `credential_process` JSON (`Version`, `AccessKeyId`, `SecretAccessKey`, `SessionToken`, `Expiration`). The runner passes its whole process environment to `tofu` (`os.Environ()` merged with the CR's env), so `BAO_TOKEN`, `VAULT_TOKEN` and `AWS_CONFIG_FILE` reach tofu, the S3 backend, the providers and the `credential_process` child.

A runner pod lives for one reconcile (`alwaysCleanupRunnerPod: true`). Planning and the later apply run in different pods, each with its own login. The OpenBao role's token TTL must cover the longest apply (e.g. 2h). The AWS credentials refresh through `credential_process`.

smithd maps the bundle's pinned OpenTofu version to an image tag and rejects versions it has no image for.

## Reading Status

smithd polls `infra.contrib.fluxcd.io/v1alpha2`, resource `terraforms`, in `tf-<app>` with the dynamic client, the same way `internal/smithd/flux/kustomization.go` polls Kustomizations. Fields:

| Field | Meaning |
|---|---|
| `metadata.generation` | Changes with every CR commit (including the approval) |
| `status.observedGeneration` | Generation the controller last looked at |
| `status.plan.pending` | Id of the plan waiting for approval, e.g. `plan-main-8fa3d36e98`; `""` if none |
| `status.plan.lastApplied` | Id of the last applied plan |
| `status.plan.isDestroyPlan` | Only set for `spec.destroy`; smithd counts destroys itself from the plan text |
| `status.lastPlannedRevision` / `lastAttemptedRevision` / `lastAppliedRevision` | `main@sha1:<full sha of commit A>` |
| `status.lastPlannedGeneration` | Generation the pending plan was made for |
| `status.lastPlanAt` | Time of the last plan; plan ids repeat (same revision), this one does not |
| `status.conditions[]` | `Ready`, `Plan`, `Apply`, `Reconciling`, `Output`, `StateLocked`, `Stalled` |

Observed conditions and how smithd maps them to deployment states (DEP-18/19):

| Deployment state | Conditions / fields |
|---|---|
| `planning` | `lastPlannedRevision` ≠ `main@sha1:<A>`, or `Reconciling=True` with message `Terraform Planning` / `Reconciliation in progress` |
| `awaiting_approval` | `Plan=True` reason `TerraformPlannedWithChanges`; `Ready=Unknown` with message `Plan generated: set approvePlan: "<id>" to approve this plan.`; `plan.pending=<id>`; `lastPlannedRevision=main@sha1:<A>` |
| `applied` (no changes) | `Plan=False` reason `TerraformPlannedNoChanges`; `Ready=True` reason `TerraformPlannedNoChanges`, message `Plan no changes: main@sha1:<A>` |
| `applying` | `Apply=Unknown` reason `Progressing` (`Apply started`); `Ready=Unknown` |
| `applied` | `Ready=True` reason `TerraformAppliedSucceed`, message `Applied successfully: main@sha1:<A>`; `Apply=True`; `plan.lastApplied=<id>`; `lastAppliedRevision=main@sha1:<A>` |
| `failed` | `Ready=False` with reason `TFExecPlanFailed`, `TFExecApplyFailed`, `TFExecInitFailed`, `TFExecNewFailed`, `ArtifactFailed`, `VarsGenerationFailed`, `RetryLimitReached`, `AccessDenied`, … The message holds the tofu error (up to 20000 chars). Also `StateLocked=True` with the lock id |
| `stale` | smithd's own state: a newer deploy was committed for the same env/root, or `lastPlannedGeneration`/`lastPlannedRevision` no longer match what the plan was recorded for |

The runner pod is deleted after each run, so its logs are gone. The condition message and the Events on the `Terraform` object (`kubectl get events`, reasons `TerraformPlanSucceed`, `TerraformAppliedSucceed`, `TFExecPlanFailed`, …) are what smithd keeps for failed runs.

**As implemented (DEP-19)**, `internal/smithd/service/track_terraform.go`: every 10 seconds smithd looks at the CR of each deployment that is `planning`, `awaiting_approval` or `applying` (state is in the database, so a restart loses nothing; a replica only reports a change it made, through a conditional update). A CR is the deployment's if its annotation `deploysmith.io/deployment-id` says so; one that still names an older deployment is Flux not having applied the commit yet, one naming a newer deployment makes this one `stale`. Within that:

- `planning` → `awaiting_approval`: `lastPlannedRevision` is the deployment's source commit, `Plan=True`/`TerraformPlannedWithChanges`, `plan.pending` is the stored plan id, and the ConfigMap's `savedPlan` is that id. smithd stores the text, summary and `plan-hash`. → `applied`: the same with `Ready=True`/`TerraformPlannedNoChanges`. → `failed`: `Ready=False` with `lastAttemptedRevision` equal to the source commit. Without any of these for an hour (Flux not applying the CR, the readable plan unreadable) it fails.
- `awaiting_approval` → `stale`: `lastPlannedRevision` or `plan.pending` is another one, the CR is gone, or the controller planned again (`lastPlanAt` changed) and the `plan-hash` is not the stored one. The same plan again only updates `plannedAt`. → `applying` if `spec.approvePlan` is the plan id (an approval made in git).
- `applying` → `applied`: `Ready=True`/`TerraformAppliedSucceed`, `lastAppliedRevision` is the source commit and `plan.lastApplied` the plan id; smithd then compares the `plan-hash` of the plan made again with the reviewed one and records the result. → `failed`: `Ready=False` for the source commit, or the CR is gone. An applying run is never made stale.
- A failed run keeps the `Ready` reason and message and the last Warning events of the CR in `error_message`.

The approval API (DEP-20, `internal/smithd/service/approve_terraform.go`) sets `applying` itself when it commits the approval (conditional on `awaiting_approval`, so an approval that is repeated or made by two replicas counts once); an approval found in the repo first is recorded with the approver `git`.

### Readable Plan

`storeReadablePlan: human` stores the output of `tofu show tfplan` (sensitive values masked) in a **ConfigMap** in the CR's namespace:

- Name `tfplan-<workspace>-<cr>` = `tfplan-default-dev-main`, key `tfplan`, plain text.
- Over 1 MiB the text is split into `tfplan-default-dev-main-0`, `-1`, … with annotation `infra.contrib.fluxcd.io/plan-chunk: "<n>"`.
- Labels `infra.contrib.fluxcd.io/plan-name=<cr>` and `infra.contrib.fluxcd.io/plan-workspace=default`. smithd lists by these labels, orders by `plan-chunk` and concatenates.
- Annotation `savedPlan: <plan id>`. smithd only uses the ConfigMap if it equals `status.plan.pending`.
- Annotation `infra.contrib.fluxcd.io/plan-hash: <sha256 of the text>`. smithd stores it with the plan.
- Each new plan overwrites it, including the re-plan before apply and the plan after `approvePlan: ""` is reset. smithd copies the text into the deployment record as soon as it sees `awaiting_approval`.

smithd builds the summary by parsing the text: `Plan: X to add, Y to change, Z to destroy.` and the `# <address> will be created / will be destroyed / will be updated in-place / must be replaced` lines. `storeReadablePlan: json` would give machine-readable output, but it is stored as a gzip'd **Secret** (`tfplan-default-<cr>.json`). smithd should not read Secrets in runner namespaces, so we use `human`.

### Binary Plan

The binary plan is a gzip'd **Secret** `tfplan-default-<cr>` (key `tfplan`), chunked into `-0`, `-1`, … over 1 MiB, with the same labels and annotations. It has an ownerReference to the CR. It never leaves the cluster, and with the `plan` encryption block above it is encrypted with the app's Transit key. smithd does not read it.

## Approval

1. Before committing an approval, smithd checks on the live CR: `status.plan.pending == <id>`, `status.lastPlannedRevision == main@sha1:<A>`, `status.lastPlannedGeneration == metadata.generation`, and the ConfigMap's `savedPlan == <id>` and `plan-hash` equals the hash stored in the deployment record. If any check fails, the plan is stale.
2. smithd commits `approvePlan: "<full id>"` and changes nothing else in that commit.
3. The controller sees the generation change, re-plans in a new runner pod, and applies if the new id equals the approved one. With a pinned source it always does.
4. When `Ready=True`/`TerraformAppliedSucceed` and `plan.lastApplied == <id>`, smithd compares the ConfigMap `plan-hash` (now from the re-plan) with the approved hash. If they differ, it marks the deployment "applied plan differed from the reviewed plan".
5. smithd commits `approvePlan: ""`. Then the next plan of the same revision (drift, spec change, retry) waits for approval again.

**As implemented (DEP-20):**

- `POST /deployments/{id}/approve` does the checks of step 1 (`checkPlanCurrent`; the CR must also be the deployment's, by its annotation, and not approve another plan), refuses plans with destroys or replacements unless the request has `confirmDestroy: true` (the error lists them), then commits through `gitops.SetTerraformApproval`. That changes the one `approvePlan:` line of `<root>.yaml`, and only if the file is still the deployment's (annotation) and holds no other approval; the pinned `GitRepository` keeps the revision. Repeating it is a no-op (no new commit) and an `applying` deployment answers 200. Who approved, when, and whether destroys were confirmed are columns of the deployment (migration 009) and in the commit message.
- Step 5 is a pass of the watcher (`resetApprovals`): every approved deployment that is `applied` or `failed` and has no `approval_reset_at` gets `approvePlan: ""` committed (only where the file still is its own and approved), then `approval_reset_at` is set. The work list is in the database, so it survives restarts, a failed push is tried again every 10 s, and replicas race harmlessly: the second finds the file reset already and commits nothing. A failed run is reset too, so the approval can't apply a later plan of the same revision. A newer deploy that rewrote the file already needs no reset.
- Rejecting (`POST /deployments/{id}/reject`) sets `rejected` (with who, when and the reason) and notifies Slack, Web Push and the GitHub check runs; it refuses if the CR already has the approval.

Rejecting a plan changes nothing in git. A new deploy rewrites `<root>.yaml` (new `ref.commit`, `approvePlan: ""`). That clears the pending plan: the controller re-plans on a revision change, and on a spec change while a plan is pending.

## Plan-Only Runs

For draft and published versions (DEP-22) smithd commits to a separate branch `plans` (one Flux `GitRepository` + `Kustomization` per environment for that branch, set up in DEP-16), so `main` stays clean:

```
plans branch:
environments/<env>/terraform-plans/<app>/<plan-name>/src/<root>/   # commit A'
environments/<env>/terraform-plans/<app>/<plan-name>.yaml          # commit B': GitRepository (branch: plans, commit: A') + Terraform
environments/<env>/terraform-plans/<app>/kustomization.yaml        # lists the <plan-name>.yaml files
environments/<env>/terraform-plans/kustomization.yaml              # lists the apps
```

`<plan-name>` is `plan-` and the first 12 characters of the plan run's id, so it is unique per run. The Terraform CR has that name, the readable plan is the ConfigMap `tfplan-default-<plan-name>`, and the GitRepository is `tf-<app>-<plan-name>` in the Flux namespace. The branch is created by smithd if it does not exist (a single commit with a README, no history in common with `main`).

```yaml
apiVersion: infra.contrib.fluxcd.io/v1alpha2
kind: Terraform
metadata:
  name: plan-6a1f3c1e5c0b
  namespace: tf-viino
  annotations:
    deploysmith.io/plan-run-id: 6a1f3c1e-5c0b-4c43-a1f4-1f0f8f4e9d10
spec:
  planOnly: true                   # never applies; there is no approvePlan
  storeReadablePlan: human
  serviceAccountName: tf-runner-dev-readonly
  plan:
    lock: false                    # read-only IAM role can't write the DynamoDB lock item
  disableDriftDetection: true
  # sourceRef → GitRepository on branch "plans" pinned to A'; plan id = plan-plans-<A'[:10]>
  # backendConfig, tfVarsFiles, runnerPodTemplate as above, with the read-only OpenBao/AWS roles
  # (tf-<app>-<env>-readonly, <app>-<env>-readonly)
```

`spec.plan.lock` is a field of v0.16.6 (`Plan configures options that apply only to the plan phase`): `false` runs `tofu plan -lock=false`, so plan-only runs never wait for, or hold, the state lock of a real run.

Status: `Plan=True`/`TerraformPlannedWithChanges` and `Ready=Unknown` with message `Plan generated: This object is in the plan only mode.`, or `TerraformPlannedNoChanges`. smithd follows it like a deployment (`planning`, then `planned` or `failed`), in its own table `plan_runs`: a plan-only run is not a deployment, so it can never be approved or applied, does not count for what runs in an environment and does not take part in the one-active-run rule of a root (DEP-18). Only one plan-only run per app, environment and root is active; a newer one supersedes it.

**Cleanup.** Once the result is stored (or the run failed or was superseded) smithd removes the run's directory and file from the `plans` branch, in one commit for all finished runs, and takes it out of the kustomizations. Flux prunes the CR, and the plan Secret/ConfigMap are garbage-collected through their ownerReference. Once an hour smithd also removes everything on the branch that no active run needs (leftovers of a crash), and when no run is left and the branch has more than 100 commits, replaces its history with a single commit (a force push that only goes through if nobody pushed meanwhile). Every run adds two commits and its cleanup one, so the branch is short and `main` has none of it.

The read-only role still needs Transit **decrypt** to read the state and a data key to encrypt the plan file. It needs `s3:GetObject` but no lock-table writes.

## RBAC

**smithd** (extends `charts/smithd/templates/rbac.yaml`):

```yaml
# ClusterRole, bound with a ClusterRoleBinding
- apiGroups: ["infra.contrib.fluxcd.io"]
  resources: ["terraforms"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["source.toolkit.fluxcd.io"]
  resources: ["gitrepositories"]
  verbs: ["get", "list"]
---
# ClusterRole "smithd-terraform-reader", bound per tf-<app> namespace by the RoleBinding smithd commits
- apiGroups: [""]
  resources: ["configmaps", "events"]
  verbs: ["get", "list"]
```

smithd needs no access to Secrets, Pods or pod logs, and no write access to the cluster. It changes Terraform CRs only through git. Optional later: `patch` on `terraforms` to set `reconcile.fluxcd.io/requestedAt` to trigger a run without a commit.

**tofu-controller** (Helm values, DEP-16):

```yaml
allowCrossNamespaceRefs: true        # GitRepositories in flux-system, CRs in tf-<app>
awsPackage: {install: false}
runner:
  image: {repository: ghcr.io/sorenmh/deploysmith-tf-runner, tag: v0.16.6-tofu1.12.9}  # default runner
  serviceAccount:
    create: false                    # smithd commits the runner ServiceAccounts per namespace
    namespacedRoleBindings: true     # never a cluster-wide RoleBinding for runners
```

The chart's default binds `tf-runner-role` (read/write Secrets and ConfigMaps) with a **ClusterRoleBinding**. That would let one app's bundle code read every Secret in the cluster. With our own ServiceAccount and a RoleBinding per `tf-<app>`, a runner can only reach its own namespace. In there it can read its own plans and the TLS Secret the controller creates for the runner, and nothing else.

**NetworkPolicy** for `tf-<app>` (DEP-17) must allow ingress from the tofu-controller pod to the runner on TCP 30000 (gRPC), and egress to OpenBao, AWS endpoints, the provider registry and DNS.

## Known Limits and Risks

- **Approval binds to the source revision and CR generation, not the plan content** (see [Decision](#decision)). Mitigations: pinned sources, resetting `approvePlan` after apply, a hash comparison after apply. A post-planning webhook (`spec.webhooks`, stage `post-planning`, which receives the JSON plan) could block a differing plan before apply. Possible follow-up for DEP-20. The JSON plan holds sensitive values in clear text, so smithd would have to hash it and drop it.
- **Plan ids are not unique**: every plan of the same revision gets the same id (drift plans, re-plans). smithd identifies a plan by `(id, lastPlanAt, plan-hash)`.
- **Drift detection** is off (`disableDriftDetection: true`). Even so, with `approvePlan: ""` each `interval` makes a normal plan. If the infrastructure drifted, that plan waits for approval like any other. smithd may report it as "drift plan pending". Each interval run starts a runner with credentials, so the interval is long (6h).
- **Two commits per deploy** (source, then CR pinned to it). Both go in one push. If the push fails, both are redone.
- **Plans over 1 MiB** are split over several Secrets/ConfigMaps (read in code, not tested). Bundles/artifacts and plans pass through gRPC between controller and runner, max 4 MiB by default (`runner.grpc.maxMessageSize`).
- **Runner image per OpenTofu version** must be built and kept in step with the controller version.
- **Upstream changes**: the reconciler was restructured on `main` the day after v0.16.6 (#1913, #1917), and `ExternalArtifact` sources were added (#1901). We pin v0.16.6 and re-run the hands-on checks before upgrading. If PR #1890 lands, pinning still stays: it also protects pending plans from unrelated commits.
- **Remote state of other apps** (e.g. the platform app's outputs) is encrypted with *their* Transit key. An app can't read it through `terraform_remote_state` unless its OpenBao policy allows that key. Prefer data sources, or outputs published to OpenBao KV.
- `remediation.retries: 1` behaviour (one attempt, no automatic retry until the next generation/revision) is read in code only. DEP-16 should confirm it.

## Own Controller (not chosen)

If tofu-controller ever has to be replaced, smithd only depends on this contract. A small controller of our own can keep it:

- CR `spec`: source (pinned commit + path), `approvePlan`, `planOnly`, `serviceAccountName`, runner image/env, backend override, tfvars files.
- `status.plan.pending` / `lastApplied` with the same id format, the `Ready`/`Plan`/`Apply` conditions with the reasons above, and `lastPlannedRevision` / `lastPlannedGeneration` / `lastPlanAt`.
- The readable plan in ConfigMap `tfplan-default-<cr>` with `savedPlan` and `plan-hash` annotations.
- Runs as a Kubernetes Job per plan/apply in `tf-<app>`, applying the **stored** binary plan rather than re-planning. That would close the "approved plan ≠ applied plan" gap. The cost is writing and maintaining it (runner lifecycle, locking, chunked plan storage, retries), which tofu-controller already does.

## Verified Hands-On vs Read

Hands-on (2026-10-28, kind v0.30.0 / Kubernetes v1.34.0, Flux v2.9.6 source/kustomize/helm controllers, tofu-controller chart 0.16.6 installed by a Flux `HelmRelease`, runner `tf-runner:v0.16.6`, a small in-cluster git server, root with `terraform_data`):

- CR in its own namespace `tf-demo` with a custom ServiceAccount bound to `tf-runner-role` through a RoleBinding (`namespacedRoleBindings: true`). The runner pod ran in `tf-demo` as `demo-dev-main-tf-runner` and was deleted after each run.
- `GitRepository` with `ref.branch: main` + `ref.commit` and an `ignore` limiting the artifact to the root. Revision `main@sha1:<commit>`, plan id `plan-main-8fa3d36e98`.
- `approvePlan: ""` → plan stops: `Plan=True TerraformPlannedWithChanges`, `Ready=Unknown` with the approve message, `status.plan.pending` set.
- Readable plan in ConfigMap `tfplan-default-demo-dev-main` (plain `tofu show` text, annotations `savedPlan`, `plan-hash`, labels `plan-name`, `plan-workspace`). Binary plan in Secret `tfplan-default-demo-dev-main`.
- **Approval by git commit** (Flux Kustomization applying the CR): the controller re-planned, then applied. `Ready=True TerraformAppliedSucceed`, `plan.lastApplied=plan-main-8fa3d36e98`.
- `TF_ENCRYPTION` from `runnerPodTemplate.spec.env` (pbkdf2 key provider): state and binary plan were encrypted.
- `backendConfig.customConfiguration` (Kubernetes backend in the test) and `tfVarsFiles` relative to the repo root.
- New deploy (re-pin to a new commit, `approvePlan: ""`) → new plan. A plan with no changes gives `Ready=True TerraformPlannedNoChanges`.
- **Risk confirmed**: with `approvePlan` left at the current revision's id, a spec-only change (`vars`) was planned and applied without a new approval.
- Source not pinned: the approval was applied only because tofu-controller reconciled before source-controller fetched the approval commit. That is a race. Code and issue #1878 show the endless loop when source-controller wins.

Read in code/docs only: S3 + DynamoDB backend, the OpenBao key provider and Kubernetes auth from the runner, AWS credentials via `credential_process`, the custom runner image/entrypoint, chunking over 1 MiB, `planOnly` and `plan.lock: false`, `remediation`, post-planning webhooks, and whether the `plan-hash` is stable between the reviewed plan and the re-plan.
