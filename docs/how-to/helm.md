---
title: Helm
sidebar_position: 1
---

Use this guide when you already understand the end-to-end setup and need to
deploy or update the bridge in an existing cluster.

## What matters in real deployments

The chart is intentionally small. The values that matter most are:

- `issuerUrl`
- `tailscale.hostname`
- `tailscale.tag`
- `tailscale.oauthSecret.name`
- `tailscale.stateSecret.name`
- `serviceAccount.*`
- `sourceIpAllowlist.*`

The chart assumes:

- the API server issuer is already configured to match `issuerUrl`
- the OAuth secret already exists
- the Tailscale tag is already allowed by the tailnet policy and has the
  `funnel` node attribute
- one replica with a `Recreate` strategy is acceptable

## Minimal install

```bash
helm upgrade --install oidc-bridge ./chart \
  --namespace oidc-system \
  --create-namespace \
  --set issuerUrl=https://oidc.example.tailnet.ts.net \
  --set tailscale.hostname=oidc-example \
  --set tailscale.tag=tag:cat-k8s-oidc \
  --set tailscale.oauthSecret.name=tailscale-oauth
```

## Common adaptations

### Reuse an existing service account

```bash
--set serviceAccount.create=false \
--set serviceAccount.name=oidc-bridge
```

### Pin the state secret name

```bash
--set tailscale.stateSecret.name=oidc-bridge-state
```

Do this if you want the bridge identity to survive redeployments cleanly and be
easy to inspect.

### Turn on request allowlisting

The bridge supports source CIDR filtering on the public listener, but it is off
by default.

```bash
--set sourceIpAllowlist.enabled=true \
--set-json 'sourceIpAllowlist.cidrs=["1.2.3.0/24"]'
```

Use it only if you are confident the callers you care about are stable enough
to allowlist.

## Upgrade checks

Before and after an upgrade:

1. confirm the pod reaches `Ready`
2. fetch public discovery and JWKS
3. compare public JWKS keys with the internal cluster JWKS
4. retry one real `AssumeRoleWithWebIdentity` path
