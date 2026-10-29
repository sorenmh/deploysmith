variable "name" {
  description = "Name of the Terraform app in smithd, e.g. viino"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", var.name))
    error_message = "name must contain only lowercase letters, digits and '-', like the app's namespace tf-<name>."
  }
}

variable "environments" {
  description = "Environments the app has Terraform enabled in (smithctl app terraform enable --env)"
  type        = set(string)
}

variable "openbao_principal_arn" {
  description = "ARN of OpenBao's own AWS identity (the IAM user or role of its AWS secrets engine), the only principal that may assume the roles"
  type        = string
}

variable "state" {
  description = <<-EOT
    The app's state backend, as recorded in smithd. bucket and lock_table are
    names. key_prefixes maps an environment to the prefix of its state keys;
    the default is <name>/<env>/ (the shared backend). An empty prefix gives
    access to the whole bucket and lock table, for an existing app that keeps
    its own backend. region is the lock table's region (default: the
    provider's). kms_key_arn is needed if the bucket uses SSE-KMS.
  EOT
  type = object({
    bucket       = string
    lock_table   = optional(string, "")
    region       = optional(string, "")
    key_prefixes = optional(map(string), {})
    kms_key_arn  = optional(string, "")
  })
}

variable "policy_arns" {
  description = "Managed policies attached to the apply roles tf-<name>-<env>: what the app's Terraform manages"
  type        = list(string)
  default     = []
}

variable "readonly_policy_arns" {
  description = "Managed policies attached to the plan-only roles tf-<name>-<env>-readonly"
  type        = list(string)
  default     = ["arn:aws:iam::aws:policy/ReadOnlyAccess"]
}

variable "permissions_boundary" {
  description = "Permissions boundary of all roles, recommended so an app cannot grant itself more than intended"
  type        = string
  default     = null
}

variable "max_session_duration" {
  description = "Longest session OpenBao may request, in seconds; at least smithd's TERRAFORM_STS_TTL"
  type        = number
  default     = 3600
}

variable "tags" {
  description = "Tags of the roles"
  type        = map(string)
  default     = {}
}
