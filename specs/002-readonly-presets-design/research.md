# Phase 0 Research: Internal Catalog Resolution & ProviderConfig Scoping

**Feature**: `002-readonly-presets-design`

**Date**: 2026-05-31 (respec; supersedes the 2026-05-20 catalog-as-CRDs research)

This document resolves the open questions left by the Technical Context section of [plan.md](./plan.md) and consolidates the design patterns that survived the 2026-05-31 respec. Anything in [`001-mvp-scaffolding/research.md`](../001-mvp-scaffolding/research.md) (R-2 external-name, R-3 error classification, R-4 immutable-field inventory) carries forward unchanged.

---

## R-1 — Resolver primitive design

**Question**: How is the in-controller catalog cache structured, and what's the interface every MR reconciler calls into?

**Decision**:

The resolver is a small package, `internal/controller/shared/resolver`, exposing a single typed entrypoint and three dimension kinds.

```go
type Resolver interface {
    Resolve(ctx context.Context, pcRef PCRef, dim Dimension, input ResolveInput) (ResolveOutput, error)
}

type PCRef struct {
    Name       string
    Namespace  string // "" for ClusterProviderConfig
    APIGroup   string // "timeweb.crossplane.io"
    Kind       string // "ProviderConfig" | "ClusterProviderConfig"
}

type Dimension struct {
    Name string // e.g. "ContainerRegistryPreset", "ServerConfigurator", "KubernetesVersion"
    Kind DimensionKind
}

type DimensionKind int
const (
    DimensionPreset DimensionKind = iota
    DimensionConfigurator
    DimensionEnum
)

// ResolveInput is a sum type per dimension kind:
//   - Preset:        { Slug string }                                                   // FR-006
//   - Configurator:  { Filters map[string]any; Sizing map[string]int64 }               // FR-007
//   - Enum:          { Value string }                                                  // FR-017
//
// ResolveOutput is correspondingly:
//   - Preset:        { UpstreamID int64 }                                              // becomes lockedPresetID
//   - Configurator:  { UpstreamID int64; LockedSizing map[string]int64 }               // becomes lockedConfiguratorID + locked cpu/ramMB/diskGB
//   - Enum:          { Valid bool; ValidValues []string /* on miss */ }
```

The internal cache is `map[cacheKey]*cacheEntry` behind a `sync.RWMutex`, where `cacheKey = (pcRef, Dimension.Name)`. Each entry holds the upstream snapshot, the fetch timestamp, and an in-flight singleflight group key. TTL is configurable per-controller via a flag (default 5 min; bounds 1 min – 1 hour per FR-011). Coalescing uses `golang.org/x/sync/singleflight` to ensure concurrent reconciles for the same key share one upstream GET. Process-local — no persistence; restart re-warms.

**Rationale**:

- One entrypoint keeps every MR reconciler's resolver call site identical, which makes adding the future `Server` and `KubernetesCluster` MRs a matter of declaring dimensions rather than wiring new helpers.
- Three dimension kinds — preset, configurator, enum — exactly cover Timeweb's three readonly catalog response shapes (list of presets with sizing fields, list of configurators with `requirements` bounds, list of plain strings).
- `singleflight` is the canonical Go idiom for "many goroutines need the same fetch result; do it once."
- Cache lifetime is process-local, not Kubernetes-persistent, per constitution §II (rebuildable side-effect-free state).

**Alternatives considered**:

- **A per-MR private cache**. Rejected: every new MR would re-implement TTL, coalescing, and 401/403 handling. Constitution §III's test-discipline cost would also multiply.
- **A `Catalog`-CRD-backed snapshot** (the 2026-05-20 design, respec'd away). Rejected: spec change moved this concern out of Kubernetes objects (per the respec input).
- **A periodic background poller writing snapshots to an in-memory store**. Rejected: identical to the respec'd timer goroutine but without the user-facing surface — pays the always-on cost without operator benefit. Lazy on-reconcile fetch with singleflight has equivalent steady-state cost and zero idle cost.

---

## R-2 — Dual-reference `ProviderConfig` / `ClusterProviderConfig` handling

**Question**: How does an MR's `spec.providerConfigRef` resolve to either a namespaced `ProviderConfig` in the MR's namespace or a cluster-scoped `ClusterProviderConfig`?

**Decision** *(revised 2026-05-31 after the T002 spike)*:

The dual-PC helpers live on the **`github.com/crossplane/crossplane-runtime/v2` Go module path** (latest stable v2.3.1), **not** on the v1 module path the MVP `go.mod` was using (`github.com/crossplane/crossplane-runtime v1.20.6`). The v1 and v2 module paths are entirely separate Go modules; the v1 path will never gain the v2 helpers.

The v2 picture:

1. The v2 runtime exposes `resource.ProviderConfigKinds` — a struct carrying type metadata for both PC kinds (namespaced + cluster-scoped). The PC reconciler is constructed via `providerconfig.NewReconciler(m, resource.ProviderConfigKinds{...}, opts...)` rather than via an option setter like `managed.WithProviderConfigKinds(...)` (the v1-era API surface the original research draft assumed).
2. The v2 `pkg/resource` package splits the Managed interface into `ModernManaged` (namespaced, with `TypedProviderConfigReferencer` carrying Kind) and `LegacyManaged` (cluster-scoped v1-shape, marked Deprecated). Namespaced MRs implement `ModernManaged`; cluster-scoped MRs implement `LegacyManaged`.
3. The v2 type set (`xpv2`) lives in **a separate module from the runtime**: `github.com/crossplane/crossplane/apis/v2/core/v2` (currently pinned to a pseudo-version per the runtime's go.mod, `v2.0.0-20260424160951-…`). The MVP's `xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"` import goes away entirely — `apis/common/v1` is removed in the v2 runtime module.
4. Several v1 types are renamed in v2: `ResourceSpec` → `ManagedResourceSpec` (namespaced) + `ClusterManagedResourceSpec` (cluster-scoped); `ResourceStatus` → `ManagedResourceStatus`. `PublishConnectionDetailsTo` is removed — namespaced MRs use `LocalConnectionSecretWriterTo` instead.
5. `Condition`, `ConditionType`, `ConditionReason`, `ConditionedStatus`, `CommonCredentialSelectors`, `CredentialsSource`, `ProviderConfigStatus`, `ProviderConfigUsage`, `ManagementPolicies`, `DeletionPolicy`, `TypedReference`, `SecretReference`, `Reference` all carry over to `xpv2` with the same names.
6. Helper functions and constants used in the MVP (`Available()`, `Creating()`, `Deleting()`, `TypeReady`, `TypeSynced`, `CredentialsSourceSecret`) are all present in `xpv2` with the same signatures.

**Migration footprint** *(observed during the 2026-05-31 spike)*:

- `go.mod`: replace `github.com/crossplane/crossplane-runtime v1.20.6` with `github.com/crossplane/crossplane-runtime/v2 v2.3.1`; add `github.com/crossplane/crossplane/apis/v2 v2.0.0-<pseudo>`; bump `k8s.io/{api,apimachinery,client-go}` from `v0.31.0` to `v0.35.x`; bump `sigs.k8s.io/controller-runtime` from `v0.19.0` to `v0.23.x`; bump `sigs.k8s.io/controller-tools` from `v0.16.0` to `v0.20.0`. `google.golang.org/genproto` needs an explicit upgrade to resolve the monolithic-vs-split-modules ambiguous-import error.
- Bulk sed across 42 Go files: `crossplane-runtime/` → `crossplane-runtime/v2/`; `xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"` → `xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"`; `xpv1.` → `xpv2.`.
- Per-MR surgery (this is the work still owed when continuing T002):
  - In each MR's `*_types.go`: replace embedded `xpv2.ResourceSpec` / `xpv2.ResourceStatus` with `xpv2.ManagedResourceSpec` / `xpv2.ManagedResourceStatus`.
  - In each MR's `managed.go`: delete the `GetPublishConnectionDetailsTo` / `SetPublishConnectionDetailsTo` method pairs (no longer required by the v2 ModernManaged interface; the v2 alternative is `GetWriteConnectionSecretToReference` on the local-secret writer).
  - Adjust method calls that read fields from `mg.Spec` / `mg.Status` that came from the embedded v1 spec — these are now reached via getter methods on the v2 ManagedResourceSpec / Status.
- Regenerate `zz_generated_deepcopy.go` via the bumped `controller-tools`.

**Rationale**:

- The dual-reference convention is the established Crossplane v2 idiom; re-implementing it would invite divergence from the ecosystem.
- Renaming the MVP's `ProviderConfig` to `ClusterProviderConfig` (rather than keeping the name on the cluster-scoped kind) avoids a future stable-API break — the long-term v2 idiom is namespaced-PC-by-default.

**Risk closure**: the R-2 risk flagged in the original research draft has now materialized in full. The migration is a real piece of work, but no architectural surprises beyond the rename + interface-shape changes documented above.

**Alternatives considered**:

- **Keep PC cluster-scoped only**, defer namespaced-PC. Rejected: the respec preserves the dual-scope decision because the v2 namespaced model is where the rest of Crossplane is heading; deferring delays the inevitable.
- **Stay on v1 runtime and hand-roll dual-PC**. Rejected per the `feedback_use_standard_ecosystem_tools` memory — invented tooling instead of using the ecosystem-standard helpers.
- **Custom Webhook resolving `providerConfigRef`**. Rejected: violates the xpkg-allowed-kinds memo (`package/` accepts only CRD/MRD/webhook-config, not arbitrary webhooks).

---

## R-3 — Removal path for `ContainerRegistryPreset` CRD and the `manager.Runnable` poller

**Question**: How do we remove the MVP-shipped `ContainerRegistryPreset` CRD and its hand-rolled poller safely?

**Decision**:

- `apis/containerregistry/v1alpha1/preset_types.go` is deleted; its registration is removed from `apis/containerregistry/v1alpha1/groupversion_info.go` and `managed.go`.
- `internal/controller/containerregistry/preset_reconciler.go` (the `manager.Runnable` timer loop) is deleted.
- `internal/controller/containerregistry/preset_resolver.go` is deleted; its slug logic moves into `internal/controller/shared/resolver/slug.go` and its lookup logic is subsumed by `resolver.Resolve(..., DimensionPreset, ...)`.
- `apis/containerregistry/v1alpha1/zz_generated.deepcopy.go` is regenerated (DeepCopy methods for the removed type disappear).
- The CRD manifest under `package/crds/` is regenerated; the `containerregistry.timeweb.crossplane.io_containerregistrypresets.yaml` file is deleted from the package on the next `make generate`.
- On upgrade, the provider package's CRD list no longer contains `ContainerRegistryPreset`. Kubernetes garbage-collects existing CRs of the removed CRD when the CRD itself is removed from the apiserver — this is standard `kubectl apply --prune` or Crossplane package-revision behavior. Since the existing CRs are observe-only and re-derivable, no data is lost.

**Rationale**: The MVP code shipped on `main` has no external consumers (per the spec's Assumptions). The cleanest possible removal — drop the types, drop the controller, regenerate generated artifacts, ship the new release — is correct.

**Alternatives considered**:

- **Migrate to a new group** (the 2026-05-20 plan: `catalog.timeweb.crossplane.io/v1alpha1`). Rejected: respec'd away.
- **Keep the CRD as a soft-deprecation alias for one release**. Rejected: violates "no external consumers" assumption; pure churn.

---

## R-4 — CEL `oneOf` enforcement on `forProvider`

**Question**: How is the `presetName XOR resources` rule expressed at the CRD level?

**Decision**:

Use `kubebuilder:validation:XValidation` rules on the Go type, which `controller-gen` emits as CEL `x-kubernetes-validations` on the CRD `forProvider` openAPIv3 schema:

```go
// +kubebuilder:validation:XValidation:rule="(has(self.presetName) ? 1 : 0) + (has(self.resources) ? 1 : 0) == 1",message="exactly one of presetName or resources must be set"
type ContainerRegistryParameters struct {
    PresetName *string                                  `json:"presetName,omitempty"`
    Resources  *ContainerRegistryResourcesParameters    `json:"resources,omitempty"`
    // … other fields (e.g. for the future Kubernetes Cluster: k8sVersion, networkDriver, availabilityZone)
}
```

The rule is enforced by the Kubernetes apiserver at `kubectl apply` time; no admission webhook needed. The xpkg-allowed-kinds constraint is respected (CEL is part of the CRD schema, not a separate webhook).

For composite MRs (forward-compat, not used in this feature), the same `XValidation` rule attaches to each sub-block's type.

**Rationale**:

- CEL on CRD is the modern Kubernetes-native validation path; no operator deploy hooks needed; surfaces clearly in `kubectl apply` errors.
- `controller-gen` already emits these rules from kubebuilder annotations — no new build-time tool.

**Alternatives considered**:

- **Pure documentation, runtime validation only**. Rejected: pushes operator pain to reconcile time when admission can catch it. Already used elsewhere (immutable-field enforcement uses a similar pattern); consistent.
- **ValidatingAdmissionPolicy in `deploy/`** (the 2026-05-20 plan). Rejected: simpler to inline as CEL on the CRD; VAP added complexity for no marginal benefit.

---

## R-5 — Kubernetes Cluster readiness: which readonly dimensions does it need, and do they map cleanly to this feature's resolver?

**Question**: Per the 2026-05-31 clarification session, "every piece should be in place before we start Kubernetes Cluster." What does that piece-list look like in concrete terms?

**Decision**:

A future `KubernetesCluster` MR (control plane only, single-block sizing) and `KubernetesNodeGroup` MR (one MR per worker group, `clusterRef` → parent cluster, single-block sizing) consume **four logical** readonly catalog dimensions (six dimension registrations counting per-type splits). All map to one of this feature's three dimension kinds. None require new resolver primitives.

| Dimension                    | Kind          | Upstream endpoint (implementation detail)       | Used by                  | Spec linkage              |
|------------------------------|---------------|-------------------------------------------------|--------------------------|---------------------------|
| `KubernetesMasterPreset`     | Preset        | `GET /api/v1/presets/k8s` (filter `type=master`)| KubernetesCluster        | FR-004, FR-006            |
| `KubernetesWorkerPreset`     | Preset        | `GET /api/v1/presets/k8s` (filter `type=worker`)| KubernetesNodeGroup      | FR-004, FR-006            |
| `ServerConfigurator`         | Configurator  | `GET /api/v1/configurator/servers`              | both (for `resources` variant) | FR-004, FR-007      |
| `KubernetesVersion`          | Enum          | `GET /api/v1/k8s/k8s-versions`                  | KubernetesCluster        | FR-016, FR-017            |
| `KubernetesNetworkDriver`    | Enum          | `GET /api/v1/k8s/network-drivers`               | KubernetesCluster        | FR-016, FR-017            |
| `AvailabilityZone`           | Enum          | derived from K8s preset response (no dedicated endpoint) | KubernetesCluster | FR-016, FR-017 |

Notes:

- Two dimensions (`KubernetesMasterPreset`, `KubernetesWorkerPreset`) share the same upstream endpoint with a response-side filter (`type` discriminator). The dimension registry handles this internally — same endpoint, two dimension entries.
- `ServerConfigurator` is shared between `KubernetesNodeGroup`'s `resources` sizing variant and the future `Server` MR's `resources` sizing variant. One cache entry, two consumers.
- `AvailabilityZone` has no dedicated catalog endpoint in the OpenAPI; the values are embedded in the K8s preset response. The resolver registers it as an enum dimension whose fetcher derives the value set from the K8s preset list (one upstream call, two cache entries — one for presets, one for zones — or one shared entry, depending on the resolver's internal arrangement).
- `KubernetesNodeGroup` references its parent `KubernetesCluster` via the standard Crossplane cross-MR reference pattern (the same one already used by `ContainerRegistry → Project`). The reference resolution wait is handled by `crossplane-runtime`'s reference machinery; the MR reconciles only once the cluster reports `Ready=True`.

**Rationale**: Confirms SC-007 — the future K8s feature ships against this spec's primitives without resolver/cache/PC schema changes. The four-dimensions-checked-against-three-kinds analysis is concrete and falsifiable on review.

---

## R-6 — Test strategy

**Question**: What's the minimum test set this feature must ship to satisfy constitution §III?

**Decision**:

Two layers:

1. **Unit tests** (`go test`), four-case rule per `external` method touched and per resolver public method:
   - `internal/controller/containerregistry/registry_external_test.go` — Observe / Create / Update / Delete × {success, not-found, transient error, terminal error}.
   - `internal/controller/s3bucket/<external>_test.go` — same.
   - `internal/controller/shared/resolver/resolver_test.go` — `Resolve` × {preset-hit, preset-miss-then-fetch, preset-not-found, preset-ambiguous, configurator-success, configurator-no-match, enum-hit, enum-unknown, unauthorized-401, unauthorized-403, transient-5xx-retried, stale-cache-invalidated-on-4xx, concurrent-miss-coalesced}.
   - Fakes: `counterfeiter`-generated `FakeTimewebClient` against the existing OpenAPI-derived interface.

2. **Integration / e2e tests** (`kuttl`), one suite per user story:
   - `test/e2e/us1-resources-variant/` — apply namespaced PC, apply ContainerRegistry with `resources`, assert Ready=True; PATCH within variant, assert update; attempt sizing switch, assert `SizingSwitchRequiresRecreate`; covers acceptance scenarios US1 1-4.
   - `test/e2e/us1-pc-migration/` — pre-create cluster-scoped PC mimicking the MVP shape, upgrade CRDs, assert Project + SshKey continue to reconcile against the renamed `ClusterProviderConfig`; covers US1 scenario 5.
   - `test/e2e/us2-presetname-variant/` — apply MR with `presetName`, assert resolve+Ready; apply MR with nonexistent slug, assert `PresetNotFound` with helpful message; covers US2 scenarios 1, 3, 4.
   - `test/e2e/us3-multi-pc/` — two PCs (one namespaced, one cluster-scoped), token-scoped 403 intercept on one, assert per-PC isolation and `CatalogUnauthorized` on the scoped PC; covers US3 scenarios 1-3.

**Rationale**: Mirrors the 001-mvp test pattern (unit-first with fakes, kuttl for cross-cutting). No new test infrastructure.

**Alternatives considered**:

- **Property-based fuzzing of the resolver**. Rejected for now — not a constitution requirement and the dimension-kind surface is small enough to be exhaustively unit-tested. May reconsider when the resolver grows beyond three kinds.
