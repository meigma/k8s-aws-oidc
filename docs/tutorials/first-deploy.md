---
title: First deploy
sidebar_position: 1
---

This tutorial walks through one complete path from a private Kubernetes cluster
to a workload that can assume an AWS IAM role with a projected service-account
token.

It uses **KinD** as the concrete cluster so the API server issuer settings are
easy to control locally. If you already have a real cluster, keep the same
issuer, audience, Helm, and AWS steps, but substitute your normal cluster
provisioning and API server configuration workflow.

You will:

1. choose a public issuer URL served through Tailscale Funnel
2. prepare the Tailscale tag and ACL policy for Funnel
3. configure the Kubernetes API server to mint tokens with that issuer
4. deploy the bridge with Helm
5. create the AWS IAM OIDC provider and role with Terraform
6. run one workload that proves web-identity authentication works

## Before you begin

You need:

- Docker, `kind`, `kubectl`, `helm`, and `tofu`
- a Tailscale tailnet where the chosen bridge tag can use Funnel
- Tailscale OAuth client credentials that can register the bridge node
- an AWS account where you can create:
  - an IAM OIDC provider
  - an IAM role trusted by that provider
- enough local network access for the bridge to reach Tailscale and for AWS to
  fetch the public issuer URL

For the examples below, export a small set of values:

```bash
export CLUSTER_NAME=oidc-demo
export ISSUER_URL=https://oidc.example.tailnet.ts.net
export TS_HOSTNAME=oidc-example
export TS_TAG=tag:cat-k8s-oidc
export NAMESPACE=demo
export SERVICE_ACCOUNT=demo-app
export ROLE_NAME=demo-app-role
```

## 1. Pick the issuer URL

Choose a stable public hostname for the bridge, for example:

```text
https://oidc.example.tailnet.ts.net
```

That exact URL must be used in three places:

- the Kubernetes API server issuer setting
- the bridge `ISSUER_URL`
- the AWS IAM OIDC provider

If any of these drift, `AssumeRoleWithWebIdentity` will fail.

## 2. Prepare the tailnet

The bridge uses a tagged Tailscale node. That tag must be allowed by the ACL
policy, and it must also have the `funnel` node attribute.

At minimum, your tailnet policy needs entries like:

```json
{
  "tagOwners": {
    "tag:cat-k8s-oidc": ["group:admin"]
  },
  "nodeAttrs": [
    {
      "target": ["tag:cat-k8s-oidc"],
      "attr": ["funnel"]
    }
  ]
}
```

Without the `nodeAttrs` entry, the bridge can register and connect to the
tailnet but will fail when it tries to open the public Funnel listener.

You also need a Tailscale OAuth client that can mint auth keys for the same
tag. The bridge expects to receive that client ID and secret through the
Kubernetes secret created later in this tutorial.

Do not continue until:

- the bridge tag exists in your tailnet policy
- the tag is owned by an identity you control
- the tag has the `funnel` node attribute
- your OAuth client can create devices with that tag

## 3. Create the KinD cluster

Write a KinD config that sets the Kubernetes API server issuer to the public
bridge URL and includes the AWS STS audience:

```yaml title="kind-config.yaml"
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        service-account-issuer: https://oidc.example.tailnet.ts.net
        api-audiences: https://kubernetes.default.svc.cluster.local,sts.amazonaws.com
```

Create the cluster:

```bash
kind create cluster --name "${CLUSTER_NAME}" --config kind-config.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=180s
```

The same logical requirement applies to a real cluster: the API server must
mint tokens with the public bridge URL as `iss`, and `sts.amazonaws.com` must
be an allowed audience.

For example, the bridge expects tokens whose claims look like this:

```json
{
  "iss": "https://oidc.example.tailnet.ts.net",
  "aud": ["sts.amazonaws.com"],
  "sub": "system:serviceaccount:demo:demo-app"
}
```

Do not continue until the cluster is minting tokens with the public issuer.

## 4. Deploy the bridge

Create the target namespace and the Tailscale OAuth secret:

```bash
kubectl create namespace oidc-system
```

```yaml title="tailscale-oauth.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: tailscale-oauth
  namespace: oidc-system
type: Opaque
stringData:
  TS_API_CLIENT_ID: <client-id>
  TS_API_CLIENT_SECRET: <client-secret>
```

```bash
kubectl apply -f tailscale-oauth.yaml
```

Deploy the chart:

```bash
helm upgrade --install oidc-bridge ./chart \
  --namespace oidc-system \
  --set issuerUrl="${ISSUER_URL}" \
  --set tailscale.hostname="${TS_HOSTNAME}" \
  --set tailscale.tag="${TS_TAG}" \
  --set tailscale.oauthSecret.name=tailscale-oauth
```

Verify the public endpoints:

```bash
kubectl -n oidc-system rollout status deployment/oidc-bridge --timeout=300s
curl "${ISSUER_URL}/.well-known/openid-configuration"
curl "${ISSUER_URL}/openid/v1/jwks"
```

The discovery document should advertise the same issuer URL and a JWKS URL on
the same host.

If the bridge pod is ready but the public URL still fails from outside the
tailnet, wait a short interval and retry. Funnel and AWS-side reachability are
not always immediate.

If the bridge logs show a Funnel permission error, go back to the tailnet
policy and confirm that the bridge tag has the `funnel` node attribute.

## 5. Create the AWS resources

Use the Terraform example as the starting point:

```bash
cd terraform/examples/basic
tofu init
tofu apply \
  -var="issuer_url=${ISSUER_URL}" \
  -var="role_name=${ROLE_NAME}" \
  -var="kubernetes_namespace=${NAMESPACE}" \
  -var="kubernetes_service_account=${SERVICE_ACCOUNT}"
```

This creates:

- one AWS IAM OIDC provider for the bridge issuer URL
- one IAM role trusted for `system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}`

## 6. Prove role assumption from a workload

Create the service account used by the workload:

```bash
kubectl create namespace "${NAMESPACE}"
kubectl -n "${NAMESPACE}" create serviceaccount "${SERVICE_ACCOUNT}"
```

Now run a small pod that:

- uses a projected service-account token with audience `sts.amazonaws.com`
- sets `AWS_ROLE_ARN`
- sets `AWS_WEB_IDENTITY_TOKEN_FILE`

```yaml title="demo-app.yaml"
apiVersion: v1
kind: Pod
metadata:
  name: demo-app
  namespace: demo
spec:
  serviceAccountName: demo-app
  automountServiceAccountToken: false
  restartPolicy: Never
  containers:
    - name: aws
      image: public.ecr.aws/aws-cli/aws-cli:2
      command: ["sh", "-c", "sleep 3600"]
      env:
        - name: AWS_ROLE_ARN
          value: "arn:aws:iam::<account-id>:role/demo-app-role"
        - name: AWS_WEB_IDENTITY_TOKEN_FILE
          value: "/var/run/secrets/oidc/token"
        - name: AWS_REGION
          value: "us-east-1"
        - name: AWS_DEFAULT_REGION
          value: "us-east-1"
      volumeMounts:
        - name: oidc-token
          mountPath: /var/run/secrets/oidc
          readOnly: true
  volumes:
    - name: oidc-token
      projected:
        sources:
          - serviceAccountToken:
              audience: sts.amazonaws.com
              expirationSeconds: 3600
              path: token
```

Apply it, substituting your real AWS account ID in the role ARN:

```bash
kubectl apply -f demo-app.yaml
kubectl -n "${NAMESPACE}" wait --for=condition=Ready pod/demo-app --timeout=180s
kubectl -n "${NAMESPACE}" exec demo-app -- aws sts get-caller-identity
```

A successful result looks like:

```json
{
  "UserId": "AROAXXXXXXXX:botocore-session-...",
  "Account": "123456789012",
  "Arn": "arn:aws:sts::123456789012:assumed-role/demo-app-role/botocore-session-..."
}
```

The important part is that the workload sees an
`arn:aws:sts::...:assumed-role/...` caller ARN, not the node or user identity
that deployed the cluster.

If you use the `terraform/examples/basic` module unchanged, you can also grant
the role `iam:GetRole` on itself and verify that as a second AWS API call.

## Next steps

- Use [Helm](../how-to/helm.md) when integrating the bridge into an existing deployment flow.
- Use [AWS](../how-to/aws.md) when wiring the Terraform modules into an existing stack.
- Use [Troubleshoot auth](../how-to/troubleshoot-auth.md) if `AssumeRoleWithWebIdentity` fails.
