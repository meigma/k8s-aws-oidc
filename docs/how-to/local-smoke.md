---
title: Local smoke
sidebar_position: 3
---

This repo ships a local smoke harness for exercising the full KinD + Helm +
AWS OIDC path against the current checkout.

It is intentionally opinionated:

- it is local-only
- it is not a CI suite
- it manages exactly one smoke environment under `tmp/smoke/`
- it assumes you already have AWS credentials available in the shell

## Prerequisites

You need:

- `just`
- `aws`
- `curl`
- `docker`
- `helm`
- `jq`
- `kind`
- `kubectl`
- `tailscale`
- `tofu`
- a Tailscale tailnet where the chosen tag can use Funnel
- ambient AWS auth that can create and destroy:
  - an IAM OIDC provider
  - an IAM role

The harness does not run `aws-vault` for you. Wrap the command yourself when
you want to use it:

```bash
aws-vault exec --no-session <profile> -- just up
```

## Configuration

The smoke harness reads the repo-root `.env` if it exists. Shell variables take
precedence over `.env`.

Required variables:

```bash
TS_API_CLIENT_ID=...
TS_API_CLIENT_SECRET=...
SMOKE_ISSUER_URL=https://oidc-smoke.<tailnet>.ts.net
SMOKE_TS_TAG=tag:k8s-oidc
```

Optional variables:

```bash
SMOKE_NAME=oidc-smoke
AWS_REGION=us-east-1
```

The smoke harness derives a single fixed environment from `SMOKE_NAME`, so the
same cluster, namespaces, Helm release, and IAM role are reused on every run.

## Bring The Stack Up

Run:

```bash
just up
```

`just up` will:

1. validate local prerequisites and ambient AWS auth
2. create or reuse the KinD cluster
3. build the current checkout into a local bridge image
4. deploy the chart from `./chart`
5. verify the public discovery and JWKS endpoints
6. apply the AWS OIDC provider and role with OpenTofu
7. run a host-side web-identity STS preflight
8. run an in-cluster AWS CLI proof job

Generated files, logs, Terraform state, rendered manifests, metrics scrapes,
and captures are kept under `tmp/smoke/`.

If the smoke config in `.env` changes after an environment is already up, the
harness will stop and tell you to tear it down first.

## Tear The Stack Down

Run:

```bash
just down
```

`just down` will:

1. destroy the AWS resources with OpenTofu when `tmp/smoke/terraform` has state
2. delete the KinD cluster

It leaves `tmp/smoke/` in place for debugging.

## Exercise Failover

Run:

```bash
just failover
```

`just failover` requires an existing smoke environment from `just up`. It will:

1. rebuild and redeploy the bridge from the current checkout
2. upgrade the bridge to active/passive HA with two replicas
3. verify the public endpoints and both STS proofs in HA mode
4. delete the current leader pod
5. wait for a different Lease holder and for the bridge deployment to recover
6. re-run the public endpoint and STS proofs after failover
7. capture before/after Lease state, per-pod logs, per-pod `/leaderz` and
   `/metrics`, and namespace events under `tmp/smoke/captures/`

This command verifies recovery after failover. It does not attempt to measure
or enforce zero downtime during leader replacement.

## Notes

- The harness never stores raw AWS credentials in `tmp/smoke/`.
- The host-side service-account token is created only for the preflight call
  and then deleted.
- Existing AWS resources with the same smoke names are a hard error when there
  is no matching local Terraform state under `tmp/smoke/`.
