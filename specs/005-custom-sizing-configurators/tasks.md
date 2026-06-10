---
description: "Task list for 005-custom-sizing-configurators — custom sizing + CR group move + tech debt"
---

# Tasks: Custom Sizing (Configurators) + Group Tidy-up + Tech Debt

**Input**: Design documents from `specs/005-custom-sizing-configurators/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Included — Constitution §III mandates Success/NotFound/Transient/Terminal unit tests for every `external` method touched; the spec adds kuttl e2e bundles per user story.

**Organization**: Grouped by user story (US1–US4 from spec.md). US1 (Server custom sizing) is the MVP target. US3 (CR move) + US4 (tech debt) are independent of the sizing work and can land in any order.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: parallelizable (different files, no dependency on incomplete tasks in the same phase)
- **[Story]**: only on user-story-phase tasks (US1–US4)

## Path Conventions

Per `plan.md → Project Structure`: API types `apis/<group>/v1alpha1/`; controllers `internal/controller/<group>/`; resolver `internal/controller/shared/resolver/`; provider package `package/`; docs `docs/`; e2e `test/e2e/`.

---

## Phase 1: Setup

**Purpose**: Confirm the configurator endpoint is already available (no codegen change) and capture upstream units.

- [X] T001 Verify the generated client exposes `GetConfiguratorsWithResponse` + the `servers-configurator` / `requirements` types (the `Облачные серверы` tag already covers `/api/v1/configurator/servers` — NO `Makefile`/`cfg.yaml` change). Curl-probe `/api/v1/configurator/servers` (per `project_timeweb_underscore_envelopes`) to record the exact units of `requirements.ram_*` and `disk_*` and confirm whether K8s `configuration.configurator_id` accepts ids from this same catalog (research R-5); record findings in `research.md`. `go build ./...` baseline clean.

---

## Phase 2: Foundational

**Purpose**: Promote the configurator resolver dimension — blocks both US1 and US2.

- [X] T002 Promote `DimServerConfigurator` from `fetchUnwired` to a real `fetchServerConfigurators` in `internal/controller/shared/resolver/dimensions.go`: call `GetConfiguratorsWithResponse`, map each `servers-configurator` to a `ConfiguratorEntry` with `Filters{location, disk_type, is_allowed_local_network, cpu_frequency}` and `Bounds{cpu, ramMB, diskGB, bandwidth, gpu}` from `requirements.*_{min,step,max}` (normalize units per research R-2 — disk MB→GB for the `diskGB` bound, ram stays MB for `ramMB`). Extend the `CatalogClient` interface with `GetConfiguratorsWithResponse`; extend the `fakeCatalog` test stub; flip `DimServerConfigurator` to `wiredUpstream: true` in `TestDefaultRegistry_Discoverable`.
- [X] T003 [P] `internal/controller/shared/resolver/*_test.go` — add a fetcher+selection test for `DimServerConfigurator`: a fake `/configurator/servers` payload resolves a `ConfiguratorInput{Filters, Sizing}` to the tightest-fit configurator id (exercises `fetchServerConfigurators` → `SelectConfigurator`), plus a `NoConfiguratorAvailable` case and a resolver-cache assertion (≤1 GET per `(PCRef, dim)` per TTL).

**Checkpoint**: the resolver resolves `resources` → `configurator_id`; nothing consumes it yet.

---

## Phase 3: User Story 1 — Server custom sizing (Priority: P1) 🎯 MVP

**Goal**: Operator declares `Server.forProvider.resources: { cpu, ramGB, diskGB, … }` (no `presetName`) → the controller resolves the configurator and provisions. Spec US1, FR-001/002/003/004/005, SC-001/SC-003.

**Independent Test**: Apply a `Server` with `forProvider.resources` (no `presetName`) + OS + location → `[Ready=True, Synced=True]`; `status.atProvider.lockedConfiguratorID` set; VM CPU/RAM/disk meet/exceed the request; SSH works; delete removes the VM.

- [X] T004 [US1] `apis/compute/v1alpha1/server_types.go` — make `presetName` optional (`*string`); add `Resources *ServerResources` (`cpu`, `ramGB`, `diskGB` required; optional `diskType`/`bandwidthMbps`/`gpu`/`cpuFrequencyTier`/`enableLocalNetwork`); add `status.atProvider.lockedConfiguratorID *int64`; add XValidation CEL `exactly one of {presetName, resources}`.
- [X] T005 [US1] `make generate` — regenerate `apis/compute/v1alpha1/zz_generated.deepcopy.go` + `package/crds/compute.m.timeweb.crossplane.io_servers.yaml` (new `resources` schema + CEL). Build clean.
- [X] T006 [US1] `internal/controller/compute/server_external.go` Create — when `resources` is set, build `resolver.ConfiguratorInput` (Filters from location/diskType/cpuFrequencyTier/enableLocalNetwork; Sizing `cpu`, `ramMB`=ramGB×1024, `diskGB`, optional `bandwidth`/`gpu`), `Resolve(DimServerConfigurator, …)`, and build the createServer body with `configurator_id` (preset path unchanged when `presetName` is set). Record `lockedConfiguratorID`. Map `resolver.ErrNoConfiguratorAvailable` → `Synced=False, reason=NoConfiguratorAvailable`.
- [X] T007 [US1] `internal/controller/compute/server_external.go` Update — sizing-variant-switch detection: if the live resource was created via one variant (`lockedPresetID` vs `lockedConfiguratorID`) and the spec now uses the other, surface `Synced=False, reason=SizingSwitchRequiresRecreate`. `resources` axes are create-only (reject changes like `presetName` already is).
- [X] T008 [P] [US1] `internal/controller/compute/server_external_test.go` — §III four-case over the resources Create path; plus `Create_Resources_SetsLockedConfiguratorID`, `Create_NoConfiguratorAvailable`, `Update_SizingSwitch_PresetToResources_Rejected` (and reverse). Extend the `fakeResolver` to return `ConfiguratorOutput`/`ErrNoConfiguratorAvailable` for `DimServerConfigurator`.
- [X] T009 [US1] `test/e2e/kuttl/tests/16-server-custom-sizing/` (01-create + 01-assert). Extend `test/e2e/scripts/kuttl.sh` to discover a satisfiable `{cpu,ramGB,diskGB}` from `/api/v1/configurator/servers` (cheapest configurator's mins) and export `$TWE_SRV_CPU`/`$TWE_SRV_RAMGB`/`$TWE_SRV_DISKGB` (+ envsubst allow-list). Bundle creates an SSHKey + a `Server` with `forProvider.resources` (no `presetName`); asserts `[Ready=True, Synced=True]` (assert timeout 720s).

**Checkpoint**: US1 functional — operators size a Server by cores/GB.

---

## Phase 4: User Story 2 — Kubernetes custom sizing (Priority: P2)

**Goal**: Same `resources` path for `KubernetesCluster` + `KubernetesClusterNodepool` → upstream `configuration` block. Spec US2, FR-006/007, SC-002.

**Independent Test**: Apply a `KubernetesCluster` + `KubernetesClusterNodepool` each with `forProvider.resources` (no `presetName`); both `[Ready=True, Synced=True]`; nodes match the requested sizing; no ambiguous slug in the manifests.

- [X] T010 [US2] `apis/kubernetes/v1alpha1/kubernetescluster_types.go` + `kubernetesclusternodepool_types.go` — make `presetName` optional; add `Resources` (`cpu`/`ramGB`/`diskGB`; nodepool +`gpu`); add `status.atProvider.lockedConfiguratorID`; CEL `exactly one of {presetName, resources}` on both.
- [X] T011 [US2] `make generate` — regenerate deepcopy + the two CRDs. Build clean.
- [X] T012 [US2] `internal/controller/kubernetes/cluster_external.go` — when `resources` set, resolve via `DimServerConfigurator` (research R-5; use `DimKubernetesConfigurator` instead if T001's probe shows a distinct catalog) and emit `ClusterIn.configuration {configurator_id, cpu, ram, disk}` (ram/disk in upstream MB); record `lockedConfiguratorID`; sizing-switch in Update; `NoConfiguratorAvailable` mapping. **T028 canary outcome: the contingency was taken** — the catalogs ARE distinct (k8s create 400-rejects server-catalog ids), so K8s resolves via `DimKubernetesConfigurator` over the undocumented `/api/v1/configurator/k8s` (see research R-5 live finding).
- [X] T013 [US2] `internal/controller/kubernetes/nodepool_external.go` — same, emitting `NodeGroupIn.configuration {configurator_id, cpu, ram, disk, gpu}`.
- [X] T014 [P] [US2] `cluster_external_test.go` + `nodepool_external_test.go` — resources-path cases: `Create_Resources_SetsConfiguration`, `Create_NoConfiguratorAvailable`, `Update_SizingSwitch_Rejected`. Extend the kubernetes `fakeResolver` for `DimServerConfigurator`.
- [X] T015 [US2] `test/e2e/kuttl/tests/17-k8s-custom-sizing/` (01-create + 01-assert) — cluster + nodepool via `resources` (reuse `$TWE_SRV_*` discovery or add `$TWE_K8S_*` sizing vars); assert `[Ready=True, Synced=True]` (assert timeout 1800s). Env-gated like other K8s bundles.

**Checkpoint**: US2 functional — K8s sized by cores/GB; no ambiguous preset slugs needed.

---

## Phase 5: User Story 3 — ContainerRegistry → kubernetes group (Priority: P2)

**Goal**: Hard move of `ContainerRegistry` + `ContainerRegistryRepository` to `kubernetes.m.timeweb.crossplane.io`. Spec US3, FR-009. Behavior unchanged.

**Independent Test**: `kubectl get containerregistries.kubernetes.m.timeweb.crossplane.io` works; a manifest under the new group reconciles `[Ready=True, Synced=True]`; the old group's CRDs are gone.

- [X] T016 [US3] Move the two kinds' types into `apis/kubernetes/v1alpha1/` (`containerregistry_types.go`, `containerregistryrepository_types.go`) — group becomes `kubernetes.m.timeweb.crossplane.io`; register their GVKs in the package `groupversion_info.go` `init()`; add ModernManaged forwarders to `apis/kubernetes/v1alpha1/managed.go`. Delete `apis/containerregistry/v1alpha1/{registry,repository}_types.go` + `groupversion_info.go` + `managed.go` (remove the package).
- [X] T017 [US3] `make generate` — emit `package/crds/kubernetes.m.timeweb.crossplane.io_containerregistries.yaml` + `…_containerregistryrepositories.yaml`; delete the old `containerregistry.m.timeweb.crossplane.io_*.yaml`; regenerate deepcopy. Build clean.
- [X] T018 [US3] `apis/apis.go` — drop `containerregistryv1alpha1.AddToScheme` (kinds now register via the kubernetes group). Repoint `internal/controller/containerregistry/` (connector/controller/external) to import `kubernetesv1alpha1` for the GVKs/types; repoint `cmd/provider/main.go` ContainerRegistry setup to the kubernetes-group GVKs.
- [X] T019 [US3] Update the CR controller tests (`registry_external_test.go`, `repository_external_test.go`) to the relocated types; update the `05-containerregistry` e2e bundle YAML `apiVersion` → `kubernetes.m.timeweb.crossplane.io/v1alpha1`.
- [X] T020 [US3] `package/crossplane.yaml` description + `README.md` resources table updated (ContainerRegistry/Repository now in the kubernetes group, with a one-line breaking-change note); fold the CR operator notes into `docs/kubernetes.md` (or cross-link).

**Checkpoint**: US3 functional — registries live in the kubernetes group; behavior identical.

---

## Phase 6: User Story 4 — Tech-debt pass (Priority: P3)

**Goal**: Fix the Server CEL latent bug, e2e harness reliability, and Connect-error reason. Spec US4, FR-010/011.

**Independent Test**: A `Server` with `networkRef` reaches `Ready=True` (no CEL `at-most-one` rejection); `make e2e.down` removes the k3d cluster+registry; a scoped multi-bundle e2e runs exactly the selected bundles; unready-dependency gating reports `reason=Reconciling`.

- [X] T021 [US4] `internal/controller/compute/refs.go` + `server_external.go` — stop mutating `cr.Spec.ForProvider` in `resolveRefs`; resolve into values carried on `serverExternal` (`resolvedNetworkID`/`resolvedProjectID`/`resolvedSSHKeyIDs`/`resolvedFloatingIPIDs`), set by the connector, consumed by the create-body builder. Mirrors the `clusterExternal` fix from feature 004.
- [X] T022 [P] [US4] `internal/controller/compute/refs_test.go` + `server_external_test.go` — assert `resolveRefs` does NOT mutate `spec.forProvider`; add `Create_NetworkRef_NoSpecMutation` regression (the at-most-one CEL would have rejected the persisted object before the fix).
- [X] T023 [US4] e2e harness fixes: `test/e2e/scripts/down.sh` (cluster + registry detection/teardown actually deletes them); `test/e2e/scripts/kuttl.sh` (forward ALL `--test` selectors for multi-bundle scoping); make the kuttl `status.conditions` asserts order-robust across bundles (the positional-array match that flapped `09-server`).
- [~] T024 [US4] Connect-error reason alignment — **partial (framework-constrained, R-9)**. ErrTargetNotReady carries a clear "waiting for dependency X" message and the path is documented in the compute connector. The surfaced *reason* stays the runtime's generic `ReconcileError`: crossplane-runtime overwrites the Synced condition after any Connect error and exposes no per-error reason hook for this manual-resolution-in-Connect design, so `reason=Reconciling` would require a custom reconciler wrapper (deferred as over-engineering for a cosmetic reason). Message-level alignment done; reason override deferred.

**Checkpoint**: US4 functional — latent Server bug gone, e2e harness reliable, reasons consistent.

---

## Phase 7: Polish & cross-cutting

- [X] T025 [P] `docs/servers.md` + `docs/kubernetes.md` — custom-sizing sections (`resources` block, the preset-vs-resources XOR, `NoConfiguratorAvailable`, sizing-switch); note presets remain supported.
- [X] T026 [P] `make lint` (full module) → 0 issues.
- [X] T027 [P] §III audit (grep) — confirm the four-case pattern across every `external` method touched (compute Server, kubernetes cluster/nodepool, containerregistry registry/repository) including the new resources paths.
- [X] T028 Live e2e canary on a funded account: bundles `16-server-custom-sizing`, `17-k8s-custom-sizing`, the updated `05-containerregistry`, and `10-server-with-network` (verifies the US4 Server-CEL fix). NOTE: K8s cluster bundles are billable + slow; non-promo configurators only (per `project_timeweb_k8s_api_quirks`). Run via `make e2e.up && make e2e.deploy && KUTTL_TEST="…" make e2e.test`; tear down with `make e2e.down`. **DONE 2026-06-10 — all 4 bundles PASS.** The canary surfaced and the same session fixed: (1) K8s sizing resolves against the undocumented `/api/v1/configurator/k8s`, split into master/worker tag families with location-first filtering (a cross-family/location id strands the cluster in ams-1 — see research R-5 live findings); (2) nodepool Ready was gated on the echoed `node_count` (true within 1s of create) — now gated on actual per-node statuses from `/groups/{id}/nodes`, with the node list published at `status.atProvider.nodes`; (3) nodepool/addon controllers now `Watches` the parent cluster (+60s-capped error backoff) so dependents wake immediately on cluster Ready instead of after up-to-16min backoff; (4) upstream `failed` state surfaces as `Ready=False reason=UpstreamFailed` on clusters and nodes.

---

## Dependencies & Execution Order

### Phase dependencies
- **Phase 1 (Setup)**: T001 — no deps.
- **Phase 2 (Foundational)**: T002–T003 — depends on T001. **Blocks US1 + US2** (both consume the configurator dimension).
- **Phase 3 (US1)**: T004–T009 — depends on Phase 2. **MVP.**
- **Phase 4 (US2)**: T010–T015 — depends on Phase 2 (configurator dimension). Independent of US1's files except the shared resolver (already done in Phase 2).
- **Phase 5 (US3)**: T016–T020 — independent of US1/US2 (touches apis/kubernetes + containerregistry; no resolver/sizing overlap). Can land first or last.
- **Phase 6 (US4)**: T021–T024 — independent of all sizing work (compute refs + e2e scripts + connector reasons).
- **Phase 7 (Polish)**: T025–T028 — after the shipped stories.

### User-story dependencies
- US1 ↔ US2: both depend on Phase 2; otherwise independent (different controller files).
- US3, US4: fully independent of the sizing stories and of each other.

### Parallel opportunities
- Phase 2: T003 parallel to nothing (single resolver change in T002 precedes it).
- US1: T008 (tests) parallel after T006/T007; T009 (e2e) after T006.
- US2: T012 + T013 are different files → parallel after T010/T011; T014 parallel after.
- US3 + US4 can be worked entirely in parallel with US1/US2 by different developers (no shared files beyond `apis.go`/`main.go`, which are append-only touch-points).
- Polish: T025–T027 all parallel.

---

## Implementation Strategy

### MVP first (US1)
1. Phase 1 (Setup) + Phase 2 (Foundational: configurator dimension) — one PR.
2. Phase 3 (US1: Server custom sizing) — one PR; **STOP AND VALIDATE** with bundle `16` on a funded account.
3. Shippable (v0.5-rc1): operators size Servers by cores/GB.

### Incremental delivery
- Then US2 (K8s sizing), US3 (CR group move), US4 (tech debt) as independent PRs, in any order. Polish last.

### Parallel team strategy
- Dev A: Phase 2 → US1 → US2 (the sizing thread).
- Dev B: US3 (CR group move) — independent.
- Dev C: US4 (tech debt) — independent.
- Dev D: Polish (docs, lint, audit, live canary).

---

## Notes
- `[P]` = different files, no dependency on incomplete tasks in the same phase.
- Custom sizing is **additive** — the preset path stays first-class throughout.
- The CR group move is the one **breaking** change (justified in plan Complexity Tracking); keep it isolated in its own PR for a clean revert boundary.
- Reuse over rebuild: `SelectConfigurator` + `ConfiguratorInput/Entry` already exist (feature 002); this feature wires them in.
