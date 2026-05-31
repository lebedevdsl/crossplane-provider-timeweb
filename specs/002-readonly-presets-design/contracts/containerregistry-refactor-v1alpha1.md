# Contract: `ContainerRegistry` v1alpha1 — refactored shape

**Group/Version**: `containerregistry.m.timeweb.crossplane.io/v1alpha1` | **Kind**: `ContainerRegistry` | **Scope**: Namespaced | **Status**: refactor (supersedes the MVP shape in [`001-mvp-scaffolding/contracts/containerregistry-v1alpha1.md`](../../001-mvp-scaffolding/contracts/containerregistry-v1alpha1.md))

Spec linkage: FR-004, FR-006, FR-009, FR-010, FR-013.

**Catalog-endpoint constraint** (spec.md §Clarifications 2026-05-31 "catalog-endpoint reality check"): Timeweb has no `/api/v1/configurator/registries` endpoint. Container Registry is platform-managed — operators have no hypervisor / DC-level placement choice. The only custom dimension is **disk size in GB**. The XOR shape is preserved (`presetName` vs `resources`); the `resources` block collapses to a single `diskGB` field. There is no configurator resolution path on this MR, no `lockedConfiguratorID`, and the `NoConfiguratorAvailable` condition is N/A here (it stays in the shared library for Server / KubernetesCluster / KubernetesNodeGroup MRs that DO have configurators).

## Operator-facing manifest

### Variant A — `resources` (custom disk size)

```yaml
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: my-registry
  namespace: team-a
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: my-registry
    resources:
      diskGB: 5
```

### Variant B — `presetName` (named tariff)

```yaml
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: my-registry
  namespace: team-a
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: my-registry
    presetName: start-ru-1
```

**Rejected at admission**: setting both `presetName` and `resources`. Setting neither.

## Spec schema (operator-facing)

```text
forProvider:
  name:        <string, required, MVP-style upstream name>
  presetName:  <string, optional, mutex with resources>
  resources:   <object, optional, mutex with presetName>
    diskGB:    <int64, required when resources is set; must be > 0>
```

(`name` and any other MVP-era operator-facing fields are unchanged from `001-mvp-scaffolding`.)

## Validation

CEL `x-kubernetes-validations` on `forProvider`:

- **XOR**: `(has(self.presetName) ? 1 : 0) + (has(self.resources) ? 1 : 0) == 1` — exactly one of `presetName` or `resources` MUST be set.
- **No raw IDs**: `!has(self.preset_id) && !has(self.configurator_id)` — defensive; the schema does not declare these fields, but the rule documents intent.

CEL on `forProvider.resources` (when present):

- `self.diskGB > 0`.

## Status (`status.atProvider`)

Mirrors the MVP fields plus the locked-sizing discriminator:

```text
atProvider:
  upstreamID:      <int64, set on Create>
  url:             <string>            # registry endpoint
  lockedPresetID:  <int64, optional>   # populated iff the presetName variant was used
  lockedResources:                     # populated iff the resources variant was used
    diskGB: <int64>
```

Invariant: exactly one of `lockedPresetID` OR `lockedResources` is populated after first successful Create. The discriminator drives FR-009 sizing-lock logic on subsequent reconciles. There is no `lockedConfiguratorID` — it doesn't apply to Container Registry.

## Conditions

Standard `Ready` / `Synced` plus:

| Reason                       | Trigger                                                                                                                  | Recoverable by operator                                          |
|------------------------------|--------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------|
| `PresetNotFound`             | Operator-supplied `presetName` did not match any upstream entry (after cache refetch).                                   | Fix the slug; reconcile resumes.                                 |
| `PresetAmbiguous`            | Operator-supplied `presetName` matched multiple upstream entries (rare).                                                 | Use the explicit `<short>-<location>-<id>` disambiguator form.   |
| `SizingSwitchRequiresRecreate` | Operator changed which side of the XOR (`presetName` ↔ `resources`) is populated on an existing, locked MR (FR-010).   | `kubectl delete` and reapply.                                    |
| `CatalogUnauthorized`        | The MR's PC token does not have permission to call the relevant catalog endpoint.                                        | Fix credentials; reconcile resumes after cache refetch succeeds. |

(`NoConfiguratorAvailable` is intentionally absent — Container Registry has no configurator step.)

## Lifecycle

1. **Create**: controller resolves the sizing block. If `presetName`, slugify-match against `GetRegistryPresets` to get `preset_id` and call Create with `{preset_id}`; record `lockedPresetID`. If `resources`, call Create with `{disk}` directly (no preset, no configurator); record `lockedResources.diskGB`. Set `status.atProvider.upstreamID`.
2. **Observe**: read-only call to Timeweb; reports `ResourceExists` / `ResourceUpToDate` against the locked sizing variant.
3. **Update (within-variant)**: the operator changes a value within the same side of the XOR (e.g. `presetName: start-ru-1` → `presetName: pro-ru-1`, or `resources.diskGB: 5` → `resources.diskGB: 10`). The controller PATCHes upstream.
4. **Update (cross-variant)**: rejected as `Synced=False, reason=SizingSwitchRequiresRecreate`; upstream is not modified.
5. **Delete**: standard Crossplane managed-resource deletion.

## Relationships

- `spec.providerConfigRef.{kind,name}` — `kind` is `ProviderConfig` or `ClusterProviderConfig` (runtime default `ClusterProviderConfig`); controller hard-switches on `kind` with no silent fallback (FR-001, post upstream-alignment clarification).
- `forProvider.projectRef` — references a `Project` MR (unchanged from MVP).

## Printer columns

| Column      | Source                                                                                       |
|-------------|----------------------------------------------------------------------------------------------|
| `READY`     | `Ready` condition                                                                            |
| `SYNCED`    | `Synced` condition                                                                           |
| `SIZING`    | `presetName` if `status.atProvider.lockedPresetID` set, else `resources`                     |
| `DISK-GB`   | `forProvider.resources.diskGB` (or `status.atProvider.lockedResources.diskGB` after lock)    |
| `AGE`       | `metadata.creationTimestamp`                                                                 |
