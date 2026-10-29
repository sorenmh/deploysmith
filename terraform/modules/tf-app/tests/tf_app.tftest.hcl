# tofu test: plans the module without AWS access (fake credentials, the
# account data sources overridden) and checks the roles and their policies

provider "aws" {
  region                      = "eu-north-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

override_data {
  target = data.aws_caller_identity.current
  values = { account_id = "123456789012" }
}

override_data {
  target = data.aws_region.current
  values = { region = "eu-north-1" }
}

override_data {
  target = data.aws_partition.current
  values = { partition = "aws" }
}

variables {
  name                  = "viino"
  environments          = ["dev", "prod"]
  openbao_principal_arn = "arn:aws:iam::123456789012:user/openbao"
  policy_arns           = ["arn:aws:iam::aws:policy/PowerUserAccess"]
}

run "shared_backend" {
  command = plan

  variables {
    state = {
      bucket     = "deploysmith-tfstate"
      lock_table = "deploysmith-tflock"
    }
  }

  assert {
    condition     = toset([for role in aws_iam_role.this : role.name]) == toset(["tf-viino-dev", "tf-viino-dev-readonly", "tf-viino-prod", "tf-viino-prod-readonly"])
    error_message = "unexpected role names"
  }

  assert {
    condition     = jsondecode(aws_iam_role.this["prod"].assume_role_policy).Statement[0].Principal.AWS == "arn:aws:iam::123456789012:user/openbao"
    error_message = "roles must trust OpenBao only"
  }

  assert {
    condition     = strcontains(data.aws_iam_policy_document.state["prod"].json, "arn:aws:s3:::deploysmith-tfstate/viino/prod/*")
    error_message = "apply role must reach its own state prefix"
  }

  assert {
    condition     = strcontains(data.aws_iam_policy_document.state["prod"].json, "deploysmith-tfstate/viino/prod/*") && strcontains(data.aws_iam_policy_document.state["prod"].json, "dynamodb:LeadingKeys")
    error_message = "lock items must be limited by LeadingKeys"
  }

  assert {
    condition     = !strcontains(data.aws_iam_policy_document.state["prod"].json, "viino/dev/")
    error_message = "prod role must not reach dev state"
  }

  assert {
    condition     = !strcontains(data.aws_iam_policy_document.state["prod-readonly"].json, "s3:PutObject") && !strcontains(data.aws_iam_policy_document.state["prod-readonly"].json, "dynamodb:PutItem")
    error_message = "plan-only role must not write state or locks"
  }

  assert {
    condition     = toset([for a in aws_iam_role_policy_attachment.this : "${a.role} ${a.policy_arn}"]) == toset(["tf-viino-dev arn:aws:iam::aws:policy/PowerUserAccess", "tf-viino-prod arn:aws:iam::aws:policy/PowerUserAccess", "tf-viino-dev-readonly arn:aws:iam::aws:policy/ReadOnlyAccess", "tf-viino-prod-readonly arn:aws:iam::aws:policy/ReadOnlyAccess"])
    error_message = "unexpected policy attachments"
  }
}

run "existing_backend" {
  command = plan

  variables {
    environments = ["prod"]
    state = {
      bucket       = "viino-tfstate"
      lock_table   = "viino-tflock"
      region       = "eu-central-1"
      key_prefixes = { prod = "" }
      kms_key_arn  = "arn:aws:kms:eu-central-1:123456789012:key/abc"
    }
  }

  assert {
    condition     = strcontains(data.aws_iam_policy_document.state["prod"].json, "arn:aws:s3:::viino-tfstate/*") && !strcontains(data.aws_iam_policy_document.state["prod"].json, "s3:prefix")
    error_message = "an empty prefix must give the whole bucket"
  }

  assert {
    condition     = strcontains(data.aws_iam_policy_document.state["prod"].json, "arn:aws:dynamodb:eu-central-1:123456789012:table/viino-tflock") && !strcontains(data.aws_iam_policy_document.state["prod"].json, "LeadingKeys")
    error_message = "an empty prefix must give the whole lock table, in the backend's region"
  }

  assert {
    condition     = strcontains(data.aws_iam_policy_document.state["prod"].json, "kms:GenerateDataKey") && !strcontains(data.aws_iam_policy_document.state["prod-readonly"].json, "kms:GenerateDataKey")
    error_message = "only the apply role may encrypt with the state's KMS key"
  }
}

run "rejects_invalid_name" {
  command = plan

  variables {
    name  = "Viino_API"
    state = { bucket = "deploysmith-tfstate" }
  }

  expect_failures = [var.name]
}
