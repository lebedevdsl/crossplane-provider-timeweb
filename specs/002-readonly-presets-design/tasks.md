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

### Delete the catalog-as-CRDs surface + refactor `ContainerRegistry` MR

- [X] T023 [US1] Deleted `apis/containerregistry/v1alpha1/preset_types.go`; removed `ContainerRegistryPreset*` GVK constants and SchemeBuilder registration from `groupversion_info.go`; deleted `package/crds/containerregistry.m.timeweb.crossplane.io_containerregistrypresets.yaml`.
- [X] T024 [US1] Deleted `internal/controller/containerregistry/preset_reconciler.go` (the MVP timer-based catalog poller) and the `SetupPresetReconciler` call from `controller.go`'s `SetupAll`.
- [X] T025 [US1] Deleted `internal/controller/containerregistry/preset_resolver.go`. No MVP slug logic carried forward — the new resolver primitive starts from the spec.
- [X] T026 [US1] Trimmed `SetupOptions` to just `PollInterval` in `controller.go`; dropped `preset-sync-interval` and `preset-target-namespace` flags from `cmd/provider/main.go` and the `PresetSyncInterval`/`PresetNamespace`/`PresetPCName` fields from the controller setup wiring.
- [X] T027 [US1] Rewrote `apis/containerregistry/v1alpha1/registry_types.go` — **shape evolved during the e2e debug session**: ends as `ContainerRegistryParameters{Name, Description, InitialSizeGB int64, Location *string, ProjectID}` with `InitialSizeGB` as a CEL enum `[5;10;25;50;75;100]`. No XOR, no `presetName`, no `resources` block — the user-facing field is the size (matching the Timeweb dashboard's tier picker) and the controller resolves to `preset_id` via `resolver.PresetBySizeInput{DiskGB, Location?}` against the registered preset dimension. Observation: `LockedPresetID *int64`. (Original CRD-XOR draft was implemented, then collapsed to enum-only after the upstream `configurator.id` requirement made the operator-friendly custom path infeasible — see spec.md §Clarifications "initialSizeGB" 2026-05-31.)
- [X] T028 [US1] Dual-PC support already in place via the shared `internal/controller/shared/credentials.go` `ResolveToken` helper (delivered with T008). Connector field `presetNamespace` dropped; connector now constructs the resolver per reconcile via `resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{})` and a `PCRef{Kind, Name, Namespace}` built from the MR's `spec.providerConfigRef`. (`managed.WithProviderConfigKinds(...)` doesn't exist in crossplane-runtime/v2 — dual-PC lookup is per-connector, not per-controller-setup.)
- [X] T029 [US1] Rewrote `registry_external.go` — **final shape**: Create resolves `initialSizeGB + location?` via `resolver.Resolve(..., resolver.PresetBySizeInput{...})` against `DimContainerRegistryPreset` → upstream `preset_id`, records `lockedPresetID`. Observe seeds `lockedPresetID` on import. Update re-resolves and PATCHes with the new `preset_id` (within-preset tier moves work; the XOR sizing-switch handling was removed alongside the `resources` field). Delete unchanged. Also wires docker-credential synthesis: registry name (as username) + the operator's API token (as password) — derived in `deriveRegistryCredentials`; endpoint is `<name>.registry.twcstorage.ru`. The Observe path treats credential-derivation as best-effort — Ready=True still flows when the registry exists upstream, falling back to an endpoint-only connection-Secret if creds can't be synthesized. TODO marker tracks the future per-registry credential API (Timeweb-creds).
- [X] T030 [US1] `mapResolverErrorToCondition` translates `resolver.Err{PresetNotFound,PresetAmbiguous,CatalogUnauthorized,CatalogTransient}` → matching `shared.Reason*` constants on `Synced=False`. `NoConfiguratorAvailable` not wired — no configurator dimension is registered for ContainerRegistry. New reasons (`PresetNotFound`, `PresetAmbiguous`, `NoConfiguratorAvailable`, `SizingSwitchRequiresRecreate`, `CatalogUnauthorized`, `CatalogTransient`, `DimensionValueNotFound`) added to `internal/controller/shared/conditions.go`.
- [X] T031 [P] [US1] Rewrote `apis/objectstorage/v1alpha1/types.go` — **final shape**: `S3BucketParameters{Name, Type, StorageClass, InitialSizeGB int64, Location *string, Description, ProjectID}` with `InitialSizeGB` CEL enum `[1;10;100;250]`. `StorageClass` is a free MR-level field (mutable bucket-policy choice). Observation: `LockedPresetID *int64`. Same enum-only pivot as CR.
- [X] T032 [US1] s3bucket connector now wires resolver + pcRef into the external (mirror of T028).
- [X] T033 [US1] Rewrote `internal/controller/s3bucket/external.go` — **final shape**: Create resolves `(initialSizeGB, location?, storageClass)` via `resolver.Resolve(..., PresetBySizeInput{...})` against `DimS3BucketPreset` → upstream `preset_id`; records `lockedPresetID`. Update re-resolves and PATCHes. Immutable-name detection retained. Resolver errors mapped via local `mapResolverErrorToCondition`. Also fixes the S3 disk-unit mismatch: `/api/v1/presets/storages` returns `disk` in MB (e.g. 1024 = 1 GB, 256000 = 250 GB) while the CR equivalent returns GB; the S3 fetcher in `internal/controller/shared/resolver/dimensions.go` normalizes to GB before storing in `PresetEntry.DiskGB`.
- [X] T034 [P] [US1] No-op: Project connector already uses `shared.ResolveToken` (delivered with T008). No setup-level changes required since `managed.WithProviderConfigKinds(...)` isn't a v2 helper — the dual-PC fallback is per-connector.
- [X] T035 [P] [US1] No-op: SSHKey connector same as T034.
- [X] T036 [US1] `make generate` clean. `package/crds/` now: dropped `containerregistry.m.timeweb.crossplane.io_containerregistrypresets.yaml`; added `timeweb.crossplane.io_clusterproviderconfigs.yaml` + `timeweb.crossplane.io_clusterproviderconfigusages.yaml`; `containerregistries.containerregistry.m.timeweb.crossplane.io` and `s3buckets.objectstorage.m.timeweb.crossplane.io` schemas updated with the `presetName/resources` shape + CEL XOR rule.
- [X] T037 [US1] `package/crossplane.yaml` description block refreshed: removed the stale "ContainerRegistryPreset land in Phase 4" note, replaced with the dual-PC pair + `initialSizeGB` sizing summary. README pointer updated. The CRD set under `package/crds/` is now 9 files (4 PC-side + 5 MR-side); no `containerregistrypresets.yaml` remains.
- [X] T038 [P] [US1] `internal/controller/containerregistry/registry_external_test.go` — `fakeResolver` keyed by `(DiskGB → upstream id)` mimicking `MatchPresetBySize`; covers Observe (Success / NotFound / TransientError / CredentialsFallback_EndpointOnly), Create (Success / PresetNotFound / UpstreamTerminalError), Update (ImmutableNameChange_Rejected / Success_ReResolveAndPatch), Delete (Success / NotFound_Idempotent). All passing post-pivot.
- [X] T039 [P] [US1] `internal/controller/s3bucket/external_test.go` — parallel structure to T038 against the S3 client; covers Observe / Create / Update / Delete with the size-based resolver fake.
- [X] T040 [US1] Done as a single unified kuttl bundle (`test/e2e/kuttl/tests/04-s3bucket/`) instead of a separate `us1-resources-variant` directory — the operator-facing variant collapsed to enum-only during the pivot, removing the need for a separate "resources variant" bundle. The bundle exercises Create + Ready + per-test deletion against the live Timeweb API with `initialSizeGB: 1`.
- [X] T041 [P] [US1] Done as part of the unified bundle. `test/e2e/kuttl/tests/01-providerconfig` was promoted into the wrapper (kuttl can't persist a PC across tests when `namespace:` is set); each test now references the wrapper-applied `ProviderConfig` by name. No separate `us1-pc-migration` bundle is needed for the v0.1 surface — the dual-PC lookup is exercised by every MR test through `shared.ResolveToken`.

**Checkpoint**: User Story 1 fully functional. Operators ship `ContainerRegistry` + `S3Bucket` via stable resources fields; PC dual-scope rename complete; `ContainerRegistryPreset` CRD + poller deleted; all MVP MRs continue to reconcile.

---

## Phase 4: User Story 2 — `presetName` resolution (Priority: P2) — **SUPERSEDED**

> **Status: Obsolete (2026-05-31).** The `presetName` operator-facing field
> never shipped. During the live-API e2e debug session, the operator-typed
> slug pattern (`start-ru-1`, `standard-ru-1-100`) was replaced with a
> **discrete-size enum** (`initialSizeGB`) — the latter matches the Timeweb
> dashboard's UX (operators pick from `1`/`10`/`100`/`250` GB tiers for
> S3 and `5`/`10`/`25`/`50`/`75`/`100` for Container Registry), eliminates
> slug-ambiguity failure modes (resolver still has slug-match code path
> for forward use by Server/K8s MRs, but no MR consumes it today), and
> drops the entire "what's my preset called" discovery burden from the
> operator. See spec.md §Clarifications 2026-05-31 (initialSizeGB) and
> the resolver's `PresetBySizeInput` + `MatchPresetBySize` in
> `internal/controller/shared/resolver/match_size.go`.
>
> What replaced US2: the existing US1 path delivers preset resolution
> end-to-end via the size-enum. Wire-up lives in T029/T033 as built;
> the controller calls `resolver.Resolve(..., PresetBySizeInput{...})`
> and the resolver matches by `(diskGB, location?, storageClass?)`. The
> error vocabulary (`PresetNotFound`, `PresetAmbiguous`) still applies —
> a bogus `location` is the most common way to hit `PresetNotFound`
> (covered by kuttl test 06-preset-not-found).

- [~] T042 [US2] **Obsolete** — `presetName` field doesn't exist. The size-enum path is wired in T029 against `PresetBySizeInput`.
- [~] T043 [US2] **Obsolete** — Update path re-resolves the preset by size on every reconcile already (see `registry_external.go::Update`).
- [~] T044 [US2] **Obsolete** — stale-cache recovery still applies in principle; the resolver's `Invalidate` is in place but not yet called from the MR external clients on 4xx. Tracked for the next iteration as a separate task: "wire resolver.Invalidate on upstream 4xx" (no spec or e2e dependency today).
- [~] T045 [P] [US2] **Done in US1** — the error→condition mapping landed in `mapResolverErrorToCondition` for both CR and S3 (T030/T033).
- [~] T046 [P] [US2] **Done in US1** — same.

### US2 unit tests

- [~] T047 [P] [US2] **Obsolete** — registry_external_test.go covers size-input matches (Success, PresetNotFound via the fakeResolver) and the runtime's ReconcileError override behavior.
- [~] T048 [P] [US2] **Obsolete** — same for s3bucket external_test.go.

### US2 e2e

- [X] T049 [US2] kuttl test `06-preset-not-found/` covers the PresetNotFound surfacing end-to-end against the live Timeweb API: applies a CR with `initialSizeGB: 5, location: "zz-nonexistent-99"`; asserts `Ready=False reason=Creating` + `Synced=False reason=ReconcileError`; the message field contains the resolver's full not-found error including the list of valid sizes.

**Checkpoint (revised)**: The operator-facing "name a tariff" affordance was implemented via `initialSizeGB` + `location` rather than free-form slugs. All three user stories are functionally delivered through Phase 3 + the post-Phase-3 redesign session.

---

## Phase 5: User Story 3 — Multi-credential isolation (Priority: P3)

**Goal**: A cluster running two `ProviderConfig`s (or one namespaced + one cluster-scoped) keeps each credential's resolver cache isolated; an unauthorized catalog endpoint on one PC does not affect MRs under another PC. Builds on US1+US2; the per-PCRef cache key was foundational, this phase verifies isolation under intentionally-divergent credentials.

**Independent Test**: Per spec.md US3 — apply two `ProviderConfig`s (one namespaced in `team-a`, one cluster-scoped), assign one a token that returns 403 on `/api/v1/configurator/registries`; MRs under that PC surface `Synced=False, reason=CatalogUnauthorized`; MRs under the other PC continue reconciling normally.

- [X] T050 [US3] Confirmed at code level: `internal/controller/shared/resolver/cache.go` `cacheKey{pc PCRef, dim Dimension}` keys on the full PCRef triple `(Kind, Name, Namespace)`. Two PCs of different kinds with the same name produce distinct cache keys — they never collide. (Unit-test addition deferred — see T053.)
- [X] T051 [US3] `cache.go::getOrFetch` stores the fetcher's error in `cacheEntry.err` only when `!isTransient(err)`. `ErrCatalogUnauthorized` is non-transient → cached → sticky until TTL expiry or `Invalidate`, per `contracts/resolver-internal.md`.
- [X] T052 [US3] Both `internal/controller/containerregistry/registry_external.go` and `internal/controller/s3bucket/external.go` `mapResolverErrorToCondition` map `resolver.ErrCatalogUnauthorized` → `shared.SyncedFalse(shared.ReasonCatalogUnauthorized, err.Error())`. The runtime overrides the reason to `ReconcileError` but preserves the upstream-detail message (same dynamic as observed for `PresetNotFound`).

### US3 unit tests

- [ ] ~~T053~~ **Superseded by T070** (Phase 7) — the multi-PC isolation unit test lands as part of the post-upstream-alignment work, against the simplified single-Spec connector. Keeping the case here as a forward marker.

### US3 e2e

- [ ] ~~T054~~ **Superseded by T071** (Phase 7) — the multi-PC kuttl bundle now uses the optional `TIMEWEB_E2E_TOKEN` env var the operator already has provisioned. Wired into `test/e2e/scripts/kuttl.sh` as a skip-if-unset gate so the single-token e2e path remains the default.

**Checkpoint**: All three user stories independently functional. Provider ships an MVP-superseding release: stable user-facing MR shapes, internal catalog resolution, dual-scope ProviderConfig.

---

## Phase 6: Polish & K8s-readiness forward-compat

**Purpose**: Documentation, K8s-readiness commitments (SC-007), final cleanup.

- [X] T055 [P] `docs/presets.md` documents the post-pivot operator surface: `initialSizeGB` enum per MR kind (with the exact allowed values), the optional `location` field, the controller's resolver flow, the PresetNotFound condition shape + valid-list hint, the dashboard's "Произвольная" (Custom) path's not-yet-supported status with its TODO, and the Container Registry docker login (registry name + Timeweb API token until per-registry credentials ship). The original slug rule + SizingSwitchRequiresRecreate vocabulary is no longer operator-visible (those code paths exist in the resolver for forward use by Server/K8s MRs but aren't on any v0.1 CRD).
- [X] T056 [P] Registered the six forward-compat dimensions in `internal/controller/shared/resolver/dimensions.go`: `DimServerConfigurator` (kind=Configurator), `DimKubernetesMasterPreset`/`DimKubernetesWorkerPreset` (Preset), `DimKubernetesVersion`/`DimKubernetesNetworkDriver`/`DimAvailabilityZone` (Enum). All six share a `fetchUnwired` stub that returns the new `ErrDimensionFetcherUnwired` sentinel (added to `errors.go`) so an accidental Resolve before the K8s feature lands fails loudly rather than returning misleading nil. Smoke test `TestDefaultRegistry_Discoverable` in new `dimensions_test.go` table-drives all eight registrations: asserts presence + correct kind, that the registry has exactly 8 entries (no drift), and that the six stubs return `ErrDimensionFetcherUnwired`. `responseFilter` (`type=="master"`/`"worker"`) and `enumDerive` (AvailabilityZone over preset list) are still data-model concerns — they'll be implemented in the K8s feature when fetcher bodies materialize, alongside the oapi-codegen tag allowlist expansion. Tests green.
- [X] T057 Created the top-level `README.md` (the repo had none). Covers: kinds table, namespaced + cluster-scoped `ProviderConfig` pair with the dual-reference fallback, `initialSizeGB` enum (linked to `docs/presets.md` for depth), how to run the e2e suite, dev quickstart. The `forProvider` XOR pivot is documented in the linked presets doc rather than the README to keep the README short.
- [X] T058 [P] Updated `apis/v1alpha1/doc.go` (now describes the dual-PC pair + the four kinds it holds), `apis/containerregistry/v1alpha1/doc.go` (no more `ContainerRegistryPreset` mention; describes the `initialSizeGB` enum + observe-only repository kind), and `apis/objectstorage/v1alpha1/doc.go` (mentions `initialSizeGB` enum + `storageClass`). No remaining "axis" or `SizingSwitchRequiresRecreate` references in any package-level godoc.
- [X] T059 [P] `make lint` clean (0 issues) after this session's cleanup: 3× gofmt fixes, drop unused `mu` in resolver_test fakeCatalog, rename `ctx` → `_` in unused-parameter positions, add inline `//nolint:revive` for the oapi-codegen-mirroring `Id`/`ResponseId` struct fields, add inline `//nolint:staticcheck` for the 3× `scheme.Builder` SA1019 deprecations (the v2-suggested replacement is the apimachinery SchemeBuilder; pending project-wide migration). New lint-config exclusion exempts `apis/*/managed.go` from the `revive`/`exported` rule — those files hold ~50 trivial interface-forwarder methods (`GetCondition`, `SetConditions`, etc.) whose semantics are already obvious from receiver+method-name.
- [X] T060 [P] Constitution §III audit done. Gaps filled (success + not-found + transient + terminal per method) in both `internal/controller/containerregistry/registry_external_test.go` and `internal/controller/s3bucket/external_test.go`. Added: Observe/TerminalError (CR + S3 missing it), Create/TransientError (both), Update/{NotFound_OnInitialGET, TransientError, TerminalError} (both — Update had only Success + ImmutableNameChange before), Delete/{TransientError, TerminalError} (both — Delete had only Success + NotFound_Idempotent before). 10 new test cases total; all green via `go test ./internal/controller/s3bucket/... ./internal/controller/containerregistry/...`.
- [X] T061 Rewrote `quickstart.md` to match the shipped reality and the project's actual e2e flow (k3d, not kind). Replaced the stale `presetName XOR resources` walkthrough with the enum-only `initialSizeGB` UX (allowed tiers per kind, derived `preset_id`, mutable `storageClass`, immutable `location`). Pointed verification at `make e2e` (k3d + local registry + kuttl) and `kubectl explain initialSizeGB` for manual checks, dropped the stale `kind` / `lockedConfiguratorID` / `SizingSwitchRequiresRecreate` references, refreshed the troubleshooting matrix (PresetNotFound triple, CatalogUnauthorized, CredentialsPending diagnosis, the stale-build configurator-null symptom), and updated "what's coming next" to point at the six newly-registered forward-compat dimensions.
- [X] T062 Walked both K8s create-bodies (`createCluster`, `createClusterNodeGroup`) from `docs/openapi-timeweb.json` and recorded the field→dimension mapping as a package-level comment block at the top of `internal/controller/shared/resolver/dimensions.go`. Mapping: `k8s_version`→`DimKubernetesVersion`, `availability_zone`→`DimAvailabilityZone`, `network_driver`→`DimKubernetesNetworkDriver`, cluster `preset_id`→`DimKubernetesMasterPreset` (XOR with `configuration`→`DimServerConfigurator`), group `preset_id`→`DimKubernetesWorkerPreset` (same XOR). Free-form scalars (name, description, counts, IDs) and recursive objects (cluster_network_cidr, maintenance_slot, oidc_provider, worker_groups[i]) explicitly called out as CRD-layer / non-resolver concerns. SC-007 ready: every operator-resolvable K8s field has a registered dimension; the K8s feature only has to wire fetchers.

---

## Phase 7: Upstream-alignment simplifications (post-2026-05-31 clarification)

**Purpose**: Implement the three decisions recorded in spec.md → Clarifications → "Session 2026-05-31 (upstream-alignment simplifications)". (1) Collapse `ProviderCredentials` + `ClusterProviderCredentials` into one shared `ProviderConfigSpec`; (2) drop the dual-reference fallback in favour of a hard `Kind` switch with a clear `InvalidProviderConfigRef` reason; (3) defer the `apis/cluster/` + `apis/namespaced/` package split (tracked under "Explicit deferrals" below — not a Phase 7 task).

Justified by upstream evidence (research.md R-2 addendum) — provider-kubernetes, provider-helm, and provider-upjet-azure all ship the single-Spec + hard-switch pattern. The interim split-Spec implementation in this repo was a bespoke divergence; the alpha-stage "no external consumers" assumption (spec.md Assumption 5) lets us break the CRD shape freely.

### API surface

- [X] T063 Collapsed both PC types onto the single shared `ProviderConfigSpec`. `ProviderCredentials` now wraps `xpv2.SecretKeySelector` (full `SecretRef{name, namespace, key}`); `ClusterProviderCredentials` deleted entirely. `ClusterProviderConfig.Spec` uses `ProviderConfigSpec` directly (no separate `ClusterProviderConfigSpec` type — same Spec, different scope). Per-PC CEL namespace rules removed; defaulting + cross-namespace rejection live in `internal/controller/shared/credentials.go::resolveSecretNamespace`. `apis/v1alpha1/managed.go` collapsed accessors via shared `credSource`/`credSecretName`/`credSecretKey`/`credSecretNamespace` helpers.
- [X] T064 [P] `apis/v1alpha1/zz_generated.deepcopy.go` regenerated via `make generate`. `controller-gen` emitted DeepCopy for the shared `ProviderConfigSpec`/`ProviderCredentials` once; `ClusterProviderConfig` reuses it directly. Build clean.
- [X] T065 CRDs regenerated. Visual diff between `timeweb.crossplane.io_providerconfigs.yaml` and `timeweb.crossplane.io_clusterproviderconfigs.yaml` (modulo identity fields): only `scope:` (Namespaced vs Cluster), the kind-level description, and the cluster-side `SECRET-NS` printer column (intentional — operator needs the namespace visible on cluster PCs). The credentials/SecretRef block is byte-identical.

### Connector behavior

- [X] T066 Rewrote `internal/controller/shared/credentials.go::ResolveToken`. New behavior: hard switch on `pcRef.Kind` — `"ProviderConfig"` looks up in MR namespace only; `"ClusterProviderConfig"` and empty kind (runtime default) look up cluster-scoped. No silent fallback in either direction. Added `shared.ErrInvalidProviderConfigRef` sentinel wrapped via `fmt.Errorf("%w: …")` for every operator-side mistake (unknown kind, named PC missing, empty cluster-PC namespace, cross-namespace secret on namespaced PC). New `resolveSecretNamespace` helper defaults secret namespace to PC namespace on the namespaced kind, errors on a different namespace, errors on empty namespace for cluster kind.
- [X] T067 [P] Added `ReasonInvalidProviderConfigRef` to `internal/controller/shared/conditions.go`. No per-controller wiring needed — all four connectors propagate `ResolveToken`'s error from `Connect()` via `fmt.Errorf("%w …", err)`, which preserves the sentinel chain. crossplane-runtime surfaces the error as `Synced=False, reason=ReconcileError` with the typed message visible (same dynamic as `PresetNotFound`). The constant lives for documentation + future direct-mapping use, plus matches the `e2e-bundle 07b-invalid-pc-kind` assert.

### Tests

- [X] T068 [P] Rewrote `internal/controller/shared/credentials_test.go` (the existing file co-located with `credentials.go`; no separate `pcref_test.go` needed). Covers all 8 cases from the spec plus 3 defence-in-depth: nil pcRef → InvalidProviderConfigRef; namespaced-PC namespace defaulting; explicit-equal namespace; cross-namespace rejected; cluster-PC explicit namespace; cluster-PC empty namespace rejected; empty kind defaults to cluster; garbage kind rejected; namespaced-missing-does-NOT-fall-back-to-cluster (and the inverse); unsupported credentials.source rejected; missing Secret + empty Secret key surfaces non-typed infra errors. All green via `errors.Is(err, ErrInvalidProviderConfigRef)`.
- [X] T069 [P] Added `TestCacheKey_PerPCRefIsolation` to `internal/controller/shared/resolver/cache_test.go`. Seeds 6 distinct PCRefs (Kind × Name × Namespace permutations including empty-kind runtime-default) each with a value tagged by its own ref; re-reads with a fetcher that errors on call. Assert: every readback returns its OWN ref's tag (no leak), and the cache holds exactly 6 entries (no key collision). Closes the open T053 — isolation is structural via the map key, and this test pins the contract.

### E2E

- [X] T070 [US3] Extended `test/e2e/scripts/kuttl.sh`: optional `TIMEWEB_E2E_TOKEN`, when set, provisions namespace `e2e-team-b`, Secret `timeweb-credentials` in that namespace, `ProviderConfig e2e-secondary` (namespaced, secretRef omits namespace → controller defaults), and `ClusterProviderConfig e2e-shared` (bound to the secondary secret with explicit namespace). When unset, the wrapper logs the skip and `rm`s the 07-* and 07b-* bundle dirs from the tmp copy before invoking kuttl (kuttl runs all remaining tests as a no-op skip). Refreshed `test/e2e/Makefile.test`'s `e2e.test` help text to document the new env var.
- [X] T071 [US3] Created `test/e2e/kuttl/tests/07-multi-pc-isolation/` with `01-create.yaml` (two SSHKeys in `e2e-team-b` — one via `kind: ProviderConfig` → `e2e-secondary`, one via `kind: ClusterProviderConfig` → `e2e-shared`) and `01-assert.yaml` (`[Ready=True, Synced=True]` on both). Created `test/e2e/kuttl/tests/07b-invalid-pc-kind/` with an SSHKey carrying `providerConfigRef.kind: BogusKind` and asserting `[Synced=False, reason=ReconcileError]`. Both bundles are gated on `TIMEWEB_E2E_TOKEN` via the wrapper-script `rm` in T070. Closes the open T054.

### Docs + examples cleanup

- [X] T072 [P] (a) `README.md` PC table rewritten to describe per-kind secretRef.namespace behavior; "dual-reference fallback" paragraph replaced with the hard-switch + `InvalidProviderConfigRef` story; namespaced PC example now shows `secretRef.namespace` omitted with a comment explaining the default. (b) `docs/presets.md` got a new "ProviderConfig resolution" subsection above "Locked-preset semantics" naming the kinds, defaulting behavior, hard-switch rule, and `InvalidProviderConfigRef` reason. (c) `apis/v1alpha1/doc.go` rewritten to name the four kinds, the single shared `ProviderConfigSpec`, the per-kind namespace semantics, and the no-fallback rule.
- [X] T073 [P] Audit done: `grep -rnE "kind: ProviderConfig" test/e2e/kuttl` returned only `providerConfigRef.kind` lines on MR specs (no inline PC manifests in test bundles — the wrapper script is the sole source of PC definitions). Wrapper-script-applied PCs already match the contract: primary `ProviderConfig` (in test namespace) omits `secretRef.namespace`; secondary `ProviderConfig e2e-secondary` (provisioned when TIMEWEB_E2E_TOKEN is set) likewise omits it; `ClusterProviderConfig e2e-shared` keeps `namespace:` explicit. `docs/presets.md` + `quickstart.md` examples already aligned via T072 and the earlier T061.

### Explicit deferrals (NOT tasks — for tracking only)

- **D-1** [LOW confidence] **Split `apis/v1alpha1/` into `apis/cluster/v1alpha1/` + `apis/namespaced/v1alpha1/`** matching the upstream layout used by `crossplane-contrib/provider-kubernetes`, `provider-helm`, and `provider-upjet-azure` (both packages declaring a kind literally named `ProviderConfig`, disambiguated by API group). Deferred per spec clarification Q3=C — the current single-package layout is internally consistent and the refactor cost (every MR import path, every manifest `apiVersion:`, every CRD group) is not justified by cosmetic alignment alone. Reconsider when a 3rd PC scope appears or alongside the K8s feature if its scope can absorb the churn.

**Checkpoint**: Provider matches the v2-modern ecosystem shape (single shared `ProviderConfigSpec`, hard-switch on `Kind`, no silent fallback) while preserving the dual-scope user-facing semantics. Multi-PC isolation has both a unit test (T069) and an e2e bundle (T071) gated on the optional `TIMEWEB_E2E_TOKEN` env var. The package-layout split (D-1) is documented as a deferral, not as outstanding work.

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1 (Setup)**: T001–T004 — no dependencies; run as the first PR.
- **Phase 2 (Foundational)**: T005–T022 — depends on Phase 1. T005 must precede T009 (regen). T010 must precede T011–T018. T019–T022 depend on T011–T018.
- **Phase 3 (US1)**: T023–T041 — depends on Phase 2. **MVP target.**
- **Phase 4 (US2)**: T042–T049 — depends on Phase 3 (US1 wired the resolver into `external.go`; US2 fills the preset branch).
- **Phase 5 (US3)**: T050–T054 — depends on Phase 2 + Phase 3 (PC dual-scope + at least one MR using the resolver). Independent of US2 in principle.
- **Phase 6 (Polish)**: T055–T062 — depends on the user stories you ship in the release. T056 (K8s-readiness registrations) ships independent of any user story — could land alongside US1 but lives in Polish to keep US1 scope focused.
- **Phase 7 (Upstream-alignment)**: T063–T073 — depends on Phase 2 (foundational PC + connector code exists) and Phase 5 (US3 multi-PC story is the consumer of the new isolation tests). Triggered by the 2026-05-31 clarification session. T063→T064→T065 is the sequential CRD chain (collapse → regen DeepCopy → regen CRD YAML). T066/T067 can land in parallel with T065 (they touch controller code, not generated files). T068/T069 are pure-unit tests parallelizable with everything else once their target code (T066/T067) compiles. T070→T071 is the e2e chain (wrapper script change must land before the new bundle that depends on it). T072/T073 are docs cleanups parallelizable with the tests.

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
- Within Phase 7: T064 + T067 + T068 + T069 + T072 + T073 all parallel after T063 lands. T070 + T071 sequential within the e2e track.

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
4. Then Upstream-alignment (Phase 7) — own PR. Reviewer focus: the CRD diff in T065 (visual) + the table-driven `pcref_test.go` cases in T068. The kuttl bundle (T071) is exercised in CI only if `TIMEWEB_E2E_TOKEN` is provisioned; otherwise it's a no-op.

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
