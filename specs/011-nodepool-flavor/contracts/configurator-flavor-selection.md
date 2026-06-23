# Contract: Resolver tag-constrained configurator selection

## `ConfiguratorInput` (resolver)

```go
type ConfiguratorInput struct {
    Filters     map[string]any     // existing — exact-match (location, …)
    Sizing      map[string]int64   // existing — cpu/ramMB/diskGB/gpu
    RequireTags []string           // NEW — entry.Tags must ⊇ RequireTags (empty = unconstrained)
}
```

- `RequireTags` empty/nil → behavior identical to today (no regressions for other dimensions).
- An entry is eligible only if, for every `t` in `RequireTags`, `t ∈ entry.Tags`.

## `SelectConfigurator` pipeline order (normative)

1. hard filter (`matchFilters`)
2. capability filter (`matchSizing`)
3. **tag filter** (`RequireTags` ⊆ `entry.Tags`) ← NEW
4. standard-vs-promo partition (prefer non-promo)
5. tightest-fit sort → by upstream id → pick first

The tag filter MUST run before steps 4–5 so the fit/promo logic never observes
out-of-family entries.

## Error contract

| Condition | Result |
|-----------|--------|
| no entry passes the tag filter (family has no entry for this location) | `NoConfiguratorAvailableError` citing the missing tag(s) |
| entries match the tag but none fits the sizing | `NoConfiguratorAvailableError` with `ClosestRejected` = the in-family entry + its sizing-rejection reason |

In neither case is a configurator from outside `RequireTags` returned (FR-005).

## kubernetes controller mapping (`resolveK8sConfigurator`)

```
flavor "standard"      → RequireTags: ["k8s_configurator_general"]
flavor "dedicated-cpu" → RequireTags: ["k8s_configurator_dedicated_cpu"]
flavor "" (unset)      → treated as "standard"
```

- Applied for the **worker** dimension (`DimKubernetesWorkerConfigurator`) only.
- The **master** dimension passes no `RequireTags` (single family) — unchanged.

## Test obligations (Constitution III)

Unit tests (fake catalog client), at minimum:
- standard selects general; dedicated-cpu selects dedicated; given a catalog where tightest-fit
  alone would pick the *other* family (the 59/69 case), the tag filter overrides it.
- unset flavor → standard/general.
- sizing valid only in the non-selected family → error, no substitution.
- `RequireTags` empty → identical result to pre-change selection (regression guard).
