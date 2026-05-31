# Contract: `S3Bucket` v1alpha1 — refactored shape

**Group/Version**: `objectstorage.m.timeweb.crossplane.io/v1alpha1` | **Kind**: `S3Bucket` | **Scope**: Namespaced | **Status**: refactor (supersedes the MVP shape in [`001-mvp-scaffolding/contracts/s3bucket-v1alpha1.md`](../../001-mvp-scaffolding/contracts/s3bucket-v1alpha1.md))

Spec linkage: FR-004, FR-006, FR-009, FR-010, FR-013.

**Catalog-endpoint constraint** (spec.md §Clarifications 2026-05-31 "catalog-endpoint reality check"): Timeweb has no `/api/v1/configurator/storages` endpoint. S3 Storage is platform-managed — no operator-visible hypervisor / location placement. The only custom dimension is **disk size in MB** (upstream uses MB; the controller stores it as MB end-to-end). `storageClass` is a separate MR-level enum that lives **outside** the XOR sizing block — it's a bucket-policy choice, not a sizing input. No configurator resolution, no `lockedConfiguratorID`, no `NoConfiguratorAvailable` condition on this MR.

## Operator-facing manifest

### Variant A — `resources` (custom disk size)

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: artifacts
  namespace: team-a
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: artifacts
    storageClass: hot           # outside the XOR block
    resources:
      diskMB: 30720             # 30 GB (upstream unit is MB)
```

### Variant B — `presetName`

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: artifacts
  namespace: team-a
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: artifacts
    storageClass: hot           # still required — bucket policy, not sizing
    presetName: standard-ru-1
```

## Spec schema (operator-facing)

```text
forProvider:
  name:         <string, required>
  storageClass: <string, required, enum "hot" | "cold">   # MR-level field, NOT inside XOR
  presetName:   <string, optional, mutex with resources>
  resources:    <object, optional, mutex with presetName>
    diskMB:     <int64, required when resources is set; must be > 0>
```

## Validation

CEL on `forProvider`:

- **XOR**: `(has(self.presetName) ? 1 : 0) + (has(self.resources) ? 1 : 0) == 1`.
- `self.storageClass in ['hot','cold']` (static enum; Timeweb's stable bucket-class set).

CEL on `forProvider.resources` (when present):

- `self.diskMB > 0`.

## Status (`status.atProvider`)

```text
atProvider:
  upstreamID:      <int64, set on Create>
  endpoint:        <string>            # bucket endpoint URL
  lockedPresetID:  <int64, optional>   # iff presetName variant was used
  lockedResources:                     # iff resources variant was used
    diskMB: <int64>
```

Invariant identical to `ContainerRegistry`: exactly one of `lockedPresetID` OR `lockedResources` populated after first successful Create. No `lockedConfiguratorID`. `storageClass` is mutable post-create (operator can flip hot↔cold without recreate) and isn't tracked in the lock.

## Conditions

Identical reason vocabulary to `ContainerRegistry` (also without `NoConfiguratorAvailable`).

## Lifecycle

Identical to `ContainerRegistry` (Create → Observe → within-variant Update or SizingSwitchRequiresRecreate → Delete). `storageClass` changes flow through plain PATCH on Update and never trigger the sizing-lock condition.

## Relationships

- `spec.providerConfigRef.{kind,name}` — `kind` is `ProviderConfig` or `ClusterProviderConfig` (runtime default `ClusterProviderConfig`); controller hard-switches on `kind` with no silent fallback (FR-001, post upstream-alignment clarification).
- `forProvider.projectRef` — references a `Project` MR (unchanged from MVP).

## Printer columns

| Column          | Source                                                                                       |
|-----------------|----------------------------------------------------------------------------------------------|
| `READY`         | `Ready` condition                                                                            |
| `SYNCED`        | `Synced` condition                                                                           |
| `SIZING`        | `presetName` if `status.atProvider.lockedPresetID` set, else `resources`                     |
| `DISK-MB`       | `forProvider.resources.diskMB` (or `status.atProvider.lockedResources.diskMB` after lock)    |
| `STORAGE-CLASS` | `forProvider.storageClass`                                                                   |
| `AGE`           | `metadata.creationTimestamp`                                                                 |
