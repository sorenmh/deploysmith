# Terraform with DeploySmith

DeploySmith deploys Terraform (OpenTofu) the way it deploys Kubernetes manifests: CI uploads a versioned bundle, smithd commits it to the gitops repo, and a controller in the cluster runs it. The difference is a plan that a person approves before anything is applied.

This is the user guide. Where to find the rest:

| You want to | Read |
|---|---|
| Understand and use it (developers, operators) | this page |
| Set up the cluster once (controller, runner image, state backend, encryption) | [terraform-cluster-setup.md](terraform-cluster-setup.md) |
| Configure smithd (chart values, OpenBao, RBAC, GitHub App) | [chart README, "Terraform apps"](../charts/smithd/README.md#terraform-apps) |
| Move an existing Terraform app over | [terraform-migration.md](terraform-migration.md) |
| See the exact CR shape and why | [specs/terraform-controller.md](specs/terraform-controller.md) |
| Look up `forge` commands | [forge.md](forge.md) |
| IAM roles for an app | [`terraform/modules/tf-app/README.md`](../terraform/modules/tf-app/README.md) |

Contents: [The model](#the-model) · [Bundle layout and rules](#bundle-layout-and-rules) · [CI workflow](#ci-workflow) · [Developer workflow](#developer-workflow) · [Operator workflow](#operator-workflow) · [Secrets and AWS credentials in a root](#secrets-and-aws-credentials-in-a-root) · [Platform app and shared infrastructure](#platform-app-and-shared-infrastructure) · [An app with manifests and Terraform](#an-app-with-manifests-and-terraform) · [Troubleshooting](#troubleshooting) · [Known limits and what has not been tested](#known-limits-and-what-has-not-been-tested)

## The model

- **CI never touches credentials.** It checks the configuration without a backend (`tofu init -backend=false`, `fmt`, `validate`) and uploads a **bundle**: the root module, the provider lock file, the pinned OpenTofu version and non-secret variables per environment. The version id is the git SHA, as for manifests. Bundles are stored in S3 like every other version.
- **Gitops and a controller.** Deploying a version makes smithd commit the bundle and a `Terraform` custom resource to the gitops repo. [tofu-controller](https://github.com/flux-iac/tofu-controller) (Flux) runs it in a pod in the app's own namespace `tf-<app>`. smithd, not the bundle, decides the state backend, the state encryption key, the ServiceAccount and the variables file.
- **Plan, approve, apply.** The controller plans and stops. smithd copies the readable plan (sensitive values masked by OpenTofu) into its database and shows it in `smithctl`, the web UI and, as a summary, on the GitHub commit. Someone approves (or rejects) it; approving is a git commit of `approvePlan: <plan id>`, and then the controller applies. Nothing is applied automatically, and auto-deploy policies never deploy a Terraform version.
- **Secrets from OpenBao, never from CI.** The runner logs in to OpenBao with its Kubernetes ServiceAccount and can read only `secret/apps/<app>/*`. The root reads secrets with the `vault` provider.
- **AWS credentials from OpenBao.** The OpenBao AWS secrets engine hands the runner short-lived STS credentials (about an hour) for the IAM role `tf-<app>-<env>`. Those roles are created by one Terraform module in the platform app.
- **State in S3 with DynamoDB locking, encrypted per app.** New apps share one bucket and lock table, separated by IAM; existing apps keep their backend. OpenTofu state encryption uses one OpenBao Transit key per app (`tf-<app>`), so an app's runner cannot decrypt another app's state.
- **Rollback is a new plan.** Terraform has no rollback. Deploying an older version produces an ordinary plan (labelled as a rollback in the record) that must be reviewed and approved like any other.

### Lifecycle

```text
 CI (no credentials)                 smithd                        cluster
 -------------------                 ------                        -------
 tofu init -backend=false
 tofu fmt -check / validate
 forge init --artifact-type terraform
 forge upload --terraform  ───────►  version (type terraform)
 forge plan --wait (optional)  ───►  plan-only run on the plans branch ──► read-only runner plans
 forge publish             ───────►  published (auto plan-only run per enabled env)

 smithctl deploy / web UI  ───────►  deployment `planning`
                                     commit A: bundle source
                                     commit B: GitRepository (pinned to A) + Terraform CR, approvePlan: ""
                                                                   Flux ──► tofu-controller ──► runner pod
                                                                                                 logs in to OpenBao
                                                                                                 plans with AWS creds
                                     deployment `awaiting_approval`  ◄── readable plan copied from the cluster
 smithctl deployment approve ─────►  commit approvePlan: <plan id>
 (or web UI)                         deployment `applying`            ──► runner re-plans, applies
                                     deployment `applied` / `failed` ◄── status of the CR
                                     commit approvePlan: ""  (reset, always)
```

Deployment states: `planning`, `awaiting_approval`, `applying`, `applied`, or `failed`, `stale` (a newer deploy or a change in the cluster replaced the plan), `rejected`. A plan without changes goes straight to `applied`.

Two things to know about how approval works, because the controller is not ours:

- The controller **plans again right before it applies** and applies that fresh plan if its id (derived from the source commit) still matches the approved one. smithd stores the hash of the reviewed plan and compares it afterwards: if the applied plan differed, the deployment shows `appliedDiffered` (web UI warning, `appliedDiffered` in the API). smithd also pins the source to one commit so nothing but a new deploy can change what is planned.
- smithd sets `approvePlan` back to `""` on every change to the CR and after every apply. Otherwise the controller would apply the next plan of the same revision (a retry, a drift plan) without asking again.

## Bundle layout and rules

A bundle is **one OpenTofu root module**. `forge upload --terraform <dir>` packs these files and leaves everything else out; smithd checks the bundle again when the version is published and when it is deployed.

```text
infra/
  versions.tf             # terraform { required_version = "1.13.2" ... }
  main.tf, ...            # *.tf and *.tofu; local modules in subdirectories are included
  templates/*.tftpl       # templates for templatefile(), anywhere
  .terraform.lock.hcl     # provider lock file, from tofu init (required)
  env/
    staging.tfvars        # non-secret variables, one file per environment
    production.tfvars
```

What is accepted:

| File | Rule |
|---|---|
| `*.tf`, `*.tofu` | Must be valid HCL. Local modules in subdirectories are included. |
| `*.tftpl` | Template files for `templatefile()`. Packed, not parsed. |
| `.terraform.lock.hcl` | Required. Run `tofu init` and commit it. |
| `env/<env>.tfvars` | Optional, one per environment. smithd passes `env/<environment>.tfvars` to the controller when the file exists; if there is none for an environment, no variables file is used (and nothing warns you). |

What makes a bundle **fail** (forge and smithd list every problem at once):

- **No exact `required_version`** in the root module's `terraform` block, set exactly once. `"1.13.2"` and `"= 1.13.2"` are fine; `">= 1.10"` and `"~> 1.13"` are not. OpenTofu enforces the pin itself, and smithd uses it to choose the runner image. A `.opentofu-version` file is not read.
- **No `.terraform.lock.hcl`.**
- **Backend settings.** `backend "s3" {}` is allowed, empty; any attribute or block inside it is refused, and so is a `cloud` block. smithd writes the backend as `backend_override.tf`.
- **Override files** (`override.tf`, `*_override.tf` and their `.tofu` and JSON variants) anywhere in the bundle: they could replace the backend smithd sets.
- **State and automatic variable files**: `*.tfstate*`, `*.auto.tfvars`, `terraform.tfvars` (and their `.json` variants). OpenTofu would load them on its own. Use `env/<env>.tfvars`.
- **A `.terraform` directory.** forge skips hidden directories when packing, so the directory `tofu init` creates does no harm in CI; a bundle that contains one is refused by smithd.
- **JSON configuration** (`*.tf.json`, `*.tofu.json`). Not supported yet ([DEP-27](#known-limits-and-what-has-not-been-tested)): leaving it out would plan the destruction of what it declares, so it is refused.
- **Symbolic links.**
- **Files that are not part of a bundle** found in an uploaded archive (smithd refuses them; forge simply does not pack them).
- Invalid HCL, or more than 32 MiB unpacked.

**Data files read with `file()`** (a `policy.json`, `*.yaml`, `*.sh`) are *not* packed: rename templates to `.tftpl`, or inline the content. Until [DEP-27](#known-limits-and-what-has-not-been-tested) lands a root that reads such a file plans with an error rather than silently wrong.

**No secrets in the bundle.** `env/<env>.tfvars` holds non-secret values only; secrets come from OpenBao at plan time ([below](#secrets-and-aws-credentials-in-a-root)).

A minimal root:

```hcl
terraform {
  required_version = "1.13.2"

  backend "s3" {} # empty: smithd sets bucket, key, table and encryption

  required_providers {
    aws = { source = "opentofu/aws", version = "~> 6.0" }
  }
}

variable "environment" {
  type = string
}
```

with `env/production.tfvars`:

```hcl
environment = "production"
```

smithd refuses to deploy a bundle whose pinned OpenTofu version has no runner image (`400 unsupported_opentofu_version`, the message lists the supported versions). The images are listed in the chart's `config.terraform.runnerImages`.

## CI workflow

CI needs `SMITHD_URL` and an API key for smithd, and **no cloud credentials**. Example for GitHub Actions, for a Terraform app `viino-backend-infra` with its root in `infra/`:

```yaml
name: infra
on:
  pull_request:
    paths: ["infra/**"]
  push:
    branches: [master]
    paths: ["infra/**"]

permissions:
  contents: read

env:
  SMITHD_URL: ${{ secrets.SMITHD_URL }}
  SMITHD_API_KEY: ${{ secrets.SMITHD_API_KEY }}
  APP: viino-backend-infra
  # The commit people see: the PR's head, not the merge commit of the run
  VERSION: ${{ github.event.pull_request.head.sha || github.sha }}

jobs:
  bundle:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: opentofu/setup-opentofu@v1
        with:
          tofu_version: 1.13.2 # the version pinned in required_version

      - name: Check the configuration (no backend, no credentials)
        run: |
          tofu -chdir=infra init -backend=false
          tofu -chdir=infra fmt -check
          tofu -chdir=infra validate

      - name: Install forge
        run: |
          curl -L https://github.com/sorenmh/deploysmith/releases/download/v1.0.0/forge-linux-amd64 -o forge
          chmod +x forge && sudo mv forge /usr/local/bin/

      - name: Upload the bundle
        run: |
          forge init --app "$APP" --version "$VERSION" --git-branch "$GITHUB_REF_NAME" --artifact-type terraform
          forge upload --terraform infra/

      # Pull requests: plan the draft, don't publish it
      - name: Plan
        if: github.event_name == 'pull_request'
        run: |
          code=0
          forge plan --wait --env production || code=$?
          if [ "$code" -eq 2 ]; then
            echo "::warning::the plan destroys or replaces resources"
          elif [ "$code" -ne 0 ]; then
            exit "$code"
          fi

      # master: publish; smithd plans the new version for every environment that has Terraform enabled
      - name: Publish
        if: github.event_name == 'push'
        run: forge publish --app "$APP" --version "$VERSION"
```

`forge init` also records the repository, the commit and the pull request from the GitHub Actions environment (the PR's head commit, not the merge commit), which smithd needs to report on the commit. Outside GitHub Actions pass `--source-repo`, `--source-sha` and `--pr`.

### `forge plan` exit codes

| Code | Meaning |
|---|---|
| `0` | The plan was made, with or without changes |
| `1` | The plan failed, was replaced by a newer plan of the same app and environment, `--timeout` (default 30m) passed, or forge could not reach smithd |
| `2` | The plan was made and **destroys or replaces resources** |

Without `--wait` forge starts the plan and returns `0`; the result is the check run on the commit and `GET /api/v1/plan-runs/<id>`. `--output json` prints the plan run for scripts, `--full` the whole plan.

### The plan on the pull request

When smithd is configured with a GitHub App ([github-checks.md](github-checks.md); the app needs **Checks: Read and write** and must be installed on the repository), every plan-only run shows up as the check run **`deploysmith/plan-<env>`** on the commit the version was built from, so on the pull request:

- in progress while the controller plans;
- *success* for a plan without destroys (including "no changes"), *neutral* if it destroys or replaces resources, *failure* if the plan failed;
- the summary lists the counts and the resources by action, with destroys highlighted. The plan text is **not** sent to GitHub; it is in the web UI and `smithctl`, behind login.

`forge publish` starts a plan-only run for every environment with Terraform enabled (`config.terraform.autoPlan`, default on). A draft that is never published only gets plans from `forge plan`. Deployments report as `deploysmith/deploy-<env>` and the plan of a deployment as `deploysmith/plan-<env>` on the same commit.

## Developer workflow

**See a plan for local changes without pushing**, with the credentials staying in the cluster:

```bash
smithctl login --url https://smithd.example.com   # once; GitHub device flow
forge plan --terraform infra/ --env staging       # --app viino-backend-infra, or forge app-bind
```

forge validates and packs `infra/` exactly like `forge upload --terraform`, uploads it as a **temporary version** `local-<user>-<content hash>`, and always waits for the plan, with the exit codes above. Details:

- It needs a signed-in user (`smithctl login`, see [login-with-github.md](login-with-github.md)). API keys are refused (`403 login_required`).
- Temporary versions can never be published or deployed, are hidden from `smithctl version list` unless you pass `--all`, and are deleted with their bundle after 24 hours (`config.terraform.localPlanTtl`).
- A newer local plan of yours for the same app and environment replaces your earlier one. Other people's plans and CI's are untouched.
- Terraform must be enabled for the app in that environment, or smithd answers `terraform_not_enabled`.
- No check runs and no Slack or push notifications are sent for local plans.

The plan is made with the **read-only** role of the app and environment, without locking the state, so it can run while a deployment of the same root is planning, waiting for approval or applying.

## Operator workflow

### Enable Terraform for an app (once per app and environment)

```bash
smithctl app register viino-backend-infra
# 1. IAM roles: one module block in the platform app (see "Platform app" below), then apply it
# 2. smithd: namespace tf-<app> in gitops, OpenBao policies/roles, Transit key
smithctl app terraform enable viino-backend-infra --env production      # shared backend
smithctl app terraform list viino-backend-infra                         # shows the backend and provisioning errors
smithctl app terraform sync viino-backend-infra [--env production]      # provision again; safe to repeat
smithctl app terraform update viino-backend-infra --env production --encryption-fallback=false
```

An existing app keeps its state: `smithctl app terraform enable <app> --env <env> --backend-bucket ... --backend-key ... --backend-region ... --backend-lock-table ... [--aws-role-arn ...] [--encryption-fallback]`. See [terraform-migration.md](terraform-migration.md). `update` only changes `--encryption-fallback`, and applies from the next deploy. There is no `disable` command yet.

### Deploy, review, approve

```bash
smithctl deploy 6996908 --app viino-backend-infra --env production
# Deployment initiated ... status planning; prints how to approve when the plan is ready

smithctl deployment show <deployment-id>            # status and plan summary
smithctl deployment show <deployment-id> --plan     # the full plan text
smithctl deployment approve <deployment-id>
smithctl deployment approve <deployment-id> --confirm-destroy   # needed if the plan destroys or replaces resources
smithctl deployment reject <deployment-id> --reason "wrong environment"
```

- smithd tells you when the plan is ready: Slack and Web Push ("plan ready for approval", with destroys highlighted), the check runs on the commit, `smithctl deployment show`.
- **Approve** commits only `approvePlan: "<full plan id>"` to the CR, after checking that the plan is still the one you reviewed. If not, the answer is `409 plan_not_current` with the reason. A plan that destroys or replaces resources is refused with `400 destroy_confirmation_required`, listing the resources, until you pass `--confirm-destroy`. Approving a deployment that is already applying is not an error.
- **Reject** marks the deployment `rejected` and leaves the CR unapproved. Deploy again to get a new plan.
- Who approved or rejected, when, and whether destroys were confirmed are recorded in the deployment (`smithctl deployment show`, the API) and in the git commit. smithd records the signed-in GitHub login, or `api-key`. Anyone who may use smithd may approve: there is no approver allowlist yet.
- A new deploy while a plan waits **replaces it**: the old deployment becomes `stale`. While a deployment is applying, a new deploy is refused with `409 terraform_applying` and can be repeated when the apply is done.

### Web UI

The deployment page shows the plan: counts, destroys highlighted, resources per action, the full text (collapsed), the plan id, a link to the source commit, who approved or rejected, a rollback tag, the `appliedDiffered` warning and the error of a failed run. **Approve** and **Reject** buttons appear only while the deployment is `awaiting_approval`; for a plan that destroys or replaces resources you must **type the environment name** before approving. The deployment list shows the plan counts and polls while a deployment is planning or applying. Web Push notifications open the deployment page.

### Rollback

`smithctl deploy <older version> --app ... --env ...` deploys an older bundle as a normal deploy: it produces a **new plan**, tagged `rollback`, which you review and approve. It reverses nothing by itself: if the newer version created a resource that the older code does not declare, the plan will destroy it.

## Secrets and AWS credentials in a root

Every runner pod starts through `tf-runner-auth`, which logs in to OpenBao with the pod's ServiceAccount (Kubernetes auth, role `tf-<app>-<env>`, or `tf-<app>-<env>-readonly` for plan-only runs) before OpenTofu starts. The pod gets these from smithd; the bundle sets none of them:

| Variable | Set by | Used for |
|---|---|---|
| `BAO_ADDR`, `VAULT_ADDR` | smithd, `tf-runner-auth` | OpenBao's address |
| `BAO_TOKEN`, `VAULT_TOKEN` | `tf-runner-auth` | the runner's OpenBao token (valid `config.terraform.runnerTokenTtl`, default 2h) |
| `AWS_CONFIG_FILE` | `tf-runner-auth` | default profile with `credential_process`, so the S3 backend and the `aws` provider get short-lived credentials from `aws/creds/<app>-<env>` on their own |
| `TF_ENCRYPTION` | smithd | state and plan encryption with the app's Transit key (a bundle cannot turn it off) |

### Reading secrets

The runner's policy allows reading `secret/data/apps/<app>/*` (KV v2 at the `secret` mount) and nothing else in KV. Put secrets there once, with the OpenBao CLI or UI, never in CI or the repo:

```bash
bao kv put -mount=secret apps/viino-backend-infra/database password=...
```

and read them with the `vault` provider, which takes its address and token from the environment:

```hcl
terraform {
  required_providers {
    vault = { source = "opentofu/vault", version = "~> 5.0" }
  }
}

provider "vault" {
  # Address and token come from VAULT_ADDR and VAULT_TOKEN.
  # The runner's token may not create child tokens, which the provider does
  # by default.
  skip_child_token = true
}

data "vault_kv_secret_v2" "database" {
  mount = "secret"
  name  = "apps/viino-backend-infra/database"
}

resource "aws_db_instance" "main" {
  # ...
  password = data.vault_kv_secret_v2.database.data["password"]
}
```

The value ends up in the state (which is encrypted) and, if it is shown in a plan, in the stored plan text unless the attribute is marked sensitive. See [Known limits](#known-limits-and-what-has-not-been-tested). The read-only role of plan-only runs can read the same secrets.

### AWS credentials

Nothing to configure for the standard providers: the `aws` provider and the S3 backend find the credentials through the shared config file. Only set the region:

```hcl
provider "aws" {
  region = "eu-north-1"
}
```

The credentials are for the IAM role `tf-<app>-<env>` (read-only variant for plan-only runs). What that role may do is whatever the platform app granted it through `policy_arns` ([next section](#platform-app-and-shared-infrastructure)); a missing permission shows as an AWS `AccessDenied` in the plan error.

## Platform app and shared infrastructure

Infrastructure that several apps use (VPC, DNS, the IAM roles themselves) belongs to one **platform app**, which is an ordinary Terraform app. Its own IAM role is the one thing created by hand (nothing else can create it); after that it creates everyone else's:

```hcl
# in the platform app
module "tf_app" {
  source = "github.com/sorenmh/deploysmith//terraform/modules/tf-app?ref=<commit>"

  name                  = "viino"
  environments          = ["dev", "prod"]
  openbao_principal_arn = aws_iam_user.openbao.arn   # OpenBao's own AWS identity
  state = {
    bucket     = "deploysmith-tfstate"
    lock_table = "deploysmith-tflock"
  }
  policy_arns = [aws_iam_policy.viino.arn]           # what viino's Terraform may manage
}
```

Adding an app is one module block and one apply, followed by `smithctl app terraform enable <app> --env <env>`. The module creates `tf-<name>-<env>` and `tf-<name>-<env>-readonly`, trusted only by OpenBao and limited to the app's own state objects and lock items. See [the module README](../terraform/modules/tf-app/README.md) for all inputs, the permissions boundary and OpenBao's own AWS identity.

How other apps use what the platform provides:

- **Data sources** (`data "aws_vpc"`, `data "aws_route53_zone"`, looked up by tag or name). This is the default.
- **Do not use `terraform_remote_state` on another app's state.** It is encrypted with that app's Transit key and the runner's IAM role cannot read another app's state objects, by design. Publish what others need as AWS resources they can look up (tags, SSM parameters; grant the read in `policy_arns`) or pass it as a variable in `env/<env>.tfvars`.
- There is **no ordering of deploys across apps**. If the platform app must be applied first, apply it first.

## An app with manifests and Terraform

An app has one artifact type per version, so an app with Kubernetes manifests **and** Terraform is, today, **two apps from the same repository**:

| App | Artifact | Example |
|---|---|---|
| `viino-backend` | manifests | the Deployment, Service, ... |
| `viino-backend-infra` | terraform | the database, queue, IAM, ... |

Both can use the git SHA as version id: version ids are unique per app, not globally. In CI run the two uploads one after the other, with `--app` on every command (`forge app-bind` binds one app per directory, and `forge init`/`publish` use the shared `.forge/` directory, which `publish` removes):

```bash
forge init --app viino-backend-infra --version "$GIT_SHA" --artifact-type terraform
forge upload --terraform infra/
forge publish --app viino-backend-infra --version "$GIT_SHA"

forge init --app viino-backend --version "$GIT_SHA"
forge upload manifests/
forge publish --app viino-backend --version "$GIT_SHA"
```

Deploy **infrastructure first**, wait for the plan, approve it and let it finish (`applied`), then deploy the app:

```bash
smithctl deploy "$GIT_SHA" --app viino-backend-infra --env production   # then approve
smithctl deploy "$GIT_SHA" --app viino-backend --env production
```

smithd does not enforce that order (see "no ordering" above), and an auto-deploy policy can deploy the manifest app on its own as soon as it is published, before the infrastructure is applied. If the order matters, do not give the manifest app an auto-deploy policy for that environment, or accept that the app may start before its infrastructure.

Both apps report on the same commit: the check run is named `deploysmith/deploy-<env>` (and `deploysmith/plan-<env>` for plans) **without the app name**, one run per deployment, with the app in the summary. smithd creates both; whether the GitHub checks list shows both or only the newest run with that name was not tested here, and GitHub may collapse runs that share a name. The app name is in each run's summary and in the web UI link. A follow-up issue tracks naming the checks per app.

## Troubleshooting

| Symptom | Likely cause and what to do |
|---|---|
| Deployment stays `planning` for a long time; `failed` after 1 hour with "Flux has not applied the Terraform CR ..." or "the controller has not planned this deployment" | Flux has not applied the commit, or tofu-controller has not run. Check the gitops repo has the two new commits under `environments/<env>/terraform/<app>/`; `flux get kustomizations terraform-<env>` and `flux get sources git` (the GitRepository `tf-<app>-<env>-main` must be Ready, which needs the `gitops-readonly` deploy key); `kubectl -n tf-<app> get terraform` and `kubectl -n tf-<app> describe terraform <env>-main` (events, `Ready` message). Check the Flux webhook/Receiver or just wait for the interval. A plan-only run also needs the `gitops-plans` GitRepository and Kustomization `terraform-plans-<env>` on the `plans` branch ([cluster setup](terraform-cluster-setup.md#gitops-repository)). |
| `400 unsupported_opentofu_version` on deploy or plan | The bundle pins a version for which `config.terraform.runnerImages` has no image. Pin one of the versions in the message, or build and add a runner image ([cluster setup](terraform-cluster-setup.md#runner-image)). |
| `400 terraform_not_enabled` / `terraform_not_provisioned` | Run `smithctl app terraform enable ...`; if provisioning failed, `smithctl app terraform list <app>` shows why (OpenBao denied a write, gitops push failed); fix and `sync`. |
| `400 terraform_disabled` / `terraform_plans_disabled` / `terraform_unavailable` | smithd has no `config.openbao.addr`, or no plans branch configured, or it does not run inside a cluster (it cannot read the CR). |
| `400 validation_failed` on publish or deploy | The bundle breaks a rule ([above](#bundle-layout-and-rules)); the message lists every problem. |
| `409 plan_not_current` on approve | The plan you are looking at is not the controller's current one: the CR was changed, planned again with a different result, or belongs to another deployment. The message names the reason. Deploy again, or wait for the new plan and review that. |
| `409 terraform_applying` / `409 terraform_deploying` on deploy | The root is being applied (wait; repeat the deploy afterwards), or another deploy is still being committed (try again in a moment). A running apply is never interrupted. |
| `409 not_awaiting_approval` | The deployment is `planning`, `stale`, `failed`, `rejected` or `applied`; the message says which and what to do. |
| Deployment is `stale` | A newer deploy replaced the plan (error message "Replaced by deployment ..."), or the cluster moved on (message "Stale: ..." with the reason: CR deleted, another revision, another pending plan, or the controller planned again with a different result). Deploy again. A stale plan cannot be approved. |
| Plan fails with "Error acquiring the state lock" | Another run holds the lock in DynamoDB (an apply in progress, or an earlier run that died). Wait; if no run is active, delete the lock item (`LockID` is `<bucket>/<state key>`) as an administrator. Plan-only runs do not lock. |
| Runner pod fails right away; `Ready` message mentions OpenBao login, `permission denied` or `invalid role name` | The runner could not log in. Check: `smithctl app terraform list <app>` for provisioning errors; the role `tf-<app>-<env>` exists in OpenBao and is bound to ServiceAccount `tf-runner-<env>` in namespace `tf-<app>`; the Kubernetes auth mount (`config.openbao.runnerAuthMount`); `config.openbao.runnerAudience` matches the token audience; OpenBao is reachable from the namespace (NetworkPolicy, `config.openbao.addr` port). `smithctl app terraform sync <app>` rewrites the roles. |
| `AccessDenied` from AWS, or OpenBao `aws/creds/...` fails | The IAM role `tf-<app>-<env>` is missing, does not trust OpenBao's identity, or lacks permissions for what the root manages (`policy_arns`). Check `config.terraform.awsAccountId` and the module block. |
| `terraform_remote_state` of another app fails to read or decrypt | Not supported by design, see [platform app](#platform-app-and-shared-infrastructure). |
| `encountered unencrypted payload without unencrypted method configured` | An existing app's plaintext state meets the enforced encryption. Enable `--encryption-fallback` for the first apply ([migration](terraform-migration.md)). |
| `approvePlan` has a value in the gitops repo after an apply | smithd resets it to `""` at the next poll after `applied`/`failed`, and on every change to the CR. This matters: with the old id left in place, the controller would apply the next plan of that revision (a retry or drift plan) without approval. If the reset keeps failing (git push errors in the smithd log), clear it by hand. |
| The plan text shows a secret | See below. |

Where to look: `smithctl deployment show <id>` (the `errorMessage` holds the controller's `Ready` message and the last warning events of the CR; runner pods and their logs are deleted after each run), the smithd log (all steps are logged with the deployment id), and `kubectl -n tf-<app> get terraform,events`.

## Known limits and what has not been tested

Limits of the design:

- **Plan text may contain secrets.** OpenTofu masks values it knows are sensitive (marked `sensitive`, or from providers that mark them). Values from elsewhere that happen to be secret are shown in the stored plan. The text is only available behind login (web UI, `smithctl deployment show --plan`, `GET /deployments/{id}`) and never sent to GitHub, Slack or push. The binary plan, which can hold anything, stays in the cluster.
- **Drift is not tracked.** The CR has drift detection off. With the 6 hour interval and `approvePlan: ""` the controller plans again now and then; if the result differs from the reviewed plan, smithd marks the deployment `stale` (it does not report "drift"). Nothing shows you infrastructure that changed outside DeploySmith until the next deploy.
- **What is applied is a fresh plan**, not the stored one; see [the model](#lifecycle). smithd detects a difference afterwards (`appliedDiffered`), not beforehand.
- **A plan waiting for approval does not expire.** It becomes `stale` when something changes.
- **One root per app and environment** (`main`). The root name is already part of the state key, so more roots later need no migration.
- **No cross-app ordering**, no approver allowlist, no `disable` command, JSON configuration and data files read with `file()` are not packed (DEP-27), providers are downloaded at runtime and pinned by the lock file.
- **Authentication in CI** is still a stored API key (GitHub Actions OIDC is DEP-28).
- Plans over 1 MiB are split over several ConfigMaps by the controller; reading them is implemented but was not tested with a real large plan.

**Not tested against real infrastructure.** All of this is covered by unit and integration tests with fakes (fake GitHub, fake Kubernetes API, a real OpenBao dev server, fake S3), and parts by hand in `kind`. Before trusting it with production, run the following once end to end:

- smithd's flow with a **real tofu-controller**: the controller was exercised in `kind` with smithd's generated files (plan, replace by a new deploy, approve by commit, apply, encrypted state), but with a Kubernetes backend and a static encryption key, and the watcher, approval and reset code was tested against a fake Kubernetes API, not with the controller running.
- **OpenBao Kubernetes auth and the AWS secrets engine** with the real plugin (`openbao-plugin-secrets-aws`): both were stood in for by the JWT auth method and a KV mount in the tests. The `vault` provider example in this guide, and `skip_child_token`, were derived from the policy smithd writes, not run.
- The **S3 + DynamoDB backend** with the `tf-app` IAM roles (state locking, the leading-keys condition), and `remediation.retries: 1`.
- **Plan-only runs on the `plans` branch** in a cluster (the `plan-plans-<sha>` id, cleanup, the `gitops-plans` Flux objects). The Flux Receiver for that branch is not set up; the example polls every minute.
- The web UI approval flow in a browser with a live smithd, and GitHub check runs against real GitHub (tests use a fake).
- Moving a real existing app ([migration checklist](terraform-migration.md)).
