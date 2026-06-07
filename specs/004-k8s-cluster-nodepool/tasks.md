---
description: "Task list for 004-k8s-cluster-nodepool — KubernetesCluster + Nodepool + Addon MRs"
---

# Tasks: Managed Kubernetes (Cluster + Nodepool + Addon MRs)

**Input**: Design documents from `specs/004-k8s-cluster-nodepool/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Included — Constitution §III mandates Success/NotFound/Transient/Terminal unit tests for every `external` method; the spec adds kuttl e2e bundles per user story.

**Organization**: Tasks are grouped by user story (US1–US5 from spec.md) so each story can be implemented, tested, and merged independently. US1 is the MVP target.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: parallelizable (different files, no dependency on incomplete tasks in the same phase)
- **[Story]**: only on tasks inside a user-story phase (US1–US5)
- Setup, Foundational, and Polish phases carry no story label

## Path Conventions

Per `plan.md → Project Structure`:

- API types: `apis/kubernetes/v1alpha1/`
- Controllers: `internal/controller/kubernetes/`
- Shared resolver: `internal/controller/shared/resolver/`
- Generated client: `internal/clients/timeweb/generated/`
- Provider package (CRDs, metadata): `package/`
- Operator docs: `docs/`
- E2E suites: `test/e2e/`

---

## Phase 1: Setup

**Purpose**: Generator + dependency prep before any types or controllers change.

- [X] T001 Added the `Kubernetes` tag to the oapi-codegen allowlist — `Makefile` `-include-tags` (source of truth) + `cfg.yaml`. Two generator defects required reproducible Makefile sed patches: (a) the `/presets/k8s` discriminated-union list item came out as the invalid identifier `200_K8sPresets_Item` → renamed to a hand-defined `K8sPresetItem` struct (`internal/clients/timeweb/generated/k8s_patch.go`); (b) the `PresetsResponse_K8sPresets_Item` union `From*`/`Merge*` builders assigned an untyped string to the typed discriminator pointer (dead code for our read-only use) → dropped via sed. **Actual generated method names** (differ from the task-time guesses): `CreateCluster`/`GetCluster`/`UpdateCluster`/`DeleteCluster`/`GetClusterKubeconfig`/`UpdateClusterVersion`; `CreateClusterNodeGroup`/`GetClusterNodeGroup`/`DeleteClusterNodeGroup`/`IncreaseCountOfNodesInGroup`/`ReduceCountOfNodesInGroup`; `GetKubernetesAddons`/`PostKubernetesAddons`/`DeleteKubernetesAddons`/`GetKubernetesAddonsConfig`; `GetKubernetesPresets`/`GetK8SVersions`/`GetK8SNetworkDrivers`. Regenerated `internal/clients/timeweb/fake.go` (counterfeiter, `-fake-name FakeClient`). Generated package + full module build clean.
- [X] T002 `go mod tidy` clean (one direct/indirect line shift; no new deps).

---

## Phase 2: Foundational

**Purpose**: API types, accessors, CRDs, resolver-dimension promotion needed by every user story.

- [X] T003 `apis/kubernetes/v1alpha1/groupversion_info.go` + `doc.go` — group const `kubernetes.m.timeweb.crossplane.io`, register all three GVKs (`KubernetesCluster`, `KubernetesClusterNodepool`, `KubernetesClusterAddon`) via SchemeBuilder; doc.go records the group commitment (future OIDC/maintenance kinds extend this group).
- [X] T004 [P] `apis/kubernetes/v1alpha1/kubernetescluster_types.go` per data-model §1.1 — required + optional fields, status observation, kubebuilder markers including `networkDriver`/`availabilityZone` enums, `masterNodesCount` default 1, two XValidation mutual-exclusion rules (`network*` trio, `project*` trio), printer columns (READY/SYNCED/K8S-VERSION/AZ/PRESET/STATE/AGE). `writeConnectionSecretToRef` supported.
- [X] T005 [P] `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go` per §1.2 — required `name`/`presetName`/`nodeCount` (1..100), cluster-ref trio, optional `labels` (`map[string]string`), `autoscaling{enabled,minSize,maxSize}`, `autohealing`. CEL: at-most-one + at-least-one of the cluster-ref trio; when `autoscaling.enabled` then `minSize>=2 && maxSize>=minSize`. Printer columns (READY/SYNCED/CLUSTER/PRESET/DESIRED/OBSERVED/AGE).
- [X] T006 [P] `apis/kubernetes/v1alpha1/kubernetesclusteraddon_types.go` per §1.3 — cluster-ref trio + required `type`/`version`, optional `yamlConfig`/`configType`. CEL: at-most-one + at-least-one of the cluster-ref trio. Printer columns (READY/SYNCED/CLUSTER/TYPE/VERSION/AGE).
- [X] T007 `apis/kubernetes/v1alpha1/managed.go` — standard v2 ModernManaged forwarders (GetCondition/SetConditions/GetProviderConfigReference/GetManagementPolicies/etc.) for all three kinds in one file.
- [X] T008 Promote the forward-compat K8s resolver dimensions in `internal/controller/shared/resolver/dimensions.go` from `fetchUnwired` to real fetchers: `fetchK8sMasterPresets` + `fetchK8sWorkerPresets` (both call `GetK8SPresetsWithResponse`, filter the discriminated `k8s_presets[]` by `type=master`/`type=worker`, MB→GB normalize, slug = `Slugify(description_short)` with NO location suffix) and `fetchK8sVersions` (`GetK8SVersionsWithResponse`, exact-string Enum). Extend the `CatalogClient` interface with the two new methods; extend the `fakeCatalog` stub; update `TestDefaultRegistry_Discoverable` to flag `DimKubernetesMasterPreset`/`DimKubernetesWorkerPreset`/`DimKubernetesVersion` as `wiredUpstream: true`. `DimKubernetesNetworkDriver` + `DimAvailabilityZone` stay at `fetchUnwired` (CRD-enum validated). Also add a **resolver-cache assertion** for the new dimensions (covers SC-006): two resolves of the same `(PCRef, dimension)` within the TTL window trigger exactly one upstream `GetK8SPresets`/`GetK8SVersions` call (fake-client call counter), proving the inherited singleflight+TTL cache carries over.
- [X] T009 `make generate` clean — emits `package/crds/kubernetes.m.timeweb.crossplane.io_kubernetesclusters.yaml`, `…_kubernetesclusternodepools.yaml`, `…_kubernetesclusteraddons.yaml` + `apis/kubernetes/v1alpha1/zz_generated.deepcopy.go`; CEL XValidation rules present on the CRDs. `go build ./...` clean.
- [X] T010 Register the `kubernetes` group in `apis/apis.go::AddToSchemes` alongside the existing groups.

**Checkpoint**: provider builds and the three CRDs install (no controllers reconcile them yet).

---

## Phase 3: User Story 1 — Provision a cluster with a worker pool (Priority: P1) 🎯 MVP

**Goal**: Operator declares a `KubernetesCluster` + a `KubernetesClusterNodepool` (clusterRef); controllers create the upstream cluster + worker group and publish a `kubeconfig` connection Secret. Spec US1, FR-001/002/004/006/007/008/010/013/015, SC-001/002/006.

**Independent Test**: Apply ProviderConfig + KubernetesCluster (smallest master preset, exact k8s version, cilium, msk-1) + KubernetesClusterNodepool (clusterRef, smallest worker preset, nodeCount 2). Cluster reaches `[Ready=True, Synced=True]` ≤20 min; Nodepool ≤15 min after; `demo-kubeconfig` Secret carries a `kubeconfig` key; `kubectl --kubeconfig get nodes` lists 2 workers. `kubectl delete` removes upstream cluster + group.

### Controller scaffolding

- [X] T011 [US1] `internal/controller/kubernetes/connector.go` + `controller.go` — `Connect` flow (track PCU → `shared.ResolveToken` → build Timeweb client → construct in-controller `resolver.Resolver` → resolve refs → return the external client). `SetupCluster(mgr, log, pollInterval)` + `SetupNodepool(...)` register reconcilers with the standard v2 ModernManaged options + `managed.WithManagementPolicies()`. (Addon setup added in US5/T037.)
- [X] T012 [US1] `internal/controller/kubernetes/refs.go` — `resolveClusterRef` (Nodepool/Addon → parent `KubernetesCluster.status.atProvider.upstreamID`; not-found → `ErrTargetNotFound`; empty upstreamID → `ErrTargetNotReady`; `clusterID` escape hatch bypasses the lookup; selector → explicit "not implemented in v0.x" error). Persists the resolved cluster id for the caller to store in `status.atProvider.clusterID`. On `ErrTargetNotFound` for a dependent whose parent cluster has vanished, **log a structured warning naming the orphaned dependent** (FR-016) via the runtime logger before surfacing the `ReconcileError`. (network/project refs added in US3/T030.)

### Cluster external methods (`cluster_external.go`)

- [X] T013 [US1] `(*clusterExternal).Observe` — GET `/api/v1/k8s/clusters/{id}` (string id = external-name); 404 → `ResourceExists:false`; unmarshal `{cluster: Cluster}`; populate `atProvider.{upstreamID,state,k8sVersion,lockedPresetID,cpu,ram,disk}`; map upstream `status` → `Ready` via `setReadyCondition` (active→True; provisioning→`Provisioning`; `no_paid`→`PaymentRequired`); report `ResourceUpToDate` over the mutable subset (name/description + version handled in US4).
- [X] T014 [US1] `(*clusterExternal).Create` — resolve master preset via `resolver.Resolve(DimKubernetesMasterPreset, …)` and version via `resolver.Resolve(DimKubernetesVersion, …)`; build the `ClusterIn` body via `buildCreateClusterBody` (name, k8s_version, network_driver, availability_zone, preset_id, master_nodes_count; NO worker_groups; network_id/project_id added in US3); POST; `meta.SetExternalName` from `cluster.id`; record `lockedPresetID`; `Creating()`.
- [X] T015 [US1] `(*clusterExternal).Update` — re-fetch via GET; `RejectImmutableChange` on drift of `networkDriver`/`availabilityZone`/`presetName`/`masterNodesCount`; PATCH `name`/`description` (`ClusterEdit`) only when changed; skip the upstream PATCH when nothing changed. (Version-upgrade path added in US4/T034.)
- [X] T016 [US1] `(*clusterExternal).Delete` — DELETE `/api/v1/k8s/clusters/{id}`; 404 idempotent; empty external-name → no-op.
- [X] T017 [US1] `clusterConnectionDetails` — fetch `GET /api/v1/k8s/clusters/{id}/kubeconfig` (`application/yaml` string) and publish it under the `kubeconfig` key via `managed.ConnectionDetails`; returned from both Observe and Create; never logged (Provider Constraints).

### Nodepool external methods (`nodepool_external.go`)

- [X] T018 [US1] `(*nodepoolExternal).Observe` — GET `/api/v1/k8s/clusters/{clusterID}/groups/{groupID}` using `status.atProvider.clusterID` + external-name; 404 → `ResourceExists:false`; unmarshal `{node_group: NodeGroup}`; populate `atProvider.{upstreamID,observedNodeCount,lockedPresetID}`; `Ready=True` when `observedNodeCount == nodeCount` (autoscaling off); `ResourceUpToDate` folds the count match. (Scaling diff added in US2/T026.)
- [X] T019 [US1] `(*nodepoolExternal).Create` — `resolveClusterRef` then **gate on the parent cluster being `Ready=True`** (`ErrTargetNotReady` until then); resolve worker preset via `DimKubernetesWorkerPreset`; build `NodeGroupIn` (name, node_count, preset_id, labels map→`array<{key,value}>`, autoscaling/autohealing fields); POST `/groups`; `meta.SetExternalName` from `node_group.id`; persist `status.atProvider.clusterID`; `Creating()`.
- [X] T020 [US1] `(*nodepoolExternal).Update` (immutable guards for `presetName` + cluster-ref) + `Delete` (DELETE `/groups/{groupID}` with parent clusterID; 404 idempotent; empty external-name → no-op). (Scaling lives in Update, added in US2/T026.)

### Unit tests

- [X] T021 [P] [US1] `cluster_external_test.go` — §III four-case (Success/NotFound/Transient/Terminal) per Observe/Create/Update/Delete; plus `Observe_ExternalNameEmpty_ReturnsNotExists`, `Observe_NoPaid_PaymentRequired`, `Observe_Success_PublishesKubeconfigSecret` (verifies the `kubeconfig` connection key), `Create_MasterPresetNotFound`, `Create_VersionNotFound`, `Update_NoChange_SkipsUpstream`, `Update_ImmutableFieldChange_NetworkDriver`. Uses a `fakeResolver` keyed by slug + the `timeweb.FakeClient`.
- [X] T022 [P] [US1] `nodepool_external_test.go` — §III four-case per Observe/Create/Update/Delete; plus `Create_GatedOnClusterNotReady` (no `CreateNodeGroup` call when parent not Ready), `Observe_UsesPersistedClusterID`, `Create_LabelsMarshaledToArray`.
- [X] T023 [P] [US1] `refs_test.go` — `resolveClusterRef`: `Resolved`, `NotFound`, `NotReady_EmptyUpstreamID`, `ClusterID_BypassesRefLookup`, `SelectorNotImplemented`. Uses controller-runtime's fake client with the multi-group scheme.

### Wiring + e2e

- [X] T024 [US1] `cmd/provider/main.go` — wire `kubernetesctrl.SetupCluster` + `SetupNodepool` alongside existing setups; `go build ./...` clean.
- [X] T025 [US1] `test/e2e/kuttl/tests/12-k8s-cluster-lifecycle/` (01-create + 01-assert + 02-delete-assert). Extend `test/e2e/scripts/kuttl.sh` to discover the smallest master + worker `/api/v1/presets/k8s` slugs (filter by `type`) and a valid entry from `/api/v1/k8s/k8s-versions`, exporting `$TWE_K8S_MASTER_PRESET`/`$TWE_K8S_WORKER_PRESET`/`$TWE_K8S_VERSION`/`$TWE_K8S_CLUSTER_NAME` (+ envsubst allow-list). Bundle creates a KubernetesCluster + KubernetesClusterNodepool and asserts both `[Ready=True, Synced=True]` (assert timeouts 20m/15m per SC-001) and that the kubeconfig Secret exists with a non-empty `kubeconfig` key. Orphan-inventory function extended to list the three K8s kinds.

**Checkpoint**: US1 independently functional — operator gets a working cluster + workers + kubeconfig. MVP reached.

---

## Phase 4: User Story 2 — Scale a worker pool (Priority: P2)

**Goal**: Mutable `nodeCount` converges via relative add/remove deltas; autoscaling/autohealing honored. Spec US2, FR-009/011, SC-003.

**Independent Test**: Pool at `nodeCount: 2` → patch to 4 → `observedNodeCount` reaches 4, group not recreated → patch to 2 → converges down. Autoscaling pool created with min/max; controller doesn't fight the autoscaler.

- [X] T026 [US2] Scaling convergence in `nodepool_external.go` Update (or a new `nodepool_scaling.go`): compute `delta = nodeCount − observedNodeCount`; `IncreaseNodeGroupNodes {count:delta}` (>0) or `ReduceNodeGroupNodes {count:-delta}` (<0); no-op at 0; **skipped entirely when `autoscaling.enabled`**. Fold the count match into `Observe`'s `ResourceUpToDate` so a drift triggers Update. While a scale delta is in flight (`observedNodeCount != nodeCount`, autoscaling off), `Observe` reports `Ready=False, reason=Scaling` (per data-model §5 / FR-015), returning to `Ready=True` once the count converges.
- [X] T027 [US2] `buildCreateNodeGroupBody` emits `is_autoscaling` + `min-size`/`max-size` (>=2) + `is_autohealing` from `forProvider.autoscaling`/`autohealing`; confirm the CEL bounds (T005) reject `minSize<2`/`maxSize<minSize`.
- [X] T028 [P] [US2] `nodepool_external_test.go` scaling cases: `Update_ScaleUp_AddsNodes`, `Update_ScaleDown_RemovesNodes`, `Update_NoChange_NoOp`, `Update_AutoscalingOn_SkipsCountReconcile`, `Update_ImmutablePresetChange_Rejected`.
- [X] T029 [US2] `test/e2e/kuttl/tests/13-k8s-nodepool-scaling/` (01-create pool `nodeCount:2` + assert; 02-patch `nodeCount:3` + assert `observedNodeCount==3` with no condition flap). Reuses `$TWE_K8S_*` from T025.

**Checkpoint**: US2 functional — pools scale up/down and support autoscaling.

---

## Phase 5: User Story 3 — Attach to a private network + project (Priority: P2)

**Goal**: `KubernetesCluster` attaches to a feat-003 `Network` (via `networkRef`/`networkID`) and lands in a feat-001 `Project` (via `projectRef`/`projectID`). Spec US3, FR-005/017.

**Independent Test**: Network `Ready=True` → cluster with `networkRef` + `projectRef` resolves both, creates attached, records `resolvedNetworkID`/`resolvedProjectID`. An incompatible cluster-AZ / VPC-region pairing is rejected upstream and surfaced as `ReconcileError` (no client-side pre-check — FR-017).

- [X] T030 [US3] Extend `refs.go` — `resolveNetworkRef` (→ `Network.status.atProvider.upstreamID`, reusing the feat-003 kind) + `resolveProjectRef` (→ `Project.status.atProvider.upstreamID`); `networkID`/`projectID` escape-hatch precedence (`ID > Ref > Selector`); selectors → not-implemented error. **No** client-side cluster-AZ/VPC-region compatibility check (FR-017 decision — different code namespaces; rely on upstream rejection).
- [X] T031 [US3] `cluster_external.go` Create — wire `resolvedNetworkID`/`resolvedProjectID` into the `ClusterIn` body and into `status.atProvider`. No VPC GET for a location pre-check; an incompatible pairing fails at the upstream create and is surfaced verbatim as `ReconcileError`.
- [X] T032 [P] [US3] `refs_test.go` + `cluster_external_test.go` — `NetworkRef_Resolved`/`_NotReady`, `ProjectRef_Resolved`, `NetworkID_BypassesRefLookup`, `Create_UpstreamRejectsIncompatibleNetwork_SurfacesReconcileError`.
- [X] T033 [US3] `test/e2e/kuttl/tests/14-k8s-cluster-with-network/` (Network `10.40.0.0/24` ru-1/msk-1 + cluster with `networkRef`; assert all `[Ready=True, Synced=True]`). Reuses `$TWE_K8S_*` + `$TWE_NETWORK_NAME` (feat-003 wrapper var).

**Checkpoint**: US3 functional — clusters join private networks + projects via refs.

---

## Phase 6: User Story 4 — Upgrade the cluster Kubernetes version (Priority: P3)

**Goal**: Forward-only in-place version upgrade. Spec US4, FR-012, SC-004.

**Independent Test**: Cluster `Ready=True` at `<v1>` → patch `k8sVersion` to a newer catalog-valid `<v2>` → `PATCH …/versions/update`, transient `Ready=False, reason=Upgrading`, back to `Ready=True` at `<v2>`. Downgrade/non-catalog rejected.

- [X] T034 [US4] `cluster_upgrade.go` — version-upgrade convergence invoked from `clusterExternal.Update`: when `observed.k8sVersion != spec.k8sVersion`, validate the target via `DimKubernetesVersion` and that it is a forward move; on success `PATCH /api/v1/k8s/clusters/{id}/versions/update {k8s_version}` and set `Ready=False, reason=Upgrading`; reject downgrade/non-catalog with `ReconcileError` and NO upstream call. `Observe` maps the upstream upgrading status → `reason=Upgrading`.
- [X] T035 [P] [US4] `cluster_external_test.go` upgrade cases: `Update_VersionUpgrade_PATCHes`, `Update_VersionDowngrade_Rejected`, `Update_VersionNotInCatalog_Rejected`, `Update_VersionNoChange_NoUpgradeCall`.

**Checkpoint**: US4 functional — clusters upgrade in place. (No dedicated kuttl bundle — upgrade is a slow live-only canary, noted in T046.)

---

## Phase 7: User Story 5 — Install and remove cluster addons (Priority: P3)

**Goal**: `KubernetesClusterAddon` installs/removes one addon per MR. Spec US5, FR-003/014 (shape refined per research R-9).

**Independent Test**: Cluster `Ready=True` → Addon MR with `type`+`version` from the catalog → installed `[Ready=True]`. Delete MR → addon removed; cluster unaffected. Unknown type/version → `ReconcileError` listing valid types.

- [X] T036 [US5] `addon_external.go` — Observe (GET `/addons`, match by `type`, populate `atProvider.{addonID,clusterID,status}`, external-name = addon id), Create (`resolveClusterRef` + gate on cluster Ready; validate `type`+`version` against `GET …/addons-configs`; POST `/addons {type, config_type, yaml_config, version}` defaulting `yaml_config`/`config_type` from the catalog), Update (immutable guards on `type`/`version`/cluster-ref), Delete (`DELETE …/addons/{addon_id}`; 404 idempotent).
- [X] T037 [US5] `connector.go` `SetupAddon(...)` + `cmd/provider/main.go` wiring; `go build ./...` clean.
- [X] T038 [P] [US5] `addon_external_test.go` — §III four-case per Observe/Create/Update/Delete; plus `Create_UnknownType_Rejected` (lists valid types), `Create_GatedOnClusterNotReady`, `Delete_404_Idempotent`, `Delete_EmptyExternalName_NoOp`.
- [X] T039 [US5] `test/e2e/kuttl/tests/15-k8s-addon/` — wrapper discovers a valid addon `type`+`version` from `addons-configs` at runtime (exports `$TWE_K8S_ADDON_*`); bundle installs the addon (assert `[Ready=True, Synced=True]`) then deletes it (assert the cluster stays Ready). Env-gated/skippable like the feat-003 import bundle if no addon catalog is available.

**Checkpoint**: US5 functional — addons install/remove per MR.

---

## Phase 8: Polish & cross-cutting concerns

**Purpose**: Operator docs, lint, constitution audit, live canary.

- [X] T040 [P] `docs/kubernetes.md` — operator guide: minimum cluster+pool, scaling, network/project attach, version upgrade, addons, the `PaymentRequired`/`no_paid` row, troubleshooting matrix, "what's NOT in v0.x". Location/AZ codes use API values.
- [X] T041 [P] `README.md` — add `KubernetesCluster`/`KubernetesClusterNodepool`/`KubernetesClusterAddon` rows to the Resources table, the kubernetes-group commitment, a `docs/kubernetes.md` pointer, and the new e2e bundles (12–15).
- [X] T042 [P] `package/crossplane.yaml` description lists the three K8s kinds and points the readme annotation appropriately.
- [X] T043 [P] `apis/kubernetes/v1alpha1/doc.go` documents the group commitment (future OIDC/maintenance kinds extend `kubernetes.m.timeweb.crossplane.io`).
- [X] T044 [P] `make lint` (full module) → 0 issues; no `//nolint` on the new `managed.go` forwarders (existing exclusion covers them).
- [X] T045 [P] §III audit (grep) confirms the four-case pattern across every `external` method in `internal/controller/kubernetes` (cluster/nodepool/addon Observe/Create/Update/Delete) plus the scaling + upgrade logical methods.
- [X] T046 Live e2e canary run against the real Timeweb account (2026-06-07). **Bundles 12 + 14 PASSED green**: `12-k8s-cluster-lifecycle` (cluster + nodepool + kubeconfig Secret, ~22 min) and `14-k8s-cluster-with-network` (cluster + networkRef, ~12 min). The run surfaced and fixed FIVE real issues invisible to unit tests: (1) live k8s version format is `v1.31.x+k0s.0` (v-prefix + `+k0s.0` build suffix) → `splitVersion`/`versionNewer` hardened so patch upgrades order correctly; (2) `resolveClusterDeps` mutated `spec.forProvider.networkID` from a ref → the `at-most-one` CEL rule rejected the object on persist → refactored to carry resolved IDs on the external client (never mutate spec); (3) K8s preset slugs are ambiguous (Timeweb ships multiple identically-named tiers, no location to disambiguate) → e2e wrapper emits the `<slug>-<id>` form the resolver already accepts; (4) `timeweb.Classify` swallowed the upstream body for transient (incl. 409) statuses → now surfaces it (Constitution §II), which is what revealed (5) Timeweb caps **promo** clusters at one per account ("Promo cluster already exists on account") → wrapper excludes promo, picks cheapest non-promo. Also observed: a **network-less cluster auto-creates a default VPC that is NOT removed when the cluster is deleted** (operator-awareness note added to `docs/kubernetes.md`). All e2e orphans from interrupted runs were cleaned and the account verified clean. US4 in-place version upgrade was not separately exercised live (no dedicated bundle); covered by unit tests.

**Checkpoint**: Release-ready. All five user stories independently functional + e2e green against live Timeweb (funded account).

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1 (Setup)**: T001–T002 — no deps; first PR.
- **Phase 2 (Foundational)**: T003–T010 — depends on Phase 1. T003 ⊂ T004/T005/T006 (types) ⊂ T007 (managed.go) ⊂ T009 (regen). T008 (dimensions) parallel to the types. T010 (scheme reg) requires T009.
- **Phase 3 (US1, MVP)**: T011–T025 — depends on Phase 2. **MVP target.**
- **Phase 4 (US2)**: T026–T029 — depends on US1 (extends `nodepool_external.go`).
- **Phase 5 (US3)**: T030–T033 — depends on US1 (extends `refs.go` + `cluster_external.go` Create). Independent of US2.
- **Phase 6 (US4)**: T034–T035 — depends on US1 (extends `cluster_external.go` Update). Independent of US2/US3.
- **Phase 7 (US5)**: T036–T039 — depends on US1 (reuses `resolveClusterRef` + connector). Independent of US2/US3/US4.
- **Phase 8 (Polish)**: T040–T046 — depends on whichever stories ship in the release.

### User-story dependencies

- US1 → US2/US3/US4/US5: all four extend files US1 introduces (`nodepool_external.go`, `refs.go`, `cluster_external.go`, the connector). US1 must merge first.
- US2 ↔ US3 ↔ US4 ↔ US5: mutually independent after US1.

### Parallel opportunities

- Within Phase 2: T004, T005, T006, T008 parallel after T003.
- Within US1: T021 + T022 + T023 parallel (test files); T013→T014→T015→T016 sequential (one file); T024 after the external methods.
- Within US2/US3/US4/US5: each phase's test task `[P]` runs parallel to its sibling stories once US1 lands.
- Within Polish: T040–T045 all parallel.

---

## Parallel example: User Story 1

```bash
# After Phase 2, in parallel:
Task T011: internal/controller/kubernetes/connector.go + controller.go
Task T012: internal/controller/kubernetes/refs.go

# Then sequentially the cluster method chain: T013 → T014 → T015 → T016 → T017
# and the nodepool method chain: T018 → T019 → T020
# Then in parallel: T021, T022, T023 (test files)
# Then T024 (wiring) → T025 (e2e bundle)
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Land Phase 1 (Setup) in one PR.
2. Land Phase 2 (Foundational) in one PR — three CRDs + promoted resolver dimensions + scheme registration. Tests stay empty stubs.
3. Land Phase 3 (US1) in one PR — operator payoff: cluster + worker pool + kubeconfig within the SC-001 window.
4. **STOP AND VALIDATE** on a funded Timeweb account: `make e2e.test` runs bundle `12-k8s-cluster-lifecycle`. Verify pass.
5. Shippable release (v0.4-rc1).

### Incremental delivery

1. After MVP, land US2 (scaling) → US3 (network/project) → US4 (upgrade) → US5 (addons), each its own PR, each independently testable.
2. Then Polish (Phase 8) — docs, lint, audit, live canary.

### Parallel team strategy

- Dev A: US1 (owns `internal/controller/kubernetes/`).
- Dev B: after US1 lands, US3 + US4 (both extend `cluster_external.go`).
- Dev C: after US1 lands, US2 + US5 (nodepool scaling + addon controller).
- Dev D: Polish — docs, README, lint, §III audit, live canary.

---

## Notes

- `[P]` = different files, no dependency on incomplete tasks in the same phase.
- `[Story]` = traces the task to a user story for cherry-pick/revert.
- The kubernetes-group commitment introduces no extra v0.x tasks — it's a doc commitment captured in T003 (groupversion doc), T043 (apis doc.go), T041 (README). Future `KubernetesClusterOIDC` / maintenance kinds extend `apis/kubernetes/v1alpha1/` + `internal/controller/kubernetes/` directly.
- Addon shape (`type`+`version`+`yamlConfig`) refines spec FR-014 per research R-9; the data-model + contract already carry the refined shape.
