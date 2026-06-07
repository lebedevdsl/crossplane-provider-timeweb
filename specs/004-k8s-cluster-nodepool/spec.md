# Feature Specification: Managed Kubernetes (Cluster + Nodepool + Addon MRs)

**Feature Branch**: `004-k8s-cluster-nodepool`

**Created**: 2026-06-06

**Status**: Draft

**Input**: User description: "KubernetesCluster + KuberneteClusterNodepool implementation"

## Clarifications

### Session 2026-06-06 (initial /speckit-specify pass)

- Q: Which master/worker sizing path does v0.x expose — fixed preset or custom configurator? → A: **Preset only**, mirroring the locked feature-003 `Server` decision. Both `KubernetesCluster` (master nodes) and `KubernetesClusterNodepool` (worker nodes) take a `presetName` slug resolved via the in-controller catalog resolver to the upstream `preset_id`. The custom `configuration{cpu, ram, disk}` path the upstream `ClusterIn`/`NodeGroupIn` bodies also accept is **deferred to a follow-up feature** (same staging as the Server configurator). The upstream "Нельзя передавать вместе" (mutually exclusive `preset_id` vs `configuration`) constraint means the v0.x CRDs simply never emit `configuration`.
- Q: Which optional cluster capabilities ship in this feature vs deferred? → A: **Networking refs + Addons (as their own MR kind).** In scope: VPC attach via `networkRef`/`networkSelector`/`networkID` (reusing the 003 `Network` resolver) and project assignment via `projectRef`/`projectSelector`/`projectID`. Addons are modeled as a **separate `KubernetesClusterAddon` MR kind** (one MR per installed addon, referencing its cluster), NOT an inline list on the Cluster spec. **Deferred**: `is_ingress` / `is_k8s_dashboard` toggles, `oidc_provider`, `maintenance_slot`, `cluster_network_cidr` overrides.
- Q: Which day-2 operations are in scope (and thus drive user stories)? → A: **All four selected** — (1) nodepool **scaling** (mutable `node_count`), (2) **autoscaling/autohealing** (`is_autoscaling` + `min-size`/`max-size`, `is_autohealing` set at nodepool create), (3) **k8s version upgrade** (mutable `k8s_version` → `PATCH /k8s/clusters/{id}/versions/update`), and (4) **kubeconfig** published as the cluster's connection Secret (`GET /k8s/clusters/{id}/kubeconfig`).
- Q: How are worker nodes modeled relative to the Cluster MR (inline worker_groups vs separate Nodepool MRs only)? → A: **Nodepool-MR-only.** Clean Crossplane split: the `KubernetesCluster` MR is the control plane and carries **no** inline worker groups; every worker pool is a standalone `KubernetesClusterNodepool` MR created via the `/groups` endpoint and referencing its cluster. This relies on a cluster being creatable with zero worker groups — the upstream `ClusterIn` schema lists `worker_groups` as **optional** (only `name`, `k8s_version`, `network_driver` are required). Per the operator's 2026-06-06 decision, a published API contract that marks a field optional is treated as authoritative — no live probe needed. (If the live API is ever found to contradict its own published contract here, the fallback is a required inline bootstrap `nodepool` block on Cluster create, with additional pools as standalone MRs; not anticipated.)

### Session 2026-06-06 (/speckit-clarify pass)

- Q: How should the operator specify `k8sVersion`, and how does the resolver match it? → A: **Exact version, 1:1 match.** The operator types the full upstream version string (e.g. `1.31.2`), matched exactly against `/api/v1/k8s/k8s-versions` (mirroring the `ServerOSImage` exact-match idiom). No minor-version-to-latest-patch resolution. An upgrade is a change to another exact catalog version; drift detection compares exact strings.
- Q: What CRD shape should `KubernetesClusterNodepool.labels` use (upstream sends an array)? → A: **`map[string]string`.** Crossplane/k8s-idiomatic map; the controller marshals it to the upstream array shape on create.
- Q: What provisioning time targets should the Success Criteria (and e2e timeouts) use? → A: **Cluster `Ready=True` ≤ 20 minutes, Nodepool `Ready=True` ≤ 15 minutes** from `kubectl apply` (control planes provision slower than the Server MR's 10-min bound).
- Q: Should v0.x support HA control planes via `masterNodesCount`? → A: **Yes — optional, default 1, allow HA.** Expose `masterNodesCount` (CRD default `1`; operator may set `3` for HA); the value is forwarded upstream and validated by the API.

### Session 2026-06-06 (/speckit-analyze remediation)

- Q: How should the cluster↔network region-compatibility check (FR-017) behave, given cluster `availabilityZone` codes (`spb-3`/`msk-1`/…) and VPC `location` codes (`ru-1`/`ru-2`/…) are different namespaces? → A: **No client-side pre-flight.** Maintaining an AZ→region mapping would drift from Timeweb's real topology. The controller forwards the resolved `network_id` and surfaces any upstream incompatibility rejection as `Synced=False, reason=ReconcileError`. FR-017 + the related edge case revised accordingly; no `ErrNetworkLocationMismatch`.
- Q: Addon identity — `name` (pre-probe draft) or the API-accurate shape? → A: **`type` + `version`** (+ optional `yamlConfig`/`configType`). The upstream install body is `{type, config_type, yaml_config, version}` and the catalog (`addons-configs`) keys on `type`+`version`; there is no separate human "name". FR-014, US5, and the Key Entities entry revised to match the downstream artifacts (research R-9, data-model, contracts, tasks).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Provision a managed Kubernetes cluster with a worker pool (Priority: P1) 🎯 MVP

A platform operator declares a `KubernetesCluster` MR (name, k8s version, network driver, availability zone, master preset) and a `KubernetesClusterNodepool` MR that references it (`clusterRef`) carrying a worker preset + node count. The Cluster controller creates the upstream cluster, the Nodepool controller creates the worker group once the cluster is ready, and the Cluster publishes a kubeconfig connection Secret. The operator uses that kubeconfig to reach a working cluster.

**Why this priority**: This is the entire user-facing payoff — a GitOps-driven path to a working managed Kubernetes cluster using only Kubernetes manifests, with no preset/version IDs typed by hand and a kubeconfig that drops straight into a Secret for downstream tooling.

**Independent Test**: With the provider installed and a `ProviderConfig` carrying a valid Timeweb token, an operator applies a `KubernetesCluster` MR with `forProvider.{name, k8sVersion, networkDriver: cilium, availabilityZone: msk-1, presetName: <smallest-master-slug>}` and a `KubernetesClusterNodepool` MR with `forProvider.{clusterRef.name: <cluster-mr>, presetName: <smallest-worker-slug>, nodeCount: 2}`. Within the provisioning window the Cluster reports `Ready=True, Synced=True` with `status.atProvider.upstreamID` + `status.atProvider.state` populated; the Nodepool reports `Ready=True, Synced=True` with its group ID + observed node count; and the Cluster's connection Secret contains a `kubeconfig` key. `kubectl --kubeconfig <secret> get nodes` lists the worker nodes. `kubectl delete` of both MRs removes the upstream cluster + group.

**Acceptance Scenarios**:

1. **Given** a `ProviderConfig` resolving to a valid token, **When** the operator applies a `KubernetesCluster` MR with a valid `presetName` + `k8sVersion` + `networkDriver` + `availabilityZone`, **Then** the controller resolves the master preset slug to an upstream `preset_id` from a warm-cache catalog GET, creates the cluster upstream, polls until upstream `status` reaches the active state, reports `Ready=True, Synced=True`, and publishes a connection Secret containing `kubeconfig`.
2. **Given** a `KubernetesCluster` MR in `Ready=True`, **When** the operator applies a `KubernetesClusterNodepool` MR with `clusterRef.name` pointing at it and `nodeCount: 2`, **Then** the controller resolves the ref to the upstream cluster ID, creates the worker group upstream, and reports `Ready=True, Synced=True` with `status.atProvider.{upstreamID, observedNodeCount}` populated once the group's nodes are provisioned.
3. **Given** a `KubernetesClusterNodepool` MR whose `clusterRef` target is not yet `Ready=True`, **When** the operator applies it, **Then** the Nodepool stays `Synced=False, reason=Reconciling` with a message naming the cluster dependency it is waiting on, and creates the group only after the cluster reaches `Ready=True`.
4. **Given** a `KubernetesCluster` MR with a `presetName` or `k8sVersion` not present in the operator's catalog view, **When** the operator applies it, **Then** the MR surfaces `Synced=False, reason=ReconcileError` with a resolver-style message listing the valid slugs / versions visible to the credential (same UX as `Server`/`ContainerRegistry`).

---

### User Story 2 — Scale a worker pool up and down (Priority: P2)

A platform operator changes `forProvider.nodeCount` on a `KubernetesClusterNodepool` MR (or enables autoscaling at create time). The controller adds or removes worker nodes to converge the group to the requested size.

**Why this priority**: Elasticity is the primary day-2 operation for a managed cluster. Lower than P1 because a fixed-size cluster is independently useful; scaling is a self-contained second slice.

**Independent Test**: With a `KubernetesClusterNodepool` MR in `Ready=True` at `nodeCount: 2`, patch `forProvider.nodeCount: 4`. The controller adds two nodes via the upstream group/nodes endpoints; within the provisioning window `status.atProvider.observedNodeCount` reaches 4 and `Ready=True` is retained. Patch back to `nodeCount: 2`; the controller removes two nodes and converges. Separately, apply a Nodepool with `forProvider.autoscaling: {enabled: true, minSize: 2, maxSize: 6}`; the group is created with autoscaling enabled and `nodeCount` is treated as ignored/advisory while autoscaling is on.

**Acceptance Scenarios**:

1. **Given** a `KubernetesClusterNodepool` in `Ready=True` at `nodeCount: 2`, **When** the operator patches `forProvider.nodeCount: 4`, **Then** the controller issues upstream node-add calls until `status.atProvider.observedNodeCount == 4`, keeping `Synced=True`, and does not recreate the group.
2. **Given** a `KubernetesClusterNodepool` in `Ready=True` at `nodeCount: 4`, **When** the operator patches `forProvider.nodeCount: 2`, **Then** the controller removes exactly two nodes and converges `observedNodeCount` to 2 without disturbing the remaining nodes.
3. **Given** a `KubernetesClusterNodepool` MR with `forProvider.autoscaling: {enabled: true, minSize: 2, maxSize: 6}` and `forProvider.autohealing: true`, **When** the operator applies it, **Then** the group is created with autoscaling + autohealing enabled, the `minSize`/`maxSize` bounds are sent upstream, and the controller does NOT fight the upstream autoscaler over `nodeCount`.
4. **Given** a `KubernetesClusterNodepool` MR, **When** the operator patches a create-only field that the upstream group API cannot mutate (e.g. `presetName`), **Then** the MR surfaces `Synced=False, reason=ImmutableFieldChange` naming the field and instructing delete-and-recreate.

---

### User Story 3 — Attach the cluster to a private network and project (Priority: P2)

A platform operator wires a `KubernetesCluster` MR into an existing private network (via a `networkRef` to a `Network` MR, or a `networkID` escape hatch) and assigns it to a Timeweb project (via `projectRef`/`projectID`).

**Why this priority**: Private networking and project placement are standard for production clusters and reuse the resolver patterns already shipped in feature 003. Same priority as scaling; independent slice.

**Independent Test**: Apply a `Network` MR (per feature 003); once `Ready=True`, apply a `KubernetesCluster` MR adding `forProvider.networkRef.name: <network-mr>` and `forProvider.projectRef.name: <project-mr>`. The controller resolves both refs to upstream IDs before create, the cluster is created attached to the VPC and placed in the project, and `status.atProvider.{resolvedNetworkID, resolvedProjectID}` reflect the resolved values.

**Acceptance Scenarios**:

1. **Given** a `Network` MR in `Ready=True` with `status.atProvider.upstreamID: vpc-xyz`, **When** the operator applies a `KubernetesCluster` MR with `forProvider.networkRef.name: <network-mr>`, **Then** the controller resolves the ref and creates the cluster attached to `vpc-xyz`, recording `resolvedNetworkID` in status.
2. **Given** a `KubernetesCluster` MR with both `networkRef` and `networkID` set, **When** the operator applies it, **Then** admission rejects the resource with a CEL error naming the mutually-exclusive fields. The same at-most-one rule applies to the `{projectRef, projectSelector, projectID}` trio.
3. **Given** a `KubernetesCluster` MR with `forProvider.networkID: <externally-managed-vpc-id>` and no `networkRef`, **When** the operator applies it, **Then** the controller attaches the cluster to that VPC without looking up any `Network` MR, and deleting the Cluster MR leaves the VPC untouched.

---

### User Story 4 — Upgrade the cluster Kubernetes version (Priority: P3)

A platform operator bumps `forProvider.k8sVersion` on a `Ready=True` `KubernetesCluster` MR to a newer supported version. The controller triggers the upstream in-place version upgrade.

**Why this priority**: Version upgrades are an important but less-frequent day-2 operation; clusters deliver value before the first upgrade. Distinct from immutable-field changes because the upstream API exposes a dedicated upgrade path.

**Independent Test**: With a `KubernetesCluster` MR in `Ready=True` at `k8sVersion: <v1>`, patch `forProvider.k8sVersion: <v2>` where `<v2>` is newer and present in the upstream version catalog. The controller calls the upstream version-update endpoint, the cluster transitions through an upgrading state and back to `Ready=True`, and `status.atProvider.k8sVersion` reflects `<v2>`.

**Acceptance Scenarios**:

1. **Given** a `KubernetesCluster` MR in `Ready=True`, **When** the operator patches `forProvider.k8sVersion` to a newer catalog-valid version, **Then** the controller invokes the upstream version-update call (not a recreate), the MR reports `Ready=False` with an `Upgrading` reason during the transition, and returns to `Ready=True` with the new version observed in status.
2. **Given** a `KubernetesCluster` MR, **When** the operator patches `k8sVersion` to a value not in the upstream version catalog (or a downgrade), **Then** the MR surfaces `Synced=False, reason=ReconcileError` with a message listing valid upgrade targets; no upstream call mutates the cluster.

---

### User Story 5 — Install and remove cluster addons (Priority: P3)

A platform operator declares a `KubernetesClusterAddon` MR referencing a `KubernetesCluster` and naming an addon to install (from the cluster's available-addons catalog). The controller installs the addon; deleting the MR removes it.

**Why this priority**: Addons (ingress controllers, monitoring, etc.) are valuable but optional; a cluster + workers is useful without them. Modeled as their own MR for clean lifecycle ownership.

**Independent Test**: With a `KubernetesCluster` MR in `Ready=True`, apply a `KubernetesClusterAddon` MR with `forProvider.{clusterRef.name: <cluster-mr>, type: <addon-type>, version: <addon-version>}`. The controller installs the addon via the upstream addons endpoint and reports `Ready=True` once installed. Deleting the MR removes the addon from the cluster; the cluster itself is unaffected.

**Acceptance Scenarios**:

1. **Given** a `KubernetesCluster` MR in `Ready=True`, **When** the operator applies a `KubernetesClusterAddon` MR whose `type`+`version` match a valid entry in that cluster's available-addons catalog, **Then** the controller installs the addon and reports `Ready=True, Synced=True` with the upstream addon ID in status.
2. **Given** an installed `KubernetesClusterAddon` MR, **When** the operator deletes it, **Then** the controller removes the addon from the cluster (idempotent on 404) and the cluster stays `Ready=True`.
3. **Given** a `KubernetesClusterAddon` MR whose `type`/`version` is not in the cluster's catalog, **When** the operator applies it, **Then** the MR surfaces `Synced=False, reason=ReconcileError` listing the valid addon types for that cluster.

### Edge Cases

- **Region/AZ not available**: operator types an `availabilityZone` upstream marks unavailable (e.g. `fra-1` sold out) → controller forwards the request, upstream returns 4xx, MR surfaces `Synced=False, reason=ReconcileError` with the upstream message. The CRD enum MUST NOT hard-block any AZ the API advertises, to allow recovery when capacity returns.
- **Network/cluster region incompatibility**: the chosen cluster `availabilityZone` is not compatible with the referenced VPC's region. The controller does no client-side pre-check (FR-017); it forwards the request and surfaces the upstream rejection as `Synced=False, reason=ReconcileError` with the upstream message.
- **Deleting a cluster with live nodepools/addons**: Crossplane v2 does NOT enforce cross-MR finalization. Deleting the `KubernetesCluster` MR while `KubernetesClusterNodepool`/`KubernetesClusterAddon` MRs still reference it is allowed at the Kubernetes layer; the controller logs a warning per orphaned dependent and those dependents surface `Synced=False, reason=ReconcileError` ("referenced cluster not found") on their next reconcile.
- **Scaling below upstream minimum / above quota**: a `nodeCount` the upstream rejects (account quota, group minimum) surfaces `Synced=False, reason=ReconcileError` with the upstream message; the controller does not partially apply.
- **Version-upgrade in flight**: patching `nodeCount` while a version upgrade is transitioning → the controller defers the scaling reconcile until the cluster returns to a steady state rather than issuing conflicting calls.
- **Payment/quota (`no_paid`)**: a cluster stuck in the upstream billing-blocked state maps to `Ready=False, reason=PaymentRequired` (same handling as the Server MR), not a controller error.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The provider MUST publish a namespaced `KubernetesCluster` MR kind in API group `kubernetes.m.timeweb.crossplane.io/v1alpha1` (following the `<svc>.m.timeweb.crossplane.io` convention). It MUST be a Crossplane v2 ModernManaged resource using the shared `ProviderConfigSpec` from feature 002.
- **FR-002**: The provider MUST publish a namespaced `KubernetesClusterNodepool` MR kind in the same API group, representing one upstream worker group, referencing its parent cluster via `forProvider.clusterRef`/`clusterSelector` (crossplane-style, same-namespace) plus a flat `clusterID` escape hatch (at most one set; admission-time CEL).
- **FR-003**: The provider MUST publish a namespaced `KubernetesClusterAddon` MR kind in the same API group, representing one installed cluster addon, referencing its parent cluster via the same `clusterRef`/`clusterSelector`/`clusterID` trio.
- **FR-004**: `KubernetesCluster.spec.forProvider` MUST require: `name` (string), `k8sVersion` (string, resolved/validated against the upstream version catalog), `networkDriver` (enum: `kuberouter`, `calico`, `flannel`, `cilium`), `availabilityZone` (enum of upstream AZ codes — `spb-3`, `msk-1`, `ams-1`, `fra-1` — not hard-blocking sold-out zones), and `presetName` (master-node preset slug resolved to upstream `preset_id`).
- **FR-005**: `KubernetesCluster.spec.forProvider` MUST accept optional: `description` (string), `masterNodesCount` (integer, CRD-default `1`; operators MAY set a higher count such as `3` for an HA control plane — the value is forwarded upstream and validated by the API), `networkRef`/`networkSelector`/`networkID` (at-most-one VPC attach trio, resolving to the upstream private-network ID), and the project-assignment trio `projectRef`/`projectSelector`/`projectID` (at-most-one; resolves to upstream `project_id`; default project when all unset). The Cluster MR carries **no** inline worker groups — all worker pools are standalone `KubernetesClusterNodepool` MRs (Nodepool-MR-only model; the cluster is created with zero worker groups, which the upstream `ClusterIn` contract permits).
- **FR-006**: The controller MUST resolve `presetName` (master) and the Nodepool's `presetName` (worker) to upstream `preset_id` values via the in-controller catalog resolver primitive (feature 002), using a `K8sPreset` (Preset-kind) dimension whose upstream endpoint is `/api/v1/presets/k8s`. The slug rule follows the established convention; mismatches surface `Synced=False, reason=ReconcileError` listing valid slugs.
- **FR-007**: The controller MUST resolve/validate `k8sVersion` against the upstream version catalog (`/api/v1/k8s/k8s-versions`) via a resolver `K8sVersion` (Enum-kind) dimension. The operator supplies the **exact** upstream version string (e.g. `1.31.2`), matched 1:1 against the catalog — there is no minor-version-to-latest-patch resolution. Mismatches surface `Synced=False, reason=ReconcileError` listing the valid exact versions. The `networkDriver` enum is validated by the CRD itself (no catalog lookup).
- **FR-008**: The controller MUST record resolved upstream identifiers + observed state in `KubernetesCluster.status.atProvider` on first successful Create and subsequent observes: `upstreamID` (cluster ID), `state` (upstream cluster status string), `k8sVersion` (observed), `lockedPresetID` (resolved master preset), `resolvedNetworkID`, `resolvedProjectID`, and the master sizing readout (`cpu`, `ram`, `disk`) the upstream returns.
- **FR-009**: `KubernetesClusterNodepool.spec.forProvider` MUST require `name`, the cluster reference trio (FR-002), `presetName` (worker preset slug), and `nodeCount` (integer). It MUST accept optional `labels` (a `map[string]string`, which the controller marshals to the upstream array shape on create), `autoscaling` (`{enabled: bool, minSize: int, maxSize: int}`), and `autohealing` (bool). When `autoscaling.enabled` is true the controller MUST NOT reconcile `nodeCount` against the upstream count (the autoscaler owns it).
- **FR-010**: The Nodepool controller MUST create the worker group via the upstream `POST /k8s/clusters/{cluster_id}/groups` endpoint only after the referenced cluster is `Ready=True`; until then it stays `Synced=False, reason=Reconciling` naming the dependency. `status.atProvider` MUST carry `upstreamID` (group ID) and `observedNodeCount`.
- **FR-011**: Changing `KubernetesClusterNodepool.forProvider.nodeCount` on a `Ready=True` pool (with autoscaling off) MUST converge the upstream node count by adding/removing nodes via the upstream group/nodes endpoints — NOT by recreating the group. Changing a create-only field (e.g. `presetName`) MUST surface `Synced=False, reason=ImmutableFieldChange`.
- **FR-012**: Changing `KubernetesCluster.forProvider.k8sVersion` on a `Ready=True` cluster to a newer catalog-valid version MUST trigger the upstream in-place upgrade (`PATCH /k8s/clusters/{id}/versions/update`), surfacing `Ready=False, reason=Upgrading` during the transition. A non-catalog or downgrade target MUST be rejected with `Synced=False, reason=ReconcileError`. Changing `name`/`description` MUST PATCH normally (`PATCH /k8s/clusters/{id}`). Changing other create-only fields (`networkDriver`, `availabilityZone`, master `presetName`, `masterNodesCount`, and the network/project bindings) MUST surface `Synced=False, reason=ImmutableFieldChange` (the control-plane size is fixed at create; recreate to change).
- **FR-013**: The `KubernetesCluster` controller MUST publish a connection Secret (via `writeConnectionSecretToRef`) containing at minimum a `kubeconfig` key fetched from `GET /k8s/clusters/{id}/kubeconfig`, created in the MR's namespace per Crossplane v2 modern-managed semantics, within one reconcile cycle of `Ready=True`.
- **FR-014**: `KubernetesClusterAddon.spec.forProvider` MUST require the cluster reference trio (FR-003), `type` (the addon identifier), and `version`, both validated against that cluster's available-addons catalog `GET /k8s/clusters/{id}/addons-configs`. It MAY accept optional `yamlConfig` (override; defaults to the catalog `yaml_config`) and `configType`. The controller installs via `POST /k8s/clusters/{id}/addons` (body `{type, config_type, yaml_config, version}`) and removes via `DELETE /k8s/clusters/{id}/addons/{addon_id}` (idempotent on 404). `status.atProvider` carries the upstream `addonID`.
- **FR-015**: All three MR kinds MUST publish standard Crossplane `Synced` + `Ready` conditions per Constitution §II. Cluster `Ready` mapping: upstream active status → `Ready=True`; provisioning/upgrading/billing-blocked → `Ready=False` with `Provisioning`/`Upgrading`/`PaymentRequired` reasons. Nodepool `Ready=True` once `observedNodeCount` matches the desired count (autoscaling off) or the group is healthy (autoscaling on); `Ready=False` `reason ∈ {Provisioning, Scaling}` while the group is being created or a node-count delta is converging. Deletion-in-flight follows standard runtime behavior (`Ready=False, reason=Deleting`).
- **FR-016**: Deleting a `KubernetesCluster` MR (default `managementPolicies: [*]`) MUST trigger an upstream cluster delete and wait for the resource to disappear (idempotent on 404 per Constitution §II). Deleting a Nodepool or Addon MR MUST remove only that group/addon, leaving the cluster intact. Cross-MR deletion ordering is NOT enforced by the controller (Crossplane v2 semantics); orphaned dependents surface a clear `referenced cluster not found` error.
- **FR-017**: The controller MUST NOT perform a client-side cluster-AZ ↔ network-region compatibility check — cluster `availabilityZone` codes (`spb-3`/`msk-1`/…) and VPC `location` codes (`ru-1`/`ru-2`/…) are different namespaces, and a hand-maintained mapping would drift from Timeweb's real topology. Instead the controller forwards the resolved `network_id` on Create and surfaces any upstream incompatibility rejection verbatim as `Synced=False, reason=ReconcileError` with the upstream message. This applies equally to `networkRef`/`networkSelector` and to `networkID`-imported VPCs.

### Key Entities

- **KubernetesCluster** (namespaced, `kubernetes.m.timeweb.crossplane.io/v1alpha1`): The managed Kubernetes control plane. `spec.forProvider` carries `name`, `k8sVersion`, `networkDriver`, `availabilityZone`, master `presetName`, optional `description`/`masterNodesCount`, and the network + project reference trios. `status.atProvider` carries `upstreamID`, `state`, observed `k8sVersion`, `lockedPresetID`, `resolvedNetworkID`, `resolvedProjectID`, and master sizing (`cpu`/`ram`/`disk`). Publishes a `kubeconfig` connection Secret. References `Network` MRs (new dependency) and `Project` MRs (existing). Observed response envelope is `{cluster: …}`.
- **KubernetesClusterNodepool** (namespaced, same group): One upstream worker group. `spec.forProvider` carries the cluster reference trio, `presetName`, `nodeCount`, optional `labels` (`map[string]string`), `autoscaling{enabled,minSize,maxSize}`, `autohealing`. `status.atProvider` carries `upstreamID` (group ID) and `observedNodeCount`. References its parent `KubernetesCluster` MR. Observed response envelope is `{node_group: …}`.
- **KubernetesClusterAddon** (namespaced, same group): One installed cluster addon. `spec.forProvider` carries the cluster reference trio + addon `type` + `version` (+ optional `yamlConfig`/`configType`), validated against the cluster's addons catalog. `status.atProvider` carries `addonID`. References its parent `KubernetesCluster` MR.
- **Resolver dimensions added**: `K8sPreset` (Preset kind, `/api/v1/presets/k8s`) for master + worker preset slugs, and `K8sVersion` (Enum kind, `/api/v1/k8s/k8s-versions`) for version validation. Both wired into the existing `internal/controller/shared/resolver` registry. `networkDriver` is a fixed CRD enum (no catalog).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator who has applied a `ProviderConfig` can create a working managed Kubernetes cluster with a worker pool using only Kubernetes manifests (no preset/version IDs typed by hand) and obtain a usable kubeconfig from the connection Secret that lists the worker nodes via `kubectl get nodes`. The `KubernetesCluster` reaches `Ready=True` within **20 minutes** of `kubectl apply`, and its `KubernetesClusterNodepool` reaches `Ready=True` within **15 minutes** of the cluster becoming ready. (These bounds also set the e2e assert timeouts.)
- **SC-002**: An operator who types an unrecognized `presetName`, `k8sVersion`, or addon `type`/`version` sees a `Synced=False` condition within two reconcile cycles whose message lists the valid options visible to their credential (≥1 valid value appears), correctable without external docs.
- **SC-003**: An operator who patches a Nodepool's `nodeCount` (autoscaling off) sees the upstream worker count converge to the new value, with `status.atProvider.observedNodeCount` reflecting it and the group never recreated.
- **SC-004**: An operator who patches a cluster's `k8sVersion` to a newer catalog-valid version sees the cluster perform an in-place upgrade (transient `Ready=False, reason=Upgrading`) and return to `Ready=True` reporting the new version — without the cluster being recreated.
- **SC-005**: Deleting a `KubernetesCluster` MR removes the upstream cluster (and its groups) and the connection Secret; deleting a Nodepool or Addon MR removes only that dependent, leaving the cluster `Ready=True`.
- **SC-006**: The provider's catalog GET volume against `/api/v1/presets/k8s` and `/api/v1/k8s/k8s-versions` remains ≤1 request per `(ProviderConfig, dimension)` per TTL window even under concurrent reconciles, per the inherited resolver cache contract (feature 002 SC-004).

## Assumptions

- A `KubernetesCluster` can be created upstream with zero worker groups — the `ClusterIn` contract lists `worker_groups` as optional (only `name`/`k8s_version`/`network_driver` are required). Per the operator's 2026-06-06 decision, the published API contract is treated as authoritative for this, so the data-model locks the Nodepool-MR-only model without a live probe.
- The dashboard's "Create Kubernetes cluster" flow is the canonical operator-side reference; its tier picker, version picker, network-driver picker, and AZ picker map 1:1 to the v0.x `KubernetesCluster.spec.forProvider` fields. Dashboard features NOT in this spec (ingress/dashboard toggles, OIDC, maintenance windows, custom pod/service CIDR, custom configurator sizing) are out of scope and land incrementally.
- The Timeweb API behaves as described in the vendored `docs/openapi-timeweb.json` for the in-scope `/api/v1/k8s/*` endpoints; paths/envelopes discovered to differ are resolved by curl-probing the live API (per `project_timeweb_underscore_envelopes`). Note the observed response envelopes are `{cluster: …}` and `{node_group: …}`.
- The shared `ProviderConfig`/`ClusterProviderConfig` pair and the in-controller resolver primitive from feature 002 are available unchanged; this feature adds two resolver dimensions + three MR controllers and does NOT re-open feature-002 or feature-003 contracts.
- The `Network` MR (feature 003) is reused for VPC attach; the `Project`/`SshKey` MRs (feature 001) are reused for project assignment. No changes to those kinds.
- Location/AZ codes use the API values (`spb-3`/`msk-1`/`ams-1`/`fra-1`), never dashboard labels (per `project_timeweb_location_codes_api_vs_dashboard`).
- The custom-configurator sizing path (`configuration{cpu,ram,disk}` on both Cluster and Nodepool), `is_ingress`/`is_k8s_dashboard`, `oidc_provider`, `maintenance_slot`, and `cluster_network_cidr` are explicitly deferred to follow-up features.
- **API group home** (forward-compat): `kubernetes.m.timeweb.crossplane.io` is the canonical group for all managed-Kubernetes kinds — currently `KubernetesCluster`, `KubernetesClusterNodepool`, `KubernetesClusterAddon`; future kinds (e.g. cluster OIDC config, maintenance policy) extend this group additively rather than fragmenting.
