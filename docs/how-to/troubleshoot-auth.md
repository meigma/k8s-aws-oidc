---
title: Troubleshoot auth
sidebar_position: 3
---

Use this guide when `AssumeRoleWithWebIdentity` is failing and you need a fast
operator flow.

## 1. Check the token claims

Inspect the projected service-account token and confirm:

- `iss` equals the public bridge URL exactly
- `aud` includes `sts.amazonaws.com`
- `sub` matches the expected Kubernetes service account

If `iss` is wrong, the API server issuer is wrong. The bridge cannot fix that.

## 2. Check public discovery and JWKS

Fetch:

```bash
curl https://oidc.example.tailnet.ts.net/.well-known/openid-configuration
curl https://oidc.example.tailnet.ts.net/openid/v1/jwks
```

Then compare them to the cluster endpoints:

```bash
kubectl get --raw /.well-known/openid-configuration
kubectl get --raw /openid/v1/jwks
```

If the public JWKS does not expose the same `kid` values as the cluster JWKS,
AWS will reject tokens signed by keys it cannot see.

## 3. Check AWS IAM configuration

Confirm:

- the OIDC provider URL exactly matches the token issuer
- the role trusts the correct provider ARN
- the trust conditions use exact `sub` and `aud` values

Typical AWS failures mean:

- `No OpenIDConnect provider found`: provider missing in the account
- `InvalidIdentityToken`: issuer, audience, or JWKS mismatch
- `AccessDenied`: the role trust passed, but the role policy denied the API

## 4. Check public reachability

Funnel propagation is not always immediate. A bridge pod can be healthy before
the public endpoint is reachable from AWS.

If AWS reports provider communication failures:

1. fetch the public discovery document from a non-tailnet path
2. wait a short interval and retry
3. confirm the bridge logs do not show repeated serve-loop failures

## 5. Check the bridge pod

Look for:

- startup fetch failures against the in-cluster JWKS
- Tailscale login issues
- Funnel permission errors
- missing `funnel` node attributes on the bridge tag
- issuer host mismatch errors

If the pod is healthy but AWS still cannot validate tokens, focus on propagation
or IAM config rather than Kubernetes scheduling.
