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

- [X] T005 Renamed the MVP cluster-scoped `ProviderConfig` into a new file `apis/v1alpha1/clusterproviderconfig_types.go` as `ClusterProviderConfig` (`scope=Cluster`). Added two XValidation rules: `credentials.source == 'Secret'` and `has(credentials.secretRef.namespace) && != ''`. Registered in `groupversion_info.go` as a separate kind alongside `ProviderConfig`.
- [X] T006 [P] `apis/v1alpha1/providerconfig_types.go` now defines a **namespaced** `ProviderConfig` (`scope=Namespaced`). Shares the same `ProviderCredentials` shape as `ClusterProviderConfig`; XValidation rules forbid a non-empty `secretRef.namespace` that doesn't equal the PC's own namespace.
- [X] T007 `apis/v1alpha1/providerconfigusage_types.go` keeps the namespaced PCU; new `apis/v1alpha1/clusterproviderconfigusage_types.go` adds the cluster-scoped twin. Both embed `xpv2.TypedProviderConfigUsage` (matches v2 ModernManaged tracker contract).
- [X] T008 Rewrote `apis/v1alpha1/managed.go` with accessor methods for all four kinds (PC × 2, PCU × 2). Introduced the `CredentialedProviderConfig` interface so connectors don't care which kind matched; `PCKindNamespaced` / `PCKindCluster` constants serve as the `providerConfigRef.kind` discriminator. Connector dual-PC lookup + Secret resolution lives in a new shared helper `internal/controller/shared/credentials.go`'s `ResolveToken(ctx, kube, mrNamespace, pcRef)`, exercised by 8 sub-tests in `credentials_test.go` (nil ref, namespaced hit, fallback to cluster, explicit cluster kind, namespaced-wins-over-cluster, neither-found, unknown-kind, missing-secret, empty-key). All four MR connectors (sshkey, project, s3bucket, containerregistry × 2 kinds) now call `shared.ResolveToken`; per-package `resolveToken` helpers deleted. Providerconfig reconciler registers one reconciler per PC kind (`setupNamespaced` + `setupCluster`).
- [X] T009 `make generate` clean. `package/crds/` now carries 4 PC-side CRDs: `providerconfigs` (Namespaced), `providerconfigusages` (Namespaced), `clusterproviderconfigs` (Cluster), `clusterproviderconfigusages` (Cluster). Both `*providerconfigs` CRDs have the source-enum + namespace-rule XValidation pair. Build green, all tests green.

### Internal resolver package (FR-006, FR-007, FR-011, FR-012, FR-013, FR-016, FR-017; contract: resolver-internal)

- [X] T010 `internal/controller/shared/resolver/resolver.go` defines the public surface: `Resolver` interface (Resolve + Invalidate), `PCRef`, `Dimension`, `DimensionKind` (Preset|Configurator|Enum), `PresetInput/Output`, `ConfiguratorInput/Output`, `EnumInput/Output`, `Options{TTL,Now}` with `MinTTL`/`MaxTTL`/`DefaultTTL` constants and `clampTTL` bounds enforcement.
- [X] T011 [P] `cache.go` — `(PCRef, Dimension)`-keyed Go map guarded by `sync.RWMutex`, passive expiry (next-access TTL check, no background sweeper), restart-cold by construction. Sticky on `ErrCatalogUnauthorized`, NOT cached on `ErrCatalogTransient`. Clock injectable via `Options.Now` for tests.
- [X] T012 [P] Coalescing is integrated into `cache.go` via `golang.org/x/sync/singleflight.Group`; concurrent misses on the same `(pcRef|dim)` key share a single in-flight upstream fetch (FR-011). Covered by `TestCacheConcurrentMissCoalesced` and `TestResolver_ConcurrentMissCoalesced`.
- [X] T013 [P] `slug.go` — `Slugify(descShort, location)` collapses non-`[a-z0-9-]` runs to "-" and lowercases; `MatchPresetSlug` returns the upstream ID for the canonical form OR for the explicit `<base>-<id>` disambiguator form (FR-008); returns typed `ErrPresetNotFound` / `ErrPresetAmbiguous` (with valid-slug hint or disambiguator suggestion respectively). The MVP `preset_resolver.go` was the catalog-CRD lookup style and is slated for deletion in T025 — no MVP logic ported here; the new slug code starts from the contract.
- [X] T014 [P] `select_configurator.go` — `SelectConfigurator(input, entries, dimensionID)` implements the 4-step algorithm: exact-match filter → capability filter (`Min/Step/Max` bounds) → tightest-fit sort on `(max_cpu, max_ramMB, max_diskGB)` → lowest-UpstreamID tiebreak. Zero survivors return `NoConfiguratorAvailableError` with the closest-rejected entry + reason.
- [X] T015 `dimensions.go` — `defaultRegistry()` ships **two** dimensions: `ContainerRegistryPreset` (fetcher `GetRegistryPresetsWithResponse` → `[]PresetEntry`) and `S3BucketPreset` (fetcher `GetStoragesPresetsWithResponse` → `[]PresetEntry`). No configurator registrations for CR / S3 per the spec clarification. `CatalogClient` is a narrow interface (only the two typed-response methods) so tests can supply a fake. Server / Kubernetes registrations land alongside their feature work.
- [X] T016 [P] `errors.go` — sentinels (`ErrPresetNotFound`, `ErrPresetAmbiguous`, `ErrNoConfiguratorAvailable`, `ErrDimensionValueNotFound`, `ErrCatalogUnauthorized`, `ErrCatalogTransient`, `ErrInvalidInput`, `ErrUnknownDimension`) plus wrapped variants (`PresetNotFoundError`, `PresetAmbiguousError`, `NoConfiguratorAvailableError`, `DimensionValueNotFoundError`) carrying operator-actionable context (slug, valid-slug list capped 20, colliding IDs, closest-rejected reason).
- [X] T017 `resolve.go` — `New(client, opts)` returns the production Resolver. `Resolve` dispatches: cache lookup → singleflight on miss via `cache.getOrFetch` → kind-specific resolution (`MatchPresetSlug` / `SelectConfigurator` / set-membership for Enum). Mismatched input shape → `ErrInvalidInput`; unregistered dimension → `ErrUnknownDimension`.
- [X] T018 `Resolve.Invalidate(pcRef, dim)` evicts the entry. Inline godoc on `Resolver.Invalidate` states the FR-013 caller contract — callers MUST invoke this on any upstream 4xx involving a previously-cached upstream ID so the next reconcile re-fetches.

### Foundational tests (constitution §III)

- [X] T019 [P] `cache_test.go` — 6 sub-tests covering: TTL clamp, fresh-hit (1 fetch for N gets), TTL expiry (re-fetch after clock advance), concurrent miss coalesced into one fetch (50 goroutines, gated callback, asserts call count == 1), sticky 401 (1 fetch reused across attempts), transient 5xx not cached (N attempts → N fetches), `Invalidate` forces refetch.
- [X] T020 [P] `slug_test.go` — `TestSlugify` (8 sub-tests: basic, no-location, only-location, empty, caps+spaces, punctuation-collapse, trailing-dash trim, non-ascii), `TestMatchPresetSlug` (5 sub-tests: unique-match, not-found with valid-slug hint, ambiguous with collision list + disambiguator hint, explicit disambiguator form, disambiguator-with-matching-id), `TestSlugWithID`.
- [X] T021 [P] `select_configurator_test.go` — 7 sub-tests against a 3-entry fixture: tightest-fit wins, only-larger-fits, filter mismatch, sizing out-of-bounds (closest-rejected populated), step misalignment, empty entries (explicit error), tie on max bounds → lowest UpstreamID wins.
- [X] T022 [P] `resolver_test.go` — `fakeCatalog` implements `CatalogClient` with per-method counters + a `gate` channel for coalescing. Covers Preset success, not-found, cache hits across calls, Invalidate forces refetch, concurrent-miss coalesced (20 goroutines → 1 fetch), Unauthorized 401 sticky in cache, Transient 503 not cached, unknown dimension, mismatched input kind. counterfeiter wasn't needed — the interface is narrow enough to hand-roll the fake (3 fields per response) and the test reads better than a generated stub would.

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

- [X] T023 [US1] Deleted `apis/containerregistry/v1alpha1/preset_types.go`; removed `ContainerRegistryPreset*` GVK constants and SchemeBuilder registration from `groupversion_info.go`; deleted `package/crds/containerregistry.m.timeweb.crossplane.io_containerregistrypresets.yaml`.
- [X] T024 [US1] Deleted `internal/controller/containerregistry/preset_reconciler.go` (the MVP timer-based catalog poller) and the `SetupPresetReconciler` call from `controller.go`'s `SetupAll`.
- [X] T025 [US1] Deleted `internal/controller/containerregistry/preset_resolver.go`. No MVP slug logic carried forward — the new resolver primitive starts from the spec.
- [X] T026 [US1] Trimmed `SetupOptions` to just `PollInterval` in `controller.go`; dropped `preset-sync-interval` and `preset-target-namespace` flags from `cmd/provider/main.go` and the `PresetSyncInterval`/`PresetNamespace`/`PresetPCName` fields from the controller setup wiring.
- [X] T027 [US1] Rewrote `apis/containerregistry/v1alpha1/registry_types.go`: `ContainerRegistryParameters{Name, Description, PresetName *string, Resources *ContainerRegistryResources{DiskGB}, ProjectID}`, kubebuilder CEL XValidation `(has(presetName) ? 1 : 0) + (has(resources) ? 1 : 0) == 1`, observation has `LockedPresetID *int64` XOR `LockedResources *ContainerRegistryLockedResources{DiskGB}`. No `lockedConfiguratorID` — Container Registry has no configurator upstream.
- [X] T028 [US1] Dual-PC support already in place via the shared `internal/controller/shared/credentials.go` `ResolveToken` helper (delivered with T008). Connector field `presetNamespace` dropped; connector now constructs the resolver per reconcile via `resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{})` and a `PCRef{Kind, Name, Namespace}` built from the MR's `spec.providerConfigRef`. (`managed.WithProviderConfigKinds(...)` doesn't exist in crossplane-runtime/v2 — dual-PC lookup is per-connector, not per-controller-setup.)
- [X] T029 [US1] Rewrote `registry_external.go`. Create: resolver-driven presetName path (`resolver.Resolve` with `DimContainerRegistryPreset`), records `lockedPresetID`; resources path sends `{disk}` directly, records `lockedResources{DiskGB}`. Observe: seeds locked variant on import; calls `sizingSwitchReason` and surfaces `SizingSwitchRequiresRecreate` when the spec switched sides. Update: re-observes for immutable-field drift, calls `sizingSwitchReason` (returns nil err + condition, no upstream PATCH on switch), within-variant PATCH otherwise. Delete: unchanged.
- [X] T030 [US1] `mapResolverErrorToCondition` translates `resolver.Err{PresetNotFound,PresetAmbiguous,CatalogUnauthorized,CatalogTransient}` → matching `shared.Reason*` constants on `Synced=False`. `NoConfiguratorAvailable` not wired — no configurator dimension is registered for ContainerRegistry. New reasons (`PresetNotFound`, `PresetAmbiguous`, `NoConfiguratorAvailable`, `SizingSwitchRequiresRecreate`, `CatalogUnauthorized`, `CatalogTransient`, `DimensionValueNotFound`) added to `internal/controller/shared/conditions.go`.
- [X] T031 [P] [US1] Rewrote `apis/objectstorage/v1alpha1/types.go`: `S3BucketParameters{Name, Type, StorageClass, PresetName *string, Resources *S3BucketResources{DiskMB}, Description, ProjectID}`. CEL XValidation rule mirrors CR. `StorageClass` is a free MR-level field outside the XOR (mutable bucket-policy choice). `S3BucketLockedResources{DiskMB}` lives on the observation.
- [X] T032 [US1] s3bucket connector now wires resolver + pcRef into the external (mirror of T028).
- [X] T033 [US1] Rewrote `internal/controller/s3bucket/external.go` mirroring the CR pattern: presetName path resolves via `DimS3BucketPreset`, resources path sends `Configurator{Disk: *float32}` (no `Id`) directly. Sizing-switch and immutable-name detection identical to CR. Resolver errors mapped via local `mapResolverErrorToCondition`.
- [X] T034 [P] [US1] No-op: Project connector already uses `shared.ResolveToken` (delivered with T008). No setup-level changes required since `managed.WithProviderConfigKinds(...)` isn't a v2 helper — the dual-PC fallback is per-connector.
- [X] T035 [P] [US1] No-op: SSHKey connector same as T034.
- [X] T036 [US1] `make generate` clean. `package/crds/` now: dropped `containerregistry.m.timeweb.crossplane.io_containerregistrypresets.yaml`; added `timeweb.crossplane.io_clusterproviderconfigs.yaml` + `timeweb.crossplane.io_clusterproviderconfigusages.yaml`; `containerregistries.containerregistry.m.timeweb.crossplane.io` and `s3buckets.objectstorage.m.timeweb.crossplane.io` schemas updated with the `presetName/resources` shape + CEL XOR rule.
- [ ] T037 [US1] Update `package/crossplane.yaml` and any MRD list (per the xpkg-allowed-kinds memo) to drop `ContainerRegistryPreset` and add the new `ClusterProviderConfig` kind.
- [X] T038 [P] [US1] Rewrote `internal/controller/containerregistry/registry_external_test.go` with `fakeResolver`. Covers Observe (Success / NotFound / TransientError / CredentialsUnavailable / SizingSwitch_DetectedByObserve), Create (Success_PresetName / Success_Resources / PresetNotFound / UpstreamTerminalError), Update (ImmutableNameChange_Rejected / SizingSwitch_Rejected / Success_WithinPresetVariant), Delete (Success / NotFound_Idempotent / TransientError). All passing.
- [X] T039 [P] [US1] Rewrote `internal/controller/s3bucket/external_test.go` with `fakeResolver`. Parallel structure to T038: Observe with SizingSwitch detection, Create both variants, PresetNotFound, ImmutableNameChange, SizingSwitch_Rejected_NoPATCH. All passing.
- [ ] T040 [US1] Author kuttl bundle `test/e2e/us1-resources-variant/` (out of scope for the in-session code-only sprint; ready for next session).
- [ ] T041 [P] [US1] Author kuttl bundle `test/e2e/us1-pc-migration/` (same).

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
