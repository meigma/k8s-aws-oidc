# aws_oidc_role

Creates an IAM role trusted by a specific AWS IAM OIDC provider for one or
more Kubernetes service-account subjects.

The trust policy is generated with `aws_iam_policy_document` and binds each
trusted subject explicitly.

## Usage

```hcl
module "role" {
  source = "github.com/meigma/k8s-aws-oidc//terraform/modules/aws_oidc_role"

  role_name         = "example-app-role"
  oidc_provider_arn = module.provider.arn
  issuer_host       = module.provider.issuer_host

  service_account_subjects = [
    "system:serviceaccount:demo:example-app",
  ]

  inline_policy_json = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["iam:GetRole"]
        Resource = "arn:aws:iam::123456789012:role/example-app-role"
      }
    ]
  })
}
```

## Requirements

| Name | Version |
|------|---------|
| terraform | >= 1.6.0 |
| aws | >= 5.0 |

## Providers

| Name | Version |
|------|---------|
| aws | >= 5.0 |

## Resources

| Name | Type |
|------|------|
| [aws_iam_role.this](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role_policy.inline](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy) | resource |
| [aws_iam_role_policy_attachment.managed](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_policy_document.assume_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/iam_policy_document) | data source |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| audiences | Allowed audiences for the trusted web identity token. | `set(string)` | `["sts.amazonaws.com"]` | no |
| inline_policy_json | Optional inline policy JSON to attach to the role. | `string` | `null` | no |
| issuer_host | Issuer host without scheme, for example oidc.example.ts.net. | `string` | n/a | yes |
| managed_policy_arns | Managed policy ARNs to attach to the role. | `list(string)` | `[]` | no |
| max_session_duration | Maximum role session duration in seconds. | `number` | `3600` | no |
| oidc_provider_arn | ARN of the IAM OIDC provider trusted by this role. | `string` | n/a | yes |
| role_name | Name of the IAM role to create. | `string` | n/a | yes |
| service_account_subjects | Fully-qualified Kubernetes service-account subjects trusted by this role. | `set(string)` | n/a | yes |
| tags | Optional tags for the IAM role. | `map(string)` | `{}` | no |

## Outputs

| Name | Description |
|------|-------------|
| role_arn | ARN of the IAM role. |
| role_name | Name of the IAM role. |
| service_account_subjects | Trusted Kubernetes service-account subjects. |
