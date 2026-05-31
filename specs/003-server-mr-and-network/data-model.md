# Phase 1 — Data Model

**Feature**: `003-server-mr-and-network` | **Date**: 2026-06-01 | **Plan**: [./plan.md](./plan.md)

Three new Kubernetes managed-resource kinds. No internal (non-Kubernetes) entities added — the resolver cache from feature 002 is reused unchanged, with two new dimension registrations.

## 1. Kubernetes entities

### 1.1 `Server` — NEW

**API**: `compute.m.timeweb.crossplane.io/v1alpha1`, kind `Server`, **scope: Namespaced**.

**Purpose**: A Timeweb cloud server (VM). Sized via `presetName` resolved against `/api/v1/presets/servers`; OS chosen via `image`/`version` resolved against `/api/v1/os/servers`.

**Spec** (Go shape; CRD shape follows from kubebuilder markers):

```go
type ServerSpec struct {
    xpv2.ManagedResourceSpec `json:",inline"`           // standard v2 namespaced MR boilerplate
    ForProvider              ServerParameters `json:"forProvider"`
}

type ServerParameters struct {
    // --- Required identifiers ---

    // Name as it appears in the Timeweb dashboard. Max 255 chars.
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=255
    Name string `json:"name"`

    // Preset slug as accepted by the in-controller resolver
    // (`<description_short>-<location>`, e.g. `premium-2-2-40-msk-1`).
    // Resolved against /api/v1/presets/servers.
    // +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*[a-z0-9]$`
    PresetName string `json:"presetName"`

    // Region of the server. Mirrors the dashboard's region picker.
    // Frankfurt (fra-1) is listed but currently sold out per the dashboard;
    // the enum stays inclusive to allow recovery if Timeweb re-enables.
    // +kubebuilder:validation:Enum=spb-3;msk-1;nsk-1;ams-1;fra-1;ala-1;buf-2
    Location string `json:"location"`

    // OS image + version. Resolved against /api/v1/os/servers via the
    // ServerOSImage Enum dimension.
    OS ServerOS `json:"os"`

    // --- Optional inputs ---

    // +optional
    Hostname *string `json:"hostname,omitempty"`

    // +kubebuilder:validation:MaxLength=255
    // +optional
    Comment *string `json:"comment,omitempty"`

    // Raw cloud-init payload. Pass-through; max 16 KiB.
    // +kubebuilder:validation:MaxLength=16384
    // +optional
    CloudInit *string `json:"cloudInit,omitempty"`

    // +optional
    AvailabilityZone *string `json:"availabilityZone,omitempty"`

    // --- Cross-resource references ---

    // SSH keys to install at create time. Mutually exclusive per-element
    // semantics: an operator typically uses sshKeyRefs OR sshKeyIDs OR
    // sshKeySelector, but not a mix.
    // +optional
    SshKeyRefs []xpv2.Reference `json:"sshKeyRefs,omitempty"`
    // +optional
    SshKeySelector *xpv2.Selector `json:"sshKeySelector,omitempty"`
    // +optional
    SshKeyIDs []int64 `json:"sshKeyIDs,omitempty"`

    // Private network attachment (single network per server in v0.1).
    // +optional
    NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`
    // +optional
    NetworkSelector *xpv2.Selector `json:"networkSelector,omitempty"`
    // +optional
    NetworkID *string `json:"networkID,omitempty"`

    // Project assignment.
    // +optional
    ProjectRef *xpv2.Reference `json:"projectRef,omitempty"`
    // +optional
    ProjectSelector *xpv2.Selector `json:"projectSelector,omitempty"`
    // +optional
    ProjectID *int64 `json:"projectID,omitempty"`

    // Observed-only references — the FloatingIP MR drives binding (FR-017).
    // Listed here so kubectl describe shows the relationship; controller
    // does NOT mutate floating IPs.
    // +optional
    FloatingIPRefs []xpv2.Reference `json:"floatingIPRefs,omitempty"`
}

type ServerOS struct {
    // OS family slug — lowercase. Matched (case-insensitive) against
    // upstream entry's name field at /api/v1/os/servers.
    // +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*$`
    Image string `json:"image"`

    // Upstream version string, exactly as listed by /api/v1/os/servers
    // (e.g. "24.04", "13", "2022", "10.0").
    // +kubebuilder:validation:MinLength=1
    Version string `json:"version"`
}
```

**XValidation CEL rules on the CRD**:

- At most one of `{networkRef, networkSelector, networkID}` MAY be set.
- At most one of `{projectRef, projectSelector, projectID}` MAY be set.
- Immutability: `presetName`, `location`, `availabilityZone`, `os.image`, `os.version`, all SSH key fields, all Network fields, all Project fields are immutable once set on a created object (`oldSelf == self` rule). Mutating any of them → `Synced=False, reason=ImmutableFieldChange`.

**Status**:

```go
type ServerStatus struct {
    xpv2.ManagedResourceStatus `json:",inline"`
    AtProvider                 ServerObservation `json:"atProvider,omitempty"`
}

type ServerObservation struct {
    UpstreamID         *int64    `json:"upstreamID,omitempty"`
    LockedPresetID     *int64    `json:"lockedPresetID,omitempty"`
    LockedOSID         *int64    `json:"lockedOSID,omitempty"`

    PublicIP           *string   `json:"publicIP,omitempty"`
    PublicIPv6         *string   `json:"publicIPv6,omitempty"`
    PrivateIP          *string   `json:"privateIP,omitempty"`

    ResolvedNetworkID  *string   `json:"resolvedNetworkID,omitempty"`
    ResolvedProjectID  *int64    `json:"resolvedProjectID,omitempty"`
    ResolvedSshKeyIDs  []int64   `json:"resolvedSshKeyIDs,omitempty"`

    // FloatingIPs observed bound to this server upstream. Populated by
    // the Server controller from the upstream observation (NOT from
    // floatingIPRefs in spec).
    BoundFloatingIPs   []int64   `json:"boundFloatingIPs,omitempty"`

    // Lifecycle state from the upstream /servers GET — one of:
    // "installing", "starting", "on", "off", "rebooting", "transfer",
    // "removing". Maps to the Ready condition per FR-014.
    State              *string   `json:"state,omitempty"`
}
```

**Conditions emitted**: standard `Synced` + `Ready`. New reason values needed: none (every reason this feature surfaces — `ReconcileError`, `Reconciling`, `ImmutableFieldChange`, `PresetNotFound`, `APIError`, `ProviderConfigInvalid` — already exists in `internal/controller/shared/conditions.go` from features 001/002).

**Lifecycle**:
- **Create**: resolver resolves `presetName` + `os` to upstream IDs; cross-resource refs resolve to flat IDs; controller POSTs to `/api/v1/servers`; records `upstreamID`, `lockedPresetID`, `lockedOSID`, resolved-* fields. State machine starts at `installing` → `starting` → `on`. `Ready=True` flips at `state=="on"`.
- **Observe**: GET `/api/v1/servers/{id}`. Sync the observable fields; detect drift on locked IDs (treat as terminal `ImmutableFieldChange`).
- **Update**: PATCH `/api/v1/servers/{id}` for the mutable fields (`name`, `hostname`, `comment`, `cloudInit`); ImmutableFieldChange for the rest (matches R-5).
- **Delete**: DELETE `/api/v1/servers/{id}`. Idempotent on 404.

**Connection Secret** (FR-015): published per Crossplane v2 modern-managed semantics. Contents:
- `publicIP` (when assigned)
- `publicIPv6` (when assigned)
- `privateIP` (when network-attached)
- `hostname` (when set on `forProvider`)
- `upstreamID` (always)

---

### 1.2 `Network` (VPC) — NEW

**API**: `network.m.timeweb.crossplane.io/v1alpha1`, kind `Network`, **scope: Namespaced**.

**Purpose**: A Timeweb VPC. Independently usable; referenced from `Server.forProvider.networkRef`.

**Spec**:

```go
type NetworkSpec struct {
    xpv2.ManagedResourceSpec `json:",inline"`
    ForProvider              NetworkParameters `json:"forProvider"`
}

type NetworkParameters struct {
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=255
    Name string `json:"name"`

    // IPv4 CIDR for the VPC. Operator-typed; the upstream validates the
    // exact constraints (RFC1918 ranges, /24 minimum) so we do a basic
    // regex check at CEL and let the API enforce semantics.
    // +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}\/([0-9]|[1-2][0-9]|3[0-2])$`
    SubnetCIDR string `json:"subnetCIDR"`

    // Same region enum as Server.
    // +kubebuilder:validation:Enum=spb-3;msk-1;nsk-1;ams-1;fra-1;ala-1;buf-2
    Location string `json:"location"`

    // +optional
    Description *string `json:"description,omitempty"`

    // +optional
    AvailabilityZone *string `json:"availabilityZone,omitempty"`
}
```

**XValidation**: `name`, `subnetCIDR`, `location`, `availabilityZone` are immutable after create. `description` is mutable.

**Status**:

```go
type NetworkObservation struct {
    UpstreamID *string `json:"upstreamID,omitempty"`   // upstream vpc_id is a string per /api/v2/vpcs
    // Reflect the upstream resource's own observed fields for kubectl describe parity.
    AssignedCIDR *string `json:"assignedCIDR,omitempty"`
}
```

**Lifecycle**:
- **Create**: POST `/api/v2/vpcs`. Record `upstreamID`.
- **Observe**: GET `/api/v2/vpcs/{id}`.
- **Update**: PATCH `/api/v2/vpcs/{id}` for `description`; immutable for the rest.
- **Delete**: DELETE `/api/v1/vpcs/{id}` (note the v1 path — per R-6).

**Connection Secret**: none. Operators ssh into Servers, not Networks.

---

### 1.3 `FloatingIP` — NEW

**API**: `network.m.timeweb.crossplane.io/v1alpha1`, kind `FloatingIP`, **scope: Namespaced**.

**Purpose**: A Timeweb floating IPv4 address. Owns its upstream allocation AND its binding to a single Server (per R-4).

**Spec**:

```go
type FloatingIPSpec struct {
    xpv2.ManagedResourceSpec `json:",inline"`
    ForProvider              FloatingIPParameters `json:"forProvider"`
}

type FloatingIPParameters struct {
    // +kubebuilder:validation:Enum=spb-3;msk-1;nsk-1;ams-1;fra-1;ala-1;buf-2
    Location string `json:"location"`

    // +optional
    Comment *string `json:"comment,omitempty"`

    // +optional
    AvailabilityZone *string `json:"availabilityZone,omitempty"`

    // DDoS guard toggle. The upstream createFloatingIP body marks this
    // required; CRD default = false so operators don't have to think
    // about it for the common case.
    // +kubebuilder:default=false
    IsDDoSGuard bool `json:"isDDoSGuard"`

    // --- Server-binding trio (mutually exclusive, all optional) ---

    // +optional
    ServerRef *xpv2.Reference `json:"serverRef,omitempty"`
    // +optional
    ServerSelector *xpv2.Selector `json:"serverSelector,omitempty"`
    // +optional
    ServerID *int64 `json:"serverID,omitempty"`
}
```

**XValidation**: at most one of `{serverRef, serverSelector, serverID}` MAY be set. `location`, `availabilityZone`, `isDDoSGuard` are immutable after create. `comment` and the binding trio are mutable.

**Status**:

```go
type FloatingIPObservation struct {
    UpstreamID         *string      `json:"upstreamID,omitempty"`
    IP                 *string      `json:"ip,omitempty"`              // the IPv4 address itself
    ResolvedServerID   *int64       `json:"resolvedServerID,omitempty"`
    BoundAt            *metav1.Time `json:"boundAt,omitempty"`
}
```

**Lifecycle** (the most-involved of the three new MRs):

- **Create**: POST `/api/v1/floating-ips` with `availability_zone` derived from spec (or default for the location). Record `upstreamID` + `ip`. If `serverRef`/`serverSelector`/`serverID` resolves AND the target Server is `Ready=True`, immediately POST `/floating-ips/{id}/bind` with `{resource_type: "server", resource_id: <id>}`. Record `resolvedServerID` + `boundAt`.
- **Observe**: GET `/api/v1/floating-ips/{id}`. Read `bound_to` field. Drift detection:
  - `status.atProvider.resolvedServerID == upstream.bound_to.resource_id` → no drift.
  - operator removed the ref (spec server-binding trio all unset) but upstream still shows bound → unbind action queued for Update.
  - operator changed the ref target → unbind-then-bind sequence queued for Update.
  - upstream got rebound out-of-band → not corrected (Crossplane v2 default is `ObserveOnly` for unmanaged drift; we don't aggressively re-bind to match spec).
- **Update**: applies the queued action(s) from Observe. Order: unbind first, then bind. If only one is needed, only one is called.
- **Delete**: if currently bound, unbind first; then DELETE `/api/v1/floating-ips/{id}`. Idempotent on 404.

**Connection Secret**: optional — published with the IP address as key `ip` so downstream resources (e.g. external DNS providers) can consume it.

---

## 2. Internal (non-Kubernetes) entities

### 2.1 Resolver dimensions added

Two new entries appended to `internal/controller/shared/resolver/dimensions.go::defaultRegistry()`:

| Name | Kind | Upstream endpoint | Fetcher behavior |
|---|---|---|---|
| `ServerPreset` | `Preset` | `GET /api/v1/presets/servers` | Maps each preset to a `PresetEntry{UpstreamID, DescShort, Location, DiskGB}`. Slug derived via `Slugify(description_short, location)`. |
| `ServerOSImage` | `Enum` | `GET /api/v1/os/servers` | Returns a list of `{image: name.lowercase, version}` pairs. The resolver's `MatchEnum` (existing) checks operator-supplied `(image, version)` against this list. |

The forward-compat `ServerConfigurator` registration from feature 002 stays at its `fetchUnwired` stub.

### 2.2 Cache key effects

No change to the cache key shape — `cacheKey{pc PCRef, dim Dimension}` covers the new dimensions naturally. Each MR reconciler that needs a preset/OS lookup constructs the dimension struct and goes through the existing `Resolver.Resolve` API.

---

## 3. Relationships

```
                        ┌─────────────────────┐
                        │   ProviderConfig    │   (feat 002, shared)
                        │   /Cluster…         │
                        └──────────┬──────────┘
                                   │ providerConfigRef
                ┌──────────────────┼─────────────────────────────┐
                │                  │                             │
                ▼                  ▼                             ▼
        ┌────────────┐      ┌────────────┐               ┌────────────┐
        │   Server   │      │  Network   │               │ FloatingIP │
        │ (compute)  │      │ (network)  │               │ (network)  │
        └─────┬──────┘      └────────────┘               └─────┬──────┘
              │ ▲                  ▲                           │
              │ │ networkRef       │ (resolved upstream ID)    │
              │ └──────────────────┘                           │
              │                                                │
              │ projectRef                                     │ serverRef
              ▼                                                ▼
        ┌────────────┐                                   ┌────────────┐
        │  Project   │ (feat 001)                        │   Server   │
        └────────────┘                                   └────────────┘
              ▲                                                ▲
              │ sshKeyRefs (list)                              │ (observe-only floatingIPRefs)
              │                                                │
        ┌────────────┐                                         │
        │   SshKey   │ (feat 001)                              │
        └────────────┘                                         │
                                                               │
                                                               (no controller-side mutation)
```

- `Server.forProvider.networkRef` → resolves to `Network.status.atProvider.upstreamID`. Controller blocks Server.Create until Network is `Ready=True`.
- `Server.forProvider.projectRef` → resolves to existing `Project.status.atProvider.upstreamID`.
- `Server.forProvider.sshKeyRefs` (list) → resolves to existing `SshKey.status.atProvider.upstreamID` per entry.
- `Server.forProvider.floatingIPRefs` (list) → **observe-only**. The Server controller never mutates these.
- `FloatingIP.forProvider.serverRef` → resolves to `Server.status.atProvider.upstreamID`. The FloatingIP controller owns the bind/unbind.

The cycle that could happen if both `Server.floatingIPRefs` and `FloatingIP.serverRef` were authoritative is avoided by FR-017: `floatingIPRefs` is observe-only.

---

## 4. Field counts and CRD size estimate

| Kind | `forProvider` fields | `atProvider` fields | CEL rules |
|---|---:|---:|---:|
| `Server` | 19 (3 required + 6 optional scalar + 3+3+3+1 ref pairs/lists) | 11 | 2 mutual-exclusion + ~10 immutability |
| `Network` | 5 (3 required + 2 optional) | 2 | 4 immutability |
| `FloatingIP` | 7 (1 required + 2 optional + 1 default + 3 ref-trio) | 4 | 1 mutual-exclusion + 3 immutability |

Generated CRD YAML estimated ~200–280 lines each — well within Crossplane's tolerance.
