# Changelog

## [1.1.0](https://github.com/meigma/k8s-aws-oidc/compare/v1.0.0...v1.1.0) (2026-04-14)


### Features

* adds audit logging and Prometheus metrics for the OIDC bridge ([#28](https://github.com/meigma/k8s-aws-oidc/issues/28)) ([342e2eb](https://github.com/meigma/k8s-aws-oidc/commit/342e2eb02fed87c04080a4bc01afa75bdfcbd7b2))
* adds leader-elected HA and smoke failover coverage ([#29](https://github.com/meigma/k8s-aws-oidc/issues/29)) ([67e0849](https://github.com/meigma/k8s-aws-oidc/commit/67e0849da43723893ea5b638f3a0f97f73f7845d))
* adds optional Kyverno image verification and default Helm releases to digest-pinned images ([#25](https://github.com/meigma/k8s-aws-oidc/issues/25)) ([8c12b2d](https://github.com/meigma/k8s-aws-oidc/commit/8c12b2d1d6ce643574012840a55ea9302657fac7))

## 1.0.0 (2026-04-13)


### Features

* add Helm chart and Moon chart validation tooling ([#3](https://github.com/meigma/k8s-aws-oidc/issues/3)) ([62d1597](https://github.com/meigma/k8s-aws-oidc/commit/62d15976ec09332326b2f674a6327439badc03ae))
* add public readiness handling and moon-based CI ([#2](https://github.com/meigma/k8s-aws-oidc/issues/2)) ([bbcf293](https://github.com/meigma/k8s-aws-oidc/commit/bbcf293a46c96ed484adada99f98032ca7ef9261))
* harden public readiness and add moon CI ([#1](https://github.com/meigma/k8s-aws-oidc/issues/1)) ([0980919](https://github.com/meigma/k8s-aws-oidc/commit/0980919b6e8e6f716b325c0855375dcb45c148c7))
* harden release automation and add rehearsal workflow ([#5](https://github.com/meigma/k8s-aws-oidc/issues/5)) ([1869827](https://github.com/meigma/k8s-aws-oidc/commit/1869827b68ad348fb108cdcedecbc9ff70daa6d6))


### Bug Fixes

* first-release draft handoff for immutable GitHub releases ([#12](https://github.com/meigma/k8s-aws-oidc/issues/12)) ([0d32fc0](https://github.com/meigma/k8s-aws-oidc/commit/0d32fc008db350487e285280a136d94f3d6a4ccb))
* local release image scanning in reusable release workflow ([#23](https://github.com/meigma/k8s-aws-oidc/issues/23)) ([78deac0](https://github.com/meigma/k8s-aws-oidc/commit/78deac01f56869bd1cb0b82b0dd9871783b4cf29))
* read release app id from actions secret ([#6](https://github.com/meigma/k8s-aws-oidc/issues/6)) ([16fcc48](https://github.com/meigma/k8s-aws-oidc/commit/16fcc48c3b551c83ac085d1f77fa2faeab39733c))
* release draft handoff for GitHub Actions ([#18](https://github.com/meigma/k8s-aws-oidc/issues/18)) ([23575aa](https://github.com/meigma/k8s-aws-oidc/commit/23575aaec4e06ce3aec3c0349f60381c88c6c3eb))
* release workflow handoff and GoReleaser tag builds ([#15](https://github.com/meigma/k8s-aws-oidc/issues/15)) ([c3e30cd](https://github.com/meigma/k8s-aws-oidc/commit/c3e30cd9b58232db534e2099d7152c648a14be63))
* rename reusable workflow token secret ([#8](https://github.com/meigma/k8s-aws-oidc/issues/8)) ([474d6fb](https://github.com/meigma/k8s-aws-oidc/commit/474d6fb46bdabed03d788447f91918239b56efdf))
