# Contract: `KubernetesClusterNodepool.resources.flavor` (v1alpha1)

## Field

```yaml
spec:
  forProvider:
    resources:
      cpu: <int>
      ramGB: <int>
      diskGB: <int>
      gpu: <int>            # optional, existing
      flavor: standard      # NEW — enum: standard | dedicated-cpu ; default: standard
```

- **Type**: string enum, values `standard` | `dedicated-cpu`.
- **Required**: no. **Default**: `standard`.
- **Applies to**: custom-sized (`resources`) worker pools only. No effect on preset-sized pools.
- **Not present on**: `KubernetesCluster.resources` (master) — master has a single family.

## Admission

- Unknown values rejected at admission (enum). No reconcile-time validation of the value itself.
- Backward compatible: manifests omitting `flavor` remain valid and behave as `standard`.

## Behavior contract

| Given | When | Then |
|-------|------|------|
| `resources` without `flavor` | create | resolves a `k8s_configurator_general` worker configurator |
| `flavor: standard` | create | resolves from the general family |
| `flavor: dedicated-cpu`, flavor-valid sizing | create | resolves from the dedicated-cpu family |
| any `flavor`, sizing unsatisfiable in that family | create | `Synced=False`, error names the flavor + unmet constraint; NO other family substituted |
| `flavor` set on a preset-sized pool | create | no effect (preset fixes the family) |
| existing pool (pre-feature), reconcile | observe/update | keeps its locked configurator; not re-resolved |

## Conditions

- Success: standard MR conditions (`Synced=True`, `Ready` gated on node state as today).
- Family-unsatisfiable: `Synced=False`, reason consistent with existing configurator-resolution
  failures (`NoConfiguratorAvailable`-class), message naming the flavor/family and the
  closest-rejected sizing constraint.

## Generated-artifact obligation

Changing `apis/kubernetes/v1alpha1` requires regenerating and committing CRD YAML +
`zz_generated.deepcopy.go` in the same PR (Constitution I).
