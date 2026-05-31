---
description: "Task list for 002-readonly-presets-design — internal catalog resolution + dual-scope ProviderConfig refactor"
---

# Tasks: Internal Catalog Resolution & ProviderConfig Scoping

**Input**: Design documents from `specs/002-readonly-presets-design/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Included — constitution §III mandates unit tests for every `external` method touched and for the new resolver package; the spec adds kuttl e2e bundles per user story.

**Organization**: Tasks are grouped by user story (US1, US2, US3 from spec.md) so each story can be implemented, tested, and merged independently.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: parallelizable (different files, no dependencies on incomplete tasks in the same phase)
- **[Story]**: only on tasks inside a user-story phase (US1 / US2 / US3)
- Setup, Foundational, and Polish phases carry no story label

## Path Conventions

This is a Go Crossplane provider — single-module layout. Paths are relative to repo root.

- API types: `apis/<group>/v1alpha1/`
- Controllers: `internal/controller/<group>/`
- Shared internal: `internal/controller/shared/`
- Generated Timeweb client: `internal/clients/timeweb/generated/`
- Provider package (CRDs, metadata): `package/`
- Operator-facing docs: `docs/`
- E2E suites: `test/e2e/`

---

## Phase 1: Setup

**Purpose**: Repository-level prep needed before any types or controllers change. No story-specific work yet.

- [X] T001 Add `golang.org/x/sync` (for `singleflight`) to `go.mod`; run `go mod tidy`; commit the resulting `go.sum` change. *(Already present as indirect at v0.20.0; verified `golang.org/x/sync/singleflight.Group` is reachable. Will flip to direct automatically when T012 imports it.)*
- [X] T002 Spike → expanded plan-revision: confirm v2 dual-reference helpers and migrate the module.
  - **2026-05-31 result**: Pinned `crossplane-runtime v1.20.6` does NOT expose v2 helpers — the v1 path is a separate Go module. Migrated to:
    - `github.com/crossplane/crossplane-runtime/v2` v2.3.1
    - `github.com/crossplane/crossplane/apis/v2/core/v2` v2.0.0-20260424160951-8f231230ebb6 (new module supplying the `xpv2` type set)
    - bumped `sigs.k8s.io/controller-runtime` v0.19.0 → v0.23.1, `k8s.io/{api,apimachinery,client-go}` v0.31.0 → v0.35.1, `sigs.k8s.io/controller-tools` v0.16.0 → v0.20.0
    - one extra `go get google.golang.org/genproto@latest` to break the monolithic-vs-split `googleapis/api` ambiguous-import.
  - **Per-MR surgery applied**:
    - `xpv2.ResourceSpec` / `xpv2.ResourceStatus` renamed to `xpv2.ManagedResourceSpec` / `xpv2.ManagedResourceStatus` across all 5 type files (SSHKey, Project, S3Bucket, ContainerRegistry, ContainerRegistryRepository — all namespaced ModernManaged MRs; no cluster-scoped MRs in the v0.1 surface).
    - Every MR's `managed.go` rewritten to the v2 ModernManaged shape: typed `*xpv2.ProviderConfigReference` (was `*xpv2.Reference`), `*xpv2.LocalSecretReference` (was `*xpv2.SecretReference`), and `GetCondition`/`SetConditions`/`GetManagementPolicies`/`SetManagementPolicies` forwarders. `GetDeletionPolicy`/`SetDeletionPolicy` and `GetPublishConnectionDetailsTo`/`SetPublishConnectionDetailsTo` dropped (not part of v2 namespaced ModernManaged).
    - `apis/v1alpha1/providerconfigusage_types.go` switched to embed `xpv2.TypedProviderConfigUsage`; `apis/v1alpha1/managed.go` PCU accessors retyped to `xpv2.ProviderConfigReference`.
    - Each controller's `connector.usage` field retyped from `resource.Tracker` → `resource.ModernTracker` (matches the `ProviderConfigUsageTracker.Track(ModernManaged)` signature).
    - `internal/controller/containerregistry/connector.go` `loadToken` retyped to take `*xpv2.ProviderConfigReference`; `internal/controller/containerregistry/preset_reconciler.go` updated to construct that typed ref (the file itself is slated for deletion in T024 and is left in place for now).
    - `internal/controller/shared/immutable_test.go` `stubManaged` trimmed to just the v2 `resource.Managed` surface (`Object + Manageable + Conditioned`) — the v1-specific stub methods are gone with the rest of v1.
    - `zz_generated.deepcopy.go` regenerated via `controller-gen object`.
  - **Smoke**: `go build ./...` clean; `go test ./...` clean (all existing MVP unit tests pass under v2).
- [X] T003 [P] Ensure `make generate` regenerates DeepCopy + CRD manifests cleanly against the current MVP code; record any pre-existing diff so subsequent diffs are attributable to this feature's edits.
  - **2026-05-31 result**: Clean re-run. DeepCopy + CRD output stable. The post-v2 CRD diff shrinks every MR YAML by ~140 lines because v2 ModernManaged dropped `deletionPolicy` and the untyped `providerConfigRef.policy` block (typed `{kind, name}` now); this is the expected v2 shape, not feature drift.
- [X] T004 [P] Verify `oapi-codegen` against the live Timeweb OpenAPI covers every catalog endpoint the dimension registry (T015) needs.
  - **2026-05-31 finding — BLOCKER for T015 / US1 sizing-XOR design**:
    Endpoint coverage in `docs/openapi-timeweb.json` (probed via `grep '"/api/v1/(configurator|presets|container-registry|k8s)…'`):
    | Endpoint                              | Present? | OpenAPI line |
    |---------------------------------------|----------|--------------|
    | `GET /api/v1/container-registry/presets` | ✓       | 12211 (`getRegistryPresets`) |
    | `GET /api/v1/configurator/registries`    | **✗**   | — does not exist upstream |
    | `GET /api/v1/presets/storages`           | ✓       | 20104 (`getStoragesPresets`) |
    | `GET /api/v1/configurator/storages`      | **✗**   | — does not exist upstream |
    | `GET /api/v1/configurator/servers`       | ✓       | 14201 (`getServersConfigurator`) — server only |
    | `GET /api/v1/presets/k8s`                | ✓       | 11083 |
    | `GET /api/v1/k8s/k8s-versions`           | ✓       | 10827 |
    | `GET /api/v1/k8s/network-drivers`        | ✓       | 11003 |
    The ONLY configurator endpoint exposed by Timeweb is `/api/v1/configurator/servers`. There is no per-tariff configurator surface for Container Registry or S3 Storage — those two services are **preset-only** upstream.
  - **Spec/data-model impact** (do NOT silently ignore):
    - `data-model.md` §Dimension Registry rows for `ContainerRegistryConfigurator` and `S3BucketConfigurator` are non-implementable as written.
    - `spec.md` "every consuming MR is `presetName XOR resources`" cannot hold for ContainerRegistry / S3Bucket — there is no `resources` path.
    - The condition `SizingSwitchRequiresRecreate` (renamed from `AxisSwitchRequiresRecreate` per the 2026-05-31 terminology refactor) becomes inert for those two MRs.
  - **Recommendation to discuss with operator before continuing**: respec ContainerRegistry / S3Bucket as preset-only (drop `resources` block; the XOR machinery stays in the resolver primitive for Server / KubernetesCluster / KubernetesNodeGroup, all of which DO have configurators). Alternative paths — hand-rolling undocumented endpoints, or treating the spec aspirationally and stubbing — both carry larger downsides than narrowing the v0.1 surface.
  - **Resolution (operator decision, same session)**: ContainerRegistry and S3Bucket are **preset-only with disk-size custom dimension** — `presetName XOR resources{diskGB|diskMB}`. The XOR shape is preserved; the `resources` branch carries disk size only and bypasses configurator selection. The `DimensionConfigurator` kind and the configurator selection algorithm (T014) stay in the resolver primitive for the forthcoming Server / KubernetesCluster / KubernetesNodeGroup MRs, which DO have configurators. spec.md, data-model.md, and both refactor contracts updated in-session; T015/T027/T029/T031/T033 task language updated to match.

---

## Phase 2: Foundational

**Purpose**: Build the dual-scope ProviderConfig pair and the internal resolver package — these block every user story.

**⚠️ CRITICAL**: No US1/US2/US3 implementation work begins until this phase is complete.

### ProviderConfig dual-scope refactor (FR-001, contracts: providerconfig-namespaced + clusterproviderconfig)

- [ ] T005 Rename `apis/v1alpha1/providerconfig_types.go` types: `ProviderConfig` (cluster-scoped) → `ClusterProviderConfig`. Update kubebuilder scope markers, `+kubebuilder:resource:scope=Cluster`, and the `groupversion_info.go` registration. CEL rule enforcing `secretRef.namespace != ""` per `contracts/clusterproviderconfig-v1alpha1.md`.
- [ ] T006 [P] Create new `apis/v1alpha1/providerconfig_types.go` defining the **namespaced** `ProviderConfig` per `contracts/providerconfig-namespaced-v1alpha1.md`. `+kubebuilder:resource:scope=Namespaced`. CEL rule enforcing `secretRef.namespace` is empty or equals the PC's own namespace.
- [ ] T007 Split `apis/v1alpha1/providerconfigusage_types.go` into a namespaced `ProviderConfigUsage` + cluster-scoped `ClusterProviderConfigUsage`. Wire both with the standard Crossplane usage helpers.
- [ ] T008 Update `apis/v1alpha1/managed.go` to register both PC kinds and both Usage kinds; expose a uniform credential accessor that hides which kind resolved.
- [ ] T009 Run `make generate`; commit regenerated `zz_generated.deepcopy.go` and the CRD YAMLs under `package/crds/`. Verify no stray references to the old cluster-scoped `ProviderConfig` remain.

### Internal resolver package (FR-006, FR-007, FR-011, FR-012, FR-013, FR-016, FR-017; contract: resolver-internal)

- [ ] T010 Create directory `internal/controller/shared/resolver/`. Add `resolver.go` defining the public `Resolver` interface, `PCRef`, `Dimension`, `DimensionKind`, `ResolveInput`, `ResolveOutput` types per `contracts/resolver-internal.md`.
- [ ] T011 [P] Add `internal/controller/shared/resolver/cache.go`: `(PCRef, dimension)`-keyed cache with TTL (default 5min, flag-configurable 1min–1hour), `sync.RWMutex` guarded, passive eviction.
- [ ] T012 [P] Add `internal/controller/shared/resolver/singleflight.go` wrapping `golang.org/x/sync/singleflight` so concurrent reconciles for the same cache key share one upstream fetch (FR-011).
- [ ] T013 [P] Add `internal/controller/shared/resolver/slug.go`: slug rule `<short>-<location>` (lowercase, non-`[a-z0-9-]` collapsed to `-`); accept the explicit `<short>-<location>-<id>` disambiguator form on resolution (FR-008). Move and adapt logic from the soon-to-be-deleted `internal/controller/containerregistry/preset_resolver.go`.
- [ ] T014 [P] Add `internal/controller/shared/resolver/select_configurator.go`: deterministic selection algorithm per FR-007 — hard-filter by `(location, diskType, enableLocalNetwork, cpuFrequencyTier)`, capability-filter against `requirements.{min,step,max}` bounds, sort by `(max_cpu, max_ramMB, max_diskGB)` ascending, tiebreak by lowest `configurator_id`.
- [ ] T015 Add `internal/controller/shared/resolver/dimensions.go`: dimension registry (logical name → kind, fetcher, optional filter, optional `Derive`). Initial registrations in this task: **`ContainerRegistryPreset`** (`GetRegistryPresets`) and **`S3BucketPreset`** (`GetStoragesPresets`). No configurator registrations for these MRs — per spec.md §Clarifications 2026-05-31 catalog-endpoint reality check, Container Registry and S3 Storage have no `/api/v1/configurator/registries|storages` endpoints upstream. (Server / KubernetesCluster / KubernetesNodeGroup configurator and enum registrations land in Phase 6 / T056 alongside that feature.)
- [ ] T016 [P] Add `internal/controller/shared/resolver/errors.go`: typed sentinel errors (`ErrPresetNotFound`, `ErrPresetAmbiguous`, `ErrNoConfiguratorAvailable`, `ErrDimensionValueNotFound`, `ErrCatalogUnauthorized`, `ErrCatalogTransient`) carrying enough context for MR reconcilers to map to conditions with operator-actionable messages.
- [ ] T017 Wire `Resolver.Resolve` end-to-end: cache lookup → singleflight on miss → fetcher → typed payload → kind-specific resolution (slug match for preset; selection algorithm for configurator; set membership for enum); error mapping to typed sentinels.
- [ ] T018 Add `Resolver.Invalidate(pcRef, dim)` for the stale-cache-on-4xx rule (FR-013). Document in a code comment that callers MUST invoke this on any upstream 4xx that involves a previously-cached upstream ID.

### Foundational tests (constitution §III)

- [ ] T019 [P] `internal/controller/shared/resolver/cache_test.go`: TTL expiry; concurrent miss coalesced into one upstream call; stale-on-4xx invalidation; restart-cold behavior simulated by constructing a fresh `Cache` instance.
- [ ] T020 [P] `internal/controller/shared/resolver/slug_test.go`: slugify rule against OpenAPI fixture data; collision detection; explicit `<short>-<location>-<id>` form.
- [ ] T021 [P] `internal/controller/shared/resolver/select_configurator_test.go`: zero match → `ErrNoConfiguratorAvailable`; single match; multiple matches with tightest-fit tiebreak; multiple matches with `configurator_id` tiebreak; capability filter exclusion.
- [ ] T022 [P] `internal/controller/shared/resolver/resolver_test.go`: matrix per dimension kind × {success, miss-then-fetch, not-found, unauthorized-401, unauthorized-403, transient-5xx-retried, concurrent-coalesced}. Use `counterfeiter`-generated `FakeTimewebClient`.

**Checkpoint**: Foundation ready. Dual-scope PC pair exists; internal resolver is built and tested; the four currently-needed dimensions are registered. US1/US2/US3 can now proceed in parallel.

---

## Phase 3: User Story 1 — Stable sizing inputs, no Catalog dependency (Priority: P1) 🎯 MVP

**Goal**: Operators can create a `ContainerRegistry` or `S3Bucket` via stable `forProvider.resources: { … }` fields, with the controller picking a configurator deterministically. PC dual-scope refactor lands here because every MR touched in this story already needs an updated `providerConfigRef` handler.

**Independent Test**: Per spec.md US1 — apply a namespaced `ProviderConfig`, a `ContainerRegistry` with `forProvider.resources: { location: ru-1, diskGB: 5 }`, and an `S3Bucket` with `forProvider.resources: { location: ru-1, diskGB: 30, storageClass: hot }`. Both reach `Ready=True` within two minutes. Patching `diskGB` upward succeeds via PATCH; attempting to switch to `presetName` surfaces `Synced=False, reason=SizingSwitchRequiresRecreate`. No `Catalog`/preset CRDs exist in the cluster.

### Delete the catalog-as-CRDs surface

- [ ] T023 [US1] Delete `apis/containerregistry/v1alpha1/preset_types.go` and remove its registration from `apis/containerregistry/v1alpha1/groupversion_info.go` and `apis/containerregistry/v1alpha1/managed.go`.
- [ ] T024 [US1] Delete `internal/controller/containerregistry/preset_reconciler.go` (the `manager.Runnable` poller).
- [ ] T025 [US1] Delete `internal/controller/containerregistry/preset_resolver.go` (slug logic already migrated to `internal/controller/shared/resolver/slug.go` in T013).
- [ ] T026 [US1] Remove the `ContainerRegistryPreset` controller registration from `internal/controller/containerregistry/controller.go` (and from any provider-level `SetupWith*` aggregator that referenced it).

### Refactor `ContainerRegistry` MR

- [ ] T027 [US1] Rewrite `apis/containerregistry/v1alpha1/registry_types.go` per `contracts/containerregistry-refactor-v1alpha1.md`: drop the MVP `preset_id` / `configuration{…}` fields; introduce `forProvider.presetName *string` and `forProvider.resources *ContainerRegistryResourcesParameters{DiskGB}`; add the kubebuilder `XValidation` rule enforcing XOR; add `status.atProvider.{lockedPresetID, lockedResources{DiskGB}}`. **No `lockedConfiguratorID`** on this MR (Container Registry has no configurator upstream).
- [ ] T028 [US1] Update `internal/controller/containerregistry/connector.go` to accept either a namespaced `ProviderConfig` or a cluster-scoped `ClusterProviderConfig` via `managed.WithProviderConfigKinds(...)`. Remove any hand-rolled PC lookup.
- [ ] T029 [US1] Rewrite `internal/controller/containerregistry/registry_external.go`:
  - **Observe**: read-only call; populate `status.atProvider.lockedPresetID` or `lockedResources{DiskGB}` from observed upstream state on import; report `ResourceExists`/`ResourceUpToDate` against the locked variant.
  - **Create**: if `forProvider.resources` set, submit upstream Create with `{disk}` directly (no resolver call — Container Registry has no configurator dimension); record `lockedResources{DiskGB}`. If `forProvider.presetName` set, call `resolver.Resolve(..., Dimension{Name:"ContainerRegistryPreset", Kind: DimensionPreset}, ...)` to get the upstream `preset_id`; submit with `{preset_id}`; record `lockedPresetID`. **Both variants implemented here** (Container Registry has no configurator selection algorithm to defer to US2 — that complexity lives on Server / K8s).
  - **Update**: compare current spec sizing variant against `status.atProvider` locked side; if different, return `Synced=False, reason=SizingSwitchRequiresRecreate` without touching upstream. If same variant, PATCH upstream within the variant (re-resolve preset slug if needed; send PATCH with `{disk}` or `{preset_id}`).
  - **Delete**: unchanged from MVP.
- [ ] T030 [US1] Map resolver errors in `registry_external.go` to MR conditions per `contracts/containerregistry-refactor-v1alpha1.md` Conditions table: `ErrPresetNotFound` → `Synced=False, reason=PresetNotFound` (with valid-slug hint); `ErrPresetAmbiguous` → `reason=PresetAmbiguous` (with disambiguator suggestion); `ErrCatalogUnauthorized` → `reason=CatalogUnauthorized`; `ErrCatalogTransient` → requeue with backoff. (`ErrNoConfiguratorAvailable` cannot fire here — no configurator dimension is registered for ContainerRegistry.)

### Refactor `S3Bucket` MR (mirror of ContainerRegistry)

- [ ] T031 [P] [US1] Rewrite `apis/objectstorage/v1alpha1/types.go` per `contracts/s3bucket-refactor-v1alpha1.md`. Same disk-only XOR shape as `ContainerRegistry` (`resources.DiskMB`); `storageClass: string` with kubebuilder static enum `["hot","cold"]` lives at the `forProvider` level **outside** the XOR block (it's a bucket-policy choice, mutable post-create, not a sizing input).
- [ ] T032 [US1] Update `internal/controller/s3bucket/connector.go` for dual-PC support (mirror of T028).
- [ ] T033 [US1] Rewrite `internal/controller/s3bucket/<external>.go` mirroring T029 against the `S3BucketPreset` dimension. Disk-only resources path: Create with `{disk}` directly; PresetName path resolves via `S3BucketPreset`. Same error-to-condition mapping (T030) applies — `NoConfiguratorAvailable` is N/A for this MR.

### Other MRs gain dual-PC support

- [ ] T034 [P] [US1] Update `internal/controller/project/` setup to register both PC kinds via `managed.WithProviderConfigKinds(...)`. No spec changes to the `Project` CRD.
- [ ] T035 [P] [US1] Update `internal/controller/sshkey/` setup to register both PC kinds via `managed.WithProviderConfigKinds(...)`. No spec changes to the `SshKey` CRD.

### Regenerate

- [ ] T036 [US1] Run `make generate`. Verify the regenerated `package/crds/`:
  - drops `containerregistry.timeweb.crossplane.io_containerregistrypresets.yaml`
  - renames the MVP `providerconfigs.timeweb.crossplane.io` CRD to `clusterproviderconfigs.timeweb.crossplane.io` (cluster-scoped)
  - adds new `providerconfigs.timeweb.crossplane.io` CRD (namespaced)
  - updates `containerregistries.containerregistry.m.timeweb.crossplane.io` schema to the XOR shape with CEL XValidation
  - updates `s3buckets.objectstorage.m.timeweb.crossplane.io` schema similarly.
- [ ] T037 [US1] Update `package/crossplane.yaml` and any MRD list (per the xpkg-allowed-kinds memo) to drop `ContainerRegistryPreset` and add the new `ClusterProviderConfig` kind.

### US1 unit tests (constitution §III four-case rule)

- [ ] T038 [P] [US1] `internal/controller/containerregistry/registry_external_test.go`: cover `Observe`/`Create`/`Update`/`Delete` × {success-resources-variant, NotFound, TransientError, TerminalError}. Plus dedicated cases for `Update/WithinVariant`, `Update/SizingSwitchRejected`, `Observe/ImportRecordsLockedVariant`. Use the `counterfeiter` fake plus the resolver fake.
- [ ] T039 [P] [US1] `internal/controller/s3bucket/<external>_test.go`: parallel structure to T038 against the S3 client.

### US1 e2e

- [ ] T040 [US1] Author kuttl bundle `test/e2e/us1-resources-variant/`: applies a namespaced `ProviderConfig`, a `ContainerRegistry` with `resources`, and an `S3Bucket` with `resources`; asserts `Ready=True` within 2 minutes; patches `diskGB`, asserts within-variant PATCH succeeds; patches to `presetName`, asserts `SizingSwitchRequiresRecreate`. No catalog CRDs installed.
- [ ] T041 [P] [US1] Author kuttl bundle `test/e2e/us1-pc-migration/`: pre-creates a cluster-scoped `ClusterProviderConfig` (rename target), applies the upgraded CRDs, asserts existing `Project` + `SshKey` MRs continue to reconcile against the renamed PC.

**Checkpoint**: User Story 1 fully functional. Operators ship `ContainerRegistry` + `S3Bucket` via stable resources fields; PC dual-scope rename complete; `ContainerRegistryPreset` CRD + poller deleted; all MVP MRs continue to reconcile.

---

## Phase 4: User Story 2 — `presetName` resolution (Priority: P2)

**Goal**: Operators set `forProvider.presetName: <slug>` and the controller resolves it via `resolver.Resolve(..., DimensionPreset, ...)`, locks in `lockedPresetID`, and submits upstream with `preset_id` (not `configuration{...}`). Builds on top of US1's resolver wiring and US1's MR refactor — only the preset-side branches of `Create`/`Update`/`Observe` get filled in.

**Independent Test**: Per spec.md US2 — apply a `ContainerRegistry` with `forProvider.presetName: <valid-slug>`; it reaches `Ready=True` within two minutes with `status.atProvider.lockedPresetID` set. Apply another with a nonexistent slug; it transitions to `Synced=False, reason=PresetNotFound` whose message lists ≤20 valid slugs visible to the PC. No catalog CRDs in the cluster.

### Wire presetName resolution

- [ ] T042 [US2] In `internal/controller/containerregistry/registry_external.go` `Create`, replace the `Unimplemented` stub for the `presetName` branch (T029) with a call to `resolver.Resolve(..., Dimension{Name:"ContainerRegistryPreset", Kind: DimensionPreset}, PresetInput{Slug: *spec.PresetName})`. On success, submit upstream with `preset_id` and record `status.atProvider.lockedPresetID`.
- [ ] T043 [US2] In `registry_external.go` `Update`, when the spec keeps `presetName` set on a locked-preset MR, re-resolve the slug (cache-hot path) and PATCH upstream with the new `preset_id` if it changed.
- [ ] T044 [US2] In `registry_external.go` `Observe`, on `ErrPresetNotFound` from a previously-locked preset, call `resolver.Invalidate(pcRef, presetDim)` once, then retry resolution on the next reconcile (FR-013 — stale-cache must not produce false `PresetNotFound`).
- [ ] T045 [P] [US2] Map resolver preset errors to conditions: `ErrPresetNotFound` → `Synced=False, reason=PresetNotFound` (message lists ≤20 valid slugs from the most-recent cache snapshot); `ErrPresetAmbiguous` → `Synced=False, reason=PresetAmbiguous` (message lists colliding upstream IDs and suggests the `<short>-<location>-<id>` form).
- [ ] T046 [P] [US2] Mirror T042–T045 in `internal/controller/s3bucket/<external>.go` against the `S3BucketPreset` dimension.

### US2 unit tests

- [ ] T047 [P] [US2] Extend `registry_external_test.go` with cases `Create/PresetName/Success`, `Create/PresetName/NotFound`, `Create/PresetName/Ambiguous`, `Update/PresetName/UpstreamReID`, `Observe/PresetName/StaleInvalidatedThenRecovered`.
- [ ] T048 [P] [US2] Same coverage in `internal/controller/s3bucket/<external>_test.go`.

### US2 e2e

- [ ] T049 [US2] Author kuttl bundle `test/e2e/us2-presetname-variant/`: applies `ContainerRegistry` with a valid `presetName`, asserts Ready=True + `lockedPresetID` set; applies another with `nonexistent-slug`, asserts `Synced=False, reason=PresetNotFound` and the message contains at least one valid slug from the suggestion list; deletes both.

**Checkpoint**: User Story 2 fully functional. Operators can name a tariff; controller resolves and locks; `Synced=False, reason=PresetNotFound|PresetAmbiguous` carry inline-actionable messages.

---

## Phase 5: User Story 3 — Multi-credential isolation (Priority: P3)

**Goal**: A cluster running two `ProviderConfig`s (or one namespaced + one cluster-scoped) keeps each credential's resolver cache isolated; an unauthorized catalog endpoint on one PC does not affect MRs under another PC. Builds on US1+US2; the per-PCRef cache key was foundational, this phase verifies isolation under intentionally-divergent credentials.

**Independent Test**: Per spec.md US3 — apply two `ProviderConfig`s (one namespaced in `team-a`, one cluster-scoped), assign one a token that returns 403 on `/api/v1/configurator/registries`; MRs under that PC surface `Synced=False, reason=CatalogUnauthorized`; MRs under the other PC continue reconciling normally.

- [ ] T050 [US3] Audit `internal/controller/shared/resolver/cache.go` to confirm `(PCRef, dimension)` is the cache key and that PCRef carries `Kind` (`ProviderConfig` vs `ClusterProviderConfig`) so two PCs of different kinds with the same name never collide. Add a unit test (in T053) that proves this if not already covered.
- [ ] T051 [US3] In `internal/controller/shared/resolver/resolver.go`, ensure `ErrCatalogUnauthorized` is sticky on its cache entry (per `contracts/resolver-internal.md`) — kept until the next successful fetch on that key clears it.
- [ ] T052 [US3] In every MR `external` Create/Update path, map `ErrCatalogUnauthorized` to `Synced=False, reason=CatalogUnauthorized` with a message identifying the upstream endpoint that 403'd and the `providerConfigRef` (kind + name) in scope. Implementation lives in `internal/controller/{containerregistry,s3bucket}/<external>.go`; no new files.

### US3 unit tests

- [ ] T053 [P] [US3] Extend `resolver_test.go` with a multi-PC-isolation case: two `PCRef`s, distinct `Kind`s, same `Name` — confirm separate cache entries, sticky 403 isolation, no cross-PC reads.

### US3 e2e

- [ ] T054 [US3] Author kuttl bundle `test/e2e/us3-multi-pc/`: pre-stages two PC secrets (one valid, one returning 403 on a specific catalog endpoint via the OpenAPI mock); applies one MR per PC; asserts the scoped MR shows `CatalogUnauthorized` and the unscoped MR reaches `Ready=True` within two minutes; deletes the 403'd PC and confirms its MR can recover after the next reconcile.

**Checkpoint**: All three user stories independently functional. Provider ships an MVP-superseding release: stable user-facing MR shapes, internal catalog resolution, dual-scope ProviderConfig.

---

## Phase 6: Polish & K8s-readiness forward-compat

**Purpose**: Documentation, K8s-readiness commitments (SC-007), final cleanup.

- [ ] T055 [P] Write `docs/presets.md` per FR-014: slug rule (`<short>-<location>`), the disambiguation form, where to find slugs on the Timeweb dashboard, the `presetName` vs `resources` XOR contract, locked-sizing behavior, `SizingSwitchRequiresRecreate` recovery path.
- [ ] T056 [P] In `internal/controller/shared/resolver/dimensions.go`, add the K8s-readiness forward-compat dimension registrations from data-model.md §2.2: `ServerConfigurator`, `KubernetesMasterPreset` (filter `type=="master"`), `KubernetesWorkerPreset` (filter `type=="worker"`), `KubernetesVersion`, `KubernetesNetworkDriver`, `AvailabilityZone` (Derive over `KubernetesMasterPreset`/`KubernetesWorkerPreset` payload). Each registration ships with at least a smoke-level table-driven test in `dimensions_test.go` proving the registration is discoverable; full per-dimension `Resolve` coverage lands with the future K8s feature.
- [ ] T057 Update top-level `README.md` to mention the dual-scope `ProviderConfig` pair, the `forProvider` XOR shape on `ContainerRegistry` and `S3Bucket`, and link to `docs/presets.md`. Remove any MVP-era references to `ContainerRegistryPreset` or the old single cluster-scoped `ProviderConfig`.
- [ ] T058 [P] Update `apis/v1alpha1/doc.go`, `apis/containerregistry/v1alpha1/doc.go`, `apis/objectstorage/v1alpha1/doc.go` godoc to reflect the refactored shapes. Remove any godoc still referring to "axis", "AxisSwitchRequiresRecreate", or `ContainerRegistryPreset`.
- [ ] T059 [P] Run `golangci-lint` via `go run` from `hack/tools.go` against the full module; fix any new findings.
- [ ] T060 [P] Run the constitution §III audit script (or an ad-hoc grep) to confirm every `external` method in `internal/controller/{containerregistry,s3bucket}/` has the four required unit-test cases. If any are missing, add them.
- [ ] T061 Run `quickstart.md`'s verification steps end-to-end on a fresh kind cluster + the provider package built from this branch. Capture any divergence in `quickstart.md` and fix.
- [ ] T062 Validate the K8s-readiness claim (SC-007): walk the create-body fields of `POST /api/v1/k8s/clusters` and `POST /api/v1/k8s/clusters/{cluster_id}/groups` from `docs/openapi-timeweb.json` against the now-registered resolver dimensions; record the mapping in a comment block at the top of `internal/controller/shared/resolver/dimensions.go` (no new test — this is documentation for the K8s feature implementer).

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1 (Setup)**: T001–T004 — no dependencies; run as the first PR.
- **Phase 2 (Foundational)**: T005–T022 — depends on Phase 1. T005 must precede T009 (regen). T010 must precede T011–T018. T019–T022 depend on T011–T018.
- **Phase 3 (US1)**: T023–T041 — depends on Phase 2. **MVP target.**
- **Phase 4 (US2)**: T042–T049 — depends on Phase 3 (US1 wired the resolver into `external.go`; US2 fills the preset branch).
- **Phase 5 (US3)**: T050–T054 — depends on Phase 2 + Phase 3 (PC dual-scope + at least one MR using the resolver). Independent of US2 in principle.
- **Phase 6 (Polish)**: T055–T062 — depends on the user stories you ship in the release. T056 (K8s-readiness registrations) ships independent of any user story — could land alongside US1 but lives in Polish to keep US1 scope focused.

### User-story dependencies

- US1 → US2: US2 only fills the `presetName` branch left as `Unimplemented` in T029. US1 must merge first.
- US1 → US3: US3 requires the dual-PC machinery (foundational + US1's external refactor). US3 does not depend on US2.
- US2 ↔ US3: independent. Can be staffed in parallel after US1 merges.

### Within-phase / within-story sequencing

- **Phase 2**: T005 (rename) ⊂ T009 (regen). T010 (interface) ⊂ T011–T018 (impl). T015 (registry) ⊂ T017 (Resolve wiring).
- **US1**: T023–T026 (deletion) ⊂ T027–T030 (CR refactor) ⊂ T031–T033 (S3 refactor) — though CR and S3 sub-orderings only need T031 to follow T027 for paste-style reuse, not strictly required. T034/T035 (Project/SshKey dual-PC) are independent — can run anytime in Phase 3. T036–T037 (regen) ⊂ T038–T041 (tests).
- **US2**: T042–T046 ⊂ T047–T049.
- **US3**: T050–T052 ⊂ T053–T054.

### Parallel opportunities

- Within Phase 2: T011, T012, T013, T014, T016 can all run in parallel after T010 lands.
- Within US1: T031 (S3 types), T034 (Project), T035 (SshKey) all parallel with the CR refactor track. T038 + T039 parallel. T040 + T041 parallel.
- Within US2: T045 + T046 parallel; T047 + T048 parallel.
- Within Polish: T055, T056, T058, T059, T060 all parallel.

---

## Parallel example: User Story 1

```bash
# After Phase 2 completes, in parallel:
Task T023: delete apis/containerregistry/v1alpha1/preset_types.go
Task T024: delete internal/controller/containerregistry/preset_reconciler.go
Task T025: delete internal/controller/containerregistry/preset_resolver.go
Task T031: rewrite apis/objectstorage/v1alpha1/types.go to XOR shape
Task T034: dual-PC support in internal/controller/project/
Task T035: dual-PC support in internal/controller/sshkey/

# Then sequentially: T027 → T028 → T029 → T030 (the CR refactor chain)
# Then T032 → T033 (S3 controller refactor)
# Then T036 → T037 (regen)
# Then in parallel: T038, T039, T040, T041
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Land Phase 1 (Setup) in one PR.
2. Land Phase 2 (Foundational) in one PR — large; the dual-PC refactor + the resolver package are both groundwork.
3. Land Phase 3 (US1) in one PR — operator-facing payoff: stable `resources`-based sizing works; PC rename ships; `ContainerRegistryPreset` deleted.
4. **STOP AND VALIDATE** on a real Timeweb account: apply the kuttl bundles `us1-resources-variant` and `us1-pc-migration` against a staging Timeweb token; verify both pass.
5. This is a shippable release. Tag and announce.

### Incremental delivery

1. After MVP ships, land US2 (Phase 4) in its own PR. Validates: named-tariff resolution; `PresetNotFound` UX.
2. Then US3 (Phase 5) — primarily a test/audit PR with one cross-cutting code change for sticky-403 cache behavior.
3. Then Polish (Phase 6), including the K8s-readiness dimension registrations (T056) and the operator docs (T055).

### Parallel team strategy

With multiple developers post-Foundational:

- Dev A: US1 (the longest path; owns the MR refactors).
- Dev B: After US1's MR refactor lands, picks up US2 (slim — only fills the preset branches).
- Dev C: After Foundational lands, picks up US3 (auditing + isolation tests + e2e).
- Dev D: Polish — `docs/presets.md`, K8s-readiness dimension registrations, README, lint cleanup.

---

## Notes

- `[P]` = different files, no dependency on incomplete tasks in the same phase.
- `[Story]` = traces the task to a specific user story for downstream cherry-pick or revert.
- Every task lists an exact file path or directory.
- Verify unit tests FAIL before implementation lands (constitution §III + TDD discipline).
- Commit per task or per logical group; the dependency chain inside US1 (especially T027→T029) makes per-task commits painful — group T027–T030 into one commit if preferred.
- Stop at each checkpoint to validate the slice independently before moving on.
- Forbidden: re-introducing the `Catalog` / `*Preset` / `*Configurator` CRDs from the 2026-05-20 design. Any sub-PR that does this is rejected as a design regression.
