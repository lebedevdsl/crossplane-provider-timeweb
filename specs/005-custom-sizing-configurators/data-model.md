# Phase 1 Data Model — Custom Sizing + Group Move

**Feature**: 005. Changes to existing kinds (no new kinds). Indicative Go shapes; kubebuilder markers abbreviated.

## 1. Shared sizing block

Operator-facing custom sizing, added to three kinds. Units are GB/cores (operator-friendly); the controller normalizes to the resolver's canonical axes (R-2).

```go
// ServerResources / cluster + nodepool resources share this core.
type Resources struct {
    // +kubebuilder:validation:Minimum=1
    CPU    int `json:"cpu"`              // cores
    // +kubebuilder:validation:Minimum=1
    RAMGB  int `json:"ramGB"`            // GB → ramMB (×1024) for matching
    // +kubebuilder:validation:Minimum=1
    DiskGB int `json:"diskGB"`           // GB
    // --- Server-only optional axes ---
    DiskType         *string `json:"diskType,omitempty"`          // → configurator disk_type filter
    BandwidthMbps    *int    `json:"bandwidthMbps,omitempty"`     // → bandwidth sizing
    GPU              *int    `json:"gpu,omitempty"`               // → gpu sizing (nodepool too)
    CPUFrequencyTier *string `json:"cpuFrequencyTier,omitempty"`  // → cpu_frequency filter
    EnableLocalNetwork *bool `json:"enableLocalNetwork,omitempty"`// → is_allowed_local_network filter
}
```

- **Server** uses the full block. **KubernetesCluster** uses `{cpu, ramGB, diskGB}`. **KubernetesClusterNodepool** uses `{cpu, ramGB, diskGB, gpu}`. (Per-kind types may embed only the relevant fields rather than a shared struct, to keep CRDs minimal.)

## 2. Per-kind changes

### 2.1 Server (`compute.m.timeweb.crossplane.io/v1alpha1`)
- `forProvider.presetName` → now `*string` (optional). NEW `forProvider.resources *ServerResources` (optional).
- **CEL** (additive): `(has(presetName)?1:0) + (has(resources)?1:0) == 1` — exactly one sizing variant.
- `status.atProvider.lockedConfiguratorID *int64` (NEW; sits beside `lockedPresetID`).
- Create: resolve `resources`→`configurator_id` (R-3), build the createServer body with `configurator_id` instead of `preset_id`.
- Update: sizing-variant flip → `RejectSizingSwitch` (`reason=SizingSwitchRequiresRecreate`).

### 2.2 KubernetesCluster (`kubernetes.m.timeweb.crossplane.io/v1alpha1`)
- `forProvider.presetName` → optional; NEW `forProvider.resources *KubernetesResources` (`cpu`,`ramGB`,`diskGB`).
- CEL: exactly one of `{presetName, resources}`.
- `status.atProvider.lockedConfiguratorID *int64` (NEW).
- Create: emit `ClusterIn.configuration {configurator_id, cpu, ram, disk}` instead of `preset_id`.

### 2.3 KubernetesClusterNodepool (same group)
- Same as cluster + optional `resources.gpu`. Emit `NodeGroupIn.configuration {configurator_id, cpu, ram, disk, gpu}`.

### 2.4 ContainerRegistry + ContainerRegistryRepository — RELOCATED
- Moved verbatim from `containerregistry.m.timeweb.crossplane.io` → `kubernetes.m.timeweb.crossplane.io`. **No field/behavior change** — only `apiVersion`. New GVKs registered in `apis/kubernetes/v1alpha1`. Old group + its CRDs removed.

## 3. Resolver dimension change

| Dimension | Before | After |
|---|---|---|
| `DimServerConfigurator` | `{kind: Configurator, fetch: fetchUnwired}` | `{kind: Configurator, fetch: fetchServerConfigurators}` over `GetConfiguratorsWithResponse` |
| `DimKubernetesMasterConfigurator` | (did not exist) | `{kind: Configurator, fetch: fetchK8sMasterConfigurators}` over `GetK8sConfiguratorsWithResponse` (undocumented `/api/v1/configurator/k8s`, hand-patched into the spec), tag-filtered to `k8s_master_configurator` |
| `DimKubernetesWorkerConfigurator` | (did not exist) | same endpoint, tag-filtered to the non-master families (`k8s_configurator_general`/`_dedicated_cpu`/`_gpu_*`) |

All fetchers normalize through a shared `configuratorEntries` helper → `[]ConfiguratorEntry`: `Filters{location, disk_type, is_allowed_local_network, cpu_frequency}`, `Bounds{cpu, ramMB, diskGB, bandwidth, gpu}` (from `requirements.*_{min,step,max}`, units normalized per R-2). `TestDefaultRegistry_Discoverable` locks all three as `wiredUpstream: true`. R-5's probe outcome: the catalogs are **separate** (the k8s create endpoint 400-rejects server-catalog ids) and the k8s one is **role-partitioned per location** — cluster resolves via the master dim, nodepool via the worker dim, both location-first from the cluster's AZ (`azLocation`); see research R-5 live findings for the ams-1 stranding a mismatched id causes.

## 4. Conditions / reasons (all already in `shared/conditions.go`)

- `NoConfiguratorAvailable` — unsatisfiable `resources`.
- `SizingSwitchRequiresRecreate` — preset↔resources flip on a live resource.
- `Reconciling` — unready-dependency gating (tech-debt R-9 aligns Connect-error paths to this).

## 5. Tech-debt structural changes

- `serverExternal` gains `resolvedNetworkID`/`resolvedProjectID`/`resolvedSSHKeyIDs` fields; `resolveRefs` returns values instead of mutating `cr.Spec.ForProvider` (R-7). Mirrors `clusterExternal`.
- e2e harness scripts (`down.sh`, `kuttl.sh`) + kuttl assert files corrected (R-8).
- Connect-error → `Reconciling` reason mapping in the compute/network/kubernetes connectors (R-9).

## 6. Migration / compatibility

- ContainerRegistry group rename is **breaking** (apiVersion changes); pre-1.0, no external consumers (justified in plan Complexity Tracking). Operators re-apply manifests under `kubernetes.m.timeweb.crossplane.io`.
- All sizing changes are **additive** — existing preset manifests keep working unchanged.
