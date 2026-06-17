# Contract: `/api/v2/locations` — Region and Zone Catalog

**Feature**: 007-maintenance-round | **Date**: 2026-06-17

This endpoint is Timeweb's authoritative source for the region→availability-zone
hierarchy. The provider uses it to replace the hardcoded `azLocation` /
`defaultAZByLocation` tables (see research.md R-1).

---

## Endpoint

```
GET /api/v2/locations
Authorization: Bearer <token>
```

No path parameters. No required query parameters.

---

## Response Shape

```json
{
  "locations": [
    {
      "location": "<human-readable name>",
      "location_code": "<api-code>",
      "availability_zones": ["<zone-1>", "<zone-2>", ...]
    }
  ]
}
```

**Field semantics**:

| Field | Type | Description |
|-------|------|-------------|
| `location` | string | Human-readable name (Russian for domestic regions); not used by the provider |
| `location_code` | string | The region code used in all other API endpoints (`location` parameter) |
| `availability_zones` | []string | AZ codes for this region; subset used in `availability_zone` parameters |

---

## Known Regions (as of 2026-06-01 live probe)

Eight regions are returned. The table also shows the **correct** zone-to-location
mapping, superseding the buggy `defaultAZByLocation` in `floatingip_external.go`:

| `location_code` | Human name | Known `availability_zones` | Notes |
|-----------------|------------|---------------------------|-------|
| `ru-1` | Россия (Санкт-Петербург) | `spb-1`, `spb-2`, `spb-3`, `spb-4`, `spb-5` | 5 zones; most products |
| `ru-2` | Россия (Новосибирск) | `nsk-1` | 1 zone; **was wrongly mapped to `msk-1`** |
| `ru-3` | Россия (Москва) | `msk-1` | 1 zone; **was wrongly mapped to `spb-3`** |
| `nl-1` | Нидерланды (Амстердам) | `ams-1` | 1 zone |
| `de-1` | Германия (Франкфурт) | `fra-1` | 1 zone |
| `kz-1` | Казахстан (Алматы) | `ala-1` | 1 zone; was missing from old table |
| `us-4` | США | `us-4` (zone code = location code) | 1 zone; was missing from old table |
| `pl-1` | Польша | `pl-1` (zone code = location code) | 1 zone; was missing from old table |

> **Note**: `ru-1` exposing five zones is the only current multi-AZ region.
> When an operator sets `location: ru-1` without specifying `availabilityZone`,
> the provider MUST ask them to specify the zone explicitly (ambiguous placement).
> For all other regions (single-AZ), the provider derives the zone automatically.

---

## Caching Strategy

The provider caches this endpoint's response per ProviderConfig reference
(`PCRef`) using the same TTL-bounded in-memory cache as the preset catalog.

**Cache key**: `(PCRef, "locations")`

**TTL**: same as the resolver's catalog TTL (configurable, default 5 minutes).

**On miss**: the provider fetches synchronously during the reconcile that needs
the zone mapping, then caches for the TTL. Subsequent reconciles within the TTL
window use the cached value.

**Error handling**:
- HTTP 401/403 → `CatalogUnauthorized` condition (same as preset catalog).
- HTTP 5xx → `CatalogTransient` condition; requeue.
- Empty `locations` slice on a 200 OK → treat as transient (return error, do
  not cache the empty result — same rule as the preset empty-slice fix).

---

## Usage in the Provider

### `AZToLocation(az string) (string, error)`

Inverts the `availability_zones` arrays from the cached response: iterates all
locations and returns the `location_code` whose `availability_zones` array
contains `az`. If not found, returns an error (unknown AZ — CRD enum and
live catalog are out of sync).

Called by: Router and KubernetesCluster controllers on the backward-compat path
where an existing manifest sets only `availabilityZone`.

### `LocationZones(location string) ([]string, error)`

Returns the `availability_zones` array for the given `location_code`. If the
location is not in the cached response, returns an error.

Called by: Router and KubernetesCluster controllers when `location` is set but
`availabilityZone` is omitted, to determine whether to auto-derive (single-AZ
region) or request operator specification (multi-AZ region).

### `availabilityZoneFor` in `floatingip_external.go`

Updated to consult the cached live lookup instead of the hardcoded
`defaultAZByLocation` map. The function signature is unchanged; callers are
unaffected.

---

## Behavioral Quirks

- The `availability_zones` field name uses **underscores** (consistent with
  Timeweb's general underscore-envelope pattern; see `project_timeweb_underscore_envelopes`
  memory entry).
- The `location` (human name) field is the Russian dashboard label — NOT the
  API code. Always use `location_code` for API interactions.
- Zone codes generally follow the pattern `<city-abbrev>-<n>` (e.g. `spb-3`,
  `msk-1`). For `us-4` and `pl-1`, the zone code equals the location code.
