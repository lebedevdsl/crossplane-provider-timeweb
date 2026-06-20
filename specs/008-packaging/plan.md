# Implementation Plan: Provider Packaging & Remote-Cluster e2e Delivery

**Branch**: `008-packaging` | **Date**: 2026-06-17 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/008-packaging/spec.md`

## Summary

Make the provider **installable from a published package** and let the e2e suite
run against an **operator-provided cluster context** (the Timeweb `twc-staging`
cluster), so live validation works from inside Timeweb where the Qrator WAF does
not block the Timeweb API. Two concrete gaps close it: (1) the tooling builds a
local `.xpkg` but never **pushes** it — add a publish path to a **Timeweb CRaaS**
registry (package + multi-arch controller image), pullable in-network; (2) the
e2e harness **hard-rejects any non-`k3d-` context** and **side-loads** a local
image — generalize it to an explicit arbitrary context and **install the
published package by pull** (with a CRaaS pull secret), keeping namespace
isolation + orphan sweep. No managed-resource behavior, CRD, or condition change
(FR-010): packaging/delivery only.

**Public release (FR-012, new in clarify session 2026-06-18):** a third goal —
publish a **public release on GitHub**: signed, multi-arch package + image to
**public `ghcr.io`**, plus a **GitHub Release** at the version tag (notes + the
`.xpkg` attached as an asset). CRaaS is now **test/e2e-only**, not a public
target; the `IMAGE_REPO` parameter selects between the public `ghcr.io` release
and the private CRaaS test publish. The public release is **gated** (SC-007):
it MUST NOT be cut until the **full e2e suite — every MR kind incl. the billable
Server/K8s/Nodepool/Router — runs green on a live cluster**.

**As-built note** (impl underway): the runtime is delivered via the **embed
model** (one multi-arch `.xpkg` bakes the controller in; `Provider.spec.package`
IS the runtime), built with `buildx --load` + `docker save` + embed. The live
e2e on `twc-staging` already found + fixed two real regressions — the S3
Ready-gating (`created` not `active`) and a leader-election crash on the flaky
apiserver (disabled via `deploy/deploymentruntimeconfig.yaml`) — which is exactly
the validation the SC-007 gate institutionalizes.

## Technical Context

**Language/Version**: Go (provider unchanged; Go 1.26.4). This feature is
build/release tooling + e2e harness, not controller code.

**Primary Dependencies**: the Crossplane CLI (`crossplane xpkg build`/`push`),
Docker buildx (multi-arch image), `cosign` (signing — already wired for the
image), the existing `Makefile` targets (`image`, `xpkg.build`, `release`),
the kuttl/k3d e2e harness, and a **Timeweb Container Registry (CRaaS)** as the
OCI publish/pull target.

**Storage**: N/A — artifacts live as OCI objects in the CRaaS registry.

**Testing**: existing §III unit tests unchanged (no `external` methods touched);
the e2e suite (kuttl) is extended to run against `twc-staging` using the
published package. No live-HTTP in unit tests (Constitution §III) is unaffected.

**Target Platform**: Linux provider pod, **multi-arch (amd64 + arm64)**; the
staging cluster is k0s (k8s v1.35.4) with **Crossplane 2.3.2** (installed by the
owner, out of scope); CRaaS OCI registry.

**Project Type**: Crossplane provider — packaging/delivery + e2e harness extension.

**Performance/Constraints**: backward-compatible (no CRD/MR change, FR-010);
the package + image MUST be pullable **in-network** from the staging cluster;
the e2e MUST require an **explicit** context (no ambient default) and isolate +
orphan-sweep on the shared cluster.

**Scale/Scope**: `Makefile` (add an `xpkg.push` + a single publish/`release`
flow to CRaaS), `Dockerfile`/image (confirm multi-arch + correct package↔image
linkage), `package/crossplane.yaml` (+ any DeploymentRuntimeConfig for pull
secrets / runtime version), `test/e2e/` (context generalization + pull-install +
cleanup on a real cluster), `docs/` (install + remote-e2e runbook), CI (optional
publish path). No `apis/` changes.

### Open clarifications (NEEDS CLARIFICATION → Phase 0 research)

- **R-1 (US1)**: Authoritative Crossplane packaging mechanics — `xpkg build` vs
  `push`, how the **controller runtime image** relates to the `.xpkg` package,
  how `Provider.spec.package` + `runtimeConfigRef`/DeploymentRuntimeConfig drive
  the install. (Consult the official Crossplane docs.)
- **R-2 (US1)**: Can `crossplane xpkg push` publish to a **Timeweb CRaaS**
  registry (OCI/Docker-v2 compatibility), and what auth does push need? What is
  the CRaaS image reference shape (host/path/tag)?
- **R-3 (US1/US2)**: **Pull-secret wiring** — how the staging cluster pulls a
  *private* CRaaS package + controller image (package-pull secret vs image-pull
  secret; where each is referenced for a Crossplane Provider install).
- **R-4 (US1)**: **CRaaS bootstrap** (chicken-and-egg — the registry can't be
  created by the provider being published) and **publish-origin reachability**
  (can the dev machine reach the CRaaS push endpoint, or must publishing run from
  CI / inside Timeweb? Is the CRaaS endpoint behind the same WAF as
  `api.timeweb.cloud`?).
- **R-5 (US2)**: e2e harness changes — remove the `k3d-`-only guard while keeping
  an explicit-context safety check; switch install from local side-load to
  pull-the-published-package; namespace isolation + interrupt-safe cleanup +
  live-API orphan sweep on a shared cluster.
- **R-6 (US3)**: package integrity — `cosign` sign/verify of the **pushed
  package** (not just the image), digest pinning, and the declared
  Crossplane/crossplane-runtime compatibility for the release.
- **R-7 (FR-012, public release)**: the public **`ghcr.io`** publish + **GitHub
  Release** mechanics — ghcr auth (GITHUB_TOKEN/PAT), `crossplane xpkg push` to
  `ghcr.io/lebedevdsl/provider-timeweb`, `gh release create` with the `.xpkg`
  asset, and the cosign **keyless/OIDC identity** for a public artifact (vs the
  local key used for CRaaS). Where does publishing run — local `gh`/`docker
  login` or a GitHub Actions release workflow (the natural home for OIDC
  keyless signing + provenance)?
- **R-8 (FR-012, multi-arch)**: the embed build currently uses `buildx --load` +
  `docker save` (single-platform per loop pass, default docker driver). A
  **public** multi-arch one-tag image needs a `docker-container` buildx builder
  (or the containerd image store) so `linux/amd64,linux/arm64` push as one index
  — confirm the builder-setup step and that embedded multi-arch `.xpkg` push
  works (per-arch `--package-files`).

## Constitution Check

*GATE: Must pass before Phase 0. Re-check after Phase 1.*

- **§I CRD Contract Stability** — no CRD/`apis` change (FR-010). **PASS.**
- **§II Idempotent Reconciliation** — no `external` client change. **PASS (N/A).**
- **§III Controller Test Discipline** — no new/changed `external` methods, so no
  new four-case units required; this feature ADDS live e2e coverage on a real
  cluster. **PASS.**
- **Provider Constraints** — Credentials still sourced only from
  `ProviderConfig`→`Secret` (the staging ProviderConfig references the token
  Secret; install docs must follow this, never bake tokens into images).
  **Runtime compatibility** — the release MUST declare the supported Crossplane /
  crossplane-runtime version (validated against staging's Crossplane 2.3.2).
  Observability unchanged. **PASS.**
- **Standard tooling** — Crossplane CLI, buildx, cosign; no invented tooling
  ([[feedback_use_standard_ecosystem_tools]]). **PASS.**
- **Dev workflow / codegen clean** — no `apis/` change → `make generate` stays
  clean; the reviewable gate is unaffected. **PASS.**
- **Release gate (FR-012/SC-007)** — the public release is gated on a green full
  e2e (all MR kinds) on a live cluster, *strengthening* the §III validation
  discipline before any public artifact ships. The release is **alpha** (`v1alpha1`
  CRDs; schema may break until `v1beta1` per §I) — stated honestly in the notes.
  **PASS / reinforced.**

No unjustified violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/008-packaging/
├── plan.md              # this file
├── spec.md              # US1–US3, FR-001..012, SC-001..007 + Clarifications (2026-06-18: public ghcr/GitHub release, gated)
├── research.md          # Phase 0 (R-1..R-8) — packaging, CRaaS, ghcr/GitHub release, multi-arch
├── data-model.md        # artifact/entity model (package, image, registry, release, pull-secret)
├── contracts/           # publish-command contract; Provider-install manifest; e2e-env contract
├── quickstart.md        # publish + install-on-twc-staging + run-remote-e2e runbook
└── tasks.md             # /speckit-tasks output
```

### Source Code (repository root) — areas touched

```text
Makefile                 # + xpkg.push (CRaaS ref); a single publish/release flow
                         #   (package + multi-arch image + sign); CRaaS IMAGE_REPO
Dockerfile               # confirm multi-arch build + package<->image linkage
package/
├── crossplane.yaml      # package meta (declared Crossplane/runtime compat)
└── (DeploymentRuntimeConfig / runtime image ref if required by R-1/R-3)
deploy/                  # (per xpkg-allowed-kinds) pull-secret + ProviderConfig
                         #   install manifests for the staging cluster (NOT in package/)
test/e2e/
├── scripts/kuttl.sh     # drop k3d-only guard -> require explicit context
├── scripts/*.sh         # install published provider by PULL (not side-load);
│                        #   cleanup/orphan-sweep on a shared real cluster
├── Makefile.test        # E2E_KUBECONTEXT param (e.g. twc-staging), package ref/version
└── kuttl/               # bundles unchanged (FR-010); only context/install plumbing
deploy/deploymentruntimeconfig.yaml  # leader-election off (as-built fix for flaky apiserver)
docs/                    # install-from-ghcr guide + remote-e2e runbook + build-from-repo (FR-008)
.github/workflows/       # release workflow on tag → public ghcr publish + cosign
                         #   keyless (OIDC) + `gh release` (.xpkg asset) (FR-012, R-7)
```

**Structure Decision**: Existing single-module layout. This feature adds a
**publish path** to the build tooling and **generalizes the e2e harness** to a
pull-install against an arbitrary context — no source/`apis` changes. Package
contents stay within the xpkg-allowed kinds (CRDs/MRDs/meta); operational
manifests (pull secret, ProviderConfig, Provider install) live under `deploy/`,
per the existing rule.

## Implementation Strategy (by user story, MVP-first)

1. **Foundational — packaging mechanics** (R-1..R-4): confirm the build->push->
   install chain against the official Crossplane docs; confirm CRaaS OCI push +
   the pull-secret model; settle the bootstrap + publish-origin question.
2. **US1 (P1)** publishable package — add `xpkg.push` to CRaaS + a single publish
   flow (package + multi-arch image), versioned/pullable; verify a fresh-cluster
   install reaches Healthy.
3. **US2 (P1)** remote e2e — parameterize the context (drop k3d guard, keep an
   explicit-context guard), pull-install the published provider on `twc-staging`,
   namespace-isolate, interrupt-safe cleanup + live-API orphan sweep; run the
   suite green.
4. **US3 (P2)** reproducible/verifiable delivery — sign/verify the pushed package
   (cosign + digest), declared compat, install + remote-e2e docs, optional CI
   publish.
5. **Public release (FR-012, gated)** — the v0.1.0-public milestone, **gated on
   SC-007** (the full e2e — incl. the billable Server/K8s/Nodepool/Router bundles
   — runs green on a live cluster first). Then: set up a `docker-container` buildx
   builder for true multi-arch (R-8); retarget `IMAGE_REPO` to public
   `ghcr.io/lebedevdsl/provider-timeweb`; push signed multi-arch package; cut a
   `gh release` at the version tag with notes + the `.xpkg` asset (R-7); ideally
   automate via a GitHub Actions release workflow on tag. The released kinds are
   ALL validated — none shipped "experimental".

**Verification posture**: per [[feedback_verify_by_reobservation]] — "published"
and "installed" are proven by re-observing the Provider reaching Healthy and the
e2e bundles going green on `twc-staging`, then sweeping live-API orphans
([[feedback_always_check_live_api_orphans]], [[feedback_pin_kubectl_context_for_e2e]]).

## Complexity Tracking

*No constitution violations to justify.*

## Out of scope

- New MR kinds or any MR/CRD/condition change.
- Provisioning the staging cluster or installing Crossplane (owner's step; done — 2.3.2).
- **Public marketplace / Upbound listing — deferred (not yet).** 008 delivers the
  provider as the standard Crossplane OCI package (`.xpkg` — itself an OCI image)
  to the private Timeweb CRaaS registry only; no separate "packaging format" is
  involved (no Helm chart / OLM bundle needed — the package already IS an OCI artifact).
- Resolving the external-network WAF block (the in-Timeweb run is the workaround).
