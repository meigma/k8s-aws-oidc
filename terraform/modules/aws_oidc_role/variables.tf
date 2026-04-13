variable "role_name" {
  description = "Name of the IAM role to create."
  type        = string
}

variable "oidc_provider_arn" {
  description = "ARN of the IAM OIDC provider trusted by this role."
  type        = string
}

variable "issuer_host" {
  description = "Issuer host without scheme, for example oidc.example.ts.net."
  type        = string

  validation {
    condition     = can(regex("^[^/:?#]+$", var.issuer_host))
    error_message = "issuer_host must be a bare host with no scheme or path."
  }
}

variable "service_account_subjects" {
  description = "Fully-qualified Kubernetes service-account subjects trusted by this role."
  type        = set(string)

  validation {
    condition = length(var.service_account_subjects) > 0 && alltrue([
      for subject in var.service_account_subjects :
      can(regex("^system:serviceaccount:[^:]+:[^:]+$", subject))
    ])
    error_message = "service_account_subjects must be non-empty and each value must match system:serviceaccount:<namespace>:<name>."
  }
}

variable "audiences" {
  description = "Allowed audiences for the trusted web identity token."
  type        = set(string)
  default     = ["sts.amazonaws.com"]

  validation {
    condition     = length(var.audiences) > 0
    error_message = "audiences must contain at least one value."
  }
}

variable "managed_policy_arns" {
  description = "Managed policy ARNs to attach to the role."
  type        = list(string)
  default     = []
}

variable "inline_policy_json" {
  description = "Optional inline policy JSON to attach to the role."
  type        = string
  default     = null
}

variable "max_session_duration" {
  description = "Maximum role session duration in seconds."
  type        = number
  default     = 3600

  validation {
    condition     = var.max_session_duration >= 3600 && var.max_session_duration <= 43200
    error_message = "max_session_duration must be between 3600 and 43200 seconds."
  }
}

variable "tags" {
  description = "Optional tags for the IAM role."
  type        = map(string)
  default     = {}
}

