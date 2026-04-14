---
title: Logging
sidebar_position: 5
---

The bridge emits structured `slog` events with a stable `component` and
`event` field on every record. `LOG_FORMAT=json` is the default for cluster
ingestion; `LOG_FORMAT=text` is available for local debugging.

## Common Fields

| Field | Meaning |
|---|---|
| `component` | Stable subsystem name such as `process`, `public_http`, `jwks_cache`, `tsnet_runner`, `leader_election`, or `tailscale_auth`. |
| `event` | Stable event name for alerting and parsing. |
| `msg` | Human-readable text only; do not parse it. |

## Event Catalog

| Event | Component | Key fields |
|---|---|---|
| `process_start` | `process` | `hostname`, `funnel_addr`, `issuer_host`, `log_format`, `log_level`, `source_ip_allowlist_enabled`, `source_ip_allowlist_cidr_count` |
| `process_stop` | `process` | `result`, optional `error_kind`, optional `error` |
| `health_server_start` | `health_http` | `addr` |
| `http_request` | `public_http` | `route`, `path`, `method`, `status`, `latency_ms`, `source_present`, optional `source_ip`, `decision` |
| `jwks_prime_success` | `jwks_cache` | `kid_count`, `kids` |
| `jwks_prime_failure` | `jwks_cache` | `error_kind`, `error`, optional `status_code`, optional `body_size_bytes` |
| `jwks_refresh_success` | `jwks_cache` | `kid_count`, `kids` |
| `jwks_refresh_failure` | `jwks_cache` | `error_kind`, `error`, optional `status_code`, optional `body_size_bytes` |
| `jwks_serving_stale` | `jwks_cache` | `stale_remaining_seconds` |
| `tsnet_state_change` | `tsnet_runner` | `state` |
| `tsnet_start_failure` | `tsnet_runner` | `error_kind`, `error`, optional `state` |
| `issuer_host_verified` | `tsnet_runner` | `expected_host`, `cert_domains`, `cert_domain_count` |
| `issuer_host_mismatch` | `tsnet_runner` | `expected_host`, `cert_domains`, `cert_domain_count` |
| `public_listener_restart` | `tsnet_runner` | `reason` |
| `leader_election_initialized` | `leader_election` | `lease_name`, `namespace`, `identity`, `lease_duration`, `renew_deadline`, `retry_period` |
| `leadership_acquired` | `leader_election` | `identity`, `lease_name` |
| `leadership_lost` | `leader_election` | `identity`, `lease_name` |
| `leader_observed` | `leader_election` | `identity`, `leader_identity` |
| `leader_runner_exit` | `leader_election` | `error_kind`, `error` |
| `auth_key_mint_success` | `tailscale_auth` | `tags`, `tag_count` |
| `auth_key_mint_failure` | `tailscale_auth` | `error_kind`, `error` |

## Decisions

`http_request.decision` uses the following values:

- `served`
- `denied_missing_source`
- `denied_cidr`
- `jwks_not_ready`
- `method_not_allowed`
- `not_found`

## Example Queries

- Allowlist denials: `event=http_request decision=denied_cidr`
- Missing Funnel source metadata: `event=http_request decision=denied_missing_source`
- Repeated JWKS refresh failures: `event=jwks_refresh_failure`
- Stale JWKS still being served: `event=jwks_serving_stale`
- Auth-key mint problems: `event=auth_key_mint_failure`
- Issuer host drift: `event=issuer_host_mismatch`
- Suspicious unknown path probes: `event=http_request route=unknown`

## Redaction Rules

The bridge does not log:

- OAuth client secrets
- Minted Tailscale auth keys
- Kubernetes service-account bearer tokens
- `Authorization` headers
- Raw request bodies or query strings
- Raw JWKS key material (`n`, `e`)
- Raw upstream response bodies
