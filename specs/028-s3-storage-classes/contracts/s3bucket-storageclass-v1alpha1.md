# Contract: `S3Bucket` v1alpha1 storage-class surface (feature 028)

Additive, NON-BREAKING. Group `objectstorage.m.timeweb.crossplane.io`.

## Admission

- `spec.forProvider.storageClass` ∈ {`hot`, `standard`, `cold`} (was
  {`hot`, `cold`}). Required, no default.
- `spec.forProvider.initialSizeGB` ∈ {1, 10, 100, 250} — unchanged.
- No CEL rule ties class to size (the catalog, not the schema, decides; a
  `cold`/10 manifest is accepted and fails at resolve time with the
  class-bearing hint).

## Create

- Resolves `(initialSizeGB, location?, storageClass)` → one `preset_id`
  (2026-10-15: `standard`/10 → 5137). Body: `{name, type, preset_id,
  description?, project_id?}` — unchanged shape.
- Vendor `409 … sold out` → transient; condition message carries the vendor
  text; no upstream object; capped retries.
- No match → `Synced=False` (`PresetNotFound` message; runtime reason
  `ReconcileError`) listing `<GB>/<loc>/<class>` combos.

## Observe

- `status.atProvider.storageClass` mirrors upstream (`hot`/`standard`/`cold`).
- Condition `StorageClassDrift`:
  `True/UpstreamReclassified` when upstream class ≠ spec; `False/Aligned`
  once re-aligned (only if previously present); absent otherwise. Warning /
  Normal Events on transitions. `Ready`/`Synced` unaffected.
- `ResourceUpToDate` ignores the class (unchanged: type, description,
  projectID).

## Update (fail-closed on class mismatch)

1. `name` differs → `ImmutableFieldChange` (unchanged).
2. upstream class non-empty and ≠ spec → **refused**: no request,
   `Synced=False` message
   `storage class mismatch: spec declares "<spec>" but upstream reports
   "<observed>"; set spec.forProvider.storageClass to "<observed>" (cross-class
   moves are not supported day-2)`, Warning Event `StorageClassMismatch`.
3. classes equal, upstream size (KB/1048576) ≠ `initialSizeGB` → PATCH with
   `preset_id` (re-resolved), `lockedPresetID` refreshed.
4. classes equal, sizes equal (or size unknown) → PATCH **without**
   `preset_id`; `lockedPresetID` untouched.

## Out of scope

Cross-class moves, automatic re-tiering, any vendor transfer/assignment API.
