---
title: Config
sidebar_position: 1
---

This page describes the bridge runtime environment variables loaded by
`internal/config`.

## Required

| Variable | Description |
|---|---|
| `ISSUER_URL` | Public issuer URL served by the bridge. Must be `https://` with no extra path. |
| `TS_HOSTNAME` | Tailscale hostname for the bridge node. |
| `TS_STATE_SECRET` | Kubernetes secret name used by `kubestore` for tsnet state. |
| `TS_API_CLIENT_ID` | Tailscale OAuth client ID. |
| `TS_API_CLIENT_SECRET` | Tailscale OAuth client secret. |
| `TS_TAG` | Tailscale tag advertised by the bridge. Must start with `tag:`. |

## Optional

| Variable | Default |
|---|---|
| `HEALTH_ADDR` | `:8080` |
| `FUNNEL_ADDR` | `:443` |
| `JWKS_CACHE_TTL` | `60s` |
| `JWKS_CACHE_MAX_AGE_HEADER` | `60s` |
| `DISCOVERY_MAX_AGE_HEADER` | `1h` |
| `STARTUP_FETCH_TIMEOUT` | `30s` |
| `TS_START_TIMEOUT` | `30s` |
| `SHUTDOWN_TIMEOUT` | `10s` |
| `TS_STATUS_POLL_INTERVAL` | `15s` |
| `LOG_FORMAT` | `json` |
| `LOG_LEVEL` | `info` |
| `SOURCE_IP_ALLOWLIST_ENABLED` | `false` |
| `SOURCE_IP_ALLOWLIST_CIDRS` | unset |
| `LEADER_ELECTION_ENABLED` | `false` |
| `LEADER_ELECTION_LEASE_NAME` | unset |
| `LEADER_ELECTION_NAMESPACE` | `POD_NAMESPACE` |
| `LEADER_ELECTION_IDENTITY` | `POD_NAME` |
| `LEADER_ELECTION_LEASE_DURATION` | `15s` |
| `LEADER_ELECTION_RENEW_DEADLINE` | `10s` |
| `LEADER_ELECTION_RETRY_PERIOD` | `2s` |

## Validation notes

- `ISSUER_URL` must be host-only and must not include a path, query, fragment,
  or explicit port.
- `SOURCE_IP_ALLOWLIST_CIDRS` is required when source allowlisting is enabled.
- when leader election is enabled, lease name, namespace, and identity must be present.
- `LEADER_ELECTION_LEASE_DURATION` must be greater than `LEADER_ELECTION_RENEW_DEADLINE`, which must be greater than `LEADER_ELECTION_RETRY_PERIOD`.
- cache and timeout durations must be positive.
- `JWKS_CACHE_TTL` must be at least `5s`.

## Removed settings

The service no longer supports overriding the upstream Kubernetes JWKS URL or
the Tailscale API base URL. It is intentionally pinned to the in-cluster API
server and the real Tailscale control plane.
