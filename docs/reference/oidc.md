---
title: OIDC
sidebar_position: 4
---

The bridge serves two public OIDC endpoints:

- `GET /.well-known/openid-configuration`
- `GET /openid/v1/jwks`

## Discovery document

The discovery document is hand-crafted and contains the AWS-relevant fields:

- `issuer`
- `jwks_uri`
- `response_types_supported`
- `subject_types_supported`
- `id_token_signing_alg_values_supported`
- `claims_supported`

The `issuer` and `jwks_uri` are derived from `ISSUER_URL`, not proxied from the
cluster API server.

## JWKS

The bridge fetches the cluster JWKS from:

```text
https://kubernetes.default.svc/openid/v1/jwks
```

It validates and re-emits only the required signing fields.

## Cache behavior

- discovery responses use `Cache-Control` based on `DISCOVERY_MAX_AGE_HEADER`
- JWKS responses use `Cache-Control` based on the current cache freshness
- the bridge primes the JWKS cache at startup and refreshes it in the background

## Health

The internal listener serves:

- `GET /livez`
- `GET /readyz`
- `GET /leaderz`
- `GET /healthz` (compatibility alias for `/readyz`)

These routes are not part of the public OIDC surface.
