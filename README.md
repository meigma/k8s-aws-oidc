# k8s-aws-oidc

`k8s-aws-oidc` republishes the Kubernetes service-account discovery document
and JWKS over Tailscale Funnel so AWS IAM can trust a private cluster issuer
without exposing the Kubernetes API server publicly.

The repository contains:

- the Go bridge service
- a Helm chart for deployment
- Terraform modules for the AWS IAM side
- documentation under [`docs/`](docs)

Start with:

- [Docs overview](docs/index.md)
- [Tutorial: first deploy](docs/tutorials/first-deploy.md)
- [Terraform AWS modules](terraform/README.md)

