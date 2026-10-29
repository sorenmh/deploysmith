# Shared state backend of the Terraform apps DeploySmith runs (new apps; existing
# apps keep their own). State keys are <app>/<env>/<root>/terraform.tfstate; each
# app's IAM role may only use its own keys and lock items (terraform/modules/tf-app).
# The state itself is also encrypted by OpenTofu with the app's OpenBao Transit key.
# See docs/terraform-cluster-setup.md.

variable "tf_state_bucket_name" {
  description = "S3 bucket for the state of Terraform apps"
  type        = string
  default     = "deploysmith-tfstate"
}

variable "tf_lock_table_name" {
  description = "DynamoDB table for the state locks of Terraform apps"
  type        = string
  default     = "deploysmith-tflock"
}

resource "aws_s3_bucket" "tf_state" {
  bucket = var.tf_state_bucket_name

  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name      = "DeploySmith Terraform state"
    ManagedBy = "Terraform"
  }
}

# Every state write is a new version, so a bad apply can be rolled back by hand
resource "aws_s3_bucket_versioning" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# Old state versions are kept for 90 days
resource "aws_s3_bucket_lifecycle_configuration" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id

  rule {
    id     = "expire-old-state-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }
}

data "aws_iam_policy_document" "tf_state" {
  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.tf_state.arn,
      "${aws_s3_bucket.tf_state.arn}/*",
    ]

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id
  policy = data.aws_iam_policy_document.tf_state.json

  depends_on = [aws_s3_bucket_public_access_block.tf_state]
}

# LockID items are "<bucket>/<key>" (the lock) and "<bucket>/<key>-md5" (the
# state digest), so dynamodb:LeadingKeys can limit an app to its own prefix
resource "aws_dynamodb_table" "tf_lock" {
  name         = var.tf_lock_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name      = "DeploySmith Terraform locks"
    ManagedBy = "Terraform"
  }
}

output "tf_state_bucket" {
  description = "Shared state bucket of Terraform apps (smithd config.terraform.stateBucket)"
  value       = aws_s3_bucket.tf_state.id
}

output "tf_lock_table" {
  description = "Shared lock table of Terraform apps (smithd config.terraform.lockTable)"
  value       = aws_dynamodb_table.tf_lock.name
}
