# Phase 0 Research — Managed Kubernetes (Cluster + Nodepool + Addon)

**Feature**: 004 | **Source of truth**: `docs/openapi-timeweb.json` (vendored, tag `Kubernetes`) + features 001/002/003 precedent.

All Technical-Context unknowns were resolvable from the vendored OpenAPI + established precedent — **no `NEEDS CLARIFICATION` markers remain**. Decisions below; each names the upstream evidence and the alternative rejected.

---

## R-1 — API group + Go package layout

**Decision**: One new group `kubernetes.m.timeweb.crossplane.io/v1alpha1`; all three kinds (`KubernetesCluster`, `KubernetesClusterNodepool`, `KubernetesClusterAddon`) in a single `apis/kubernetes/v1alpha1` package and a single `internal/controller/kubernetes` package.

**Rationale**: Mirrors the feature-003 `network` package (which hosts `Network` + `FloatingIP`). Per `project_crossplane_v2_conventions`, namespaced MRs use `<svc>.m.timeweb.crossplane.io`. One package per group keeps `groupversion_info.go` / `AddToScheme` / Crossplane registration singular and lets future K8s kinds land additively.

**Alternatives rejected**: Separate `cluster.m.…` / `nodepool.m.…` groups — needless fragmentation; these kinds share the dashboard "Kubernetes" section and the same cluster-ref machinery.

---

## R-2 — Sizing path: preset-only, master/worker dimension split

**Decision**: Both master (`KubernetesCluster.presetName`) and worker (`KubernetesClusterNodepool.presetName`) resolve via the in-controller catalog resolver to upstream `preset_id`. The custom `configuration{cpu,ram,disk}` path is **not** emitted by the v0.x CRDs (deferred). The upstream `/api/v1/presets/k8s` list is a **discriminated union** — each item is `MasterPresetOutApi` *or* `WorkerPresetOutApi`, discriminated by `type ∈ {master, worker}`. Therefore the resolver uses **two dimensions**: `DimKubernetesMasterPreset` (filters `type=master`) and `DimKubernetesWorkerPreset` (filters `type=worker`). Both are `DimensionPreset` kind and were **already registered as `fetchUnwired` stubs in feature 002** — this feature promotes them to real fetchers reading `/api/v1/presets/k8s` and filtering by `type`.

**Slug rule**: same convention as the Server/CR presets — derive from `description_short` (lowercased, non-`[a-z0-9-]` → `-`, optional `-<id>` disambiguator). K8s presets carry **no location field** (region is set at cluster level via `availabilityZone`), so the slug is `description_short`-based only, no `-<location>` suffix.

**Evidence**: `MasterPresetOutApi`/`WorkerPresetOutApi` both expose `{id, description, description_short, price, cpu, ram, disk, network, type}`; `MasterPresetOutApi` additionally has `limit`. Mutual exclusion `preset_id` vs `configuration` is stated in both `ClusterIn` and `NodeGroupIn`.

**Alternatives rejected**: A single `DimKubernetesPreset` dimension keyed on `(slug, role)` — the existing registry keys dimensions by name and the `Preset` kind matches a flat slug; two dimensions is the lower-friction fit and gives role-scoped "valid slugs" error lists.

---

## R-3 — k8sVersion: exact-match Enum dimension

**Decision**: `KubernetesCluster.k8sVersion` is the **exact** upstream version string, validated 1:1 against `/api/v1/k8s/k8s-versions` via `DimKubernetesVersion` (`DimensionEnum`, promoted from its feature-002 stub). No minor→latest-patch resolution.

**Rationale**: Per the /speckit-clarify decision. `k8s_versions` items are plain strings, so exact match is the natural fit and keeps drift detection deterministic (compare observed string to spec string). Mirrors the `ServerOSImage` exact-match idiom.

**Alternatives rejected**: minor-version auto-latest-patch — non-deterministic drift (the resolved patch changes under the operator's feet), harder tests.

---

## R-4 — networkDriver + availabilityZone: CRD enums, not catalog lookups

**Decision**: `networkDriver` is a CRD enum (`kuberouter`, `calico`, `flannel`, `cilium`); `availabilityZone` is a CRD enum (`spb-3`, `msk-1`, `ams-1`, `fra-1`) that does **not** hard-block sold-out zones beyond the listed set evolution. The forward-compat `DimKubernetesNetworkDriver` + `DimAvailabilityZone` dimensions **stay at `fetchUnwired`** for this feature.

**Rationale**: Both value sets are small, stable, and enforced by the upstream `ClusterIn` schema's own enums (`network_driver`, `availability_zone`). A CRD enum rejects bad input at admission time with the valid list — no catalog round-trip needed. Promoting these dimensions adds a GET with no UX gain.

**Evidence**: `ClusterIn.network_driver` enum + `ClusterIn.availability_zone` enum in the OpenAPI.

**Alternatives rejected**: Wiring both dimensions to `/network-drivers` — the endpoint exists but duplicates a stable enum; deferred unless Timeweb starts varying drivers per account.

---

## R-5 — Worker nodes: Nodepool-MR-only (zero-worker cluster create)

**Decision**: The `KubernetesCluster` MR carries **no** inline worker groups. Cluster create (`POST /api/v1/k8s/clusters`) sends `worker_groups` empty/omitted; every worker pool is a standalone `KubernetesClusterNodepool` MR created via `POST /api/v1/k8s/clusters/{id}/groups`.

**Rationale**: The `ClusterIn` contract lists `worker_groups` as **optional** (only `name`, `k8s_version`, `network_driver` are required). Per the operator's 2026-06-06 decision, a published API contract that marks a field optional is authoritative — no live probe. This is the clean Crossplane split (control plane and pools have independent lifecycles/finalizers).

**Fallback (not anticipated)**: if the live API ever rejects a zero-worker create, the Cluster MR gains a required inline bootstrap `nodepool` block, additional pools staying standalone MRs.

---

## R-6 — Nodepool scaling: relative count deltas, converge from observed

**Decision**: `nodeCount` is mutable. The upstream scaling endpoints are **relative**: `POST /groups/{id}/nodes {count}` adds `count` nodes; `DELETE /groups/{id}/nodes {count}` removes `count` nodes (`IncreaseNodes`/`ReduceNodes` bodies). Each `Update` reconcile computes `delta = desiredNodeCount − observedNodeCount` and issues one add (delta>0) or one remove (delta<0); `delta==0` is a no-op. `nodeCount` bounds: upstream `1..100`.

**Idempotency (Constitution §II)**: because the delta is recomputed from the freshly-observed count every reconcile, a re-invoked `Update` after a partial apply converges without double-adding — the defining safety property for relative-mutation endpoints. `observedNodeCount` is read in `Observe` and folded into `ResourceUpToDate` (`observed == desired` when autoscaling off).

**Autoscaling interaction**: when `autoscaling.enabled`, the controller does **not** reconcile `nodeCount` against the observed count (the upstream autoscaler owns it). `min-size`/`max-size` have an upstream minimum of `2`; the CRD enforces `minSize >= 2` and `maxSize >= minSize` via CEL when `autoscaling.enabled` is true.

**Evidence**: `IncreaseNodes{count*, labels}`, `ReduceNodes{count*}`; `NodeGroupIn.node_count` min 1 max 100; `min-size`/`max-size` minimum 2.

**Alternatives rejected**: per-node delete by `node_id` for scale-down (`DELETE /groups/{id}/nodes/{node_id}`) — needs node enumeration + a selection policy; the count-based reduce is simpler and matches the dashboard's "set worker count" affordance.

---

## R-7 — In-place version upgrade

**Decision**: Changing `KubernetesCluster.k8sVersion` on a `Ready=True` cluster to a newer catalog-valid version fires `PATCH /api/v1/k8s/clusters/{id}/versions/update {k8s_version}` (not a recreate). During the transition the MR reports `Ready=False, reason=Upgrading`. A non-catalog or downgrade target is rejected (`Synced=False, reason=ReconcileError`) before any upstream call. Other create-only fields (`networkDriver`, `availabilityZone`, master `presetName`) reject with `ImmutableFieldChange`; `name`/`description` PATCH via `PATCH /api/v1/k8s/clusters/{id}` (`ClusterEdit` = `{name, description, oidc_provider}`).

**Idempotency**: the upgrade call fires only when `observed.k8sVersion != desired.k8sVersion` and the target validates; otherwise no-op.

**Evidence**: dedicated `versions/update` endpoint with `{k8s_version}` body; `ClusterEdit` only allows name/description/oidc mutation.

---

## R-8 — Kubeconfig as the connection Secret

**Decision**: `GET /api/v1/k8s/clusters/{id}/kubeconfig` returns the kubeconfig as an **`application/yaml` plain-string body**. The controller publishes it verbatim under the `kubeconfig` key of the cluster's connection Secret (`writeConnectionSecretToRef`), within one reconcile of `Ready=True`. The kubeconfig is a credential → never logged, never written to status/spec (Constitution Provider Constraints).

**Evidence**: the kubeconfig GET response declares `content: application/yaml`, schema `type: string`, with a `kind: Config` example.

**Alternatives rejected**: parsing the kubeconfig to also surface the API-server endpoint/CA as separate Secret keys — unnecessary for v0.x; downstream tooling consumes the full kubeconfig.

---

## R-9 — Addon model (discovered shape refines spec FR-014)

**Decision**: `KubernetesClusterAddon.spec.forProvider` carries `type` (the addon identifier, e.g. an ingress controller), `version`, optional `yamlConfig` (override; defaults to the catalog's `yaml_config`), and `configType` (defaults sensibly). Install: `POST /api/v1/k8s/clusters/{id}/addons {type, config_type, yaml_config, version}`. Remove: `DELETE /api/v1/k8s/clusters/{id}/addons/{addon_id}` (404-idempotent). Validation: `type`+`version` checked against `GET /api/v1/k8s/clusters/{id}/addons-configs` (`AddonConfigOut{id, type, version, dependencies, yaml_config}`). Observe: `GET /api/v1/k8s/clusters/{id}/addons` (`AddonOut{id, type, status, version, config, yaml_config, config_type}`); external-name = the addon `id`.

**Note**: the probe revealed install needs `type`+`config_type`+`yaml_config`+`version` (not a single `name`). Spec FR-014/US5/Key-Entities were updated to this shape in the /speckit-analyze remediation pass, so spec + data-model + contracts + tasks now agree. Addons are P3 (US5) — lowest-priority slice.

**Alternatives rejected**: modeling addons as an inline list on the Cluster spec — the spec already chose a separate MR (clean per-addon lifecycle + the install/remove endpoints are per-addon).

---

## R-10 — External-name & cross-resource identity

**Decision**:
- `KubernetesCluster` external-name = upstream cluster `id` (integer, stored as string verbatim — same as the Server kind).
- `KubernetesClusterNodepool` external-name = upstream group `id`. **But** `GET/DELETE` on a group require the parent `cluster_id` in the path → the Nodepool persists `status.atProvider.clusterID` (resolved from `clusterRef`/`clusterID` at Create) so Observe/Delete never depend on a live ref lookup (Constitution §II idempotency). Nodepool `Create` is gated on the parent cluster being `Ready=True` (`ErrTargetNotReady`, mirroring feature-003's `resolveNetworkRef`).
- `KubernetesClusterAddon` external-name = upstream addon `id`; likewise persists `status.atProvider.clusterID`.

**Evidence**: `GET /k8s/clusters/{cluster_id}/groups/{group_id}` and `…/addons/{addon_id}` both require `cluster_id`; observed response envelopes are `{cluster: …}` and `{node_group: …}`.

**Alternatives rejected**: re-resolving the cluster ref on every Observe — fragile if the cluster MR is renamed/deleted; persisting the resolved ID is the established pattern (`Server.status.atProvider.resolvedNetworkID`).

---

## R-11 — Cross-resource reference resolution (network + project + cluster)

**Decision**: Reuse feature-003's same-namespace `client.Get`-based resolution (`internal/controller/compute/refs.go` idiom), **not** crossplane-runtime's `reference.ResolveOne`. `KubernetesCluster` resolves `networkRef`→`Network.status.atProvider.upstreamID` (reusing the feat-003 `Network` kind) and `projectRef`→`Project.status.atProvider.upstreamID`. `KubernetesClusterNodepool`/`KubernetesClusterAddon` resolve `clusterRef`→`KubernetesCluster.status.atProvider.upstreamID`. Not-found → `ErrTargetNotFound`; empty upstreamID → `ErrTargetNotReady`; selectors → explicit "not implemented in v0.x" error pointing at `Ref`/`ID` (selector support deferred, matching feature 003). **No client-side cluster-AZ/VPC-region compatibility pre-check** (FR-017, /speckit-analyze decision): cluster `availabilityZone` codes (`spb-3`/`msk-1`/…) and VPC `location` codes (`ru-1`/`ru-2`/…) are different namespaces, and a hand-maintained AZ→region map would drift from Timeweb's topology — the controller forwards `network_id` and surfaces any upstream rejection as `ReconcileError`.

**Rationale**: same-namespace-only refs; `client.Get` is simpler and already proven in feature 003.

---

## R-12 — Generated client + e2e strategy

**Decision**: Add tag `Kubernetes` to the `Makefile` `-include-tags` allowlist (source of truth) + `cfg.yaml` (kept in sync). Regenerate the client + counterfeiter fake. E2E: four kuttl bundles (`12`–`15`); the wrapper discovers the smallest master/worker preset slug + a valid k8s version at runtime and exports `$TWE_K8S_*` vars, mirroring the feature-003 preset-discovery section. Cluster provisioning is the slowest canary — assert timeouts derived from SC-001 (cluster 20 min, pool 15 min). Per the no-`deletionPolicy` rule (`project_no_deletionpolicy`), e2e manifests use `managementPolicies` only.

**Cost caveat**: a real managed cluster is billable and slow; the live e2e canary is gated the same way as feature 003 (run only on a funded account; the test account's `no_paid` state maps to `Ready=False, reason=PaymentRequired` and fails fast).
