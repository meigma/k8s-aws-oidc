# aws_oidc_provider

Creates the AWS IAM OIDC provider for the bridge issuer URL.

This is an account-level resource and should be created once per issuer URL in
a given AWS account.

## Usage

```hcl
module "provider" {
  source = "github.com/meigma/k8s-aws-oidc//terraform/modules/aws_oidc_provider"

  issuer_url = "https://oidc.example.tailnet.ts.net"
  tags = {
    environment = "dev"
  }
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
| [aws_iam_openid_connect_provider.this](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_openid_connect_provider) | resource |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| client_id_list | Allowed client IDs for the OIDC provider. | `list(string)` | `["sts.amazonaws.com"]` | no |
| issuer_url | Public issuer URL served by the bridge. Must be an https URL with no path beyond an optional trailing slash. | `string` | n/a | yes |
| tags | Optional tags for the OIDC provider. | `map(string)` | `{}` | no |
| thumbprint_list | Optional thumbprints for the OIDC provider. Leave unset to let AWS manage trusted public CA thumbprints. | `list(string)` | `null` | no |

## Outputs

| Name | Description |
|------|-------------|
| arn | ARN of the IAM OIDC provider. |
| issuer_host | Issuer host without the https scheme. |
| issuer_url | Normalized issuer URL used to create the provider. |
| thumbprint_list | Thumbprints recorded on the IAM OIDC provider. |
