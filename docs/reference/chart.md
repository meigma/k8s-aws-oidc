---
title: Chart
sidebar_position: 2
---

This page describes the public Helm chart surface.

## Key values

| Value | Purpose |
|---|---|
| `issuerUrl` | Public issuer URL the bridge serves and the API server must match. |
| `image.*` | Bridge image repository, tag, digest, and pull policy. |
| `tailscale.hostname` | Tailscale hostname used by tsnet and Funnel. |
| `tailscale.tag` | Tailscale tag used when minting auth keys. |
| `tailscale.oauthSecret.*` | Existing secret that holds the OAuth client credentials. |
| `tailscale.stateSecret.*` | Secret used for persistent tsnet state. |
| `serviceAccount.*` | Bridge service-account creation or reuse. |
| `rbac.create` | Whether to create the role and role binding for the state secret. |
| `sourceIpAllowlist.*` | Optional public request CIDR gating. |
| `durations.*` | Cache and startup timing knobs. |

## Important rendered behavior

- one replica only
- `Recreate` deployment strategy
- no `Service`, `Ingress`, or `NetworkPolicy`
- readiness and startup checks use the internal health listener on `:8080`
- the pod runs as non-root with a read-only root filesystem
- writable state is limited to `emptyDir` mounts for `/var/lib/tsnet` and `/tmp`

## What the chart does not do

The chart does not:

- reconfigure the Kubernetes API server issuer
- create AWS IAM resources
- create the Tailscale OAuth client
- enable Funnel permissions in the tailnet policy

