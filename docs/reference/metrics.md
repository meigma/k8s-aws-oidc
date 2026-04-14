---
title: Metrics
sidebar_position: 6
---

The bridge exposes Prometheus-format metrics on the internal listener at
`GET /metrics`. This endpoint is not served on the public Funnel listener.

## Core Metric Families

- `oidc_proxy_http_requests_total{route,method,decision,status_code}`
- `oidc_proxy_http_request_duration_seconds{route,method,decision}`
- `oidc_proxy_jwks_prime_total{result}`
- `oidc_proxy_jwks_refresh_total{result,error_kind}`
- `oidc_proxy_jwks_serving_stale_total{error_kind}`
- `oidc_proxy_jwks_age_seconds`
- `oidc_proxy_jwks_ready`
- `oidc_proxy_jwks_kid_count`
- `oidc_proxy_tsnet_start_total{result,error_kind}`
- `oidc_proxy_tsnet_state_transitions_total{state}`
- `oidc_proxy_public_listener_restarts_total{reason}`
- `oidc_proxy_leader_election_transitions_total{state}`
- `oidc_proxy_leader`
- `oidc_proxy_public_ready`
- `oidc_proxy_issuer_host_verification_total{result}`
- `oidc_proxy_auth_key_mint_total{result,error_kind}`
- `oidc_proxy_process_start_time_seconds`
- `oidc_proxy_health_server_start_total`
- `oidc_proxy_build_info{version,go_version}`

## Label Policy

Metrics intentionally use low-cardinality labels only:

- `route`: `discovery`, `jwks`, `unknown`
- `decision`: `served`, `denied_missing_source`, `denied_cidr`, `jwks_not_ready`, `method_not_allowed`, `not_found`
- `result`: `success`, `failure`
- `error_kind`: sanitized error taxonomy only

The bridge does not use client IPs, raw paths, tokens, auth keys, or request
headers as metric labels.

## Chart Integration

The Helm chart can render:

- an internal ClusterIP `Service` for scraping `/metrics`
- an optional `ServiceMonitor` for Prometheus Operator

See `metrics.*` in the chart values for the scrape-resource controls.
