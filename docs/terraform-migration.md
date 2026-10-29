# Moving an Existing Terraform App to DeploySmith

Checklist for the existing Terraform apps (3 today) that are applied from their own CI. After the move, forge uploads a bundle, smithd commits it with a `Terraform` CR, tofu-controller plans, and a person approves the apply (DEP-13). The app **keeps its S3 bucket, state key and DynamoDB table**. Its state is converted from Terraform to OpenTofu and encrypted on the first apply. Cluster prerequisites: [terraform-cluster-setup.md](terraform-cluster-setup.md).

Do one app and one environment at a time, starting with the least important environment. Nothing else may apply the app's Terraform while it is moved: stop its CI apply job before step 5.

## 1. Check the Terraform version and the state

- [ ] Note the app's Terraform version (`required_version`, CI image, `terraform version`) and the `terraform_version` in the state (`aws s3 cp s3://<bucket>/<key> - | jq -r .terraform_version`).
- [ ] Pick the OpenTofu version: the newest one we build a runner for (`tf-runner` matrix in `.github/workflows/ci.yml`). OpenTofu reads state written by Terraform up to 1.5.x as is. For state and code from later Terraform versions, check the [OpenTofu migration guide](https://opentofu.org/docs/intro/migration/) for the app's version, and look for features the app uses that OpenTofu doesn't have or implements differently.
- [ ] Back up the current state object: `aws s3 cp s3://<bucket>/<key> ./<app>-<env>-backup.tfstate`. The bucket should be versioned too.
- [ ] Check the providers: OpenTofu uses `registry.opentofu.org`, which mirrors the HashiCorp providers. Providers only on `registry.terraform.io` need a full source address.

## 2. Convert the code

In the app's repository:

- [ ] Remove the `backend` block (smithd sets the backend; forge rejects bundles with a `backend`/`cloud` block or `*override.tf` files). Remove any `encryption` block.
- [ ] Set `required_version` to the chosen OpenTofu version, e.g. `"~> 1.13.0"`.
- [ ] `tofu init -backend=false -upgrade`, commit the new `.terraform.lock.hcl` (it now has `registry.opentofu.org` hashes; add `tofu providers lock -platform=linux_amd64 -platform=linux_arm64` if CI runs on another platform than the cluster).
- [ ] Move secrets out of CI and tfvars into OpenBao `secret/apps/<app>/...` and read them with the `vault` provider (DEP-17). Non-secret values go in `tfvars/<env>.tfvars`.
- [ ] Remove the cloud credentials and the `terraform apply` job from CI; add `forge upload` of the bundle (DEP-15).

## 3. Register the app in smithd with its existing backend

- [ ] `smithctl app terraform enable <app> --env <env> --backend-bucket <bucket> --backend-key <key> --backend-region <region> --backend-lock-table <table> --encryption-fallback`
- [ ] In the platform app, add the `tf-app` module block with `state = { bucket, lock_table, region, key_prefixes }` matching that backend (`terraform/modules/tf-app/README.md`) and the policies the app's Terraform needs, and apply it.
- [ ] `smithctl app terraform list <app>` shows the environment provisioned without errors.

## 4. Turn on encryption with a fallback

The first apply has to read plaintext state and write it back encrypted. `--encryption-fallback` in step 3 turns this on for the environment (later: `smithctl app terraform update <app> --env <env> --encryption-fallback`); `smithctl app terraform list` and the CR show it. smithd then writes this `TF_ENCRYPTION` into the environment's CR:

```hcl
key_provider "openbao" "app" {
  key_name            = "tf-<app>"
  transit_engine_path = "/transit"
}
method "aes_gcm" "app" {
  keys = key_provider.openbao.app
}
method "unencrypted" "migrate" {}
state {
  method   = method.aes_gcm.app
  enforced = false
  fallback {
    method = method.unencrypted.migrate
  }
}
plan {
  method   = method.aes_gcm.app
  enforced = false
  fallback {
    method = method.unencrypted.migrate
  }
}
```

`method` is what is written; `fallback` is also tried when reading. `enforced = false` is required while an `unencrypted` method is configured.

## 5. First deploy

A plan with no changes is never applied, and only an apply writes the state, so the first deploy needs a change. Add a marker resource to the app's root in the same commit as step 2, and remove it after step 6:

```hcl
# Forces an apply so the state is written back encrypted; remove after the move
resource "terraform_data" "deploysmith_migration" {}
```

- [ ] Deploy that version. The plan must show **only** `terraform_data.deploysmith_migration` to be created, plus whatever changed through the Terraform → OpenTofu move (e.g. provider upgrades). Read it carefully before approving.
- [ ] Approve. The apply writes the state back, now encrypted.
- [ ] Check the state object: `aws s3 cp s3://<bucket>/<key> - | jq 'keys'` shows `encrypted_data` and `meta`, and no `resources`.

## 6. Remove the fallback

- [ ] Switch `TF_ENCRYPTION` back to the normal config (`enforced = true`, no `unencrypted` method; [terraform-cluster-setup.md](terraform-cluster-setup.md#state-encryption)): `smithctl app terraform update <app> --env <env> --encryption-fallback=false`. It applies from the next deploy on, i.e. the one below.
- [ ] Remove the marker resource and deploy. The plan must show only its removal. With `enforced = true`, plaintext state is refused, so a missed environment fails instead of being read unencrypted.
- [ ] Delete the old plaintext versions of the state object from the bucket, or let them expire, and delete the local backup once you are confident.

## 7. Optional: move to the shared backend

Not required; an app can keep its own backend for good. To move it, from a machine with the app's apply credentials (e.g. a one-off pod with the runner image and the app's ServiceAccount, so `tf-runner-auth` provides the token and AWS credentials):

- [ ] Write a `backend_override.tf` with the new backend (`deploysmith-tfstate`, key `<app>/<env>/<root>/terraform.tfstate`, `deploysmith-tflock`) next to the old backend config, set `TF_ENCRYPTION` to the normal config, and run `tofu init -migrate-state`. The state stays encrypted with the app's key.
- [ ] Update the backend in smithd and the `tf-app` module's `state` to the shared backend, apply the platform app, and deploy. The plan must show no changes.
- [ ] Remove the old state object, and later the old bucket and table.

## Per-App Record

| App | Env | Terraform version | OpenTofu version | Backend (bucket / key / table) | Encrypted | Fallback removed | Shared backend |
|---|---|---|---|---|---|---|---|
| | | | | | | | |
