---
title: Caching
sidebar_position: 3
---

The bridge caches JWKS data because the cluster signing keys can rotate and AWS
can fetch the JWKS repeatedly during role assumption.

The design trades absolute freshness for bounded stability:

- the bridge primes the JWKS cache at startup
- it refreshes in the background on a short interval
- it keeps serving the previous good value for a bounded stale window

This matters operationally because there are two propagation windows:

1. the bridge must learn about new signing keys from the cluster
2. AWS must be able to fetch the updated public JWKS

Short, bounded caching helps avoid outages during additive key rotation while
still limiting how long stale keys are served.

