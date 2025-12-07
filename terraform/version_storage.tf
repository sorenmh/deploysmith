# Recommended S3 Bucket Configuration for DeploySmith
# This file shows best practices - replace version_storage.tf with this approach

# Data source to get current AWS account ID
data "aws_caller_identity" "current" {}

# Main S3 bucket for version storage
resource "aws_s3_bucket" "versions" {
  bucket = var.bucket_name

  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name        = "DeploySmith Versions"
    Environment = "Production"
    ManagedBy   = "Terraform"
  }
}

# Enable versioning to protect against accidental deletions
resource "aws_s3_bucket_versioning" "versions" {
  bucket = aws_s3_bucket.versions.id

  versioning_configuration {
    status = "Enabled"
  }
}

# Enable server-side encryption
resource "aws_s3_bucket_server_side_encryption_configuration" "versions" {
  bucket = aws_s3_bucket.versions.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }
}

# Block public access
resource "aws_s3_bucket_public_access_block" "versions" {
  bucket = aws_s3_bucket.versions.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Lifecycle rules to manage old drafts and versions
resource "aws_s3_bucket_lifecycle_configuration" "versions" {
  bucket = aws_s3_bucket.versions.id

  # Delete abandoned drafts after 7 days
  rule {
    id     = "delete-old-drafts"
    status = "Enabled"

    filter {
      prefix = "drafts/"
    }

    expiration {
      days = 7
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }

  # Keep published versions but archive old ones to Glacier
  rule {
    id     = "archive-old-published"
    status = "Enabled"

    filter {
      prefix = "published/"
    }

    transition {
      days          = 90
      storage_class = "GLACIER_IR"
    }

    transition {
      days          = 365
      storage_class = "DEEP_ARCHIVE"
    }
  }
}

# IAM user for smithd service
resource "aws_iam_user" "smithd_service" {
  name = "deploysmith-smithd-prod"
  path = "/service-accounts/"

  tags = {
    Name        = "DeploySmith Service Account"
    Environment = "Production"
  }
}

# IAM policy for smithd operations
resource "aws_iam_user_policy" "smithd_s3_access" {
  name = "deploysmith-s3-access"
  user = aws_iam_user.smithd_service.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:DeleteObject"
        ]
        Resource = "${aws_s3_bucket.versions.arn}/drafts/*"
      },
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:ListBucket",
          "s3:CopyObject"
        ]
        Resource = [
          "${aws_s3_bucket.versions.arn}/published/*",
          aws_s3_bucket.versions.arn
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = aws_s3_bucket.versions.arn
        Condition = {
          StringLike = {
            "s3:prefix" = [
              "drafts/*",
              "published/*"
            ]
          }
        }
      }
    ]
  })
}

# Create access key for smithd (store in secrets manager in production!)
resource "aws_iam_access_key" "smithd_service" {
  user = aws_iam_user.smithd_service.name
}

# Store access key in AWS Secrets Manager (recommended)
resource "aws_secretsmanager_secret" "smithd_credentials" {
  name                    = "deploysmith/smithd/aws-credentials"
  description             = "AWS credentials for DeploySmith smithd service"
  recovery_window_in_days = 7

  tags = {
    Name        = "DeploySmith AWS Credentials"
    Environment = "Production"
  }
}

resource "aws_secretsmanager_secret_version" "smithd_credentials" {
  secret_id = aws_secretsmanager_secret.smithd_credentials.id
  secret_string = jsonencode({
    access_key_id     = aws_iam_access_key.smithd_service.id
    secret_access_key = aws_iam_access_key.smithd_service.secret
  })
}

