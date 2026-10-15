# Data model: S3Bucket storage classes (feature 028)

## Spec changes (additive)

### `spec.forProvider.storageClass` (existing, required)

| | before | after |
|---|---|---|
| enum | `hot`, `cold` | `hot`, `standard`, `cold` |
| doc | hot = frequently-accessed default, cold = archives | `hot` = panel "Premium"; `standard` = panel "Standard" (recommended default); `cold` = pay-as-you-go, 1 GB only. Create-time choice; day-2 a mismatch with upstream refuses updates (see conditions). |
| default | none | none (unchanged) |

### `spec.forProvider.initialSizeGB` (existing, unchanged enum `1;10;100;250`)

Doc clarified: mutable **within the same class** (re-resolved + PATCHed);
`cold` only has the 1 GB tier.

## Status changes

### `status.atProvider.storageClass` (existing)

Doc: upstream-reported class (`hot` / `standard` / `cold`); may differ from
spec after a vendor reclassification — see `StorageClassDrift`.

### `status.atProvider.lockedPresetID` (existing)

Semantics narrowed in docs: historical snapshot of the last preset the
provider chose (create or resize). May name a preset no longer in the
catalog; never consulted for decisions (R-1).

### `status.conditions` — new type `StorageClassDrift`

| Status | Reason | When | Event |
|---|---|---|---|
| `True` | `UpstreamReclassified` | Observe: upstream class non-empty and ≠ `spec.storageClass` | Warning on transition |
| `False` | `Aligned` | Observe: classes match AND the condition was previously present | Normal on transition |
| absent | — | bucket never drifted | — |

Message (True): `declared "<spec>", upstream reports "<observed>"; set
spec.forProvider.storageClass to "<observed>" to align (no automatic
re-tiering — a class change is a paid move)`.

`Ready` and `Synced` are not affected by this condition.

### `Synced=False` — new reason `StorageClassMismatch`

Set by Update (fail-closed) when `spec.storageClass` ≠ upstream class. Runtime
shows `reason=ReconcileError` on the condition (message preserved); the Event
carries `StorageClassMismatch`.

## Update decision table (after the immutable-`name` check)

| spec.class vs upstream | spec.sizeGB vs upstream (KB/1048576) | action |
|---|---|---|
| ≠ | any | refuse: `StorageClassMismatch`, no request |
| = | ≠ | PATCH with `preset_id` = resolve(size, class, location?); refresh lock |
| = | = | PATCH without `preset_id` (`bucket_type`, `description` only) |
| = | upstream size unknown (`is_unlimited` / 0) | PATCH without `preset_id` |
| upstream class empty | — | treated as "=" (no mismatch derivable) |

`isUpToDate` is unchanged (type, description, projectID) → Update runs only
when one of those differs; a drifted-but-untouched bucket never reaches the
refusal.

## Catalog (resolver `PresetEntry`, 2026-10-15 fixture)

| UpstreamID | DiskGB | Location | StorageClass |
|---|---|---|---|
| 5127 | 1 | ru-1 | hot |
| 5135 | 1 | ru-1 | standard |
| 5143 | 1 | ru-1 | cold |
| 5129 | 10 | ru-1 | hot |
| 5137 | 10 | ru-1 | standard |
| 5131 | 100 | ru-1 | hot |
| 5139 | 100 | ru-1 | standard |
| 5133 | 250 | ru-1 | hot |
| 5141 | 250 | ru-1 | standard |

`DiskGB` = upstream `disk` (MB) / 1024 — unchanged.
