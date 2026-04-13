variable "issuer_url" {
  description = "Public issuer URL served by the bridge. Must be an https URL with no path beyond an optional trailing slash."
  type        = string

  validation {
    condition     = can(regex("^https://[^/]+/?$", var.issuer_url))
    error_message = "issuer_url must be an https URL with no path other than an optional trailing slash."
  }
}

variable "client_id_list" {
  description = "Allowed client IDs for the OIDC provider."
  type        = list(string)
  default     = ["sts.amazonaws.com"]

  validation {
    condition     = length(var.client_id_list) > 0
    error_message = "client_id_list must contain at least one audience."
  }
}

variable "thumbprint_list" {
  description = "Optional thumbprints for the OIDC provider. Leave unset to let AWS manage trusted public CA thumbprints."
  type        = list(string)
  default     = null
}

variable "tags" {
  description = "Optional tags for the OIDC provider."
  type        = map(string)
  default     = {}
}

