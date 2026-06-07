# Phase 1 Data Model — Managed Kubernetes

**Feature**: 004 | **Group**: `kubernetes.m.timeweb.crossplane.io/v1alpha1` | **Kinds**: `KubernetesCluster`, `KubernetesClusterNodepool`, `KubernetesClusterAddon`

Go-style shapes below are indicative (kubebuilder markers abbreviated). All three are Crossplane v2 ModernManaged resources embedding `xpv2.ManagedResourceSpec` (shared `ProviderConfigSpec` from feature 002). Field casing follows Go-canonical (`SSHKey`-style) conventions; JSON tags shown where they diverge.

---

## 1.1 KubernetesCluster

```go
type KubernetesClusterParameters struct {
    // Required
    Name           string `json:"name"`                          // 1..255
    K8sVersion     string `json:"k8sVersion"`                    // exact catalog string, e.g. "1.31.2"
    NetworkDriver  string `json:"networkDriver"`                 // enum: kuberouter|calico|flannel|cilium
    AvailabilityZone string `json:"availabilityZone"`            // enum: spb-3|msk-1|ams-1|fra-1 (non-blocking)
    PresetName     string `json:"presetName"`                    // master preset slug → preset_id

    // Optional
    Description      *string `json:"description,omitempty"`
    MasterNodesCount *int    `json:"masterNodesCount,omitempty"` // default 1; 3 for HA

    // VPC attach (at-most-one — CEL)
    NetworkRef      *NamespacedRef `json:"networkRef,omitempty"`
    NetworkSelector *Selector      `json:"networkSelector,omitempty"`
    NetworkID       *string        `json:"networkID,omitempty"`

    // Project assignment (at-most-one — CEL)
    ProjectRef      *NamespacedRef `json:"projectRef,omitempty"`
    ProjectSelector *Selector      `json:"projectSelector,omitempty"`
    ProjectID       *int64         `json:"projectID,omitempty"`
}

type KubernetesClusterObservation struct {
    UpstreamID        string `json:"upstreamID,omitempty"`        // cluster id (verbatim)
    State             string `json:"state,omitempty"`            // upstream status string
    K8sVersion        string `json:"k8sVersion,omitempty"`       // observed (drives upgrade diff)
    LockedPresetID    int64  `json:"lockedPresetID,omitempty"`   // resolved master preset
    ResolvedNetworkID string `json:"resolvedNetworkID,omitempty"`
    ResolvedProjectID int64  `json:"resolvedProjectID,omitempty"`
    CPU               int    `json:"cpu,omitempty"`              // master sizing readout
    RAM               int    `json:"ram,omitempty"`
    Disk              int    `json:"disk,omitempty"`
}
```

- **CEL** (additive, on `forProvider`): at-most-one of `{networkRef, networkSelector, networkID}`; at-most-one of `{projectRef, projectSelector, projectID}`.
- **Printer columns**: READY, SYNCED, K8S-VERSION, AZ, PRESET, STATE, AGE.
- **External-name**: upstream cluster `id` (string verbatim). Envelope `{cluster: Cluster}`.
- **Connection Secret**: key `kubeconfig` (verbatim YAML from the kubeconfig GET).
- **Immutability (R-7)**: `networkDriver`, `availabilityZone`, `presetName`, `masterNodesCount`, and the network/project bindings are create-only → controller-side `RejectImmutableChange` on Update. `k8sVersion` is **upgrade-mutable** (forward only). `name`/`description` PATCH normally.

---

## 1.2 KubernetesClusterNodepool

```go
type KubernetesClusterNodepoolParameters struct {
    // Required
    Name      string `json:"name"`
    PresetName string `json:"presetName"`                       // worker preset slug → preset_id
    NodeCount int    `json:"nodeCount"`                         // 1..100 (mutable when autoscaling off)

    // Parent cluster (at-most-one — CEL; required that one is set)
    ClusterRef      *NamespacedRef `json:"clusterRef,omitempty"`
    ClusterSelector *Selector      `json:"clusterSelector,omitempty"`
    ClusterID       *string        `json:"clusterID,omitempty"`

    // Optional
    Labels      map[string]string `json:"labels,omitempty"`     // → upstream array<{key,value}>
    Autoscaling *Autoscaling      `json:"autoscaling,omitempty"`
    Autohealing *bool             `json:"autohealing,omitempty"`
}

type Autoscaling struct {
    Enabled bool `json:"enabled"`
    MinSize int  `json:"minSize"`                               // >=2 (upstream min)
    MaxSize int  `json:"maxSize"`                               // >=minSize
}

type KubernetesClusterNodepoolObservation struct {
    UpstreamID        string `json:"upstreamID,omitempty"`       // group id
    ClusterID         string `json:"clusterID,omitempty"`        // resolved parent cluster id (persisted)
    ObservedNodeCount int    `json:"observedNodeCount,omitempty"`
    LockedPresetID    int64  `json:"lockedPresetID,omitempty"`
}
```

- **CEL**: at-most-one of `{clusterRef, clusterSelector, clusterID}` AND at-least-one set; when `autoscaling.enabled` then `minSize >= 2 && maxSize >= minSize`.
- **Printer columns**: READY, SYNCED, CLUSTER, PRESET, DESIRED, OBSERVED, AGE.
- **External-name**: upstream group `id`. Envelope `{node_group: NodeGroup}`. GET/DELETE require parent `cluster_id` → read from `status.atProvider.clusterID`.
- **Scaling (R-6)**: Update computes `delta = nodeCount − observedNodeCount`; `POST …/nodes {count:delta}` if >0, `DELETE …/nodes {count:-delta}` if <0, no-op if 0. Skipped entirely when `autoscaling.enabled`.
- **Immutability**: `presetName`, the cluster-ref trio → create-only (`RejectImmutableChange`). `nodeCount` mutable (scaling). `labels`/`autoscaling`/`autohealing` mutability: create-only for v0.x (upstream group has no PATCH; document as immutable, recreate to change).
- **Gating**: Create blocked until the parent `KubernetesCluster` is `Ready=True` (`ErrTargetNotReady`).

---

## 1.3 KubernetesClusterAddon

```go
type KubernetesClusterAddonParameters struct {
    // Parent cluster (at-most-one — CEL; required that one is set)
    ClusterRef      *NamespacedRef `json:"clusterRef,omitempty"`
    ClusterSelector *Selector      `json:"clusterSelector,omitempty"`
    ClusterID       *string        `json:"clusterID,omitempty"`

    // Required
    Type    string `json:"type"`                                // addon identifier (spec FR-014 "name" → upstream type)
    Version string `json:"version"`

    // Optional
    YAMLConfig *string `json:"yamlConfig,omitempty"`            // default: catalog yaml_config
    ConfigType *string `json:"configType,omitempty"`
}

type KubernetesClusterAddonObservation struct {
    AddonID   string `json:"addonID,omitempty"`                 // upstream addon id
    ClusterID string `json:"clusterID,omitempty"`              // resolved parent cluster id (persisted)
    Status    string `json:"status,omitempty"`
}
```

- **CEL**: at-most-one of the cluster-ref trio AND at-least-one set.
- **Printer columns**: READY, SYNCED, CLUSTER, TYPE, VERSION, AGE.
- **External-name**: upstream addon `id`. Validate `type`+`version` against `GET …/addons-configs` (`AddonConfigOut`). Install `POST …/addons {type, config_type, yaml_config, version}`; remove `DELETE …/addons/{addon_id}` (404-idempotent).
- Matches spec FR-014 (addon identified by `type` + `version`, + optional `yamlConfig`/`configType`) per research R-9.

---

## 2. Shared ref types (reused)

`NamespacedRef{ Name string }` and `Selector{ MatchLabels map[string]string }` follow the feature-003 shapes (same-namespace `client.Get` resolution; selectors raise "not implemented in v0.x"). No new ref machinery.

---

## 3. Resolver dimensions (promote stubs → real)

The forward-compat dimensions registered in feature 002 (`internal/controller/shared/resolver/dimensions.go`) are promoted:

| Dimension const | Kind | Fetcher | Endpoint | Filter |
|---|---|---|---|---|
| `DimKubernetesMasterPreset` | Preset | `fetchK8sMasterPresets` (NEW) | `/api/v1/presets/k8s` | `type=master` |
| `DimKubernetesWorkerPreset` | Preset | `fetchK8sWorkerPresets` (NEW) | `/api/v1/presets/k8s` | `type=worker` |
| `DimKubernetesVersion` | Enum | `fetchK8sVersions` (NEW) | `/api/v1/k8s/k8s-versions` | — |
| `DimKubernetesNetworkDriver` | Enum | stays `fetchUnwired` | — | CRD enum instead |
| `DimAvailabilityZone` | Enum | stays `fetchUnwired` | — | CRD enum instead |

`CatalogClient` interface gains `GetK8SPresetsWithResponse` + `GetK8SVersionsWithResponse`; the `fakeCatalog` test stub + `TestDefaultRegistry_Discoverable` expectations update (3 dimensions flip to `wiredUpstream: true`).

Slug rule (R-2): `Slugify(description_short)`; no location suffix (K8s presets are AZ-set at cluster level). MB→GB normalization for the sizing readout follows the S3/Server fetcher idiom.

---

## 4. Relationships

```text
KubernetesCluster ──(networkRef)──▶ Network (feat 003, network.m.…)        [optional]
        │         ──(projectRef)──▶ Project (feat 001, project.m.…)        [optional]
        │
        ├──◀(clusterRef)── KubernetesClusterNodepool   (1 cluster : N pools)
        └──◀(clusterRef)── KubernetesClusterAddon      (1 cluster : N addons)

resolver dimensions: KubernetesMasterPreset, KubernetesWorkerPreset, KubernetesVersion → /api/v1/presets/k8s, /api/v1/k8s/k8s-versions
```

Cross-MR deletion ordering is NOT controller-enforced (Crossplane v2 semantics); orphaned Nodepool/Addon MRs surface `referenced cluster not found` (`ReconcileError`).

---

## 5. Lifecycle / state mapping (FR-015)

| Kind | Ready=True when | Ready=False reasons |
|---|---|---|
| KubernetesCluster | upstream `status` = active | `Provisioning` / `Upgrading` / `PaymentRequired` (`no_paid`) / `Deleting` |
| KubernetesClusterNodepool | `observedNodeCount == nodeCount` (autoscaling off) or group healthy (on) | `Provisioning` / `Scaling` / `Deleting` |
| KubernetesClusterAddon | upstream addon `status` = installed | `Installing` / `Deleting` |

`Synced=False, reason=ReconcileError` for resolver/ref failures (unknown preset/version, missing/unready dependency) and for upstream rejections (e.g. an incompatible cluster-AZ/VPC-region pairing — surfaced verbatim, no client-side pre-check per FR-017); `reason=ImmutableFieldChange` for create-only field edits.
