variable "issuer_url" {
  description = "Public issuer URL served by the bridge."
  type        = string
}

variable "role_name" {
  description = "IAM role name to create."
  type        = string
  default     = "k8s-aws-oidc-example"
}

variable "kubernetes_namespace" {
  description = "Kubernetes namespace of the trusted service account."
  type        = string
}

variable "kubernetes_service_account" {
  description = "Kubernetes service-account name trusted by the IAM role."
  type        = string
}

variable "client_id_list" {
  description = "Allowed client IDs for the OIDC provider."
  type        = list(string)
  default     = ["sts.amazonaws.com"]
}

variable "audiences" {
  description = "Allowed token audiences for the IAM role trust policy."
  type        = set(string)
  default     = ["sts.amazonaws.com"]
}

variable "managed_policy_arns" {
  description = "Managed policies to attach to the role."
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Tags to apply to created resources."
  type        = map(string)
  default     = {}
}

