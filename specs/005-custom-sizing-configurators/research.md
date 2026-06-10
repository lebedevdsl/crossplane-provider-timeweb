# Phase 0 Research — Custom Sizing + Group Move + Tech Debt

**Feature**: 005 | **Source of truth**: `docs/openapi-timeweb.json` + features 001–004 precedent + the live-API facts in [[project_timeweb_k8s_api_quirks]].

All Technical-Context unknowns resolved below — **no `NEEDS CLARIFICATION` markers remain** (the three spec-level ones were answered in the /speckit-specify clarification round). Each decision names the evidence and the rejected alternative.

---

## R-1 — Configurator resolution reuses the existing primitive

**Decision**: Promote `DimServerConfigurator` from its `fetchUnwired` stub to a real fetcher (`fetchServerConfigurators`) reading `GetConfiguratorsWithResponse` (`/api/v1/configurator/servers`). The selection logic is already implemented and unit-tested in feature 002: `SelectConfigurator` (hard-filter → capability-filter → tightest-fit → lowest-id), `ConfiguratorInput{Filters, Sizing}`, `ConfiguratorEntry{Filters, Bounds}`, `CapacityBound{Min,Step,Max}`, `NoConfiguratorAvailableError`. Nothing in the resolver package changes except the fetcher.

**Evidence**: `internal/controller/shared/resolver/select_configurator.go` (complete algorithm), `GetConfiguratorsWithResponse` present in the generated client, `servers-configurator.requirements` = flat `{cpu,ram,disk,network_bandwidth,gpu}_{min,step,max}`.

**Alternatives rejected**: writing a new selection algorithm — it already exists and matches FR-003/007 verbatim.

---

## R-2 — Canonical axis keys + unit normalization

**Decision**: The resolver's canonical Sizing/Bounds axis keys (matching `SelectConfigurator`'s existing tightest-fit sort) are **`cpu`** (cores), **`ramMB`** (MB), **`diskGB`** (GB), plus optional **`bandwidth`** and **`gpu`**. The fetcher normalizes upstream `requirements` into `ConfiguratorEntry.Bounds` under these keys (probe the exact upstream ram/disk units at impl per [[project_timeweb_underscore_envelopes]] — server presets express disk in MB, so disk is `/1024`→GB for the `diskGB` bound; ram stays MB for `ramMB`). The controller builds `ConfiguratorInput.Sizing` from the **operator-facing GB inputs**: `ramGB × 1024 → ramMB`, `diskGB → diskGB` (already GB), `cpu → cpu`, optional `bandwidthMbps → bandwidth`, `gpu → gpu`.

**Rationale**: operator types GB (FR-001, the 2026-06-07 ram-unit decision); the resolver's internal keys are an implementation detail; a single conversion point in the controller keeps Bounds and Sizing in the same units per axis.

**Alternatives rejected**: changing `SelectConfigurator`'s sort axis names — unnecessary churn to a tested function; the fetcher adapts to its keys instead.

---

## R-3 — Server `resources` field set + filter/sizing split

**Decision**: `Server.forProvider.resources` = required `cpu`/`ramGB`/`diskGB` + optional `diskType`/`bandwidthMbps`/`gpu`/`cpuFrequencyTier`/`enableLocalNetwork` — exactly the feature-003 deferral clarification. Mapping to `ConfiguratorInput`:
- **Filters** (exact-match hard filters): `location` (the Server's location), `disk_type` (from `diskType`), `is_allowed_local_network` (from `enableLocalNetwork`), `cpu_frequency` (from `cpuFrequencyTier`).
- **Sizing** (capability bounds): `cpu`, `ramMB`, `diskGB`, optional `bandwidth`, `gpu`.

`location` joins the existing `Server.forProvider.location` (no new field). `lockedConfiguratorID` recorded in status on Create.

**Evidence**: `servers-configurator` = `{id, location, disk_type, is_allowed_local_network, cpu_frequency, requirements{…}}` — every filter axis has a configurator attribute.

---

## R-4 — Sizing-variant immutability + switch detection

**Decision**: CEL enforces **exactly one** of `{presetName, resources}` per kind (admission-time). The sizing variant is create-time immutable: on Update, if the live resource was created via one variant and the spec now uses the other, the controller surfaces `Synced=False, reason=SizingSwitchRequiresRecreate` (no in-place resize). Detection uses the locked-id pair already in status: a resource sized by preset has `lockedPresetID` set; by configurator has `lockedConfiguratorID` set. A spec that flips which one is populated is the switch.

**Evidence**: `shared.ReasonSizingSwitchRequiresRecreate` already exists in `conditions.go` (added in feature 002 vocabulary).

---

## R-5 — K8s custom sizing → `configuration` block

**Decision**: `KubernetesCluster.forProvider.resources` (`cpu`/`ramGB`/`diskGB`) emits the upstream `ClusterIn.configuration` block `{configurator_id, cpu, ram, disk}`; `KubernetesClusterNodepool.forProvider.resources` (+`gpu`) emits `NodeGroupIn.configuration` `{configurator_id, cpu, ram, disk, gpu}`. The `configurator_id` is resolved via the **same** `DimServerConfigurator` catalog (K8s nodes are cloud servers; the `configuration` block carries cpu/ram/disk for the size and configurator_id for the family). **Probe at impl**: confirm `/api/v1/configurator/servers` is the right catalog for K8s `configurator_id` (vs a K8s-specific list); if K8s needs a distinct catalog, add a `DimKubernetesConfigurator` dimension mirroring the server one. Working assumption: reuse the server configurator catalog.

**Evidence**: `ClusterIn.configuration` = `{configurator_id, cpu, ram, disk}`; `NodeGroupIn.configuration` adds `gpu`; both are XOR with `preset_id` upstream.

---

## R-6 — ContainerRegistry group move (hard rename)

**Decision**: Move `ContainerRegistry` + `ContainerRegistryRepository` **types** into `apis/kubernetes/v1alpha1` (so the package's `Group = "kubernetes.m.timeweb.crossplane.io"` const applies), register them in that package's SchemeBuilder + `managed.go`, regenerate DeepCopy + CRDs (filenames become `kubernetes.m.timeweb.crossplane.io_containerregistries.yaml` / `…_containerregistryrepositories.yaml`), delete `apis/containerregistry/`, drop its `AddToScheme` from `apis/apis.go`. **Controllers** stay in `internal/controller/containerregistry/` repointed to the relocated types (and their `*GroupVersionKind` now resolves to the kubernetes group); `cmd/provider/main.go` setup calls are unchanged except the GVK source package. Update `package/crossplane.yaml`, README, docs, and the `05-containerregistry` e2e bundle to the new apiVersion.

**Rationale**: dashboard co-locates registries under Kubernetes (operator's screenshot). Breaking but `v1alpha1` + no external consumers (Constitution §I allows it).

**Alternatives rejected**: additive alias (operator rejected — doubles surface); moving controllers too (more churn, no behavior gain — deferred as optional cleanup).

---

## R-7 — Server `resolveRefs` spec-mutation fix (tech debt)

**Decision**: `internal/controller/compute/refs.go::resolveRefs` currently writes resolved upstream ids back onto `cr.Spec.ForProvider` (e.g. `NetworkID` from `networkRef`), which trips the `at-most-one {networkRef,networkSelector,networkID}` CEL rule when the runtime persists the object (finalizer/external-name write) — the **identical bug** fixed on `KubernetesCluster` in feature 004. Fix: resolve into values carried on the `serverExternal` struct (and the Create body), never mutating spec. Mirrors `clusterExternal.resolvedNetworkID/resolvedProjectID`.

**Evidence**: feature 004's live e2e caught this class on the cluster; the Server's `resolveRefs` has the same shape (feature-004 analysis flagged it as latent). Covered by an e2e bundle (`10-server-with-network`) that would otherwise fail on persist.

---

## R-8 — e2e harness fixes (tech debt)

**Decision**:
- `make e2e.down` / `test/e2e/scripts/down.sh`: fix the cluster+registry detection (it reports "not found; skipping" when they exist — name-match bug) so teardown actually deletes them.
- kuttl multi-`--test`: the wrapper already gained a `KUTTL_TEST` filter (feature-004 session); ensure it forwards **all** selectors (kuttl honors repeated `--test`; verify the array build) so a scoped multi-bundle run executes every named bundle.
- Condition-order asserts: make the kuttl `status.conditions` asserts robust to ordering (the positional-array match made `09-server` flap) — assert with single-condition matchers or normalize order.

**Evidence**: observed live in the feature-004 e2e runs this session (`e2e.down` skipped a live cluster; `--test` honored only the last selector; `09-server-lifecycle` failed on `Ready` vs `Synced` ordering).

---

## R-9 — Connect-error condition-reason alignment (tech debt)

**Decision**: When a Connect-time ref resolution fails because a dependency isn't ready (`ErrTargetNotReady`), surface `Synced=False, reason=Reconciling` (dependency-wait) rather than the generic `ReconcileError`, consistently across compute/network/kubernetes connectors. crossplane-runtime's Connect-error path doesn't expose a custom reason directly, so the controllers map `ErrTargetNotReady` to a `Reconciling`-reason condition set on the MR before returning (or via the reconciler's error-to-condition hook). Documented as a known minor deviation in feature 003/004; this aligns it with the specs.

**Alternatives rejected**: leaving it as `ReconcileError` — acceptable but inconsistent with the FR wording; cheap to align.
