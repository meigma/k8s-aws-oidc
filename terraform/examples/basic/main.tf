data "aws_caller_identity" "current" {}

locals {
  service_account_subject = "system:serviceaccount:${var.kubernetes_namespace}:${var.kubernetes_service_account}"
  role_arn                = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.role_name}"
  inline_policy_json = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["iam:GetRole"]
        Resource = local.role_arn
      }
    ]
  })
}

module "provider" {
  source = "../../modules/aws_oidc_provider"

  issuer_url     = var.issuer_url
  client_id_list = var.client_id_list
  tags           = var.tags
}

module "role" {
  source = "../../modules/aws_oidc_role"

  role_name                = var.role_name
  oidc_provider_arn        = module.provider.arn
  issuer_host              = module.provider.issuer_host
  service_account_subjects = [local.service_account_subject]
  audiences                = var.audiences
  managed_policy_arns      = var.managed_policy_arns
  inline_policy_json       = local.inline_policy_json
  tags                     = var.tags
}
