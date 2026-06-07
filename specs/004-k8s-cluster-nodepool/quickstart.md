# Quickstart — Managed Kubernetes (Cluster + Nodepool + Addon)

Operator walkthrough for feature 004. Assumes the provider is installed and a `ProviderConfig` named `default` resolves to a valid Timeweb token (per feature 001/002). All location/AZ codes are **API values** (`msk-1`, `spb-3`, `ams-1`, `fra-1`), never dashboard labels.

## 1. Minimum cluster + worker pool (US1, MVP)

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata: { name: demo, namespace: default }
spec:
  forProvider:
    name: demo
    k8sVersion: "1.31.2"        # must be an exact entry from /api/v1/k8s/k8s-versions
    networkDriver: cilium
    availabilityZone: msk-1
    presetName: <smallest-master-slug>
  writeConnectionSecretToRef: { name: demo-kubeconfig }
  managementPolicies: ["*"]
---
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata: { name: demo-workers, namespace: default }
spec:
  forProvider:
    name: workers
    clusterRef: { name: demo }
    presetName: <smallest-worker-slug>
    nodeCount: 2
  managementPolicies: ["*"]
```

```bash
kubectl apply -f cluster.yaml
kubectl wait --for=condition=Ready kubernetescluster/demo --timeout=20m
kubectl wait --for=condition=Ready kubernetesclusternodepool/demo-workers --timeout=15m

# kubeconfig lands in the connection Secret:
kubectl get secret demo-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > demo.kubeconfig
kubectl --kubeconfig demo.kubeconfig get nodes        # lists the 2 workers
```

The Nodepool waits for the cluster to be `Ready=True` before creating the worker group (you'll see `Synced=False, reason=Reconciling` naming the cluster until then).

## 2. Scale the worker pool (US2)

```bash
kubectl patch kubernetesclusternodepool/demo-workers --type merge \
  -p '{"spec":{"forProvider":{"nodeCount":4}}}'
# controller adds 2 nodes; observedNodeCount converges to 4; the group is NOT recreated
kubectl get kubernetesclusternodepool/demo-workers -o jsonpath='{.status.atProvider.observedNodeCount}'
```

Autoscaling instead of manual scaling:

```yaml
    autoscaling: { enabled: true, minSize: 2, maxSize: 6 }   # minSize >= 2
    autohealing: true
```

When autoscaling is on, the controller stops reconciling `nodeCount` (the upstream autoscaler owns the count).

## 3. Attach to a private network + project (US3)

```yaml
    networkRef: { name: shared-vpc }     # a feature-003 Network MR, Ready=True
    projectRef: { name: team-a-project } # a feature-001 Project MR
```

The cluster's `availabilityZone` must be compatible with the referenced VPC's region — if it isn't, the upstream API rejects the create and the cluster surfaces `Synced=False, reason=ReconcileError` with the upstream message (the controller does no client-side region pre-check). Use `networkID: <vpc-id>` to attach an externally-managed VPC without a `Network` MR.

## 4. Upgrade the Kubernetes version (US4)

```bash
kubectl patch kubernetescluster/demo --type merge \
  -p '{"spec":{"forProvider":{"k8sVersion":"1.32.0"}}}'   # newer, catalog-valid, forward-only
# Ready=False reason=Upgrading during the transition, then back to Ready=True
```

A downgrade or non-catalog version is rejected (`Synced=False, reason=ReconcileError`) with the valid list — no upstream call is made.

## 5. Install an addon (US5)

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterAddon
metadata: { name: demo-ingress, namespace: default }
spec:
  forProvider:
    clusterRef: { name: demo }
    type: <addon-type-from-addons-configs>
    version: <addon-version>
  managementPolicies: ["*"]
```

Delete the Addon MR to remove the addon; the cluster stays `Ready=True`.

## Troubleshooting matrix

| Symptom | Likely cause | Fix |
|---|---|---|
| Cluster `Synced=False, reason=ReconcileError`, message lists versions | `k8sVersion` not an exact catalog entry | copy a value from `/api/v1/k8s/k8s-versions` |
| Cluster `Synced=False`, message lists slugs | `presetName` not a valid master slug | use a slug from the error / `/api/v1/presets/k8s` (`type=master`) |
| Cluster `Ready=False, reason=PaymentRequired` | account `no_paid` (billing/quota) | fund the account; not a controller bug |
| Nodepool stuck `Synced=False, reason=Reconciling` | parent cluster not `Ready=True` yet | wait for the cluster; check `clusterRef.name` |
| Nodepool `nodeCount` change ignored | `autoscaling.enabled` is true | autoscaler owns count; disable autoscaling to scale manually |
| Cluster edit rejected `ImmutableFieldChange` | changed `networkDriver`/`availabilityZone`/`presetName`/`masterNodesCount` | delete + recreate; only `k8sVersion`/`name`/`description` are mutable |
| Cluster `ReconcileError` on create with a `networkRef`/`networkID` | upstream rejected the cluster-AZ / VPC-region pairing | pick an `availabilityZone` compatible with the VPC's region (per the upstream message) |

## What's NOT in v0.x

Custom-configurator sizing (`cpu`/`ram`/`disk`), `is_ingress`/`is_k8s_dashboard` toggles, OIDC provider, maintenance windows, custom pod/service CIDR, per-addon config PATCH, nodepool label/autoscaling mutation in place. Each is a follow-up feature extending `kubernetes.m.timeweb.crossplane.io`.
