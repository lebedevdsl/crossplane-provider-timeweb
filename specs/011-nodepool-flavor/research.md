# Phase 0 Research: Nodepool Worker Configurator Flavor

All "unknowns" were resolved by live debugging on `twc-staging` (2026-06-23); no research
agents were dispatched. Findings below are the decisions feeding Phase 1.

## R-1 — Flavor → catalog-tag mapping (live-verified)

**Decision**: `standard → k8s_configurator_general`, `dedicated-cpu → k8s_configurator_dedicated_cpu`.

**Rationale**: The undocumented `/api/v1/configurator/k8s` catalog tag-partitions worker
configurators. Live `ru-3` entries:
- `id=59` tags=`[k8s_configurator_general]` cpu 2–32, ram 2048–262144, disk 40960–1228800
- `id=69` tags=`[k8s_configurator_dedicated_cpu]` cpu 1–32, ram 4096–131072, disk 30720–1228800

`general` is the cloud panel's default tab ("Premium NVMe") and accepts low RAM-per-CPU
(2cpu/2GB verified creating live). `dedicated_cpu` enforces a hidden ≈4 GB/cpu floor not
present in its absolute ram range, which is what produced
`400 invalid_configuration_ram: For 2 cpu min ram should be: 8192`.

**Alternatives considered**: exposing raw tag strings in the CRD (rejected — leaks upstream
internals, brittle); a numeric "cpu class" (rejected — opaque). A friendly two-value enum is
clearest.

## R-2 — Where to filter by family

**Decision**: Add `RequireTags []string` to `ConfiguratorInput`; add a tag-filter step in
`SelectConfigurator` **after** the capability (sizing) filter and **before** the
standard/promo partition and the tightest-fit sort. An entry survives only if its `Tags`
contains every tag in `RequireTags`.

**Rationale**: `ConfiguratorEntry` already carries `Tags` (populated in `configuratorEntries`).
Keeping the resolver generic (tag-based, not k8s-aware) means the kubernetes controller owns
the `flavor → tag` mapping. Filtering before the fit sort is what actually fixes the bug — the
tightest-fit comparator never sees the other family, so it can't grab it.

**Alternatives considered**:
- Bake the family into `ConfiguratorEntry.Filters` and reuse `matchFilters` (rejected —
  `configuratorEntries` is shared with the server catalog whose tags differ; deriving a
  synthetic `family` filter there is awkward and couples the shared normalizer to k8s tags).
- Change the tightest-fit comparator to prefer `general` (rejected — implicit, doesn't let the
  operator choose dedicated, and the spec only takes cpu/ram/disk).

## R-3 — Default value & backward compatibility

**Decision**: default `standard` via a kubebuilder default on the field; existing/omitted →
`standard`. Already-provisioned pools keep their **locked** configurator (`status.atProvider`)
and are not re-resolved.

**Rationale**: `standard`/general matches the panel default and the broadest sizing envelope,
so the common small-pool case succeeds (SC-001). Configurator resolution happens at Create and
the result is locked; flavor therefore has no effect on existing pools (FR-007/SC-005), so the
new default cannot disrupt them even though tightest-fit may previously have locked a dedicated
id.

**Alternatives considered**: no default / required field (rejected — breaks existing manifests,
worse ergonomics). Controller-side empty→standard fallback (viable, but a CRD default makes the
effective value explicit in the object and is the idiomatic Crossplane choice).

## R-4 — Field placement (nodepool only)

**Decision**: `flavor` lives on the nodepool worker `Resources` only. The cluster master
`Resources` gets no flavor field.

**Rationale**: the master catalog has a single family (`k8s_master_configurator`); there is no
choice to expose (FR-006). Adding it there would be dead surface.

## R-5 — Error surface when sizing is invalid for the chosen family

**Decision**: when no entry carrying the required tag survives the capability filter, return the
existing `NoConfiguratorAvailableError` with the flavor/tag and the closest-rejected reason
surfaced; never substitute another family (FR-005).

**Rationale**: reuses the resolver's existing closest-rejected machinery; turns a misleading
upstream `invalid_configuration_ram` into a diagnosable provider-side condition that names the
flavor.

## R-6 — CRD validation

**Decision**: `+kubebuilder:validation:Enum=standard;dedicated-cpu`,
`+kubebuilder:default=standard`, `+optional`. No CEL needed (plain enum + default; no
cross-field rule, so no CEL cost-budget exposure).

**Rationale**: enum admission rejects unknown values before reconcile (FR-008) with zero CEL
cost-budget risk. Regenerate CRD YAML + DeepCopy in the same PR (Principle I).
