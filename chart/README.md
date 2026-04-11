# k8s-aws-oidc-chart

This chart deploys the `k8s-aws-oidc` service that republishes the Kubernetes
service-account issuer discovery document and JWKS over Tailscale Funnel.

The server image is published as `ghcr.io/meigma/k8s-aws-oidc`.
The chart OCI artifact is published as `oci://ghcr.io/meigma/k8s-aws-oidc-chart`.

## What this chart creates

- `Deployment`
- `ServiceAccount`
- `Role`
- `RoleBinding`
- Optional empty state `Secret` for `tailscale.com/ipn/store/kubestore`

This chart does not create a Kubernetes `Service`, `Ingress`, `HPA`,
`PodDisruptionBudget`, or `NetworkPolicy`.

## Prerequisites

1. The kube-apiserver must already be configured with a public
   `--service-account-issuer=https://<hostname>.<tailnet>.ts.net` and an
   audience set that includes `sts.amazonaws.com`.
2. Tailscale Funnel must be enabled for the chosen hostname and tag.
3. An existing Secret must hold the Tailscale OAuth client credentials used to
   mint ephemeral auth keys.
4. The cluster should retain the default
   `system:service-account-issuer-discovery` ClusterRoleBinding. If it has been
   removed, recreate an equivalent binding manually for the workload's
   ServiceAccount or for `system:serviceaccounts`.

## Install

Create the OAuth credential Secret in the target namespace:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tailscale-oauth
type: Opaque
stringData:
  TS_API_CLIENT_ID: <client-id>
  TS_API_CLIENT_SECRET: <client-secret>
```

Install the chart:

```bash
helm install oidc-proxy oci://ghcr.io/meigma/k8s-aws-oidc-chart \
  --version 0.0.0-dev \
  --set issuerUrl=https://oidc.example.ts.net \
  --set tailscale.hostname=oidc \
  --set tailscale.tag=tag:oidc-proxy \
  --set tailscale.oauthSecret.name=tailscale-oauth
```

## Key values

- `issuerUrl`: public issuer URL served by the workload
- `tailscale.hostname`: tsnet hostname used for Funnel
- `tailscale.tag`: Tailscale auth key tag
- `tailscale.oauthSecret.name`: existing Secret with the OAuth credentials
- `tailscale.stateSecret.name`: optional override for the kubestore state Secret
- `tailscale.stateSecret.create`: pre-create the empty state Secret
- `serviceAccount.create`: create a dedicated ServiceAccount
- `serviceAccount.name`: required when `serviceAccount.create=false`
- `rbac.create`: create the Role and RoleBinding
- `sourceIpAllowlist.enabled`: enable source CIDR gating for public requests
- `sourceIpAllowlist.cidrs`: CIDR list used when the allowlist is enabled

## Security defaults

- Runs as UID/GID `65532`
- `allowPrivilegeEscalation: false`
- `readOnlyRootFilesystem: true`
- `seccompProfile: RuntimeDefault`
- Drops all Linux capabilities except `NET_BIND_SERVICE`

The chart mounts writable `emptyDir` volumes at `/var/lib/tsnet` and `/tmp` and
sets `XDG_CONFIG_HOME` and `TMPDIR` so `tsnet` can keep its mutable local state
off the root filesystem.

## Operations

- The workload is intentionally single-replica. The chart hard-codes one
  replica and uses a `Recreate` strategy.
- The readiness and startup probes hit `GET /healthz` on the internal listener
  at `:8080`.
- Rotating the external OAuth Secret does not restart the pod automatically.
  Restart the Deployment after credential rotation.
