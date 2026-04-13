---
title: k8s-aws-oidc docs
sidebar_label: Overview
sidebar_position: 1
slug: /
---

`k8s-aws-oidc` republishes the Kubernetes service-account issuer metadata that
AWS IAM needs to validate `AssumeRoleWithWebIdentity` for private clusters.

The service is small, but the full setup spans a few systems:

- Kubernetes API server issuer and audience configuration
- Tailscale Funnel for public OIDC discovery and JWKS
- Helm deployment for the bridge
- AWS IAM OIDC provider and trusted roles

This site is organized with Diátaxis:

- [Tutorials](/tutorials): one complete end-to-end path
- [How-to](/how-to): operator tasks and targeted fixes
- [Reference](/reference): config, chart, Terraform, and endpoint details
- [Explanation](/explanation): why the setup works the way it does

Start here if you are deploying the bridge for the first time:

- [First deploy](./tutorials/first-deploy.md)

