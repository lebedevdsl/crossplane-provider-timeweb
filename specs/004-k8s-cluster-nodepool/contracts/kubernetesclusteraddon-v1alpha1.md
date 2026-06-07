# Contract — `KubernetesClusterAddon` (kubernetes.m.timeweb.crossplane.io/v1alpha1)

One installed cluster addon. Lowest-priority slice (US5, P3). Shape matches spec FR-014 (install needs `type`+`config_type`+`yaml_config`+`version`); see research R-9.

## spec.forProvider

| Field | Type | Req | Notes |
|---|---|---|---|
| `clusterRef` / `clusterSelector` / `clusterID` | ref/sel/string | ✓¹ | at-most-one (CEL); at-least-one required |
| `type` | string | ✓ | addon identifier (spec's "name"); validated against `…/addons-configs` |
| `version` | string | ✓ | validated against the catalog |
| `yamlConfig` | string | – | override; defaults to catalog `yaml_config` |
| `configType` | string | – | defaults sensibly |

¹ exactly one of the cluster-ref trio.

## status.atProvider

`addonID` (upstream addon id, = external-name), `clusterID` (resolved parent, persisted), `status`.

## Behavior

- **Install (Create)**: `POST …/addons {type, config_type, yaml_config, version}` after parent cluster `Ready=True`.
- **Observe**: `GET …/addons`, match by `type`; `Ready=True` when upstream addon `status` = installed; `reason ∈ {Installing, Deleting}`.
- **Delete**: `DELETE …/addons/{addon_id}` (404-idempotent); cluster unaffected.
- **Validation**: unknown `type`/`version` → `Synced=False, reason=ReconcileError` listing valid addon types for the cluster.
- **Immutable**: `type`, `version`, cluster-ref trio (recreate to change). `yamlConfig` mutability deferred (no per-addon PATCH in v0.x).

## Example

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterAddon
metadata: { name: ingress, namespace: team-a }
spec:
  forProvider:
    clusterRef: { name: prod }
    type: <addon-type-from-catalog>
    version: <addon-version>
  managementPolicies: ["*"]
```
