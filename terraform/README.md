# Terraform AWS Modules

This directory contains reusable Terraform modules for the AWS side of the
OIDC bridge setup.

## What is here

- `modules/aws_oidc_provider`: creates the AWS IAM OIDC provider for the bridge
- `modules/aws_oidc_role`: creates an IAM role trusted for one or more
  Kubernetes service-account subjects
- `examples/basic`: a minimal example that wires both modules together

## Prerequisites

Before applying these modules, consumers must already have:

1. The bridge deployed and healthy.
2. A public issuer URL served by the bridge.
3. The Kubernetes API server configured with a matching
   `--service-account-issuer`.
4. The Kubernetes projected service-account token audience including
   `sts.amazonaws.com`.
5. Tailscale Funnel enabled and propagated for the bridge hostname.

These modules intentionally do not manage the Tailscale side.

## Module split

The AWS IAM OIDC provider is account-level and unique per issuer URL, so it is
managed separately from workload roles.

- Use `aws_oidc_provider` once per AWS account and issuer URL.
- Use `aws_oidc_role` for each workload role you want to trust that provider.

## Example

The example shows the working pattern from the live smoke test:

- issuer URL shaped like `https://<hostname>.<tailnet>.ts.net`
- audience `sts.amazonaws.com`
- subject `system:serviceaccount:<namespace>:<service-account>`

Run the example manually with your normal AWS authentication flow:

```bash
cd terraform/examples/basic
tofu init
tofu plan \
  -var='issuer_url=https://oidc.example.tailnet.ts.net' \
  -var='kubernetes_namespace=demo' \
  -var='kubernetes_service_account=demo-app'
```

## Moon tasks

This subtree is configured as its own Moon project.

```bash
moon run terraform:fmt
moon run terraform:lint
moon run terraform:validate
```

These tasks are static quality checks only. For the repo-owned local live smoke
path, use `just up` and `just down` from the repo root.
