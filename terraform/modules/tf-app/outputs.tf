output "role_arns" {
  description = "ARNs of the roles by environment: apply roles under the environment's name, plan-only roles under <env>-readonly"
  value       = { for key, role in aws_iam_role.this : key => role.arn }
}
