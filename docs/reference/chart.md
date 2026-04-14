---
title: Chart
sidebar_position: 2
---

This page describes the public Helm chart surface.

## Key values

| Value | Purpose |
|---|---|
| `issuerUrl` | Public issuer URL the bridge serves and the API server must match. |
| `image.*` | Bridge image repository, tag override, digest override, and pull policy. |
| `tailscale.hostname` | Tailscale hostname used by tsnet and Funnel. |
| `tailscale.tag` | Tailscale tag used when minting auth keys. |
| `tailscale.oauthSecret.*` | Existing secret that holds the OAuth client credentials. |
| `tailscale.stateSecret.*` | Secret used for persistent tsnet state. |
| `replicaCount` | Number of bridge pods to run. |
| `leaderElection.*` | Kubernetes Lease-based active/passive HA settings. |
| `serviceAccount.*` | Bridge service-account creation or reuse. |
| `rbac.create` | Whether to create the role and role binding for the state secret and Lease access. |
| `podDisruptionBudget.*` | Optional PDB for HA installs. |
| `kyverno.*` | Optional namespaced Kyverno policy for image signature and provenance enforcement. |
| `sourceIpAllowlist.*` | Optional public request CIDR gating. |
| `durations.*` | Cache and startup timing knobs. |

## Important rendered behavior

- single replica by default
- `RollingUpdate` only when leader election is enabled; otherwise `Recreate`
- published OCI charts default the workload image to the release digest embedded in chart metadata
- no public `Service`, `Ingress`, or `NetworkPolicy`
- startup and liveness checks use `/livez` on the internal health listener
- readiness checks use `/readyz` on the internal health listener
- the pod runs as non-root with a read-only root filesystem
- writable state is limited to `emptyDir` mounts for `/var/lib/tsnet` and `/tmp`

## What the chart does not do

The chart does not:

- reconfigure the Kubernetes API server issuer
- create AWS IAM resources
- create the Tailscale OAuth client
- enable Funnel permissions in the tailnet policy
- install Kyverno itself when `kyverno.enabled=true`
