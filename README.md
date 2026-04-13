# k8s-aws-oidc

`k8s-aws-oidc` republishes the Kubernetes service-account issuer discovery
document and JWKS so AWS IAM can validate `AssumeRoleWithWebIdentity` for a
private cluster. It exposes only the public OIDC metadata endpoints through
Tailscale Funnel, which lets AWS trust the cluster issuer without exposing the
Kubernetes API server publicly.

The full project documentation is published at
[k8s.oidc.meigma.dev](https://k8s.oidc.meigma.dev/).

## Table of Contents

- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Documentation](#documentation)
- [Repository Layout](#repository-layout)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Features

- Republishes `/.well-known/openid-configuration` and `/openid/v1/jwks` for a
  private Kubernetes issuer
- Uses Tailscale Funnel to publish the issuer URL without exposing the API
  server
- Ships a Helm chart for cluster deployment
- Ships Terraform modules for the AWS IAM OIDC provider and trusted roles
- Includes Diataxis documentation for deployment, operations, and troubleshooting

## Installation

Before installing the bridge, make sure you have:

- a Kubernetes cluster where you can set `--service-account-issuer`
- an API server audience list that includes `sts.amazonaws.com`
- a Tailscale tailnet where the chosen tag can use Funnel
- Tailscale OAuth client credentials for the bridge node
- an AWS account where you can create an IAM OIDC provider and IAM roles
- `kubectl`, `helm`, and `tofu`

The same public issuer URL must be used in all three places:

1. the Kubernetes API server `--service-account-issuer`
2. the bridge `issuerUrl`
3. the AWS IAM OIDC provider

The API server audience list must also include `sts.amazonaws.com`, for example
through `--api-audiences=https://kubernetes.default.svc.cluster.local,sts.amazonaws.com`.

Export a minimal set of values:

```bash
export ISSUER_URL=https://oidc.example.tailnet.ts.net
export TS_HOSTNAME=oidc-example
export TS_TAG=tag:k8s-oidc
export NAMESPACE=oidc-system
```

Create the namespace and the Secret that holds the Tailscale OAuth client
credentials:

```bash
kubectl create namespace "${NAMESPACE}"
kubectl -n "${NAMESPACE}" create secret generic tailscale-oauth \
  --from-literal=TS_API_CLIENT_ID='<client-id>' \
  --from-literal=TS_API_CLIENT_SECRET='<client-secret>'
```

Install the chart from GHCR:

```bash
helm upgrade --install oidc-bridge oci://ghcr.io/meigma/k8s-aws-oidc-chart \
  --namespace "${NAMESPACE}" \
  --set issuerUrl="${ISSUER_URL}" \
  --set tailscale.hostname="${TS_HOSTNAME}" \
  --set tailscale.tag="${TS_TAG}" \
  --set tailscale.oauthSecret.name=tailscale-oauth
```

For local chart development, replace the OCI reference with `./chart`.

## Usage

Verify that the bridge is serving the public metadata endpoints AWS needs:

```bash
kubectl -n "${NAMESPACE}" rollout status deployment/oidc-bridge --timeout=300s
curl "${ISSUER_URL}/.well-known/openid-configuration"
curl "${ISSUER_URL}/openid/v1/jwks"
```

Create the AWS IAM provider and a workload role from the included example:

```bash
cd terraform/examples/basic
tofu init
tofu apply \
  -var="issuer_url=${ISSUER_URL}" \
  -var="role_name=demo-app-role" \
  -var="kubernetes_namespace=demo" \
  -var="kubernetes_service_account=demo-app"
```

For a complete end-to-end walkthrough, including a projected service-account
token test from a pod, use
[First deploy](https://k8s.oidc.meigma.dev/tutorials/first-deploy).

## Configuration

The chart values that matter most in real deployments are:

- `issuerUrl`
- `tailscale.hostname`
- `tailscale.tag`
- `tailscale.oauthSecret.name`
- `tailscale.stateSecret.name`
- `serviceAccount.*`
- `sourceIpAllowlist.*`

Runtime environment variables and validation rules are documented in the
[config reference](https://k8s.oidc.meigma.dev/reference/config).

## Documentation

The deployed documentation site is the primary operator guide:

- [Overview](https://k8s.oidc.meigma.dev/)
- [First deploy tutorial](https://k8s.oidc.meigma.dev/tutorials/first-deploy)
- [Helm how-to](https://k8s.oidc.meigma.dev/how-to/helm)
- [AWS how-to](https://k8s.oidc.meigma.dev/how-to/aws)
- [Troubleshoot auth](https://k8s.oidc.meigma.dev/how-to/troubleshoot-auth)
- [Reference](https://k8s.oidc.meigma.dev/reference)
- [Explanation](https://k8s.oidc.meigma.dev/explanation)

Repository-local docs live under [`docs/`](docs/).

## Repository Layout

- [`chart/`](chart/) - Helm chart for deploying the bridge
- [`terraform/`](terraform/) - Terraform modules and example configuration for AWS
- [`docs/`](docs/) - Docusaurus source for the published documentation site
- [`internal/`](internal/) - Go packages for config, OIDC metadata handling,
  network helpers, and Tailscale runtime integration
- [`DESIGN.md`](DESIGN.md) - architecture notes and design decisions

## Development

The repo uses `moon` for local task orchestration.

Run the Go and chart checks:

```bash
moon run :build
moon run :test
moon run :vet
moon run :lint
moon run :chart-lint
moon run :chart-validate
```

Run the Terraform checks:

```bash
moon run terraform:fmt
moon run terraform:lint
moon run terraform:validate
```

Work on the documentation site locally with Node.js 20 or newer:

```bash
cd docs
npm install
npm run start
```

## Contributing

Issues and pull requests should stay scoped to the existing project structure.
Before opening a change, run the relevant `moon` tasks for the code, chart,
Terraform, or docs areas you touched.

## License

This repository is dual-licensed under either of:

- [Apache License, Version 2.0](LICENSE-APACHE)
- [MIT License](LICENSE-MIT)

You may use this repository under either license, at your option.
