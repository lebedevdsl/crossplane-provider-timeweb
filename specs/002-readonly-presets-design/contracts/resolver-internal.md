# Contract: Internal Catalog Resolver

**Location**: `internal/controller/shared/resolver` (Go package, not a Kubernetes object)

**Status**: new in feature `002-readonly-presets-design`. Spec linkage: FR-006, FR-007, FR-011, FR-012, FR-013, FR-016, FR-017.

This is not a Kubernetes API surface — it is the internal contract every MR reconciler depends on for resolving operator-supplied stable inputs to upstream Timeweb identifiers. Documented here so the MR contracts elsewhere in this directory can reference it without re-describing the abstraction.

## Public API

```go
package resolver

type Resolver interface {
    Resolve(ctx context.Context, pcRef PCRef, dim Dimension, input ResolveInput) (ResolveOutput, error)
    Invalidate(pcRef PCRef, dim Dimension) // explicit cache invalidation for stale-detected entries (FR-013)
}

type PCRef struct {
    Kind      string // "ProviderConfig" | "ClusterProviderConfig"
    Name      string
    Namespace string // "" for ClusterProviderConfig
}

type Dimension struct {
    Name string
    Kind DimensionKind
}

type DimensionKind int

const (
    DimensionPreset DimensionKind = iota
    DimensionConfigurator
    DimensionEnum
)
```

## Dimension kinds

### Preset (FR-006)

Resolves an operator-typed slug to an upstream preset ID.

```go
type PresetInput  struct { Slug string }
type PresetOutput struct { UpstreamID int64 }
```

Slug rule: `<short>-<location>` (lowercase, non-`[a-z0-9-]` collapsed to `-`). Explicit disambiguator form `<short>-<location>-<id>` is also accepted (FR-008).

Errors:
- `ErrPresetNotFound` — no match. Wrapped with the list of currently-valid slugs (capped 20).
- `ErrPresetAmbiguous` — multiple matches (rare). Wrapped with the colliding upstream IDs.

### Configurator (FR-007)

Picks a configurator deterministically from operator-supplied stable fields.

```go
type ConfiguratorInput struct {
    Filters map[string]any // location, diskType, enableLocalNetwork, cpuFrequencyTier, gpu? (kind-specific)
    Sizing  map[string]int64 // cpu, ramMB, diskGB, bandwidthMbps (kind-specific)
}

type ConfiguratorOutput struct {
    UpstreamID    int64
    LockedSizing  map[string]int64
}
```

Selection algorithm (deterministic per FR-007):

1. Hard-filter entries by `Filters` (exact match on `location`, `diskType`, etc.).
2. Capability-filter by `Sizing` against each entry's `requirements.{min,step,max}` bounds; reject any entry where any requested sizing falls outside its bounds.
3. Tightest fit: sort survivors ascending on `(max_cpu, max_ramMB, max_diskGB)`.
4. Tiebreaker: lowest `upstream_id`.

Errors:
- `ErrNoConfiguratorAvailable` — no survivor. Wrapped with the operator-supplied values and the closest-rejected entry's bounds.

### Enum (FR-017)

Validates that an operator-supplied free-form string is a member of an upstream enum set.

```go
type EnumInput  struct { Value string }
type EnumOutput struct { Valid bool; ValidValues []string /* populated on miss */ }
```

Errors:
- `ErrDimensionValueNotFound` — value is not in the set. Wrapped with the list of valid values (capped 20).

## Cross-cutting errors (any dimension kind)

- `ErrCatalogUnauthorized` — upstream 401/403. Sticky on the cache entry until next successful fetch.
- `ErrCatalogTransient` — upstream 5xx (after backoff exhaustion). Caller MUST treat as transient and requeue.

The MR reconciler MAPS these errors to the conditions in its CR contract (`PresetNotFound`, `NoConfiguratorAvailable`, `CatalogUnauthorized`, etc.) — that mapping is documented in each MR's contract file.

## Cache semantics

| Property               | Value                                                      |
|------------------------|------------------------------------------------------------|
| Storage                | process-local Go map; no persistence                       |
| Key                    | `(PCRef, Dimension.Name)`                                  |
| TTL                    | default 5 min; flag-configurable in [1 min, 1 hour]        |
| Coalescing             | `golang.org/x/sync/singleflight`, one in-flight GET per key |
| Eviction               | passive (next-access TTL check); no background sweeper     |
| Restart behavior       | cold; re-warms lazily                                      |
| 401/403 behavior       | sticky error on entry; cleared on next successful fetch    |
| 4xx on previously-cached entry | invalidate entry, re-fetch on next access (FR-013) |
| 5xx behavior           | exponential backoff with retry; transient error to caller (FR-012) |

## Dimension registry

Implementation-side table (not part of the public Go API but part of the package's responsibilities):

```go
type dimensionDef struct {
    Kind     DimensionKind
    Fetch    func(ctx context.Context, client timeweb.Client) (any, error)
    Filter   func(entry any) bool         // optional response-side filter
    Derive   func(payload any) any        // for Enum dims derived from another dimension's payload
}

var registry = map[string]dimensionDef{
    "ContainerRegistryPreset":      { … },
    "ContainerRegistryConfigurator":{ … },
    "S3BucketPreset":               { … },
    "S3BucketConfigurator":         { … },

    // Forward-compat (shipped now, exercised by the K8s feature):
    "ServerConfigurator":           { … },
    "KubernetesMasterPreset":       { Kind: DimensionPreset, Fetch: …, Filter: typeIs("master") },
    "KubernetesWorkerPreset":       { Kind: DimensionPreset, Fetch: …, Filter: typeIs("worker") },
    "KubernetesVersion":            { Kind: DimensionEnum, Fetch: … },
    "KubernetesNetworkDriver":      { Kind: DimensionEnum, Fetch: … },
    "AvailabilityZone":             { Kind: DimensionEnum, Derive: deriveZonesFromKubernetesPreset },
}
```

This table is the only place that knows endpoint paths and response shapes. Adding a new MR kind = adding rows here, no spec edit (per FR-006 & the 2026-05-31 endpoint-mapping clarification).

## Tests (constitution §III)

See [research.md §R-6](../research.md#r-6--test-strategy) for the full test matrix. Summary:

- `resolver_test.go` — exhaustive table-driven coverage of every dimension-kind × every error path.
- `cache_test.go` — TTL expiry, coalescing under concurrent miss, stale invalidation.
- `slug_test.go` — slug rule against the OpenAPI fixture data.
- `select_configurator_test.go` — selection algorithm with multiple candidates, tiebreakers.

Fakes via `counterfeiter` over the existing `timeweb.Client` interface; no live HTTP.
