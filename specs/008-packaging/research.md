# Phase 0 Research: Provider Packaging & Remote-Cluster e2e Delivery

**Feature**: `008-packaging` | **Date**: 2026-06-17 | **Plan**: [plan.md](./plan.md)

Resolves the six open clarifications (R-1..R-6) from `plan.md`. Each decision is
grounded in the official Crossplane docs (cited inline) and cross-checked against
the repo's current tooling (`Makefile`, `Dockerfile`, `package/crossplane.yaml`,
`test/e2e/`) and project memory. Where the truth can only be established against
a live system (CRaaS push auth, WAF on the registry host), the item is marked
**VERIFY-LIVE** with an explicit impl-time check.

## Sources (authoritative)

- Providers (install model, `spec.package`, `packagePullSecrets`,
  `runtimeConfigRef`): <https://docs.crossplane.io/latest/packages/providers/>
- CLI command reference (`crossplane xpkg build` / `push`):
  <https://docs.crossplane.io/latest/cli/command-reference/>
- Image Configs (private-registry pull auth via `ImageConfig`; cosign signature
  verification): <https://docs.crossplane.io/latest/packages/image-configs/>
- xpkg specification (the `.xpkg` IS an OCI image; meta + CRD layers):
  <https://github.com/crossplane/crossplane/blob/main/contributing/specifications/xpkg.md>
- Project memory: `project_xpkg_allowed_kinds`, `project_crossplane_v2_conventions`
  (the `spec.package` registry-must-contain-a-dot regex),
  `project_crossplane_provider_repull_annotation_bump`,
  `feedback_always_check_live_api_orphans`, `feedback_pin_kubectl_context_for_e2e`.
- Repo doc fact: CRaaS host shape `<registry-name>.cr.twcstorage.ru`
  (`docs/presets.md:109`).

---

## R-1 — Packaging mechanics: how `.xpkg` relates to the controller image, and the build→push→install chain

**Decision.** Treat the release as **two correlated OCI artifacts** in the CRaaS
registry plus a thin install manifest:

1. **Controller runtime image** — the multi-arch (`linux/amd64,linux/arm64`)
   image built by the `Dockerfile` (today `make image` pushes it to ghcr). This
   is the container the provider pod actually runs.
2. **Provider package (`.xpkg`)** — itself an OCI image, built by
   `crossplane xpkg build` from `package/` (`crossplane.yaml` meta + `crds/`).
   It carries the package **metadata** and the **CRDs**, and it records the
   controller image in `spec.controller.image` of `package/crossplane.yaml`.
   The `.xpkg` is what a cluster installs; the package manager reads its
   `spec.controller.image` to know which runtime image to deploy.

The chain is: **build image → push image → build `.xpkg` (referencing that image)
→ push `.xpkg` → `Provider.spec.package = <xpkg ref>`**. On install the Crossplane
package manager pulls the `.xpkg`, creates a `ProviderRevision`, and stands up a
Deployment running the `spec.controller.image` runtime image. `Provider.spec.package`
points at the **`.xpkg`**, never at the runtime image.

`DeploymentRuntimeConfig` (`pkg.crossplane.io/v1beta1`, referenced from
`Provider.spec.runtimeConfigRef`) is the override hook for the generated runtime
Deployment — pod template, container args, service account, and (relevant here)
pull-secret plumbing. The package manager names the runtime container
`package-runtime`
([providers docs](https://docs.crossplane.io/latest/packages/providers/)). For
008 we do **not** need a `DeploymentRuntimeConfig` for the *pull-secret* path
(see R-3) — `packagePullSecrets` already covers it — so a runtime config is
optional and only introduced if a future runtime tweak needs it.

**Two ways to bind the runtime image to the package** (both are first-class):
- **Reference** — keep `spec.controller.image: <CRaaS>/provider-timeweb:<VERSION>`
  in `package/crossplane.yaml`; push the image separately. (Current repo style for
  ghcr; the value is hard-coded `v0.1.0` today and must be templated to `VERSION`
  + the CRaaS host.)
- **Embed** — `crossplane xpkg build --embed-runtime-image=<image>` bakes the
  runtime image into the `.xpkg`. The repo's e2e `deploy.sh` already uses
  `--embed-runtime-image` against the k3d local registry.

For 008 we standardize on the **reference** model for the published CRaaS release
(image and package are independent, digest-pinnable artifacts; matches `make image`
+ `make release`), and keep `--embed-runtime-image` only as the k3d-local
convenience it already is.

**Rationale.** The xpkg spec defines the package as an OCI image whose layers hold
meta + CRDs; the runtime image is a *separate* image the package merely points to.
Keeping them as two references (not one embedded blob) preserves independent
multi-arch image publishing (`docker buildx`), independent cosign signing of each,
and digest pinning of each — and mirrors how upstream contrib providers ship.

**Alternatives considered.**
- *Embed-runtime-image for the published release* — simpler single artifact, but
  loses independent image signing/pinning and forces an `.xpkg` rebuild for any
  runtime-only change; rejected for the published path, retained for k3d-local.
- *Single OCI image doing both* — not how Crossplane models it; rejected.

---

## R-2 — Publishing to Timeweb CRaaS with `crossplane xpkg push`

**Decision.** Publish both artifacts to CRaaS:

- Image: `docker buildx build --platform linux/amd64,linux/arm64 --push --tag <ref>`
  (already what `make image` does; only `IMAGE_REPO` changes to the CRaaS host).
- Package: `crossplane xpkg push -f <file>.xpkg <ref>` — push takes a fully
  qualified OCI tag (`registry/repo:tag`) and authenticates via the **local Docker
  credential store**, so a prior `docker login <CRaaS-host>` is the auth step
  ([CLI reference](https://docs.crossplane.io/latest/cli/command-reference/)).

CRaaS is an OCI/Docker-v2 registry, so both `docker push` and `crossplane xpkg push`
work against it unchanged. The **CRaaS reference shape** is
`<registry-name>.cr.twcstorage.ru/<repo>:<tag>` — host `cr.` per the repo's own
doc fix (`docs/presets.md:109`, `<registry-name>.cr.twcstorage.ru`). Concretely:

```
IMAGE_REPO ?= <registry-name>.cr.twcstorage.ru/provider-timeweb        # runtime image
XPKG_REPO  ?= <registry-name>.cr.twcstorage.ru/provider-timeweb-xpkg   # .xpkg package
```

The host **contains a dot** so it satisfies the Crossplane v2 `spec.package`
validator regex (`project_crossplane_v2_conventions`) — no workaround needed
(unlike the k3d `e2e.localhost` dance).

**VERIFY-LIVE.** (a) Exact CRaaS host string for the owner's registry (the
`<registry-name>` prefix and whether the repo path is free-form or registry-fixed)
— confirm in the Timeweb dashboard at impl. (b) `docker login` credential form
(username = registry login vs token; password = registry password/token) — probe
with a throwaway `docker push` of a tiny image first. (c) Whether CRaaS enforces
a fixed single-repo namespace per registry (some managed registries do), which
would change `XPKG_REPO`/`IMAGE_REPO` to share one repo with distinct tags.

**Rationale.** `crossplane xpkg push` is explicitly an OCI push that reuses Docker
creds; CRaaS is a standard OCI registry; the dot-bearing host already satisfies
the install-time validator. No invented tooling
(`feedback_use_standard_ecosystem_tools`).

**Alternatives considered.**
- *`oras`/`crane` to push the `.xpkg`* — works (it's just an OCI artifact) but
  redundant; `crossplane xpkg push` is the standard, already a repo dependency.
  Rejected.
- *ghcr only* — fails the in-network-pull requirement (FR-011); the staging
  cluster must pull from CRaaS in-network. Rejected.

---

## R-3 — Pull-secret wiring for a PRIVATE CRaaS install

**Decision.** Use a single `kubernetes.io/dockerconfigjson` Secret in the
`crossplane-system` namespace and reference it from `Provider.spec.packagePullSecrets`.
This one field covers **both** pulls:

- **Package pull** (the package manager fetching the `.xpkg`) — Crossplane uses
  `packagePullSecrets` exactly as Kubernetes uses `imagePullSecrets`, and the
  Secret **must be in the same namespace as Crossplane**
  ([providers docs](https://docs.crossplane.io/latest/packages/providers/)).
- **Runtime image pull** (the controller Deployment pulling
  `spec.controller.image`) — the package manager **"adds pull secrets provided in
  the Package spec (`spec.packagePullSecrets`) as image pull secrets"** on the
  generated runtime Deployment
  ([providers docs](https://docs.crossplane.io/latest/packages/providers/)). So a
  separate `DeploymentRuntimeConfig.imagePullSecrets` is **not required** when the
  image and package live in the same private registry behind the same credential.

So the minimal private-install wiring is:

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  package: <CRaaS-host>/provider-timeweb-xpkg:<VERSION>   # or @sha256:<digest>
  packagePullSecrets:
    - name: craas-pull        # dockerconfigjson Secret in crossplane-system
  packagePullPolicy: IfNotPresent
  revisionActivationPolicy: Automatic
  revisionHistoryLimit: 1
```

**Fleet-wide alternative (no per-Provider edit): `ImageConfig`.** A cluster-scoped
`ImageConfig` (`pkg.crossplane.io/v1beta1`) attaches a pull Secret to every image
matching a prefix — useful so the credential is declared once for the whole CRaaS
host ([image-configs docs](https://docs.crossplane.io/latest/packages/image-configs/)):

```yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: ImageConfig
metadata:
  name: craas-pull
spec:
  matchImages:
    - type: Prefix
      prefix: <CRaaS-host>/
  registry:
    authentication:
      pullSecretRef:
        name: craas-pull        # in crossplane-system
```

For 008 the **`packagePullSecrets` on the Provider** is the primary documented
path (explicit, local to the install manifest); `ImageConfig` is offered in
`provider-install.md` as the fleet-wide equivalent. Both manifests live under
`deploy/` (NOT `package/` — `project_xpkg_allowed_kinds`).

**Rationale.** One credential, one Secret, one namespace, both pulls covered —
the docs are explicit that `packagePullSecrets` becomes the runtime image-pull
secret too, so we avoid an unnecessary `DeploymentRuntimeConfig`.

**Alternatives considered.**
- *`DeploymentRuntimeConfig.imagePullSecrets` for the runtime image* — redundant
  given the package-manager auto-propagation; only needed if image and package are
  in *different* private registries. Documented as a fallback, not the default.
- *Baking creds into the image* — forbidden (Provider Constraints: creds only via
  `ProviderConfig`→`Secret`; never in images). Rejected.

---

## R-4 — CRaaS bootstrap (chicken-and-egg) + publish-origin reachability

**Decision (bootstrap).** The target CRaaS registry is a **one-time out-of-band
prerequisite**, created in the Timeweb dashboard (or via the existing
`ContainerRegistry` MR on a *different* already-running provider install) **before**
008 publishes. The provider package cannot be hosted by the very provider being
published. Treat "CRaaS registry exists + push creds + a `dockerconfigjson` pull
Secret" as a documented precondition (quickstart step 0), not an automated step.

**Decision (publish origin).** Publishing originates from whichever network can
reach the CRaaS **push** endpoint. The dev machine's WAF block is specific to
`api.timeweb.cloud` (the Qrator-fronted control-plane API); the **CRaaS registry
host is a different host** (`*.cr.twcstorage.ru`, an OCI registry, not the API), so
it is **plausibly reachable** from the dev machine even while the API is blocked.

**VERIFY-LIVE.** Confirm at impl whether `*.cr.twcstorage.ru` is reachable from
the dev network (a `docker login` + tiny `docker push` probe). **If blocked**, fall
back to publishing from **inside Timeweb** (a jump host / the staging node) or from
**CI** with CRaaS creds — the publish path must therefore be a self-contained
script/`make` target runnable from any of those origins (no dependency on local-only
state). This is exactly the spec's "publish-origin reachability" edge case.

**Rationale.** Separates the *control-plane API* WAF block (known, affects
reconciliation → solved by running the provider in-cluster) from *registry*
reachability (unknown for the dev host → must be probed). The bootstrap ordering
is a hard logical constraint, not a tooling gap.

**Alternatives considered.**
- *Provision CRaaS via this provider during publish* — impossible (the provider
  isn't installed yet); rejected.
- *Mirror through ghcr then copy in* — adds a hop and still needs in-network pull;
  rejected in favor of publishing straight to CRaaS (with the CI/in-Timeweb origin
  fallback).

---

## R-5 — e2e harness: arbitrary explicit context + pull-install + isolation/cleanup

**Decision.** Four targeted changes to `test/e2e/`, all plumbing-only (bundles
unchanged, FR-010):

1. **Drop the `k3d-`-only guard, add an explicit-context guard.** Replace the
   hard `[[ "$E2E_KUBECONTEXT" != k3d-* ]]` reject (`kuttl.sh:49`) with: *require*
   `E2E_KUBECONTEXT` to be set non-empty (it already is, via `:?` at line 41) AND
   require it be passed explicitly (no ambient-default derivation). Keep the
   "context exists locally" check (`kuttl.sh:64`) and the minified single-context
   kubeconfig (`kuttl.sh:491`). **Remove the local-API-server URL assertion**
   (`kuttl.sh:507`, the `127.0.0.1`/`localhost` case) — it is k3d-specific and
   would reject `twc-staging`. The wrong-cluster safety is preserved by (a) the
   mandatory explicit context, (b) the minified kubeconfig that can reach *only*
   that one cluster, and (c) the `current-context == E2E_KUBECONTEXT` assertion.
   (`feedback_pin_kubectl_context_for_e2e`.)

2. **Pull-install the published provider instead of side-loading.** Today
   `deploy.sh` does docker build → push-to-local-registry → `--embed-runtime-image`
   → install. For a remote cluster, replace this with: apply the CRaaS
   `dockerconfigjson` pull Secret + the `Provider` CR pointing at the **published**
   `Provider.spec.package` (CRaaS ref at a configurable `E2E_PACKAGE`/`E2E_VERSION`)
   + `packagePullSecrets`, then `kubectl wait --for=condition=Healthy`. No local
   image build, no local registry. Add the **annotation bump**
   (`project_crossplane_provider_repull_annotation_bump`) so a mutable-tag re-push
   re-resolves; digest-pinned refs avoid it but break the rebuild-same-tag loop.

3. **Namespace isolation (unchanged) + interrupt-safe cleanup (extend).** Keep the
   dedicated `timeweb-e2e` namespace + minified kubeconfig. The existing SIGHUP/
   SIGINT/SIGTERM traps and `cleanup_mrs`/`report_orphans` already handle
   interrupt-safe teardown on a shared cluster — they are cluster-agnostic and
   carry over as-is.

4. **Live-API orphan sweep (extend reporting → action).** `report_orphans` today
   only lists in-cluster MRs. Per `feedback_always_check_live_api_orphans`, add a
   post-run **live Timeweb API** sweep (`GET /api/v1/routers`, `/api/v2/vpcs`,
   `/api/v1/k8s/clusters`, `/api/v1/floating-ips`) diffed against a pre-run
   baseline, flagging `e2e-*`/probe-named unattached resources for confirm-then-
   delete. On the shared `twc-staging` account this is mandatory, not advisory.

**Rationale.** The k3d-only guard exists purely to prevent accidental prod
applies; the *same* safety is met by explicit-context + minified single-context
kubeconfig + current-context assertion, which travel to any cluster. Pull-install
is the only way to validate the *published* artifact (US2). The shared-account
orphan sweep is a standing project rule.

**Alternatives considered.**
- *Keep side-load against twc-staging* — would validate a locally-built image, not
  the published package; defeats SC-002. Rejected.
- *Rename twc-staging to `k3d-…`* (the old escape hatch in `kuttl.sh:56`) — abusive
  and fragile; rejected in favor of a real explicit-context guard.
- *New per-run namespace* — `timeweb-e2e` is already isolated and asserted; a
  random namespace adds churn for no isolation gain on a single-tenant suite.
  Rejected (keep `timeweb-e2e`).

---

## R-6 — Integrity: cosign sign/verify of the PUSHED PACKAGE + digest pinning + declared compat

**Decision.** Extend signing from the image (today `make release` runs
`cosign sign --yes $(IMAGE_REPO):$(VERSION)`) to **both** OCI artifacts —
the controller image AND the pushed `.xpkg` — since the `.xpkg` is itself an OCI
image and `cosign sign <xpkg-ref>` signs it the same way. Verification on the
target cluster is declarative via a Crossplane **`ImageConfig` with
`spec.verification.provider: Cosign`** matching the CRaaS prefix, so the package
manager refuses to install an unsigned/incorrectly-signed package
([image-configs docs](https://docs.crossplane.io/latest/packages/image-configs/)):

```yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: ImageConfig
metadata:
  name: verify-timeweb
spec:
  matchImages:
    - type: Prefix
      prefix: <CRaaS-host>/provider-timeweb
  verification:
    provider: Cosign
    cosign:
      authorities:
        - name: verify timeweb provider
          keyless:                     # OR key.secretRef for a static key
            identities:
              - issuer: https://token.actions.githubusercontent.com
                subject: <publish-workflow-or-identity>
```

**Digest pinning.** Publish by tag for humans but **pin `Provider.spec.package`
to `@sha256:<digest>`** for reproducible installs (SC-005); record the digest as a
publish output. The repull-annotation quirk
(`project_crossplane_provider_repull_annotation_bump`) only bites mutable tags, so
digest pinning sidesteps it (at the cost of the rebuild-same-tag loop — fine for a
release, kept off the k3d iteration path).

**Declared compatibility.** `package/crossplane.yaml` already declares
`spec.crossplane.version: ">=v2.0.0-0"`; staging runs Crossplane **2.3.2**, which
satisfies it. Keep the constraint (do not over-pin); validate at install that the
Provider reaches `Healthy` on 2.3.2 (the live compat proof). crossplane-runtime
compat is transitive via the provider's `go.mod`; no separate declaration field is
required by the package format.

**VERIFY-LIVE.** (a) The exact cosign identity/issuer to assert (keyless via CI
OIDC vs a static cosign key) depends on where publishing runs (R-4) — pick at impl
once the publish origin is fixed. (b) Whether CRaaS stores cosign signature tags
(`sha256-<digest>.sig`) without issue — most OCI registries do; confirm on first
sign+verify.

**Rationale.** `cosign` already in the toolchain; signing the `.xpkg` is the same
operation on a different OCI ref; `ImageConfig` is the documented in-cluster gate
that turns the signature into an *enforced* install precondition (FR-007/SC-005).

**Alternatives considered.**
- *Sign only the image* (status quo) — leaves the installed artifact (the `.xpkg`)
  unverified; fails FR-007 which is about the *published package*. Rejected.
- *Manual `cosign verify` in docs only* — not enforced at install; weaker than an
  `ImageConfig` gate. Kept as an operator spot-check, not the mechanism.
</content>

---

## R-7 — Public `ghcr.io` publish + GitHub Release (FR-012)

**Decision.** Publish the public release to **`ghcr.io/lebedevdsl/provider-timeweb`**
(public, no pull secret) via the existing `IMAGE_REPO`-parameterized flow, and cut
a **GitHub Release** at the version git tag with the built `.xpkg` attached as a
release asset. CRaaS stays test/e2e-only.

- **ghcr auth:** `docker login ghcr.io -u <user> -p <GITHUB_TOKEN|PAT>` (the PAT
  needs `write:packages`); `crossplane xpkg push` then pushes to the ghcr ref.
- **Signing:** prefer **cosign keyless (OIDC)** when publishing from a **GitHub
  Actions** workflow (`id-token: write`) — the signature's identity is the
  workflow, giving public provenance without a managed key. A local key still
  works for manual pushes but is weaker for a public artifact.
- **GitHub Release:** `gh release create <tag> bin/provider-<tag>-*.xpkg --notes …`
  (or the `softprops/action-gh-release` action) attaches the `.xpkg`.
- **Where it runs:** a **GH Actions release workflow on tag push** is the natural
  home — it gives keyless signing + provenance + runs from a non-WAF, clean
  network. Local `gh`/`docker login` is the fallback.

**Rationale.** Matches crossplane-contrib public-release convention (signed public
OCI + GitHub Release); reuses 008's build; keeps the private CRaaS path intact.

**Alternatives.** Upbound Marketplace listing — deferred (out of scope). A local
cosign key for the public artifact — works but no OIDC provenance; rejected as the
primary mechanism.

## R-8 — True multi-arch for the public image (FR-012)

**Decision.** For the **public** release, build `linux/amd64,linux/arm64` as one
multi-arch tag. The embed build's `buildx --load` + `docker save` path is
single-platform per pass (default docker driver); a one-tag multi-arch image needs
a **`docker-container` buildx builder** (`docker buildx create --driver
docker-container --use --bootstrap`) or the containerd image store. With the
container driver, build a per-arch runtime, embed each into a per-arch `.xpkg`, and
`crossplane xpkg push --package-files=<amd64>.xpkg,<arm64>.xpkg <ref>` to publish
the multi-platform index.

**Rationale.** A public provider must run on both amd64 and arm64 clusters; one tag
keeps install simple. The container-driver step is a one-time CI/runner setup.

**Alternatives.** amd64-only public image — rejected (excludes arm64 clusters). Two
separate tags — rejected (a single multi-arch ref is the norm and what
`Provider.spec.package` expects).
