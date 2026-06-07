# Contract — `KubernetesClusterNodepool` (kubernetes.m.timeweb.crossplane.io/v1alpha1)

One upstream worker group. Scalable via mutable `nodeCount` (relative add/remove deltas) or upstream autoscaling.

## spec.forProvider

| Field | Type | Req | Notes |
|---|---|---|---|
| `name` | string | ✓ | group name |
| `presetName` | string | ✓ | worker preset slug → `preset_id` |
| `nodeCount` | int | ✓ | 1..100; mutable (ignored when autoscaling on) |
| `clusterRef` / `clusterSelector` / `clusterID` | ref/sel/string | ✓¹ | at-most-one (CEL); at-least-one required |
| `labels` | map[string]string | – | → upstream `array<{key,value}>` |
| `autoscaling` | object | – | `{enabled, minSize>=2, maxSize>=minSize}` |
| `autohealing` | bool | – | auto-recover failed nodes |

¹ exactly one of the cluster-ref trio.

## status.atProvider

`upstreamID` (group id), `clusterID` (resolved parent, persisted for Observe/Delete), `observedNodeCount`, `lockedPresetID`.

## Behavior

- **Create gate**: blocked until parent `KubernetesCluster` is `Ready=True` → `Synced=False, reason=Reconciling` naming the dependency (`ErrTargetNotReady`).
- **Scaling (Update)**: `delta = nodeCount − observedNodeCount`; `POST …/nodes {count:delta}` (delta>0) or `DELETE …/nodes {count:-delta}` (delta<0); no-op at 0. Re-observes each reconcile → idempotent convergence, group never recreated. Skipped entirely when `autoscaling.enabled`.
- **Ready**: `observedNodeCount == nodeCount` (autoscaling off) or group healthy (on). `reason ∈ {Provisioning, Scaling, Deleting}`.

## Mutability

- **Mutable**: `nodeCount` (scaling).
- **Immutable** (`RejectImmutableChange`): `presetName`, cluster-ref trio, `labels`, `autoscaling`, `autohealing` (upstream group has no PATCH; recreate to change).

## Example

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata: { name: workers, namespace: team-a }
spec:
  forProvider:
    name: workers
    clusterRef: { name: prod }
    presetName: <smallest-worker-slug>
    nodeCount: 2
    labels: { pool: general }
    # autoscaling: { enabled: true, minSize: 2, maxSize: 6 }
    # autohealing: true
  managementPolicies: ["*"]
```
