locals {
  sorted_subjects  = sort(tolist(var.service_account_subjects))
  sorted_audiences = sort(tolist(var.audiences))
}

data "aws_iam_policy_document" "assume_role" {
  dynamic "statement" {
    for_each = local.sorted_subjects

    content {
      effect  = "Allow"
      actions = ["sts:AssumeRoleWithWebIdentity"]

      principals {
        type        = "Federated"
        identifiers = [var.oidc_provider_arn]
      }

      condition {
        test     = "StringEquals"
        variable = "${var.issuer_host}:sub"
        values   = [statement.value]
      }

      condition {
        test     = "StringEquals"
        variable = "${var.issuer_host}:aud"
        values   = local.sorted_audiences
      }
    }
  }
}

resource "aws_iam_role" "this" {
  name                 = var.role_name
  assume_role_policy   = data.aws_iam_policy_document.assume_role.json
  max_session_duration = var.max_session_duration
  tags                 = var.tags
}

resource "aws_iam_role_policy_attachment" "managed" {
  for_each = toset(var.managed_policy_arns)

  role       = aws_iam_role.this.name
  policy_arn = each.value
}

resource "aws_iam_role_policy" "inline" {
  count = var.inline_policy_json == null || trimspace(var.inline_policy_json) == "" ? 0 : 1

  name   = "${var.role_name}-inline"
  role   = aws_iam_role.this.id
  policy = var.inline_policy_json
}

