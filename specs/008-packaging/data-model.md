# Phase 1 Data Model: Packaging & Delivery Artifacts

**Feature**: `008-packaging` | **Date**: 2026-06-17 | **Plan**: [plan.md](./plan.md)

This feature ships **no new managed-resource kinds and no CRD/`apis` changes**
(FR-010). The "data model" here is the **artifact + install** model: the OCI
artifacts produced by the publish path, the cluster-side objects that install and
verify them, and the relationships/flow that bind a published version to a running,
reconciling provider on `twc-staging`. Entities are grouped as **Build/Publish
artifacts** (live in the CRaaS registry) and **Install objects** (live on the
target cluster).

---

## Entities

### 1. Controller runtime image  *(build/publish artifact)*

The multi-arch container the provider pod runs once installed.

| Attribute | Value / shape |
|---|---|
| Built by | `docker buildx build --platform linux/amd64,linux/arm64 --push` (`Dockerfile`, `make image`) |
| Architectures | `linux/amd64`, `linux/arm64` (FR-003, SC-003) — manifest list |
| Reference | `<CRaaS-host>/provider-timeweb:<VERSION>` and `…@sha256:<digest>` |
| Base | `scratch` runtime stage; non-root `USER 65532`; CA bundle for TLS |
| Version stamp | `-X …/internal/version.Version=<VERSION>` ldflag |
| Signature | `cosign sign <image-ref>` (R-6) |

### 2. Provider package — `.xpkg`  *(build/publish artifact; itself an OCI image)*

The installable Crossplane package: meta + CRDs. **This is what a cluster installs.**

| Attribute | Value / shape |
|---|---|
| Built by | `crossplane xpkg build --package-root=package --examples-root=examples` (`make xpkg.build`) |
| Contents | `package/crossplane.yaml` (meta `Provider`) + `package/crds/*.yaml` (CRDs only — `project_xpkg_allowed_kinds`) |
| Embedded runtime ref | `crossplane.yaml > spec.controller.image` = the runtime image (R-1); today hard-coded `ghcr.io/lebedevdsl/provider-timeweb:v0.1.0` → must template to `<CRaaS-host>/provider-timeweb:<VERSION>` |
| Declared compat | `crossplane.yaml > spec.crossplane.version: ">=v2.0.0-0"` (satisfied by staging 2.3.2) |
| Pushed by | `crossplane xpkg push -f <file>.xpkg <ref>` (R-2) |
| Reference | `<CRaaS-host>/provider-timeweb-xpkg:<VERSION>` and `…@sha256:<digest>` |
| Signature | `cosign sign <xpkg-ref>` (R-6) |

### 3. CRaaS registry  *(publish/pull target)*

The Timeweb Container Registry hosting both artifacts; pulled **in-network** by
`twc-staging` (FR-011).

| Attribute | Value / shape |
|---|---|
| Host shape | `<registry-name>.cr.twcstorage.ru` (`docs/presets.md:109`) — **contains a dot** ⇒ passes the v2 `spec.package` validator (`project_crossplane_v2_conventions`) |
| API | OCI / Docker Registry v2 (works with `docker push` + `crossplane xpkg push`) |
| Push auth | local Docker credential store (`docker login <host>`) (R-2) |
| Pull auth | `kubernetes.io/dockerconfigjson` Secret (entity #6) |
| Lifecycle | **pre-created out-of-band** (R-4); not provisioned by this provider |
| VERIFY-LIVE | exact host string, repo-namespace rules, dev-network reachability (R-2/R-4) |

### 4. Release version  *(version identity)*

The immutable, verifiable identity of one published build (FR-007/SC-005).

| Attribute | Value / shape |
|---|---|
| Tag | `VERSION` (e.g. `v0.1.0`) — `git describe --tags` default in `Makefile` |
| Digest | `@sha256:<digest>` per artifact (image + `.xpkg`) — the pin used in `spec.package` |
| Signature | cosign signatures over both artifacts; verifiable before install |
| Compat | declared Crossplane range `>=v2.0.0-0` (validated against 2.3.2) |
| No-overwrite | re-publishing an existing tag is controlled (FR-009) — see `publish-command.md` |

### 5. ProviderConfig + token Secret  *(install object — credentials)*

Unchanged from prior features; the provider's Timeweb credential source.

| Attribute | Value / shape |
|---|---|
| Token Secret | `Secret` `timeweb-credentials` (key `token`) in the workload namespace (`timeweb-e2e`) |
| ProviderConfig | `timeweb.crossplane.io/v1alpha1` `ProviderConfig` → `credentials.source=Secret`, `secretRef{name,key}` |
| Constraint | credentials ONLY via `ProviderConfig`→`Secret`; never baked into images (Provider Constraints) |
| Scope | namespaced (`kuttl.sh` provisions it; `ClusterProviderConfig` for the multi-PC bundle) |

### 6. Package-pull / image-pull Secret  *(install object — registry auth)*

The single private-registry credential. **One Secret covers both pulls** (R-3).

| Attribute | Value / shape |
|---|---|
| Kind | `Secret` type `kubernetes.io/dockerconfigjson` |
| Name | e.g. `craas-pull` |
| Namespace | **`crossplane-system`** (MUST match Crossplane's namespace — providers docs) |
| Referenced by | `Provider.spec.packagePullSecrets[].name` — Crossplane uses it as the **package** pull secret AND auto-adds it as the runtime Deployment's **image** pull secret (R-3) |
| Fleet alt | `ImageConfig.spec.registry.authentication.pullSecretRef` matching the CRaaS prefix (R-3) |

### 7. ImageConfig (verification + optional pull)  *(install object — integrity gate)*

Declarative in-cluster gate (R-6). Optional but recommended for FR-007/SC-005.

| Attribute | Value / shape |
|---|---|
| Kind | `pkg.crossplane.io/v1beta1` `ImageConfig` |
| Match | `spec.matchImages[].prefix: <CRaaS-host>/provider-timeweb` |
| Verify | `spec.verification.provider: Cosign` + `cosign.authorities[]` (keyless or key) |
| Pull (alt) | `spec.registry.authentication.pullSecretRef` (fleet-wide pull cred — alternative to per-Provider `packagePullSecrets`) |

### 8. Provider CR  *(install object — the install itself)*

| Attribute | Value / shape |
|---|---|
| Kind | `pkg.crossplane.io/v1` `Provider` |
| `spec.package` | `<CRaaS-host>/provider-timeweb-xpkg:<VERSION>` (or `@sha256:<digest>` — pinned) |
| `spec.packagePullSecrets` | `[{name: craas-pull}]` |
| `spec.packagePullPolicy` | `IfNotPresent` (release) / `Always`+annotation-bump (mutable-tag iteration) |
| `spec.runtimeConfigRef` | optional → `DeploymentRuntimeConfig` (not required for pull secrets, R-3) |
| Readiness | `condition=Healthy` ⇒ ProviderRevision active + runtime Deployment Ready |

### 9. DeploymentRuntimeConfig  *(install object — OPTIONAL)*

| Attribute | Value / shape |
|---|---|
| Kind | `pkg.crossplane.io/v1beta1` `DeploymentRuntimeConfig` |
| Runtime container name | `package-runtime` (fixed by the package manager) |
| Use in 008 | **not needed** for pull secrets (R-3); introduce only for a real runtime tweak (args/resources/SA) |

### 10. Target cluster / context  *(install location)*

| Attribute | Value / shape |
|---|---|
| Context | `twc-staging` (explicit; no ambient default — FR-004) |
| Platform | k0s, k8s `v1.35.4+k0s`, **Crossplane 2.3.2** (owner-installed, out of scope) |
| Namespace | `timeweb-e2e` (workload isolation — FR-006) + `crossplane-system` (Crossplane + pull Secret) |
| Reachability | in-network to CRaaS pull AND to `api.timeweb.cloud` (no WAF inside Timeweb) |

---

## Relationships

```
                         cosign sign
   Controller image  <─────────────────  Release version (tag + digest + sig)
   (multi-arch OCI)                              │  cosign sign
        ▲  spec.controller.image                 ▼
        │                                  Provider package (.xpkg, OCI)
        └───────────────── referenced by ────────┘
                                  │ pushed to
                                  ▼
                          CRaaS registry  ◄──── docker login (push auth)
                                  │
              in-network pull (dockerconfigjson Secret in crossplane-system)
                                  ▼
   ┌────────────────────── Target cluster: twc-staging ──────────────────────┐
   │  ImageConfig (verify Cosign + match CRaaS prefix)                        │
   │        │ gates                                                           │
   │  Provider CR ──spec.package──▶ .xpkg     ──spec.packagePullSecrets──▶ Secret
   │        │ creates                                                         │
   │  ProviderRevision ──▶ runtime Deployment (image = spec.controller.image) │
   │        │ Healthy                                                         │
   │  provider pod ──reads──▶ ProviderConfig ──secretRef──▶ token Secret      │
   │        │ reconciles                                                      │
   │  managed resources (timeweb-e2e ns) ──▶ api.timeweb.cloud (no WAF)       │
   └─────────────────────────────────────────────────────────────────────────┘
```

## Publish → Install → Reconcile flow

1. **Build** — `docker buildx` multi-arch image; `crossplane xpkg build` the `.xpkg`
   (its `spec.controller.image` = the image ref).
2. **Sign** — `cosign sign` both the image and the `.xpkg`.
3. **Push** — `docker push` image + `crossplane xpkg push` `.xpkg` → CRaaS (no
   silent overwrite of an existing tag — FR-009).
4. **Record** — emit the tag + both digests + signature refs (the Release version).
5. **Bootstrap target** (one-time) — CRaaS pull `Secret` in `crossplane-system`;
   token `Secret` + `ProviderConfig` in `timeweb-e2e`; optional `ImageConfig` (verify).
6. **Install** — apply `Provider` (`spec.package` = CRaaS `.xpkg` ref, pinned by
   digest; `packagePullSecrets` = the pull Secret). Crossplane verifies (ImageConfig),
   pulls the `.xpkg`, creates a ProviderRevision, deploys the runtime image (pull
   Secret auto-propagated), reaches **Healthy**.
7. **Reconcile** — the pod reads `ProviderConfig`→token `Secret`, reconciles MRs in
   `timeweb-e2e` against `api.timeweb.cloud` from inside Timeweb (no WAF).
8. **e2e + sweep** — run the kuttl bundles against `twc-staging`; on teardown remove
   cluster objects and sweep live-API orphans (`feedback_always_check_live_api_orphans`).

## State / invariants

- **Version immutability** — a tag, once published, is not silently overwritten;
  digest pins make installs reproducible (FR-007/FR-009).
- **Credential separation** — registry creds (pull Secret) ≠ Timeweb API creds
  (ProviderConfig token); neither is ever baked into an image.
- **Namespace placement** — pull Secret in `crossplane-system` (else the package
  manager can't read it); token Secret + ProviderConfig in `timeweb-e2e`.
- **No CRD drift** — `package/crds/` is byte-identical to `make generate-crds`
  output; publishing changes only meta refs (image/version), not CRD schemas (FR-010).
</content>
