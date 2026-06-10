# Feature Specification: Custom Sizing (Configurators) + Group Tidy-up + Tech Debt

**Feature Branch**: `005-custom-sizing-configurators`

**Created**: 2026-06-07

**Status**: Draft

**Input**: User description: "Tech Debt pass, Container registry goes to kubernetes group, Then we implement custom sizings to first Server, then KuberneteCluster/nodegroups as presets are impossible to comprehend, ambigous id in the preset name"

## Context / Motivation

The feature-004 live e2e exposed that Timeweb **preset slugs are operator-hostile**: the catalog ships several identically-named tiers (e.g. four "K8S Promo (1 Rub)" masters) with no location to disambiguate, so an operator must append an opaque upstream id (`k8s-promo-1-rub-1999`) just to pick a tier. Presets are "impossible to comprehend." The remedy is the dashboard's **"Произвольная" (custom configurator) path**: the operator declares the resources they actually want — CPU, RAM, disk — and the provider resolves them to the right upstream configurator. This was scoped-and-deferred in feature 003 and is now the headline of this feature, applied to `Server` first and then `KubernetesCluster` / `KubernetesClusterNodepool`.

Bundled with it: a **group tidy-up** (move the ContainerRegistry kind into the kubernetes group — scope under clarification) and a **tech-debt pass** that fixes latent issues found during feature 004 (most importantly the same spec-mutation/CEL bug class on the `Server` controller that was fixed on `KubernetesCluster`).

## Clarifications

### Session 2026-06-07 (initial /speckit-specify pass)

- Q: ContainerRegistry → kubernetes group — migration scope + rationale? → A: **Hard move** to `kubernetes.m.timeweb.crossplane.io` (old `containerregistry.m.timeweb.crossplane.io` group removed). **Rationale**: the Timeweb dashboard co-locates registries *inside* the Kubernetes section — the "Kubernetes" page has two tabs, "Кластеры" (Clusters) and "Реестры контейнеров" (Container registries) — so panel users already think of registries as part of Kubernetes; the CRD group should mirror that. This is a breaking apiVersion change, acceptable because the project is pre-1.0 with no external consumers ([[user_project_owner]]). Both `ContainerRegistry` and `ContainerRegistryRepository` move; behavior is unchanged.
- Q: Once custom sizing exists, what happens to the preset path? → A: **Additive — keep presets first-class.** `presetName` stays fully supported; `resources` is an alternative (CEL: exactly one of the two). Non-breaking; named tiers remain available for operators who want them.
- Q: Which tech-debt items are in scope? → A: **Three**: (1) the `Server.resolveRefs` spec-mutation / `at-most-one` CEL-reject-on-persist latent bug (same fix as `KubernetesCluster` in feat 004); (2) e2e harness fixes (`make e2e.down` not deleting the k3d cluster/registry, the kuttl multi-`--test` scoping quirk, condition-order assert fragility); (3) Connect-error condition-reason alignment (feat-004 `reason=ReconcileError` vs the spec's intended `reason=Reconciling` for unready-dependency gating). **Deferred**: the network-less-cluster auto-created-VPC orphan (documented in feat-004 docs already; controller-side tracking is a separate future item).

### Session 2026-06-08 (/speckit-clarify pass)

- Q: Add a "CRaaS" capability (provision container-registry image-pull Secrets into K8s cluster namespaces via the undocumented `POST /api/v1/k8s/clusters/{id}/container-registry`) to this feature? → A: **No — deferred to a future feature 006.** It's net-new scope and 005 is already planned + tasked; folding it in would force a re-plan/re-tasks. It depends on 005's ContainerRegistry→kubernetes-group move, so it builds cleanly afterwards. The agreed design is captured in memory ([[project_craas_registry_pull_secrets_deferred]]) for 006: a `registries: [{ registryRef|registryID, pullSecretNamespaces:[…] }]` **field on the existing `KubernetesCluster` MR** (no separate kind, no `clusterRef`), reconciled **create/declare-only** (POST `registry_items`, never DELETE), `status.atProvider.registrySecrets[]` mirroring the per-item `{status, secretName}`, Observe probing GET with a POST-echo fallback. **No change to the 005 spec** results from this — recorded only as an explicit out-of-scope boundary.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Size a Server by typing CPU/RAM/disk (Priority: P1) 🎯

A platform operator declares a `Server` with `forProvider.resources: { cpu: 4, ramGB: 8, diskGB: 80 }` (plus optional `diskType`, `bandwidthMbps`, `gpu`, `cpuFrequencyTier`, `enableLocalNetwork`) instead of a `presetName`. The provider resolves those values to the correct upstream **configurator** and provisions the server — no preset slug, no upstream id typed by hand.

**Why this priority**: Directly removes the "presets are impossible to comprehend" pain on the most-used kind. An operator reasons in the units they care about (cores, GB) rather than memorizing ambiguous tier names.

**Independent Test**: Apply a `Server` with `forProvider.resources` (no `presetName`) + OS + location. Within the provisioning window it reaches `[Ready=True, Synced=True]`; `status.atProvider.lockedConfiguratorID` is populated and the running VM's CPU/RAM/disk match (or exceed, per tightest-fit) the requested values. SSH succeeds. Deleting removes the upstream VM.

**Acceptance Scenarios**:

1. **Given** a valid `ProviderConfig`, **When** the operator applies a `Server` with `forProvider.resources: { cpu, ramGB, diskGB }` and no `presetName`, **Then** the controller selects the tightest-fit configurator satisfying those values in the server's location, creates the VM, and records `lockedConfiguratorID` in status.
2. **Given** a `Server` MR that sets **both** `presetName` and `resources`, **When** applied, **Then** admission rejects it with a CEL error naming the mutually-exclusive fields.
3. **Given** a `Server` whose `resources` cannot be satisfied by any configurator in the chosen location (e.g. `cpu: 999`), **When** applied, **Then** the MR surfaces `Synced=False, reason=NoConfiguratorAvailable` with a message describing the unmet axis and the available bounds.
4. **Given** a live, `Ready=True` `Server` sized via `resources`, **When** the operator flips it to a `presetName` (or vice-versa), **Then** the MR surfaces `Synced=False, reason=SizingSwitchRequiresRecreate` (the sizing variant is immutable; recreate to change).

---

### User Story 2 — Size a Kubernetes cluster + nodepool by CPU/RAM/disk (Priority: P2)

The same custom-sizing path for `KubernetesCluster` (master nodes) and `KubernetesClusterNodepool` (workers): `forProvider.resources: { cpu, ramGB, diskGB }` (nodepool also `gpu`) instead of `presetName`, resolving to the upstream cluster/group `configuration` block.

**Why this priority**: This is where the ambiguity bites hardest (the feature-004 e2e literally failed on it). Lower than Server only because Server is the higher-traffic kind and proves the pattern first.

**Independent Test**: Apply a `KubernetesCluster` with `forProvider.resources` (no `presetName`) + a `KubernetesClusterNodepool` with `forProvider.resources`. Both reach `[Ready=True, Synced=True]`; nodes match the requested sizing; `kubectl get nodes` works. No ambiguous slug anywhere in the manifests.

**Acceptance Scenarios**:

1. **Given** a valid `ProviderConfig`, **When** the operator applies a `KubernetesCluster` with `forProvider.resources: { cpu, ramGB, diskGB }` and no `presetName`, **Then** the controller emits the upstream `configuration` block and the cluster provisions to that sizing, recording `lockedConfiguratorID`.
2. **Given** a `KubernetesClusterNodepool` with `forProvider.resources: { cpu, ramGB, diskGB, gpu }`, **When** applied against a `Ready` cluster, **Then** the worker group provisions to that sizing.
3. **Given** a cluster/nodepool with **both** `presetName` and `resources`, **When** applied, **Then** admission rejects it (CEL mutual-exclusion). Switching sizing variant on a live resource surfaces `SizingSwitchRequiresRecreate`.

---

### User Story 3 — ContainerRegistry lives in the kubernetes group (Priority: P2)

Operators find the ContainerRegistry kind under the `kubernetes.m.timeweb.crossplane.io` group — a **hard move** from its own `containerregistry.m.timeweb.crossplane.io` group (old group removed). This mirrors the Timeweb dashboard, which lists container registries as a tab *inside* the Kubernetes section ("Реестры контейнеров" next to "Кластеры"). Both `ContainerRegistry` and `ContainerRegistryRepository` relocate.

**Why this priority**: Architectural tidy-up requested alongside the sizing work; user-visible because it changes the apiVersion operators write. Acceptable as breaking because the project is pre-1.0 with no external consumers.

**Independent Test**: After the change, `kubectl get containerregistries.kubernetes.m.timeweb.crossplane.io` lists registries and a manifest under the new group reconciles to `[Ready=True, Synced=True]`; the old `containerregistry.m.timeweb.crossplane.io` group is gone.

**Acceptance Scenarios**:

1. **Given** the provider installed, **When** an operator applies a `ContainerRegistry` (+ `ContainerRegistryRepository`) under the new group, **Then** it reconciles identically to before the move (no behavior change, only the group).

---

### User Story 4 — Tech-debt pass (Priority: P3)

Maintainers clean up concrete latent issues found during feature 004 so the provider is consistent and the e2e harness is reliable.

**Why this priority**: Hardening, not new operator capability — but one item is a latent correctness bug (below).

**Independent Test**: The targeted items each have a verification (unit test added, e2e bundle passes, or a script behaves correctly). The full unit suite + lint stay green.

**Acceptance Scenarios**:

1. **Given** a `Server` with a `networkRef` (or project/sshKey ref), **When** the controller resolves it, **Then** it does NOT mutate `spec.forProvider` (so the `at-most-one` CEL rule cannot reject the object on persist) — the same fix already applied to `KubernetesCluster` in feature 004. *(Latent bug: `Server.resolveRefs` currently writes the resolved id back onto spec.)*
2. **Given** the e2e teardown, **When** `make e2e.down` runs, **Then** it actually deletes the k3d cluster + registry (the current detection skips them with "not found" when they exist).
3. **Given** a scoped e2e run, **When** multiple bundles are selected, **Then** the wrapper honors all selectors (kuttl's repeated `--test` quirk is worked around).
2. **Given** the e2e teardown, **When** `make e2e.down` runs, **Then** it actually deletes the k3d cluster + registry; and a scoped multi-bundle e2e run honors all selectors (kuttl repeated-`--test` quirk worked around); and condition-order asserts are robust (no positional-array fragility).
3. **Given** a dependent MR (e.g. nodepool) whose parent isn't ready, **When** it reconciles, **Then** the surfaced reason matches the spec's intended `Reconciling` for unready-dependency gating (not a generic `ReconcileError`), consistently across compute/network/kubernetes controllers.

**In scope (per clarification)**: (a) the `Server.resolveRefs` spec-mutation / `at-most-one` CEL latent bug, (b) e2e harness fixes (`e2e.down`, multi-`--test`, condition-order), (c) Connect-error condition-reason alignment. **Deferred**: controller-side handling of the network-less-cluster auto-created-VPC orphan (already documented operator-side in feature 004).

### Edge Cases

- **Configurator catalog empty / location unsupported**: `resources` in a location with no configurators → `Synced=False, reason=NoConfiguratorAvailable` with the location named.
- **Partial sizing**: `cpu`, `ramGB`, `diskGB` are all required (CRD-required); omitting any is an admission error. Other axes (`diskType`, `bandwidthMbps`, `gpu`, `cpuFrequencyTier`, `enableLocalNetwork`) are optional filters/capabilities.
- **GPU on a non-GPU configurator**: `gpu` requested where no configurator offers it → `NoConfiguratorAvailable`.
- **ContainerRegistry old-group manifests after the move**: behavior depends on the US3 clarification (hard move vs alias).
- **Sizing-variant flip**: switching `presetName` ↔ `resources` on a live resource → `SizingSwitchRequiresRecreate` (no in-place resize in this feature).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `Server.spec.forProvider` MUST accept a `resources` object — required `cpu` (cores), `ramGB` (GB), `diskGB` (GB); optional `diskType`, `bandwidthMbps`, `gpu`, `cpuFrequencyTier`, `enableLocalNetwork` — as an alternative to `presetName`. The controller converts `ramGB`/`diskGB` to the upstream MB units when matching/emitting.
- **FR-002**: `Server.spec.forProvider` MUST enforce **exactly one** of `{presetName, resources}` via admission-time CEL (mutual exclusion).
- **FR-003**: The controller MUST resolve a `resources` block to one upstream server configurator using the existing in-controller resolver `Configurator` dimension (`DimServerConfigurator`, promoted from its `fetchUnwired` stub to a real fetcher over `GetConfigurators`). Selection = hard-filter by `(location, diskType, enableLocalNetwork, cpuFrequencyTier)`, capability-filter by the `requirements` bounds containing the requested `cpu`/`ramGB`/`diskGB`/`bandwidthMbps`, tightest-fit rank, lowest-id tiebreak.
- **FR-004**: On successful create the controller MUST record `status.atProvider.lockedConfiguratorID` (distinct from `lockedPresetID`). Changing the sizing variant (`presetName` ↔ `resources`) on a live resource MUST surface `Synced=False, reason=SizingSwitchRequiresRecreate`.
- **FR-005**: An unsatisfiable `resources` request MUST surface `Synced=False, reason=NoConfiguratorAvailable` with a message naming the unmet axis/location.
- **FR-006**: `KubernetesCluster.spec.forProvider` MUST accept a `resources` object (`cpu`, `ramGB`, `diskGB`) as an alternative to master `presetName`, with the same CEL mutual-exclusion, configurator resolution, `lockedConfiguratorID`, and sizing-switch behavior. The controller emits the upstream cluster `configuration` block.
- **FR-007**: `KubernetesClusterNodepool.spec.forProvider` MUST accept a `resources` object (`cpu`, `ramGB`, `diskGB`, optional `gpu`) as an alternative to worker `presetName`, with the same rules, emitting the upstream nodegroup `configuration` block.
- **FR-008**: Existing `presetName`-based sizing MUST continue to work unchanged — custom sizing is **additive**. Presets remain a first-class, supported path (operators who want named tiers keep them); `resources` is an alternative, CEL-enforced as exactly-one-of.
- **FR-009**: The provider MUST publish `ContainerRegistry` + `ContainerRegistryRepository` under `kubernetes.m.timeweb.crossplane.io` and remove the old `containerregistry.m.timeweb.crossplane.io` group (hard move). Reconciliation behavior, fields, and resolver wiring MUST be unchanged by the move — only the API group/apiVersion changes. Generated artifacts (CRDs, deepcopy, scheme registration, `main.go` wiring, docs) MUST be updated in the same change set.
- **FR-010**: The `Server` controller's reference resolution MUST NOT mutate `spec.forProvider` (carry resolved upstream ids on the external client instead), eliminating the latent `at-most-one` CEL-rejection-on-persist bug — matching the `KubernetesCluster` fix from feature 004.
- **FR-011**: The e2e harness MUST tear down cleanly (`make e2e.down` removes the k3d cluster + registry) and MUST honor multi-bundle test scoping. Selected tech-debt items (per the US4 clarification) MUST each ship with a verification.
- **FR-012**: All changes MUST keep the unit-test suite + `make lint` green and regenerate any `apis/`-derived artifacts in the same change set (Constitution §I).

### Key Entities

- **Server.resources** (new optional block): operator-facing sizing — `cpu`, `ramGB`, `diskGB` (required) + `diskType`/`bandwidthMbps`/`gpu`/`cpuFrequencyTier`/`enableLocalNetwork` (optional). Resolves to upstream `configurator_id`. Mutually exclusive with `presetName`.
- **KubernetesCluster.resources / KubernetesClusterNodepool.resources** (new optional blocks): `cpu`/`ramGB`/`diskGB` (+ `gpu` for nodepool). Emit the upstream `configuration` block. Mutually exclusive with `presetName`.
- **status.atProvider.lockedConfiguratorID** (new, on all three kinds): the resolved configurator id, drives drift + sizing-switch detection.
- **ContainerRegistry / ContainerRegistryRepository** (relocated kinds): same shape/behavior, new API group (US3).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can provision a `Server` by typing only `cpu`/`ramGB`/`diskGB` (no preset slug, no upstream id) and the running VM meets or exceeds those values, reaching `Ready=True` within the same window as the preset path.
- **SC-002**: An operator can provision a `KubernetesCluster` + nodepool by `cpu`/`ramGB`/`diskGB` with **zero** ambiguous preset slugs in the manifests; `kubectl get nodes` shows the requested sizing.
- **SC-003**: An unsatisfiable sizing request yields a `Synced=False` condition within two reconcile cycles whose message names the unmet axis and the available bounds — correctable without external docs.
- **SC-004**: A `Server` with a `networkRef` reaches `Ready=True` (no CEL `at-most-one` rejection) — the latent bug is gone, proven by an e2e bundle that previously would have failed.
- **SC-005**: `make e2e.down` leaves zero residual k3d clusters/registries; a scoped multi-bundle e2e run executes exactly the selected bundles.
- **SC-006**: `ContainerRegistry` manifests under the new group reconcile identically to the pre-move behavior (no functional regression).

## Assumptions

- The configurator resolver primitive from feature 002 (`DimensionConfigurator`, `ConfiguratorInput/Entry`, `SelectConfigurator`, `CapacityBound`) is reused unchanged; this feature promotes `DimServerConfigurator` to a real fetcher and adds K8s configurator wiring. The exact Server `resources` field set + selection algorithm follow the feature-003 deferral clarification verbatim.
- Required sizing axes are `cpu`, `ramGB`, `diskGB`; all other axes are optional filters/capabilities. Units are operator-friendly: `cpu` in cores, `ramGB` and `diskGB` in **GB**. The controller normalizes to the upstream `configuration`/`requirements` units (the Timeweb API expresses ram + disk in **MB**), so `ramGB`/`diskGB` are multiplied by 1024 before matching/emitting.
- Custom sizing is **additive** — presets keep working as a first-class path (clarified 2026-06-07).
- The ContainerRegistry move targets `kubernetes.m.timeweb.crossplane.io` as a hard move (old group removed), mirroring the dashboard's Kubernetes→"Реестры контейнеров" tab (clarified 2026-06-07).
- Sizing is **create-time immutable** (no in-place resize); switching variants requires recreate. Resize is a separate future feature.
- The three kinds are namespaced v2 ModernManaged resources; no changes to ProviderConfig, refs, or the catalog cache contract.
- The user's stated sequence (tech-debt → CR move → Server sizing → K8s sizing) maps to independently-shippable PRs; priorities above reflect operator value (sizing is the headline) but any order is viable.
- **Out of scope (deferred to feature 006)**: CRaaS — provisioning container-registry image-pull Secrets into K8s cluster namespaces (the undocumented `POST /k8s/clusters/{id}/container-registry`). Builds on this feature's CR-in-kubernetes-group move; agreed design captured in [[project_craas_registry_pull_secrets_deferred]].
