# IAM roles of one Terraform app run by DeploySmith: per environment the
# apply role tf-<name>-<env> and the plan-only role tf-<name>-<env>-readonly.
# OpenBao's AWS secrets engine assumes them (assumed_role) for the app's
# runner; smithd creates the matching OpenBao roles <name>-<env>[-readonly].

locals {
  roles = merge([
    for env in var.environments : {
      for readonly in [false, true] :
      "${env}${readonly ? "-readonly" : ""}" => {
        env      = env
        readonly = readonly
        name     = "tf-${var.name}-${env}${readonly ? "-readonly" : ""}"
        prefix   = lookup(var.state.key_prefixes, env, "${var.name}/${env}/")
      }
    }
  ]...)

  bucket_arn = "arn:${data.aws_partition.current.partition}:s3:::${var.state.bucket}"
  lock_table_arn = format("arn:%s:dynamodb:%s:%s:table/%s",
    data.aws_partition.current.partition,
    coalesce(var.state.region, data.aws_region.current.region),
    data.aws_caller_identity.current.account_id,
    var.state.lock_table,
  )
}

data "aws_partition" "current" {}
data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "AWS"
      identifiers = [var.openbao_principal_arn]
    }
  }
}

resource "aws_iam_role" "this" {
  for_each = local.roles

  name                 = each.value.name
  description          = "DeploySmith Terraform runner of ${var.name} in ${each.value.env}${each.value.readonly ? " (plan-only)" : ""}"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = var.max_session_duration
  permissions_boundary = var.permissions_boundary

  tags = merge(var.tags, {
    "deploysmith.io/terraform-app" = var.name
    "deploysmith.io/environment"   = each.value.env
  })
}

# The state of this app and environment only: objects under its prefix and
# lock items whose key starts with <bucket>/<prefix>. The lock table holds
# both the lock (<bucket>/<key>) and the state digest (<bucket>/<key>-md5).
data "aws_iam_policy_document" "state" {
  for_each = local.roles

  statement {
    sid       = "ListState"
    actions   = ["s3:ListBucket"]
    resources = [local.bucket_arn]

    dynamic "condition" {
      for_each = each.value.prefix == "" ? [] : [each.value.prefix]
      content {
        test     = "StringLike"
        variable = "s3:prefix"
        values   = ["${condition.value}*"]
      }
    }
  }

  statement {
    sid = "State"
    actions = each.value.readonly ? ["s3:GetObject"] : [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject", # the S3 lock file (use_lockfile) and workspaces
    ]
    resources = ["${local.bucket_arn}/${each.value.prefix}*"]
  }

  dynamic "statement" {
    for_each = var.state.lock_table == "" ? [] : [var.state.lock_table]
    content {
      sid = "Lock"
      # Plan-only runs do not lock (plan.lock: false) but read the digest
      actions = each.value.readonly ? ["dynamodb:DescribeTable", "dynamodb:GetItem"] : [
        "dynamodb:DescribeTable",
        "dynamodb:GetItem",
        "dynamodb:PutItem",
        "dynamodb:DeleteItem",
      ]
      resources = [local.lock_table_arn]

      dynamic "condition" {
        for_each = each.value.prefix == "" ? [] : [each.value.prefix]
        content {
          test     = "ForAllValues:StringLike"
          variable = "dynamodb:LeadingKeys"
          values   = ["${var.state.bucket}/${condition.value}*"]
        }
      }
    }
  }

  dynamic "statement" {
    for_each = var.state.kms_key_arn == "" ? [] : [var.state.kms_key_arn]
    content {
      sid       = "StateKey"
      actions   = each.value.readonly ? ["kms:Decrypt"] : ["kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey"]
      resources = [statement.value]
    }
  }
}

resource "aws_iam_role_policy" "state" {
  for_each = local.roles

  name   = "terraform-state"
  role   = aws_iam_role.this[each.key].id
  policy = data.aws_iam_policy_document.state[each.key].json
}

resource "aws_iam_role_policy_attachment" "this" {
  for_each = merge([
    for key, role in local.roles : {
      for arn in(role.readonly ? var.readonly_policy_arns : var.policy_arns) :
      "${key} ${arn}" => { role = key, arn = arn }
    }
  ]...)

  role       = aws_iam_role.this[each.value.role].name
  policy_arn = each.value.arn
}
