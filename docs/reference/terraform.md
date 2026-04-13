---
title: Terraform
sidebar_position: 3
---

This repo ships two AWS modules under `terraform/modules/`.

## `aws_oidc_provider`

Creates the IAM OIDC provider for the bridge issuer URL.

### Inputs

- `issuer_url`
- `client_id_list` default `["sts.amazonaws.com"]`
- `thumbprint_list` optional
- `tags`

### Outputs

- `arn`
- `issuer_url`
- `issuer_host`
- `thumbprint_list`

## `aws_oidc_role`

Creates a role trusted by the OIDC provider for one or more Kubernetes
service-account subjects.

### Inputs

- `role_name`
- `oidc_provider_arn`
- `issuer_host`
- `service_account_subjects`
- `audiences` default `["sts.amazonaws.com"]`
- `managed_policy_arns`
- `inline_policy_json`
- `max_session_duration`
- `tags`

### Outputs

- `role_arn`
- `role_name`
- `service_account_subjects`

## Why the split exists

The OIDC provider is account-level and unique per issuer URL. Roles are
workload-level. Most consumers should create the provider once and reuse it.

