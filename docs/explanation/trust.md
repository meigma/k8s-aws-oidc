---
title: Trust
sidebar_position: 1
---

AWS does not trust Kubernetes service-account tokens directly. It trusts:

1. the public issuer URL registered in IAM
2. the public JWKS behind that issuer
3. the role trust policy conditions on `aud` and `sub`

In this design, the bridge exists to expose only the two OIDC endpoints AWS
needs without exposing the Kubernetes API server itself.

The trust chain is:

```text
Kubernetes token -> public issuer URL -> public JWKS -> AWS IAM OIDC provider -> IAM role trust policy
```

If any link in that chain drifts, web-identity role assumption fails.

