# Contract — `resources` on KubernetesCluster / KubernetesClusterNodepool

Custom sizing as an alternative to `presetName` on the managed-Kubernetes kinds. CEL: exactly one of `{presetName, resources}` per kind.

## KubernetesCluster.forProvider.resources

| Field | Type | Req |
|---|---|---|
| `cpu` | int (cores) | ✓ |
| `ramGB` | int (GB) | ✓ |
| `diskGB` | int (GB) | ✓ |

→ emits upstream `ClusterIn.configuration { configurator_id, cpu, ram, disk }` (ram/disk converted to upstream MB units). `configurator_id` resolved via the server configurator catalog (R-5; probe whether K8s needs a distinct catalog). `status.atProvider.lockedConfiguratorID` recorded.

## KubernetesClusterNodepool.forProvider.resources

Same as cluster plus optional `gpu` (int) → emits `NodeGroupIn.configuration { configurator_id, cpu, ram, disk, gpu }`.

## Behavior

- Resolution + `NoConfiguratorAvailable` + `SizingSwitchRequiresRecreate` semantics identical to the Server contract. Presets remain supported (additive). Removes the ambiguous-preset-slug pain (feature-004 e2e blocker).

## Example

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata: { name: prod, namespace: team-a }
spec:
  forProvider:
    name: prod
    k8sVersion: "v1.34.7+k0s.0"
    networkDriver: cilium
    availabilityZone: msk-1
    resources: { cpu: 4, ramGB: 8, diskGB: 80 }   # no presetName — no ambiguous slug
  writeConnectionSecretToRef: { name: prod-kubeconfig }
  managementPolicies: ["*"]
---
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata: { name: prod-workers, namespace: team-a }
spec:
  forProvider:
    name: workers
    clusterRef: { name: prod }
    resources: { cpu: 4, ramGB: 16, diskGB: 120 }
    nodeCount: 2
  managementPolicies: ["*"]
```
