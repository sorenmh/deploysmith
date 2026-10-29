# tf-app

IAM roles for one Terraform app that DeploySmith runs. Use it in the
platform app (the one Terraform app whose role is set up by hand), one block
per app:

```hcl
module "tf_app_viino" {
  source = "github.com/sorenmh/deploysmith//terraform/modules/tf-app?ref=<commit>"

  name                  = "viino"
  environments          = ["dev", "prod"]
  openbao_principal_arn = aws_iam_user.openbao.arn # OpenBao's own AWS identity
  state = {
    bucket     = "deploysmith-tfstate"            # the shared backend
    lock_table = "deploysmith-tflock"
  }
  policy_arns          = [aws_iam_policy.viino.arn] # what viino's Terraform manages
  permissions_boundary = aws_iam_policy.tf_app_boundary.arn
}
```

Adding an app is one module block and one apply, plus
`smithctl app terraform enable <app> --env <env>` for each environment.

## What it creates

For each environment:

| Role | Used by | Permissions |
|---|---|---|
| `tf-<name>-<env>` | apply runs | `policy_arns`, and read/write of the app's state and lock items |
| `tf-<name>-<env>-readonly` | plan-only runs (DEP-22) | `readonly_policy_arns` (default `ReadOnlyAccess`), read of the state and of the lock table's digest items, no locking |

Both trust only `openbao_principal_arn`. smithd creates the matching OpenBao
AWS secrets engine roles `<name>-<env>` and `<name>-<env>-readonly`
(`credential_type = assumed_role`), so a runner asks OpenBao for
`aws/creds/<name>-<env>` and gets credentials for about an hour.

### State access

With the shared backend the state keys are `<name>/<env>/<root>/terraform.tfstate`
(smithd's default). Each role may only use objects under `<name>/<env>/` and
DynamoDB items whose `LockID` starts with `<bucket>/<name>/<env>/`
(`dynamodb:LeadingKeys`), so an app can't read or lock another app's or
environment's state.

An existing app that keeps its own backend sets the prefix of each
environment, or `""` for the whole bucket and table:

```hcl
  state = {
    bucket       = "viino-tfstate"
    lock_table   = "viino-tflock"
    region       = "eu-central-1"            # the table's region, if not the provider's
    key_prefixes = { prod = "prod/", dev = "dev/" }
    kms_key_arn  = aws_kms_key.viino_state.arn # if the bucket uses SSE-KMS
  }
```

The prefix must match the key recorded in smithd
(`smithctl app terraform enable --backend-key`).

## OpenBao's AWS identity

Set up once, by hand or in the platform app: an IAM user (or role) for
OpenBao's AWS secrets engine that may only assume the `tf-*` roles:

```hcl
data "aws_iam_policy_document" "openbao" {
  statement {
    actions   = ["sts:AssumeRole"]
    resources = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/tf-*"]
  }
}
```

and configure the engine with it (`bao write aws/config/root ...`). See the
smithd chart README, "Terraform apps", for the OpenBao side.

## Tests

`tofu test` plans the module with fake credentials and checks the role
names, trust and state permissions:

```sh
docker run --rm -v "$PWD:/work" -w /work ghcr.io/opentofu/opentofu:1.12.9 init -backend=false
docker run --rm -v "$PWD:/work" -w /work ghcr.io/opentofu/opentofu:1.12.9 test
```
