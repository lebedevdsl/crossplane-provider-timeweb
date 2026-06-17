# Research: Feature 007 — Maintenance Round

**Date**: 2026-06-17 | **Branch**: `007-maintenance-round`

Resolves open items R-1…R-5 from `plan.md`. Each item follows the
**Decision / Rationale / Alternatives** structure.

---

## R-1 — `/api/v2/locations` as the authoritative region→zone source

### Decision

`GET /api/v2/locations` is the single authoritative source for the region→zone
mapping. Replace the hardcoded `azLocation` table in
`internal/controller/shared/azlocation.go` and the `defaultAZByLocation` table
in `internal/controller/network/floatingip_external.go` with a cached lookup
backed by this endpoint.

**Cache shape**: one entry per ProviderConfig reference (`PCRef`), TTL-bounded
(same pattern as the resolver's preset catalog). At `AZToLocation` call sites
the table is consulted in memory; a miss triggers a synchronous fetch and
populates the cache for the TTL window.

**Structural finding** (verified live during feature 006): the response is:

```json
{
  "locations": [
    {
      "location": "Россия (Санкт-Петербург)",
      "location_code": "ru-1",
      "availability_zones": ["spb-1", "spb-2", "spb-3", "spb-4", "spb-5"]
    },
    ...
  ]
}
```

Eight locations are returned (ru-1, ru-2, ru-3, nl-1, de-1, kz-1, us-4, pl-1).
The `ru-1` region currently has **five** zones, confirming both the multi-AZ
reality and the incompleteness of the old 4-entry table.

**Known bug to fix unconditionally** (before the live-sourced lookup lands):
the existing `defaultAZByLocation` table in `floatingip_external.go` has the
`ru-2` and `ru-3` entries **inverted**:

| Location | Table (wrong) | Live API (correct) |
|----------|---------------|-------------------|
| `ru-2`   | `msk-1`       | `nsk-1` (Novosibirsk) |
| `ru-3`   | `spb-3`       | `msk-1` (Moscow) |

Additionally, `us-4` and `pl-1` are absent from the table, causing
`floatingip_external.go:availabilityZoneFor` to return an error for those
locations even when the operator provides a valid `location`. Both gaps are
fixed with the live-sourced table.

**`AZToLocation` (shared/azlocation.go)**: the inverse direction (zone → region)
is still needed by the Router and KubernetesCluster controllers when only an
`availabilityZone` is provided (backward-compat path, see R-2). After the
switch to live sourcing, `AZToLocation` is implemented by inverting the fetched
`availability_zones` arrays from the same catalog fetch.

**`LocationZones` (shared/azlocation.go)**: used to enumerate valid zones for a
region when the operator omits `availabilityZone`. After the switch, it reads
from the same cached payload.

### Rationale

- The hardcoded table already has **confirmed wrong entries** (ru-2↔ru-3
  inversion), two **missing entries** (us-4, pl-1), and zero entries for the
  five `ru-1` sub-zones.
- The spec (FR-002, SC-001) requires all 8 regions to be reachable; a static
  table cannot satisfy that without ongoing hand-maintenance.
- `/api/v2/locations` was verified live in feature 006 for the Router
  placement work and has stable structure.
- A cached lookup matches the existing resolver pattern: a PCRef-keyed,
  TTL-bounded in-memory map; no new dependency.

### Alternatives

**A. Keep the static table but expand it manually.**
Rejected: every new Timeweb region or zone addition requires a code change;
the inverted entries prove the table drifts; fails SC-001 for ru-2 today.

**B. Derive zones from the preset catalog payloads.**
Rejected: preset catalogs are product-specific (server presets, router tiers,
k8s presets all differ); not every placement field has a catalog. `/api/v2/locations`
is the explicit placement authority.

**C. Require operators to always specify both `location` and `availabilityZone`.**
Rejected by FR-004 (existing manifests break) and is worse UX (redundant
precision for single-AZ regions).

---

## R-2 — Router / KubernetesCluster placement: `location` required + `availabilityZone` optional

### Decision

Add a required `location` field to `RouterParameters` and
`KubernetesClusterParameters`, matching the model already used by Server,
Network, and FloatingIP. Make the existing `AvailabilityZone` field **optional**
(pointer, `// +optional`) so existing manifests continue to parse and validate
without change.

**Admission invariant (CEL)**: when both `location` and `availabilityZone` are
set, the zone must belong to the location per the live catalog — validated at
admission time using the resolved mapping:

```
// expressed as CEL; the provider's webhook validates cross-referencing
self.spec.forProvider.availabilityZone == ""
  || zonesForLocation(self.spec.forProvider.location).contains(
       self.spec.forProvider.availabilityZone)
```

Because kubebuilder CEL rules cannot call external functions, the cross-field
zone/location consistency check is enforced **in the controller's Create path**
(before the upstream call), not at admission. The CRD constraint covers the
individual field formats.

**Derivation rule in the controller**:

1. If the operator sets both `location` and `availabilityZone` → use both as
   given, but validate zone belongs to location.
2. If the operator sets `location` only → the controller fetches the zones list
   for that region; if exactly one zone exists, use it; if multiple zones exist,
   error with a message listing the valid zones.
3. If the operator sets `availabilityZone` only (backward-compat path for
   existing manifests) → derive `location` by calling `AZToLocation(az)` from
   the live-sourced table (R-1).
4. Neither set → validation error at admission (location is required by the new
   schema).

**Schema change details** (additive, backward-compatible):

| Kind | Old | New |
|------|-----|-----|
| `RouterParameters.AvailabilityZone` | `string` required | `*string` optional |
| `RouterParameters.Location` | absent | `string` required, enum=8 regions |
| `KubernetesClusterParameters.AvailabilityZone` | `string` required | `*string` optional |
| `KubernetesClusterParameters.Location` | absent | `string` required, enum=8 regions |

The `AvailabilityZone` enum constraint (`spb-3;msk-1;ams-1;fra-1`) is **removed**
because the live-sourced table now carries the complete valid-zone set; CEL
or controller validation replaces it.

**Immutability**: the new `location` field carries the same
`self == oldSelf` immutability CEL guard as other placement fields. The
`availabilityZone` immutability guard is unchanged.

### Rationale

- Router and KubernetesCluster are the only kinds where placement was modeled
  as `availabilityZone`-only (sc-001 gap: only 4 of 8 regions reachable).
- Making `availabilityZone` optional (pointer) is the only backward-compatible
  schema change that allows existing applied manifests to pass validation
  unchanged; a new `location` field becomes required for new manifests.
- The 4-value enum on `availabilityZone` was a compensating control for the
  incomplete `azLocation` table — with the live-sourced lookup the closed enum
  becomes a constraint, not an affordance.

### Alternatives

**A. Remove `availabilityZone` and replace with `location`-only.**
Rejected by FR-004 (breaking change for applied manifests) and by the
zone-hierarchy reality (`ru-1` has five zones; location-only cannot express
sub-zone pinning).

**B. Keep `availabilityZone` required but expand the enum.**
Rejected: enum expansion is not backward-compatible for older applied manifests
that used `ru-3` (which is a location code, not a zone). The existing enum
values mix zones and locations, making it conceptually broken; fixing the model
is cleaner than expanding a broken enum.

**C. Add `location` as optional with a mutual-exclusion CEL.**
Rejected: an optional-but-preferred field creates a two-tier manifest style
that perpetuates confusion; making `location` required for new resources while
accepting `availabilityZone`-only on existing ones is the standard
deprecation-without-breaking-change pattern.

---

## R-3 — Bare-slug matching + back-compat + location-scoped not-found list

### Decision

Extend `MatchPresetSlug` (and the `PresetInput` struct) to accept three
equivalent slug forms, all resolved through the same catalog entries:

| Form | Example | When used |
|------|---------|-----------|
| Long (existing) | `ssd-15-ru-1` | Existing manifests; keep forever |
| Short (new) | `ssd-15` | Operator omits location suffix already in `location` field |
| Disambiguated (existing) | `ssd-15-ru-1-199` | Collision resolution; unchanged |

**Matching algorithm** (location-aware, in order):

1. Normalize the input slug.
2. Try the disambiguator split (`<base>-<id>`) as today — if the numeric
   suffix parses and matches an entry's upstreamID AND the base matches the
   entry's full slug, return immediately. Use `strconv.ParseInt` with a length
   guard (≤18 digits) to replace the overflow-prone `id*10+digit` loop
   (backlog overflow fix).
3. Location-filter the entries to the operator's declared location (from
   `PresetInput.Location`, which mirrors `ForProvider.Location`). This means
   the "valid" list in a not-found error is **location-scoped**.
4. Try full-slug match (`<short>-<location>`) against location-filtered entries.
5. Try short-slug match (just `<short>`, no location suffix) against
   location-filtered entries.
6. Return the unique match, or `PresetAmbiguousError` (multiple), or
   `PresetNotFoundError` (zero, with the location-filtered valid list).

**`PresetInput` struct extension** (additive field, zero-value = unconstrained):

```go
type PresetInput struct {
    Slug     string  // operator's preset name (any form)
    Zone     string  // existing zone filter (feature 006)
    Location string  // NEW: region filter for short-slug resolution + not-found scoping
}
```

**Not-found error format** (location-scoped, simplified):

```
resolver: preset not found: slug "ssd-99" in dimension "DimServerPreset"
  does not match any upstream entry for location "ru-1"
  (valid: ssd-15, ssd-25, ssd-50, ssd-100, ssd-250, ssd-500, ssd-1000, …)
```

The valid list contains **simplified slugs** (short form, no location suffix)
when all listed entries share the operator's location.

**Back-compat guarantee**: the long-form slug (`ssd-15-ru-1`) continues to
match because step 4 (full-slug match against location-filtered entries) finds
the same entry as before. No existing manifest breaks.

**`splitDisambiguator` overflow fix**: replace the manual
`id = id*10 + int64(r-'0')` accumulation with:

```go
suffix := s[i+1:]
if len(suffix) > 18 { return s, 0, false } // overflow guard
id, err := strconv.ParseInt(suffix, 10, 64)
if err != nil { return s, 0, false }
```

### Rationale

- FR-003 and FR-005: operators should not need to repeat the location they
  already specified in `spec.forProvider.location`. The short-form slug
  addresses this directly.
- FR-004: the long-form slug is accepted indefinitely. The matching algorithm
  tries the full-slug form first (location-filtered), so `ssd-15-ru-1` still
  resolves to the same entry.
- SC-002: not-found errors currently list ALL valid slugs from ALL locations
  (potentially dozens of cross-location entries). Location-filtering the list
  makes the error self-service.
- The `splitDisambiguator` int64 overflow is a confirmed latent bug (backlog
  [BOTH]); the fix is mechanical and safe.

### Alternatives

**A. Accept short slugs by stripping the location suffix from catalog entries
during matching.**
Rejected: it conflates the slug form with the catalog shape. Filtering by
location and then matching on the short-slug computed by `Slugify(short, "")` is
cleaner and handles cases where a location suffix appears organically in the
preset name (e.g. a preset named `custom-ru-2` in the `nl-1` region).

**B. Add a `locationPrefix` resolver option that prepends location to the input
slug before matching.**
Rejected: it forces the long form on the resolver, hiding the simplification
from the match algorithm. The proposed approach makes the short-slug a
first-class match.

**C. Make the not-found error list global but sorted/prefixed by the declared
location.**
Rejected by SC-002 ("zero cross-location entries in the message"); a global
list with cross-location noise defeats the self-service goal.

---

## R-4 — Per-kind no-pay signal and `PaymentRequired` scope

### Decision

`PaymentRequired` is wired **only** where a confirmed upstream no-pay signal
has been observed. The mapping is:

| Kind | Upstream no-pay signal | `PaymentRequired` wired? | Evidence |
|------|------------------------|--------------------------|----------|
| Server | `status == "no_paid"` | **Yes** (already in 006) | Live-verified: clean, unambiguous, stable state string |
| Router | `status == "error"` | **Best-effort** (already in 006 F-7) | Only upstream state that plausibly maps to payment-blocked; confirmed in 006 as ambiguous (also fires on non-payment errors); wired with best-effort caveat |
| KubernetesCluster | `status == "error"` | **No** — `UpstreamFailed` only | The cluster's error state covers multiple failure modes; no distinct no-pay string observed in any probe |
| KubernetesClusterNodepool | none observed | **No** | No distinct no-pay state in node-group payloads |
| KubernetesClusterAddon | none observed | **No** | No distinct no-pay state; addon installs are post-cluster |
| Network (VPC) | none observed | **No** | VPC creation is free; no payment-gate observed |
| FloatingIP | none observed | **No** | IP allocation has no quota-gate in observed probes |
| ContainerRegistry | none observed | **No** | Registry provisioning is tariff-gated at preset selection, not post-create |
| S3Bucket (ObjectStorage) | none observed | **No** | Same as ContainerRegistry |

**Implementation gate**: a live probe per kind is required before wiring
`PaymentRequired`. Speculative detection (wiring a reason without an observed
signal) is explicitly prohibited (FR-008: "it MUST NOT be promised for kinds
where no such signal has been observed"). For kinds without a no-pay signal,
a `status == "failed"` or `status == "error"` surfaces as `UpstreamFailed`
(terminal condition, Ready=False, Synced=True).

**Router caveat**: the Router's `status == "error"` is ambiguous — it fires for
payment-blocked AND for configuration errors. The 006 implementation mapped it
to `PaymentRequired` with a comment noting the ambiguity. In 007 the mapping is
re-evaluated: if a configuration-error signal becomes distinguishable (e.g. an
additional field), the condition will be split; until then, `PaymentRequired`
remains the best available mapping for Router.

### Rationale

- FR-008 is explicit: "PaymentRequired MUST be reported only where a confirmed
  upstream no-pay signal exists". Speculative wiring produces false-positive
  conditions that confuse operators.
- Server's `no_paid` is the gold standard — unambiguous, stable, confirmed live.
  Other kinds do not expose an equivalent.
- The `UpstreamFailed` condition is the general-purpose terminal-state signal
  for all other failure modes.

### Alternatives

**A. Wire `PaymentRequired` for all kinds based on heuristics (e.g. 402 HTTP
response, account-level quota check).**
Rejected: the Timeweb API does not return 402 for billing-blocked resources; the
upstream silently accepts the create call and the failure surfaces in the
resource's status. Account-level quota checks are not available without a
separate endpoint that is undocumented and untested.

**B. Omit `PaymentRequired` entirely and use `UpstreamFailed` everywhere.**
Rejected: Server's `no_paid` is a well-defined, operator-actionable state
distinct from an unrecoverable failure. Collapsing it to `UpstreamFailed` would
remove the operator's signal to top up the account.

---

## R-5 — Shared condition-reason vocabulary + `MapResolverErrorToCondition` + transition-only Events

### Decision

#### Vocabulary

The canonical reason set is already defined in
`internal/controller/shared/conditions.go`. The 007 round adds three missing
reasons:

| Reason | Condition type | Already in shared? | Description |
|--------|---------------|---------------------|-------------|
| `Available` | Ready=True | No (use `xpv2.Available()` directly) | Standard xpv2 constructor, not a custom reason |
| `Creating` | Ready=False | No (use `xpv2.Creating()` directly) | Standard xpv2 constructor |
| `Deleting` | Ready=False | No (use `xpv2.Deleting()` directly) | Standard xpv2 constructor |
| `PaymentRequired` | Ready=False | **Yes** | Upstream no-pay state (Server/Router) |
| `UpstreamFailed` | Ready=False | **Yes** | Terminal upstream failure state |
| `ImmutableFieldChange` | Synced=False | **Yes** | Create-time-only field edited |
| `ParentNotReady` | Synced=False | **No — ADD** | Dependency (cluster, registry) not yet Ready=True |
| `PresetNotFound` | Synced=False | **Yes** | Resolver: preset slug unmatched |
| `PresetAmbiguous` | Synced=False | **Yes** | Resolver: slug matches multiple entries |
| `NoConfiguratorAvailable` | Synced=False | **Yes** | Resolver: sizing inputs rejected |
| `SizingSwitchRequiresRecreate` | Synced=False | **Yes** | preset→resources or vice-versa |
| `CatalogUnauthorized` | Synced=False | **Yes** | 401/403 on catalog endpoint |
| `CatalogTransient` | Synced=False | **Yes** | 5xx on catalog endpoint |
| `DimensionValueNotFound` | Synced=False | **Yes** | Enum value not in upstream set |
| `ProviderConfigInvalid` | Synced=False | **Yes** | Post-resolution PC failure |
| `InvalidProviderConfigRef` | Synced=False | **Yes** | Wrong PC reference |
| `APIError` | Synced=False | **Yes** | Generic upstream API error |
| `RateLimited` | Synced=False | **Yes** | Upstream rate limit |
| `ReconcileError` | (not a shared reason) | — | Retire: replace with typed reasons above |

**Add to `shared/conditions.go`**:
```go
// ReasonParentNotReady surfaces when a dependent resource (cluster, registry,
// router) has not yet reached Ready=True. The controller will re-reconcile
// when the parent changes (via Watches).
ReasonParentNotReady xpv2.ConditionReason = "ParentNotReady"
```

#### `MapResolverErrorToCondition` contract

Extract the per-controller resolver-error-to-condition mapping into a single
shared function:

```go
// MapResolverErrorToCondition maps a resolver sentinel error to the
// appropriate shared condition on the managed resource. It handles all
// known resolver sentinels; an unknown error is mapped to APIError.
// Returns the condition (caller applies it).
func MapResolverErrorToCondition(err error) xpv2.Condition {
    switch {
    case errors.Is(err, resolver.ErrPresetNotFound):
        return SyncedFalse(ReasonPresetNotFound, err.Error())
    case errors.Is(err, resolver.ErrPresetAmbiguous):
        return SyncedFalse(ReasonPresetAmbiguous, err.Error())
    case errors.Is(err, resolver.ErrNoConfiguratorAvailable):
        return SyncedFalse(ReasonNoConfiguratorAvailable, err.Error())
    case errors.Is(err, resolver.ErrDimensionValueNotFound):
        return SyncedFalse(ReasonDimensionValueNotFound, err.Error())
    case errors.Is(err, resolver.ErrCatalogUnauthorized):
        return SyncedFalse(ReasonCatalogUnauthorized, err.Error())
    case errors.Is(err, resolver.ErrCatalogTransient):
        return SyncedFalse(ReasonCatalogTransient, err.Error())
    default:
        return SyncedFalse(ReasonAPIError, err.Error())
    }
}
```

This replaces the four controllers (Server, KubernetesCluster, KubernetesClusterNodepool,
Router) that currently propagate resolver errors as generic `ReconcileError` or
do not surface conditions at all. The two controllers that already have a local
`mapResolverErrorToCondition` (S3Bucket, ContainerRegistry) are updated to call
the shared version.

#### Transition-only Events

Events MUST fire on condition **changes** only — not on every reconcile pass.
The mechanism:

1. Before applying a new condition, read the **current** condition value from
   `cr.GetCondition(condType)`.
2. If the reason or status has changed, record an Event (Warning for non-
   healthy reasons, Normal for Available/Creating transitions).
3. If the condition is unchanged, skip the Event call entirely.

**Helper signature** (new file `internal/controller/shared/events.go`):

```go
// RecordConditionChange records an Event if and only if the condition
// on mg has changed from its current value. Must be called BEFORE
// mg.SetConditions so the comparison sees the prior state.
func RecordConditionChange(
    recorder record.EventRecorder,
    mg resource.Managed,
    newCondition xpv2.Condition,
)
```

Event types by transition:
- `Available` → `Normal` / `"Provisioned"` message
- `Creating` → `Normal` / `"Provisioning"` message
- `PaymentRequired` → `Warning` / condition message
- `UpstreamFailed` → `Warning` / condition message
- `ImmutableFieldChange` → `Warning` / condition message
- `ParentNotReady` → `Warning` / condition message
- Any resolver reason → `Warning` / condition message

### Rationale

- FR-009a: a single shared vocabulary eliminates the four-controller gap where
  resolver errors surface as generic `ReconcileError`.
- `ParentNotReady` is the only missing reason in the existing vocabulary (used
  literally as a string in some controllers, not as a typed constant).
- FR-009: transition-only Events prevent per-reconcile spam while making
  `kubectl describe` useful for debugging.
- The `MapResolverErrorToCondition` helper eliminates the local copies in
  S3Bucket and ContainerRegistry (which duplicate but not generalize the
  mapping) and fills the gap in the four controllers that don't map at all.

### Alternatives

**A. Record Events unconditionally on every reconcile.**
Rejected by FR-009: per-reconcile events create noise that buries the useful
signal and fills the Event ring buffer.

**B. Maintain per-controller local vocabularies.**
Rejected by FR-009a: inconsistent reasons across kinds are the root cause of
the "need to read source code to diagnose" problem (US4).

**C. Use a middleware/wrapper around `SetConditions` to auto-detect transitions.**
Considered but rejected: wrapping Crossplane's condition methods introduces
indirection that is harder to test and reason about. An explicit
`RecordConditionChange` call at each site is more legible and forces the
developer to think about the transition point.
