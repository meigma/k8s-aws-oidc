output "oidc_provider_arn" {
  description = "ARN of the created IAM OIDC provider."
  value       = module.provider.arn
}

output "issuer_host" {
  description = "Normalized issuer host."
  value       = module.provider.issuer_host
}

output "role_arn" {
  description = "ARN of the created IAM role."
  value       = module.role.role_arn
}

output "service_account_subject" {
  description = "Trusted Kubernetes service-account subject."
  value       = local.service_account_subject
}

