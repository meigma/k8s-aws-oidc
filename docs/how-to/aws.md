---
title: AWS
sidebar_position: 2
---

Use this guide when you want to add the AWS IAM side to an existing Terraform
stack.

## Module split

The repo ships two modules:

- `aws_oidc_provider` for the account-level IAM OIDC provider
- `aws_oidc_role` for workload roles trusted by that provider

This split matters because the provider URL is unique per AWS account. Most
consumers should create the provider once and reuse it for many roles.

## One-time provider setup

```hcl
module "bridge_provider" {
  source = "github.com/meigma/k8s-aws-oidc//terraform/modules/aws_oidc_provider"

  issuer_url = "https://oidc.example.tailnet.ts.net"
}
```

## Workload role setup

```hcl
module "demo_role" {
  source = "github.com/meigma/k8s-aws-oidc//terraform/modules/aws_oidc_role"

  role_name         = "demo-app-role"
  oidc_provider_arn = module.bridge_provider.arn
  issuer_host       = module.bridge_provider.issuer_host

  service_account_subjects = [
    "system:serviceaccount:demo:demo-app",
  ]
}
```

## Subject and audience rules

The role trust expects exact matches on:

- `${issuer_host}:aud`
- `${issuer_host}:sub`

The default audience is `sts.amazonaws.com`.

Subjects must be fully qualified:

```text
system:serviceaccount:<namespace>:<service-account>
```

## Attaching permissions

The role module supports:

- `managed_policy_arns`
- `inline_policy_json`

If your organization already has a policy attachment pattern, you can leave the
role module focused on the trust policy and attach permissions elsewhere.

