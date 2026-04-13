---
title: Issuer
sidebar_position: 2
---

The API server issuer must exactly match the public bridge URL because AWS
compares the token `iss` claim directly to the IAM OIDC provider URL.

That is why a simple discovery-document rewrite is not enough. Even if the
bridge serves a valid public discovery document, AWS will still reject tokens
minted with the wrong `iss`.

The bridge does not own token minting. The Kubernetes API server does. The
bridge only republishes the metadata AWS needs to validate those tokens.

