# Contract — `KubernetesCluster` (kubernetes.m.timeweb.crossplane.io/v1alpha1)

Operator-facing contract for the managed Kubernetes control plane. v0.x = fixed master preset, exact k8s version, kubeconfig connection Secret, in-place version upgrade.

## spec.forProvider

| Field | Type | Req | Notes |
|---|---|---|---|
| `name` | string | ✓ | 1..255 |
| `k8sVersion` | string | ✓ | exact upstream version (e.g. `1.31.2`); resolved 1:1 against `/k8s/k8s-versions` |
| `networkDriver` | enum | ✓ | `kuberouter` \| `calico` \| `flannel` \| `cilium` (CRD enum) |
| `availabilityZone` | enum | ✓ | `spb-3` \| `msk-1` \| `ams-1` \| `fra-1` (CRD enum; sold-out not hard-blocked) |
| `presetName` | string | ✓ | master preset slug → `preset_id` |
| `description` | string | – | PATCH-mutable |
| `masterNodesCount` | int | – | default `1`; `3` for HA |
| `networkRef` / `networkSelector` / `networkID` | ref/sel/string | – | at-most-one (CEL); VPC attach via feat-003 `Network` |
| `projectRef` / `projectSelector` / `projectID` | ref/sel/int | – | at-most-one (CEL); default project if all unset |

## status.atProvider

`upstreamID` (cluster id), `state`, `k8sVersion` (observed), `lockedPresetID`, `resolvedNetworkID`, `resolvedProjectID`, `cpu`, `ram`, `disk`.

## Conditions

- `Ready=True` when upstream `status` is active; `Ready=False` `reason ∈ {Provisioning, Upgrading, PaymentRequired, Deleting}`.
- `Synced=False` `reason ∈ {ReconcileError (unknown preset/version, unready/missing dep, upstream cluster/network incompatibility — no client-side pre-check per FR-017), ImmutableFieldChange}`.

## Mutability

- **Upgrade-mutable**: `k8sVersion` (forward-only; fires `PATCH …/versions/update`; transient `Ready=False, reason=Upgrading`). Downgrade/non-catalog → `ReconcileError`.
- **PATCH-mutable**: `name`, `description` (`PATCH …/{id}`, `ClusterEdit`).
- **Immutable** (controller-side `RejectImmutableChange`): `networkDriver`, `availabilityZone`, `presetName`, `masterNodesCount`, network/project bindings.

## Connection Secret

`writeConnectionSecretToRef` → key `kubeconfig` (verbatim YAML from `GET …/kubeconfig`), published within one reconcile of `Ready=True`. Credential — never logged.

## Example

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata:
  name: prod
  namespace: team-a
spec:
  forProvider:
    name: prod
    k8sVersion: "1.31.2"
    networkDriver: cilium
    availabilityZone: msk-1
    presetName: <smallest-master-slug>
    masterNodesCount: 1
    projectRef: { name: team-a-project }
  writeConnectionSecretToRef: { name: prod-kubeconfig }
  managementPolicies: ["*"]
```
