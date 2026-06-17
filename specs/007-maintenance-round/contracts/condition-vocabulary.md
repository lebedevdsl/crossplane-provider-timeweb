# Contract: Condition-Reason Vocabulary and `MapResolverErrorToCondition`

**Feature**: 007-maintenance-round | **Date**: 2026-06-17

This document is the authoritative reference for the single shared condition-reason
vocabulary applied across all managed resource kinds in the provider. It also
specifies the `MapResolverErrorToCondition` helper contract and the not-found
error message format (location-scoped simplified slugs).

---

## 1. Canonical Reason Set

All reason constants are defined in `internal/controller/shared/conditions.go`.
Crossplane's standard constructors (`xpv2.Available()`, `xpv2.Creating()`,
`xpv2.Deleting()`) are used directly for the standard transitions and are not
re-exported as custom reasons.

### Ready Conditions

| Reason / Constructor | Condition Type | Status | Meaning | Kinds |
|----------------------|---------------|--------|---------|-------|
| `xpv2.Available()` | Ready | True | Upstream resource observed healthy | All |
| `xpv2.Creating()` | Ready | False | First reconcile / Create in progress | All |
| `xpv2.Deleting()` | Ready | False | Deletion in progress | All |
| `ReasonPaymentRequired` | Ready | False | Account billing-blocked; resource created but cannot start | Server (clean signal); Router (best-effort) |
| `ReasonUpstreamFailed` | Ready | False | Terminal upstream `failed`/`error` state | All (where observed) |

### Synced Conditions

| Reason constant | Condition Type | Status | Meaning | Trigger |
|-----------------|---------------|--------|---------|---------|
| `ReasonImmutableFieldChange` | Synced | False | Create-time-only field edited | Controller immutability check |
| `ReasonParentNotReady` | Synced | False | Dependency (cluster, registry, router) not yet Ready=True | Dependency gate in controller |
| `ReasonPresetNotFound` | Synced | False | No upstream preset matches the operator's slug | Resolver `ErrPresetNotFound` |
| `ReasonPresetAmbiguous` | Synced | False | Multiple upstream presets match the slug | Resolver `ErrPresetAmbiguous` |
| `ReasonNoConfiguratorAvailable` | Synced | False | No upstream configurator matches sizing inputs | Resolver `ErrNoConfiguratorAvailable` |
| `ReasonSizingSwitchRequiresRecreate` | Synced | False | Operator switched between presetName and resources | Controller sizing-variant check |
| `ReasonCatalogUnauthorized` | Synced | False | 401/403 on catalog endpoint (preset or locations) | Resolver `ErrCatalogUnauthorized` |
| `ReasonCatalogTransient` | Synced | False | 5xx on catalog endpoint | Resolver `ErrCatalogTransient` |
| `ReasonDimensionValueNotFound` | Synced | False | Enum value not in upstream set | Resolver `ErrDimensionValueNotFound` |
| `ReasonProviderConfigInvalid` | Synced | False | Post-resolution ProviderConfig failure | Connector |
| `ReasonInvalidProviderConfigRef` | Synced | False | Wrong or missing ProviderConfig reference | Connector |
| `ReasonAPIError` | Synced | False | Generic upstream API error (default mapping) | All |
| `ReasonRateLimited` | Synced | False | Upstream rate limit hit | All |
| `ReasonSecretMissing` | Synced | False | Referenced connection Secret absent | Connector |
| `ReasonSecretKeyEmpty` | Synced | False | Connection Secret key empty | Connector |

---

## 2. `MapResolverErrorToCondition` Contract

**Location**: `internal/controller/shared/map_resolver_error.go`

```go
// MapResolverErrorToCondition maps a resolver sentinel error to the
// appropriate shared Synced=False condition. It handles all known resolver
// sentinel errors from internal/controller/shared/resolver/errors.go.
// An error that does not match any sentinel maps to ReasonAPIError.
//
// The caller MUST apply the returned condition to the managed resource via
// cr.SetConditions(...) and SHOULD call RecordConditionChange before
// SetConditions to emit a transition-only Event.
func MapResolverErrorToCondition(err error) xpv2.Condition
```

### Mapping Table

| Resolver sentinel | Reason | Condition |
|-------------------|--------|-----------|
| `resolver.ErrPresetNotFound` | `ReasonPresetNotFound` | Synced=False |
| `resolver.ErrPresetAmbiguous` | `ReasonPresetAmbiguous` | Synced=False |
| `resolver.ErrNoConfiguratorAvailable` | `ReasonNoConfiguratorAvailable` | Synced=False |
| `resolver.ErrDimensionValueNotFound` | `ReasonDimensionValueNotFound` | Synced=False |
| `resolver.ErrCatalogUnauthorized` | `ReasonCatalogUnauthorized` | Synced=False |
| `resolver.ErrCatalogTransient` | `ReasonCatalogTransient` | Synced=False |
| `resolver.ErrInvalidInput` | `ReasonAPIError` | Synced=False (programming error; surface as generic) |
| `resolver.ErrUnknownDimension` | `ReasonAPIError` | Synced=False (programming error) |
| `resolver.ErrDimensionFetcherUnwired` | `ReasonAPIError` | Synced=False (forward-compat stub) |
| any other error | `ReasonAPIError` | Synced=False |

### Call Sites

All six resolver-calling controllers must call `MapResolverErrorToCondition` at
every resolver invocation that can return a sentinel error. The prior state:

| Controller | Status before 007 |
|------------|-------------------|
| `s3bucket/external.go` | Local `mapResolverErrorToCondition` — replace with shared |
| `containerregistry/registry_external.go` | Local `mapResolverErrorToCondition` — replace with shared |
| `compute/server_external.go` | No mapping — resolver errors become generic ReconcileError |
| `kubernetes/cluster_external.go` | No mapping — resolver errors become generic ReconcileError |
| `kubernetes/nodepool_external.go` | No mapping — resolver errors become generic ReconcileError |
| `network/router_external.go` | No mapping — resolver errors become generic ReconcileError |

---

## 3. Transition-Only Event Contract

**Location**: `internal/controller/shared/events.go`

```go
// RecordConditionChange emits an Event if and only if the condition on mg
// has changed from its current value (compared by Type, Status, and Reason).
// Call this BEFORE mg.SetConditions so the comparison reads the prior state.
//
// eventType is corev1.EventTypeNormal for healthy transitions (Available,
// Creating) and corev1.EventTypeWarning for all degraded/failed reasons.
func RecordConditionChange(
    recorder record.EventRecorder,
    mg resource.Managed,
    newCondition xpv2.Condition,
)
```

### Event Type by Condition

| Condition / Reason | Event type | Suggested reason string |
|--------------------|------------|------------------------|
| Ready=True (Available) | Normal | `"Provisioned"` |
| Ready=False (Creating) | Normal | `"Provisioning"` |
| Ready=False (Deleting) | Normal | `"Deleting"` |
| Ready=False (PaymentRequired) | Warning | `"PaymentRequired"` |
| Ready=False (UpstreamFailed) | Warning | `"UpstreamFailed"` |
| Synced=False (any) | Warning | the reason string (e.g. `"PresetNotFound"`) |

### Non-Spam Guarantee

`RecordConditionChange` checks `mg.GetCondition(newCondition.Type)` before
emitting. If the existing condition's `Status` and `Reason` both match the new
condition, no Event is recorded. This prevents the event ring buffer from
filling on every reconcile pass (the standard 30s requeue would produce 2
events/minute per resource indefinitely without this guard).

---

## 4. Not-Found Error Message Format

### `PresetNotFoundError.Error()` — Location-Scoped

When `PresetInput.Location` is set, the not-found error lists **only** the
presets valid for that location, in **simplified (short) form**:

```
resolver: preset not found: slug "ssd-99" in dimension "DimServerPreset"
  does not match any upstream entry for location "ru-1"
  (valid: ssd-15, ssd-25, ssd-50, ssd-100, ssd-250, ssd-500, ssd-1000)
```

Rules:
- The valid list contains short-form slugs (`<short>` without `-<location>`) when
  all listed entries share the operator's declared location.
- The list is capped at 20 entries, suffixed with `…` if more exist.
- If `PresetInput.Location` is empty (zero value), the list contains full-form
  slugs from all locations (current behavior; no regression).

### `NoConfiguratorAvailableError.Error()` — Unchanged

The configurator error message format is unchanged (carries filter/sizing inputs
and the closest rejection). No location-scoping is added (configurators are
already region-specific via the Zone filter).

---

## 5. Applying Conditions Consistently

The standard pattern at every controller call-site:

```go
// 1. Resolve (call resolver.Resolve or equivalent)
out, err := r.Resolve(ctx, pcRef, dim, input)
if err != nil {
    // 2. Map error to condition
    cond := shared.MapResolverErrorToCondition(err)
    // 3. Record event if condition changed
    shared.RecordConditionChange(e.recorder, cr, cond)
    // 4. Apply condition
    cr.SetConditions(cond)
    // 5. Return error to trigger requeue (transient) or nil (terminal)
    if errors.Is(err, resolver.ErrCatalogTransient) {
        return managed.ExternalObservation{}, err
    }
    return managed.ExternalObservation{}, nil  // terminal; don't requeue
}
```

**Terminal vs. transient requeue** rule:

| Reason | Requeue? |
|--------|---------|
| `PresetNotFound`, `PresetAmbiguous`, `NoConfiguratorAvailable`, `DimensionValueNotFound`, `ImmutableFieldChange` | No — operator must fix the manifest |
| `CatalogTransient`, `RateLimited`, `APIError` | Yes — return the error so the runtime requeues |
| `CatalogUnauthorized` | No — credentials must be fixed first |
| `UpstreamFailed`, `PaymentRequired` | No — operator must act (delete/recreate or top up account) |
| `ParentNotReady` | No — `Watches` on the parent triggers re-reconcile automatically |
