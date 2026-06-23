# Phase 1 Data Model: Nodepool Worker Configurator Flavor

## API change — `KubernetesClusterNodepool` worker `Resources`

New field on the existing custom-sizing struct (the one carrying `cpu`/`ramGB`/`diskGB`/`gpu`):

| Field    | Type     | Required | Default    | Validation                                  |
|----------|----------|----------|------------|---------------------------------------------|
| `flavor` | `string` | no       | `standard` | enum `standard` \| `dedicated-cpu`          |

Markers:
```go
// +kubebuilder:validation:Enum=standard;dedicated-cpu
// +kubebuilder:default=standard
// +optional
Flavor string `json:"flavor,omitempty"`
```

- Scope: nodepool worker `Resources` only. The cluster master `Resources` struct is NOT
  modified (single master family upstream).
- Semantics: governs **custom-configurator** selection only; with a preset-sized pool the
  family is already fixed and `flavor` has no effect.
- Mutability: follows the existing nodepool `resources` change semantics (configurator is
  locked at Create; this feature does not introduce re-resolution of existing pools).

## Flavor → catalog family

| `flavor`        | Worker configurator tag           | Panel label   | RAM/CPU floor |
|-----------------|-----------------------------------|---------------|---------------|
| `standard`      | `k8s_configurator_general`        | Premium NVMe  | low (~1 GB/cpu) |
| `dedicated-cpu` | `k8s_configurator_dedicated_cpu`  | Dedicated CPU | ~4 GB/cpu     |

## Resolver change — `ConfiguratorInput`

| Field         | Type       | Meaning                                                                 |
|---------------|------------|-------------------------------------------------------------------------|
| `RequireTags` | `[]string` | An entry is eligible only if its `Tags` contains every listed tag. Empty = no tag constraint (current behavior). |

`ConfiguratorEntry.Tags` already exists and is populated from the catalog — no change there.

## Selection pipeline (after change)

`SelectConfigurator` order:
1. hard filter (`matchFilters`: location, …) — unchanged
2. capability filter (`matchSizing`: cpu/ramMB/diskGB/gpu bounds) — unchanged
3. **NEW: tag filter** — keep only entries whose `Tags` ⊇ `RequireTags`
4. standard-vs-promo partition (prefer non-promo) — unchanged
5. tightest-fit sort, then by upstream id — unchanged

If step 3 empties the survivor set, return `NoConfiguratorAvailableError` naming the required
tag(s)/flavor and the closest-rejected reason.

## Validation rules (from spec)

- FR-001/FR-008: `flavor ∈ {standard, dedicated-cpu}` enforced by CRD enum (admission).
- FR-002: omitted → `standard` (CRD default).
- FR-003/FR-004: resolved configurator always belongs to the mapped family (tag filter before fit sort).
- FR-005: empty family-survivor set → clear error, no cross-family substitution.
- FR-006: field absent on master sizing.
- FR-007: existing pools keep locked configurator; no re-resolution.

## Relationships

```
KubernetesClusterNodepool.spec.forProvider.resources.flavor
        │  (controller maps flavor → tag)
        ▼
resolveK8sConfigurator(... RequireTags:[tag] ...)
        │
        ▼
resolver.SelectConfigurator → ConfiguratorEntry (tag-matched, sized) → configurator_id
        │
        ▼
upstream node-group create  (configuration.configurator_id)
```
