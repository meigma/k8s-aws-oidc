# Design

> Working document. Captures architecture decisions reached so far.
> Deployment specifics (Dockerfile, k8s manifests, RBAC, CI) are deferred.

## Problem

A bare-metal Kubernetes cluster's API server is not publicly reachable for security reasons. Workloads in the cluster need to authenticate to AWS via OIDC federation (IRSA-style `AssumeRoleWithWebIdentity`). AWS requires public reachability of two OIDC metadata endpoints — and only those two — to establish trust with the cluster's service account token issuer. Exposing the entire kube-apiserver publicly just to satisfy those two endpoints is unacceptable.

## Solution

A small Go service that runs inside the cluster, fetches the cluster's OIDC metadata from `kubernetes.default.svc`, and re-publishes only the two endpoints AWS needs:

- `GET /.well-known/openid-configuration`
- `GET /openid/v1/jwks`

The service is exposed publicly via Tailscale Funnel, embedded directly in the Go binary using `tailscale.com/tsnet`. AWS reaches it at `https://<hostname>.<tailnet>.ts.net`.

## What AWS actually validates

AWS only ever makes two HTTPS GETs against the registered OIDC provider URL: the discovery document and the JWKS it points at. It never calls `authorization_endpoint`, `token_endpoint`, `userinfo_endpoint`, or any other OIDC endpoint. On every `AssumeRoleWithWebIdentity` call, the trust chain is:

1. JWT `iss` claim equals the registered IAM provider URL (string match, byte-for-byte).
2. JWT signature verifies against a `kid` present in the JWKS fetched from `jwks_uri`.
3. The TLS chain on the JWKS endpoint anchors to a publicly trusted CA. (Since July 2024, the IAM thumbprint is ignored when the chain anchors to a public root, which Funnel's Let's Encrypt cert satisfies.)
4. JWT `aud` and `sub` claims match the IAM role's trust policy.

Required fields in the discovery document: `issuer`, `jwks_uri`, `response_types_supported: ["id_token"]`, `subject_types_supported: ["public"]`, `id_token_signing_alg_values_supported: ["RS256"]`, `claims_supported: ["aud","iat","iss","sub"]`.

## Critical constraint: cluster reconfiguration is mandatory

A naive proxy that simply rewrites the discovery document is **insufficient**. AWS validates the `iss` claim from inside the JWT, which is set at signing time by the kube-apiserver based on its `--service-account-issuer` flag. The cluster admin must reconfigure the apiserver:

- `--service-account-issuer=https://<funnel-hostname>.<tailnet>.ts.net`
- `--api-audiences=sts.amazonaws.com,...` (must include the AWS STS audience)
- For a graceful rollover, pass `--service-account-issuer` twice — first the new public URL (signs new tokens), second the old internal URL (still accepted for in-cluster validation).

The Go service is then a pure metadata republisher. Its job is to make the matching public metadata available; the JWT-signing identity is owned by the apiserver.

## Architecture decisions

### A1. Embedded `tsnet`, not the Tailscale Kubernetes operator

`tailscale.com/tsnet` is a first-class supported package. `Server.ListenFunnel("tcp", ":443")` returns a `net.Listener` that terminates HTTPS with a Tailscale-issued Let's Encrypt cert and is publicly reachable. Tailscale's own `golink` and the operator's proxies use this same machinery.

For a single-purpose, single-replica proxy, embedding `tsnet` removes the need for the operator (no CRDs, no controller, no proxy sidecars, no broad RBAC). The deployment becomes one binary, one Deployment, one Secret, one ServiceAccount with narrow RBAC. Fewer components, smaller blast radius, simpler ops.

The operator's real advantages — dynamic provisioning, multi-proxy fleet management, CRD-driven reconciliation — are not relevant to this use case.

### A2. State persistence via `tailscale.com/ipn/store/kubestore`

The operator's proxies persist tsnet identity (machine key, node key) to a Kubernetes Secret using the public `kubestore` package. We use the same package directly. No PVC, no on-disk state. The pod's ServiceAccount needs `get/update/patch` on its single state Secret only.

```go
import "tailscale.com/ipn/store/kubestore"

st, _ := kubestore.New(logger.Discard, "oidc-proxy-state")
s := &tsnet.Server{
    Hostname: "oidc",
    Store:    st,
    // AuthKey set conditionally; see A3.
}
```

### A3. Bootstrap with OAuth client credentials, not a static auth key

A static auth key in a Secret is long-lived and difficult to rotate or audit. Instead, mount Tailscale OAuth client credentials (`CLIENT_ID` + `CLIENT_SECRET`) and have the binary mint a fresh, single-use, tagged, ephemeral auth key on demand using `tailscale.com/client/tailscale`.

After successful registration, the persisted node identity in the kubestore Secret is what authenticates to the control plane. Auth keys are never refreshed periodically. Reissue is purely **reactive**: if `tsnet` reports `NeedsLogin` (lost identity, manual deletion, key expiry, state corruption), mint a fresh auth key, retry. This mirrors `containerboot`'s behavior in `cmd/containerboot/main.go:374-424`, just inlined.

The OAuth credential stays mounted at runtime (not just bootstrap) so the binary can self-recover from a lost-identity scenario without human intervention. For an OIDC proxy whose downtime breaks every IRSA-using workload in the cluster, automatic recovery is worth more than the marginal credential-exposure improvement of deleting the credential post-bootstrap.

### A4. Hand-crafted discovery document, not pass-through

The `/.well-known/openid-configuration` response is a **compile-time constant** in the binary, containing only the six fields AWS requires. The `issuer` and `jwks_uri` values are baked in at build/config time and match the apiserver's `--service-account-issuer` flag.

Rationale: the discovery document's `jwks_uri` field tells AWS where to fetch keys. If we proxied verbatim and the upstream apiserver ever returned (through misconfiguration, compromise, or a future k8s version) a discovery doc with a different `jwks_uri`, we'd faithfully forward it and AWS would fetch keys from wherever it pointed. By making `jwks_uri` and `issuer` immutable in our binary, those critical fields cannot be influenced by upstream behavior. The proxy becomes a small trust anchor.

Secondary benefits: schema stability against future k8s additions, byte-level predictability of the served response, smaller exposed metadata surface.

### A5. Parse, validate, and re-emit the JWKS

The JWKS endpoint cannot be a constant (keys rotate), but it should not be passed through verbatim either. The fetcher:

1. Fetches `/openid/v1/jwks` from `https://kubernetes.default.svc` using the in-cluster service account token and CA bundle. The default `system:service-account-issuer-discovery` ClusterRole (bound to `system:serviceaccounts`) authorizes any pod's SA.
2. Parses the JSON, validates structure, and enforces an allowlist:
   - Algorithm in `{RS256}` (configurable; whatever the apiserver actually uses).
   - Required fields per key: `kid`, `kty`, `alg`, `use: "sig"`, `n`, `e`.
   - Cap on number of keys (AWS limits to 100 RSA + 100 EC).
3. Re-emits a clean JSON object containing only the validated fields.

This protects against malformed upstream responses, schema drift, and unknown-field injection. It does **not** protect against malicious key injection if the apiserver itself is compromised — those keys are by definition what we're republishing — but it bounds what we'll forward.

### A6. Short-TTL background refresh cache

The fetcher caches the JWKS in memory with a short TTL (target: 60s). A background goroutine refreshes on a schedule; serving never blocks on upstream. On refresh failure, the previous cached value is retained and served (fail-stale, never fail-empty). HTTP responses set `Cache-Control: public, max-age=...` so AWS STS honors caching on its side — without this header AWS may re-fetch on every `AssumeRoleWithWebIdentity` call and risk throttling.

The TTL must be short enough that additive key rotation in the cluster (operator-driven, multi-key overlap windows) reaches AWS before old keys are removed.

### A7. Stdlib-only HTTP server, hardened defaults

Go 1.22+ enhanced `ServeMux` for the two routes. No third-party HTTP framework. `http.Server` configured with tight timeouts (`ReadHeaderTimeout: 5s` is the most important — Slowloris mitigation), small `MaxHeaderBytes`, graceful shutdown via `signal.NotifyContext`. Responses set `Content-Type: application/json`, `X-Content-Type-Options: nosniff`, and the `Cache-Control` from A6. No CORS (AWS STS is server-side, not browser).

Method+path routing rejects everything except the two exact endpoints. Any other path returns 404 immediately.

The readiness endpoint is served on a separate, plain HTTP listener for Kubernetes probes (default `:8080`), not via Funnel. That keeps the internet-facing surface at exactly the two AWS-required endpoints while preserving ordinary probe wiring inside the cluster.

## Trust model

The trust chain that protects AWS-side credentials issued via this provider is:

```
AWS  →  DNS for <host>.<tailnet>.ts.net  →  TLS cert  →  discovery doc  →  jwks_uri  →  JWKS  →  JWT signature
```

Each link is owned by:

| Link | Owner | Compromise impact |
|---|---|---|
| DNS | Tailscale (operates `*.ts.net`) | Full takeover possible by tailnet admin |
| TLS cert | Let's Encrypt, issued to whoever holds the DNS name | Re-issued automatically on hostname takeover |
| Discovery doc | Our binary | Pinned at compile time (A4) — can't be redirected |
| JWKS | Our binary republishing apiserver keys | Bounded by parser allowlist (A5) |
| JWT signature | kube-apiserver's signing key | Owned by the cluster |

There is no AWS-side per-endpoint pinning primitive. AWS does not let us register a specific cert fingerprint or out-of-band public key with teeth. The thumbprint feature was deprecated in practice in 2024.

The result: **whoever controls the tailnet entry for the OIDC hostname owns the entire trust chain to AWS.** A tailnet admin compromise leads directly to AWS credential issuance for whatever IAM role's trust policy matches a spoofable `sub`. The apiserver doesn't need to be involved; the cluster is irrelevant to the attack.

Real mitigations (none of which live in this binary):

- Tailnet admin hygiene: SSO + MFA, restricted node creation, OAuth client credentials over static auth keys, audit logging.
- ACL discipline: `funnel` nodeAttr granted only to a single narrow tag (`tag:oidc-proxy`), not broadly.
- IAM least privilege: trust policies match specific `sub` values (`system:serviceaccount:<ns>:<sa>`), not wildcards. Add `aws:SourceVpc`/`aws:SourceIp` conditions where workload egress is stable. Short `MaxSessionDuration`. Tight role permissions.
- Certificate Transparency monitoring for unexpected LE issuance against the hostname (tripwire for hostname takeover).
- CloudTrail alerting on `AssumeRoleWithWebIdentity` from unexpected source IPs / unexpected subjects.
- Don't reuse the OIDC provider URL across environments.

## Optional: AWS source-IP allowlisting

`tsnet`'s Funnel listener wraps each connection in an `*ipn.FunnelConn` (`ipn/serve.go:106`) whose `Src` field contains the **real public client IP** (not the Tailscale relay address — that's `Conn.RemoteAddr()`). It can be extracted in `http.Server.ConnContext`:

```go
srv := &http.Server{
    ConnContext: func(ctx context.Context, c net.Conn) context.Context {
        if tc, ok := c.(*tls.Conn); ok {
            if fc, ok := tc.NetConn().(*ipn.FunnelConn); ok {
                ctx = context.WithValue(ctx, srcKey{}, fc.Src)
            }
        }
        return ctx
    },
    Handler: mux,
}
```

A middleware can then check the source against a CIDR set built from AWS's published `ip-ranges.json`, refreshed periodically.

**Decision: implement the plumbing, ship it disabled by default, gate it on config.** AWS does not formally guarantee that STS calls OIDC discovery from a published IP range, so fail-closed allowlisting risks a hard outage on AWS infrastructure changes. The discovery document and JWKS are public information by design, so the security benefit is mostly nuisance reduction (scanners, fingerprinting, log noise) rather than confidentiality. The plumbing is cheap; default-on is not.

## Build, release, and supply chain

The build/release pipeline targets **SLSA Build Level 3** for both the binary and the container image, with cosign keyless signing, syft-generated SBOMs, and pre-publication vulnerability scanning. The release artifacts are a multi-arch Linux container image at `ghcr.io/<owner>/<repo>` and platform binaries attached to the GitHub Release.

### B1. moonrepo for task orchestration

`moon` provides a single command surface (`moon run :lint`, `moon run :test`, `moon run :build`) that is identical locally and in CI. CI invokes `moon ci`, which detects affected projects from the git diff and runs only the necessary tasks. For a single-binary repo, moon's affected-detection benefit is small, but the unified local/CI surface and the input/output-driven caching are still worth the small amount of configuration.

The workspace defines task inheritance via tags (mirroring the catalyst-platform pattern). All third-party tooling is invoked through moon tasks so that adding a new check (e.g., a second linter) is one config change rather than a workflow edit.

### B2. release-please for version management

A single `release-please-config.json` with `release-type: simple` and a single root package. Tags are formatted `vX.Y.Z` (no component prefix; this is a single-package repo). Conventional Commits drive version bumps (`feat:` → minor, `fix:` → patch, `feat!:` or `BREAKING CHANGE:` → major).

The release-please workflow runs on push to `master` and maintains a release PR. When the PR merges, release-please creates the tag, which triggers the release workflow.

### B3. GoReleaser for build and publish

GoReleaser handles the actual build and publish of both the binaries and the container image:

- Multi-platform binaries (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) with `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w -buildid="` for reproducibility.
- Multi-arch container image built from a distroless static base, pushed to GHCR with both per-arch tags and a manifest list.
- SBOMs generated by syft via the `sboms:` block, attached to the GitHub Release.
- Cosign keyless signing of `checksums.txt` (`signs:` block) and of each container image (`docker_signs:` block) using ambient OIDC from GitHub Actions.
- Emits `dist/checksums.txt` and `dist/digests.txt` for downstream attestation.

GoReleaser runs inside the reusable release workflow's build/publish jobs. Provenance attestations are generated in sibling jobs in that same reusable workflow, which is then invoked by a thin caller workflow on tag push.

### B4. SLSA Build Level 3 via isolated reusable workflow

SLSA BL3 requires the build to run via a reusable workflow and the attestations to be generated from that reusable workflow context. The canonical 2026 path is:

```
.github/workflows/
  release.yml            # caller — runs on tag push and delegates
  reusable-release.yml   # reusable (workflow_call) — build, publish, and attest
  release-rehearsal.yml  # trusted manual caller — synthetic version full-dress rehearsal
```

- **`release.yml` `release` job**: invokes `./.github/workflows/reusable-release.yml` with `contents: write`, `packages: write`, `id-token: write`, `attestations: write`, and `artifact-metadata: write`.
- **`release-rehearsal.yml` `rehearsal` job**: computes a synthetic SemVer such as `0.0.0-rc.<run>.<sha>`, invokes the same reusable release workflow, publishes/signs/attests the image and chart to GHCR under throwaway tags, and skips GitHub Release creation/assets. This is the trusted dress rehearsal path before cutting a real tag.
- **`reusable-release.yml` `scan-local` job**: checkout, setup-go, qemu/buildx, syft, `goreleaser release --clean --skip=announce,publish,sign`, then Trivy scans the locally built `-amd64` and `-arm64` image refs and fails on `CRITICAL,HIGH`.
- **`reusable-release.yml` `publish` job**: reruns `goreleaser release --clean`, signs `checksums.txt` and the published image via GoReleaser/cosign, packages/pushes/signs the Helm chart, generates `dist/checksums.txt` and `dist/digests.txt`, creates an image SBOM with syft, uploads that SBOM to the GitHub Release, and attaches it to GHCR as an OCI referrer.
- **`reusable-release.yml` `attest-binaries` job**: runs `actions/attest@v4` against `dist/checksums.txt`.
- **`reusable-release.yml` `attest-oci` job**: runs `actions/attest@v4` with `subject-name`/`subject-digest` for the published image and chart, pushing the provenance attestations to the registry.

This matches GitHub's current BL3 guidance for reusable workflows and artifact attestations: the caller workflow is thin, the reusable workflow is the trusted build identity, and verification points at that reusable workflow as the signer.

Consumers verify with:
```
gh attestation verify --owner <owner> \
  --signer-workflow <owner>/<repo>/.github/workflows/reusable-release.yml ./binary
gh attestation verify --owner <owner> oci://ghcr.io/<owner>/<repo>:<tag>
```

### B5. Cosign keyless signing

`sigstore/cosign-installer@v4.1.1` installs cosign v3.0.5. All signing is keyless via ambient OIDC from GitHub Actions — no long-lived keys anywhere. GoReleaser invokes cosign through its `signs:` and `docker_signs:` blocks; cosign v3 requires `--yes` and `--bundle` for `sign-blob`.

Cosign signatures are complementary to the SLSA attestations — signatures prove provenance from this specific GitHub Actions identity, attestations prove the build process. Both can be verified independently.

```
cosign verify \
  --certificate-identity-regexp "^https://github.com/<owner>/<repo>/.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/<owner>/<repo>:<tag>
```

### B6. SBOM generation via syft

`anchore/sbom-action/download-syft@v0` installs syft. GoReleaser's `sboms:` block runs syft against each release archive and attaches those SBOM files (SPDX JSON) to the GitHub Release. The reusable release workflow also runs syft against the published image digest, uploads that image SBOM to the GitHub Release, and attaches it to GHCR as an OCI referrer.

### B7. Container image scanning with Trivy

Trivy (`aquasecurity/trivy-action`) is the 2026 default for fail-on-critical scanning. Two scan passes:

1. **Pre-push, in `reusable-release.yml` `scan-local`**: Trivy scans the locally built `:${version}-amd64` and `:${version}-arm64` images with `exit-code: 1, severity: CRITICAL,HIGH`. The release fails before the publish job runs if a CRITICAL or HIGH CVE is detected. Staying on OSS GoReleaser means doing this as a two-pass local-build-then-publish flow instead of Pro `prepare/continue`, but the security gate is the same.
2. **Scheduled post-push rescan**: a separate workflow runs Trivy against the latest published tag on a daily schedule and opens a GitHub issue if newly disclosed CVEs affect already-released images. This catches the gap between "clean at release time" and "vulnerability disclosed an hour later."

### B8. Static analysis and vulnerability scanning in CI

CI runs the following on every push and PR, all as moon tasks:

- **`golangci-lint` v2** (`golangci/golangci-lint-action@v7`): comprehensive Go linting. Enabled linters for a security-sensitive minimal service: `errcheck, govet, staticcheck, gosec, gocritic, revive, unused, ineffassign, bodyclose, contextcheck, errorlint, nilerr, copyloopvar`. Formatters: `gofumpt`, `goimports`. (`staticcheck` is bundled in golangci-lint v2 — do not run it separately.)
- **`govulncheck`**: invoked directly via `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...` rather than the stale `golang/govulncheck-action`. Wrapped as a moon task. Also run on a daily schedule against `main` to catch vulnerabilities disclosed in already-merged code.
- **`go test`**: standard test pass. No special flags beyond `-race -shuffle=on`.
- **`goreleaser check`**: schema-validates the release config before the heavier release smoke test runs.
- **`goreleaser release --snapshot --clean --skip=sign`**: verifies the release config and produces unsigned local artifacts. Catches GoReleaser config errors before they hit the actual release without requiring OIDC signing in unprivileged CI.
- **Local Trivy scan**: scans the locally built snapshot `-amd64` and `-arm64` images so CVE gate failures show up in PR CI instead of only in the trusted release workflow.
- **Local image SBOM generation**: runs syft against the locally built snapshot image to catch image-SBOM regressions before the publish workflow.
- **`actionlint`**: statically validates every workflow file in PR CI so broken workflow syntax or unsupported keys do not first surface during a tag or rehearsal run.

### B9. Action SHA pinning

Every third-party GitHub Action is pinned to a full 40-character commit SHA, with the version as a trailing comment for human readability:

```yaml
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683  # v4.2.2
```

SHAs are resolved from each action's GitHub Releases page at the moment of pinning — not made up, not auto-generated. Renovate (with the `helpers:pinGitHubActionDigests` preset) handles ongoing updates and re-pins on new releases.

First-party `actions/*` are also pinned to SHAs — GitHub-owned actions are the supply chain too.

### B10. Helm chart packaged and pushed alongside the release

Consumers deploy the service via a Helm chart published as an OCI artifact to GHCR. The chart is the canonical deployment surface — `helm install`, Argo CD `targetRevision` against the OCI URL, Flux `OCIRepository`, and Crossplane all consume it identically.

**Layout**: a single `chart/` directory at the repo root. Coupled versioning: `Chart.yaml` `version` and `appVersion` both track the binary tag. One release PR, one set of artifacts per release.

**Contents**: Deployment, ServiceAccount, Role, RoleBinding, and an optional empty tsnet kubestore state Secret. The Tailscale OAuth credential Secret is always referenced, never created, by the chart. Values surface exposes only what consumers actually need to override: image repository/tag (defaulted to the release), the issuer URL, the Tailscale hostname, the secret names, source-IP allowlist settings, and pod-level security overrides. The runtime remains intentionally single-replica, so the chart does **not** expose `replicaCount`. The chart also does **not** create a Kubernetes `Service` or `Ingress`, because the public trust surface is Tailscale Funnel, not cluster networking. The chart does **not** include or manage the apiserver `--service-account-issuer` flag — that's a cluster admin responsibility (see Deferred).

**Publishing**: GoReleaser does not have native Helm support, and adding a third-party action would expand the supply chain unnecessarily. Helm 3.8+ supports OCI push directly, so a dedicated step in `release.yml` (after the goreleaser step) does:

```
helm package chart/ -d dist/
helm push dist/<chart>-<version>.tgz oci://ghcr.io/<owner>
```

The published chart lives at `ghcr.io/<owner>/<chart>`. Consumers install with:

```
helm install <name> oci://ghcr.io/<owner>/<chart> --version <version>
```

**Signing and attestation**: the chart is a first-class OCI artifact and goes through the same supply-chain treatment as the binary and the container image:

- **Cosign signature** with the same keyless OIDC identity, in the same release job: `cosign sign --yes ghcr.io/<owner>/<chart>@<digest>`.
- **SLSA BL3 provenance** via the same `reusable-release.yml` workflow — the chart digest is attested with `actions/attest@v4` using `subject-name`/`subject-digest` and pushed to the registry as provenance.

Verification is symmetric with the other artifacts:

```
gh attestation verify --owner <owner> oci://ghcr.io/<owner>/<chart>:<version>
cosign verify \
  --certificate-identity-regexp "^https://github.com/<owner>/<repo>/.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/<owner>/<chart>:<version>
```

The chart is also lint-checked in CI as a moon task using `helm lint chart/` plus `helm template chart/ | kubeconform -strict -summary` to catch schema breakage before release.

### Workflow permissions

Every workflow uses **per-job minimum permissions**, never workflow-level grants. Defaults at the top of each workflow are `permissions: {}` (deny all), and each job opts back in to only what it needs:

| Job | `contents` | `pull-requests` | `packages` | `id-token` | `attestations` | `artifact-metadata` |
|---|---|---|---|---|---|---|
| `ci.yml` (lint/test/build) | read | — | — | — | — | — |
| `release-please.yml` | write | write | — | — | — | — |
| `release.yml` caller | write | — | write | write | write | write |
| `reusable-release.yml` `scan-local` | read | — | — | — | — | — |
| `reusable-release.yml` `publish` | write | — | write | write | — | — |
| `reusable-release.yml` `attest-binaries` | read | — | — | write | write | write |
| `reusable-release.yml` `attest-oci` | read | — | write | write | write | write |

`id-token: write`, `attestations: write`, and `artifact-metadata: write` are granted only on the caller and the attestation jobs, matching GitHub's current artifact-attestation permissions model for reusable workflows.

## Threat model summary

In scope (mitigated by this design):

**Runtime:**
- Upstream schema drift or apiserver returning unexpected discovery doc fields → A4 (constants).
- Malformed JWKS, unknown-field injection → A5 (parser allowlist).
- Upstream apiserver outage → A6 (fail-stale cache).
- Slowloris and basic HTTP attacks → A7 (timeouts, small headers).
- Internet noise / opportunistic scanners → optional source-IP allowlist.

**Supply chain:**
- Tampered binary, image, or chart in transit → B5/B10 (cosign signature verification).
- Compromised build runner injecting backdoor → B4 (SLSA BL3 provenance from isolated builder, covering binaries, image, and chart).
- Vulnerable Go dependencies → B8 (govulncheck in CI + scheduled).
- Vulnerable base image at release time → B7 (Trivy pre-push, fail on CRITICAL/HIGH).
- Vulnerable base image after release → B7 (scheduled rescan).
- Compromised third-party action → B9 (SHA pinning, Renovate).
- Broken chart schema reaching consumers → B10 (helm lint + kubeconform in CI).
- Unauthorized release → release-please + branch protection + required reviews on `main`.

Out of scope (must be mitigated elsewhere):
- Tailnet admin compromise → tailnet hygiene + IAM least privilege.
- Tailscale infrastructure compromise → trust assumption inherent to the architecture.
- Let's Encrypt mis-issuance → CT monitoring.
- kube-apiserver compromise / malicious key injection → cluster security.
- Compromise of our pod's state Secret → k8s RBAC, etcd encryption-at-rest, audit logging.
- GitHub.com compromise → trust assumption inherent to using GitHub Actions and GHCR.

## Deferred

- File and module layout
- RBAC scope details (chart manifests will define these, but the exact verbs and resource names are deferred)
- Configuration surface (env vars, flags, defaults)
- Observability (metrics, logging, health checks)
- Bootstrap procedure for the apiserver `--service-account-issuer` flag rollover (admin runbook, not chart content)
- Specific moon workspace and task definitions
- Specific `.goreleaser.yaml` and Dockerfile (distroless base) contents
- Specific `release-please-config.json` and `.release-please-manifest.json` contents
- Specific `chart/` contents (`Chart.yaml`, `values.yaml`, templates) and the `values.schema.json` if we ship one
- Renovate configuration for SHA pinning maintenance
