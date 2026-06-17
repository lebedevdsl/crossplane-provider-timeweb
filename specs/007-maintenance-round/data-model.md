# Data Model: Feature 007 — Maintenance Round

**Date**: 2026-06-17 | **Branch**: `007-maintenance-round`

All changes in this document are **additive and backward-compatible** unless
explicitly marked otherwise. No upstream resource creation behavior changes
(FR-007). Every existing applied manifest continues to parse, validate, and
reconcile unchanged (FR-004).

---

## 1. Placement Fields — Per-Kind Changes

### 1.1 Uniform Model (FR-001)

Every regionally-placed MR expresses placement as:

```
location          string    required  region code (e.g. "ru-1")
availabilityZone  *string   optional  zone within region (e.g. "spb-3")
```

Kinds that already have both fields at the correct cardinality require no
schema change. The table below covers only the kinds that need changes.

### 1.2 Affected Kinds

#### Router (`apis/network/v1alpha1/router_types.go`)

| Field | Before | After | Breaking? |
|-------|--------|-------|-----------|
| `availabilityZone` | `string` required, enum=4 | `*string` optional, no enum (live-validated) | **No** — pointer to optional; existing manifests pass validation |
| `location` | absent | `string` required, enum=8 regions, immutable | **No** — additive field; existing manifests without it fail admission (migration path: controller derives from AZ, see §2) |

**CEL additions**:
```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable"
Location string `json:"location"`
// +optional
AvailabilityZone *string `json:"availabilityZone,omitempty"`
```

**Backward-compat note**: an operator's existing manifest that sets only
`availabilityZone` will fail admission against the new CRD (location is now
required). The migration path is to add `location: <region>` to the manifest.
Existing *applied* resources (already in etcd) are not re-validated until
edited. This is a CRD schema version advance; existing resources are not
broken mid-flight.

#### KubernetesCluster (`apis/kubernetes/v1alpha1/kubernetescluster_types.go`)

Same changes as Router:

| Field | Before | After | Breaking? |
|-------|--------|-------|-----------|
| `availabilityZone` | `string` required, enum=4 | `*string` optional, no enum | **No** for applied manifests |
| `location` | absent | `string` required, enum=8 regions, immutable | Additive |

**CEL additions**:
```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable"
Location string `json:"location"`
// +optional
AvailabilityZone *string `json:"availabilityZone,omitempty"`
```

#### Kinds with no placement change needed

| Kind | location | availabilityZone | Notes |
|------|----------|-----------------|-------|
| Server | `string` required ✓ | `*string` optional ✓ | Already uniform |
| Network | `string` required ✓ | `*string` optional ✓ | Already uniform |
| FloatingIP | `string` required ✓ | `*string` optional ✓ | Already uniform (defaultAZByLocation bug fixed in controller, no type change) |
| KubernetesClusterNodepool | inherits cluster | — | Placement via parent |
| KubernetesClusterAddon | inherits cluster | — | Placement via parent |
| ContainerRegistry | `*string` optional | — | CRaaS is region-optional by design |
| S3Bucket | — | — | Storage; region derived from preset |
| Project / SshKey | — | — | Account-scoped, not regionally placed |

### 1.3 CEL Immutability Gaps — Additions (FR-013)

Fields documented immutable but missing `self == oldSelf` CEL:

| Kind | Field | File |
|------|-------|------|
| Network | `Name`, `SubnetCIDR` | `network_types.go` |
| FloatingIP | `Location` | `floatingip_types.go` (already has `availabilityZone` and `isDDoSGuard` controller-side; add CEL for location) |
| KubernetesClusterNodepool | `Name` | `kubernetesclusternodepool_types.go` |
| KubernetesClusterAddon | `Type`, `Version` | `kubernetesclusteraddon_types.go` |
| SshKey | `Name`, `Body` | `apis/sshkey/v1alpha1/types.go` |

Each addition is of the form:
```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="<fieldName> is immutable"
```

---

## 2. Preset-Slug Resolution Changes (US2)

### 2.1 `PresetInput` struct (`internal/controller/shared/resolver/`)

```go
type PresetInput struct {
    Slug     string  // operator's preset name (any form: short, long, disambiguated)
    Zone     string  // existing zone filter (unchanged)
    Location string  // NEW: region filter for short-slug match + not-found scoping
}
```

The `Location` field is zero-value safe: passing `Location: ""` reproduces
existing behavior (global slug match, global not-found list).

### 2.2 `MatchPresetSlug` behavior change

Resolution order (see research.md R-3 for full algorithm):
1. Disambiguator form (`<base>-<id>`) — unchanged.
2. Location-filter entries to `PresetInput.Location`.
3. Full-slug match (`<short>-<location>`) — unchanged within filtered set.
4. **New**: short-slug match (`<short>` without location suffix) within filtered set.
5. Error or unique result.

**Not-found error format** adds location context:
```
resolver: preset not found: slug "ssd-99" in dimension "DimServerPreset"
  does not match any upstream entry for location "ru-1"
  (valid: ssd-15, ssd-25, ssd-50, ...)
```

### 2.3 `splitDisambiguator` fix

Replace manual int64 accumulation with `strconv.ParseInt` + 18-digit cap.
No behavior change for valid inputs; fixes overflow on very-long numeric suffix.

---

## 3. Printcolumns — Uniform Layout (US3, FR-006)

### 3.1 Target Column Order

Every MR kind follows this schema:

```
READY    type=string  .status.conditions[?(@.type=='Ready')].status
SYNCED   type=string  .status.conditions[?(@.type=='Synced')].status
LOCATION type=string  .spec.forProvider.location                       (where applicable)
<kind-specific columns in the most operator-useful order>
ID       type=string  .status.atProvider.upstreamID   priority=1       (hidden by default)
AGE      type=date    .metadata.creationTimestamp
```

Rules:
- `READY` and `SYNCED` are always first (Crossplane convention).
- `LOCATION` (or `CLUSTER` for child kinds) follows immediately after `SYNCED`.
- Internal IDs (`UPSTREAM-ID`, `EXTERNAL-NAME`) move to `priority=1` (shown
  only with `-o wide`) or are renamed to `ID`.
- `AGE` is always last.
- `STATE` surfaces near the right of the kind-specific block (it's diagnostic,
  not operational).

### 3.2 Per-Kind Changes

#### Server

```
READY | SYNCED | LOCATION | PRESET | PUBLIC-IP | STATE | ID(p=1) | AGE
```

Change: add `ID` at priority=1 (today there is no ID column).

#### Network

```
READY | SYNCED | LOCATION | CIDR | STATE(new) | ID(p=1) | AGE
```

Changes:
- `UPSTREAM-ID` → `ID` at priority=1.
- Add `STATE` → `.status.atProvider.state` (new status mirror, see §4.1).

#### FloatingIP

```
READY | SYNCED | LOCATION | IP | BOUND-TO | ID(p=1) | AGE
```

Changes:
- Collapse `BOUND-RES` + `BOUND-TO` + `BOUND-UUID` into a single `BOUND-TO`
  column showing the bound resource name/UUID (most useful to operators).
  JSONPath: prefer `.status.atProvider.observedBoundTo.resourceUUID` (Router
  binding), fall back to `.status.atProvider.observedBoundTo.resourceID`.
  Implementation: a computed status field `ObservedBoundSummary *string` set by
  the controller to `<resourceType>/<id-or-uuid>` (e.g. `router/abc-123`) so
  a single JSONPath column covers both binding types.
- Add `ID` at priority=1 for the floating IP's own upstream ID.

#### Router

```
READY | SYNCED | LOCATION | PRESET | STATE | ID(p=1) | AGE
```

Changes:
- `AZ` column removed (AZ is now in the spec's `availabilityZone` field,
  accessible via `-o yaml`; not needed in the default listing).
- `UPSTREAM-ID` → `ID` at priority=1.

#### KubernetesCluster

```
READY | SYNCED | LOCATION | K8S-VERSION | PRESET | STATE | ID(p=1) | AGE
```

Changes:
- `AZ` column removed (same rationale as Router).
- `UPSTREAM-ID` → `ID` at priority=1 (currently absent; add).

#### KubernetesClusterNodepool

```
READY | SYNCED | CLUSTER | PRESET | PUBLIC-IP(new) | DESIRED | OBSERVED | ID(p=1) | AGE
```

Changes:
- Add `PUBLIC-IP` column → `.spec.forProvider.publicIPs` (boolean, "True"/"False").
- Add `ID` at priority=1.

#### KubernetesClusterAddon

```
READY | SYNCED | CLUSTER | TYPE | VERSION | INSTALLED-VERSION(new) | AGE
```

Change: add `INSTALLED-VERSION` → `.status.atProvider.installedVersion` (new
status mirror, see §4.3).

#### ContainerRegistry

```
READY | SYNCED | STATE(new) | SIZE-GB | ENDPOINT(new) | ID(p=1) | AGE
```

Changes:
- Add `STATE` → `.status.atProvider.state` (new mirror, see §4.2).
- Add `ENDPOINT` → `.status.atProvider.endpoint` (new mirror, see §4.2).
- `EXTERNAL-NAME` → `ID` at priority=1.

#### ContainerRegistry Repository

```
READY | SYNCED | REGISTRY | NAME | TAGS | AGE
```

Change: add `AGE` column (currently missing per FR-016).

#### S3Bucket

```
READY | SYNCED | SIZE-GB | CLASS | ID(p=1) | AGE
```

Change: `EXTERNAL-NAME` → `ID` at priority=1.

#### SshKey / Project

No change — non-regional, no ID column needed.

---

## 4. Status Mirror Additions (FR-011)

All fields are additive (new optional fields in existing `*Observation` structs).

### 4.1 Network — Add `State`

```go
// State is the upstream VPC status string (e.g. "active", "deleting").
// +optional
State *string `json:"state,omitempty"`
```

Populated in `network_external.go:Observe`. Allows `kubectl get` to show
whether the VPC is ready without `kubectl describe`.

### 4.2 ContainerRegistry — Add `State` + `Endpoint`

```go
// State is the upstream registry status.
// +optional
State *string `json:"state,omitempty"`

// Endpoint is the Docker-pull hostname for this registry
// (e.g. "cr.timeweb.cloud/<name>"). Mirrors the upstream `domain_name` field.
// +optional
Endpoint *string `json:"endpoint,omitempty"`
```

The `ENDPOINT` printcolumn surfaces this for `kubectl get`. Today the registry
endpoint is only discoverable from the Timeweb panel or the connection Secret.

### 4.3 KubernetesClusterAddon — Add `InstalledVersion`

```go
// InstalledVersion is the version string the upstream reports for the
// installed addon (may differ from spec.forProvider.version during
// upgrades).
// +optional
InstalledVersion *string `json:"installedVersion,omitempty"`
```

Mirrors the upstream addon's observed version so operators can confirm an
upgrade converged.

### 4.4 FloatingIP — Add `ObservedBoundSummary`

```go
// ObservedBoundSummary is a computed display field combining the
// upstream resourceType + id/uuid into a single string
// (e.g. "server/42" or "router/abc-uuid"). Populated by Observe.
// Drives the BOUND-TO printcolumn without multi-path JSONPath.
// +optional
ObservedBoundSummary *string `json:"observedBoundSummary,omitempty"`
```

---

## 5. Shared Condition-Reason Vocabulary (FR-008 / FR-009a)

### 5.1 Canonical Reason Table

Defined in `internal/controller/shared/conditions.go`. See research.md R-5
for the full rationale.

| Reason constant | Condition | Meaning |
|-----------------|-----------|---------|
| `xpv2.Available()` | Ready=True | Upstream resource healthy and observed |
| `xpv2.Creating()` | Ready=False | First reconcile, resource being created |
| `xpv2.Deleting()` | Ready=False | Deletion in progress |
| `ReasonPaymentRequired` | Ready=False | `no_paid` / billing-blocked (Server; Router best-effort) |
| `ReasonUpstreamFailed` | Ready=False | Terminal upstream `failed`/`error` state |
| `ReasonImmutableFieldChange` | Synced=False | Create-time-only field edited |
| `ReasonParentNotReady` | Synced=False | **NEW** — dependency (cluster/registry) not yet Ready=True |
| `ReasonPresetNotFound` | Synced=False | Resolver: slug unmatched |
| `ReasonPresetAmbiguous` | Synced=False | Resolver: slug matches multiple entries |
| `ReasonNoConfiguratorAvailable` | Synced=False | Resolver: sizing inputs rejected |
| `ReasonSizingSwitchRequiresRecreate` | Synced=False | Sizing variant changed |
| `ReasonCatalogUnauthorized` | Synced=False | 401/403 on catalog endpoint |
| `ReasonCatalogTransient` | Synced=False | 5xx on catalog endpoint |
| `ReasonDimensionValueNotFound` | Synced=False | Enum value not in upstream set |
| `ReasonProviderConfigInvalid` | Synced=False | Post-resolution PC failure |
| `ReasonInvalidProviderConfigRef` | Synced=False | Wrong PC reference |
| `ReasonAPIError` | Synced=False | Generic upstream API error |
| `ReasonRateLimited` | Synced=False | Upstream rate limit |

### 5.2 `MapResolverErrorToCondition` (new shared helper)

Location: `internal/controller/shared/map_resolver_error.go`

Signature:
```go
func MapResolverErrorToCondition(err error) xpv2.Condition
```

Used at every resolver call-site in every controller. Replaces:
- The per-controller `mapResolverErrorToCondition` in S3Bucket and ContainerRegistry.
- The missing mapping in Server, KubernetesCluster, KubernetesClusterNodepool, Router.

### 5.3 Transition-Only Events

New helper: `internal/controller/shared/events.go`

```go
func RecordConditionChange(
    recorder record.EventRecorder,
    mg resource.Managed,
    newCondition xpv2.Condition,
)
```

Reads the current condition from `mg`, compares reason+status to `newCondition`;
records an Event only on change. Called at every condition-set site that
corresponds to a named transition (payment-blocked, upstream-failed,
dependency-wait, resolver failure).

---

## 6. Relationships Diagram

```
                  /api/v2/locations  (cached per PCRef)
                        │
                        ▼
             shared.LocationLookup
              ┌──────────────────┐
              │  AZToLocation()  │   Router.Create / K8sCluster.Create
              │  LocationZones() │   (backward-compat AZ-only path)
              └──────────────────┘

 ForProvider.Location ──────────────────────────────────┐
 ForProvider.AvailabilityZone (optional)                │
          │                                             │
          ▼                                             ▼
  PresetInput{Location, Zone, Slug}          controller derives AZ
          │                                  (if AZ omitted)
          ▼
  resolver.MatchPresetSlug
   ├── Full slug match (long-form: back-compat)
   └── Short slug match (NEW: location-filtered)
          │
          └── not-found → location-scoped valid list
```

---

## 7. Backward-Compatibility Checklist

| Change | Back-compat? | Notes |
|--------|-------------|-------|
| `location` added to Router/KubernetesCluster | New manifests must add field; existing applied resources not re-validated | Standard CRD evolution |
| `availabilityZone` on Router/K8sCluster → `*string` optional | Yes — pointer is superset of required string for existing values | |
| Short-slug resolution | Yes — long-form slug still matches first | FR-004 |
| `splitDisambiguator` overflow fix | Yes — only changes behavior for pathological long-digit suffixes | |
| `PresetInput.Location` field | Yes — zero value = current behavior | |
| New status mirror fields | Yes — additive `// +optional` fields | |
| `ID` printcolumn replaces `UPSTREAM-ID` | CRD version bump required; no behavioral change | |
| `ObservedBoundSummary` replacing BOUND-RES + BOUND-TO + BOUND-UUID | CRD version bump; old columns removed | |
| `ReasonParentNotReady` added | Yes — new constant, no prior manifests reference it | |
| `MapResolverErrorToCondition` | Yes — same reasons, centralized | |
| Transition-only Events | Yes — fewer events, same information | |
