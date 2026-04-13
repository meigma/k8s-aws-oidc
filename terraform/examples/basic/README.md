# basic example

This example creates:

- one AWS IAM OIDC provider for the bridge issuer URL
- one IAM role trusted for a single Kubernetes service-account subject

It also attaches an inline policy that allows `iam:GetRole` on the created
role, matching the live smoke-test proof path used in this repo.

