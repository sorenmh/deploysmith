
# Outputs
output "bucket_name" {
  description = "S3 bucket name for version storage"
  value       = aws_s3_bucket.versions.id
}

output "bucket_arn" {
  description = "S3 bucket ARN"
  value       = aws_s3_bucket.versions.arn
}

output "smithd_access_key_id" {
  description = "Access key ID for smithd service"
  value       = aws_iam_access_key.smithd_service.id
  sensitive   = true
}

output "smithd_secret_access_key" {
  description = "Secret access key for smithd service"
  value       = aws_iam_access_key.smithd_service.secret
  sensitive   = true
}

output "secrets_manager_arn" {
  description = "ARN of the secrets manager secret containing smithd credentials"
  value       = aws_secretsmanager_secret.smithd_credentials.arn
}
