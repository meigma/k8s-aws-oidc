output "arn" {
  description = "ARN of the IAM OIDC provider."
  value       = aws_iam_openid_connect_provider.this.arn
}

output "issuer_url" {
  description = "Normalized issuer URL used to create the provider."
  value       = local.normalized_issuer_url
}

output "issuer_host" {
  description = "Issuer host without the https scheme."
  value       = local.issuer_host
}

output "thumbprint_list" {
  description = "Thumbprints recorded on the IAM OIDC provider."
  value       = aws_iam_openid_connect_provider.this.thumbprint_list
}

