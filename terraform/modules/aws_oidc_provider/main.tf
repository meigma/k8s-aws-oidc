locals {
  normalized_issuer_url = trimsuffix(var.issuer_url, "/")
  issuer_host           = trimprefix(local.normalized_issuer_url, "https://")
}

resource "aws_iam_openid_connect_provider" "this" {
  url             = local.normalized_issuer_url
  client_id_list  = var.client_id_list
  thumbprint_list = var.thumbprint_list
  tags            = var.tags
}

