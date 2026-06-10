# Quickstart — Custom sizing + ContainerRegistry group move

Feature 005. Assumes the provider is installed with a working `ProviderConfig`. Location/AZ codes are API values (`ru-1`, `msk-1`, …).

## 1. Size a Server by CPU/RAM/disk (no preset)

```yaml
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata: { name: app, namespace: default }
spec:
  forProvider:
    name: app
    location: ru-1
    os: { image: ubuntu, version: "24.04" }
    resources: { cpu: 4, ramGB: 8, diskGB: 80 }   # instead of presetName
    sshKeyRefs: [{ name: my-key }]
  writeConnectionSecretToRef: { name: app-conn }
  managementPolicies: ["*"]
```

The controller picks the tightest-fit configurator in `ru-1` satisfying 4 cores / 8 GB / 80 GB and records `status.atProvider.lockedConfiguratorID`. Presets still work — set `presetName` instead of `resources` (never both; admission rejects both).

Optional axes: `diskType`, `bandwidthMbps`, `gpu`, `cpuFrequencyTier`, `enableLocalNetwork`. An unsatisfiable request → `Synced=False, reason=NoConfiguratorAvailable` naming the unmet axis.

## 2. Size a Kubernetes cluster + nodepool by CPU/RAM/disk

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata: { name: prod, namespace: default }
spec:
  forProvider:
    name: prod
    k8sVersion: "v1.34.7+k0s.0"     # exact catalog string (v… + k0s build suffix)
    networkDriver: cilium
    availabilityZone: msk-1
    resources: { cpu: 4, ramGB: 8, diskGB: 80 }   # no ambiguous preset slug
  writeConnectionSecretToRef: { name: prod-kubeconfig }
  managementPolicies: ["*"]
---
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata: { name: prod-workers, namespace: default }
spec:
  forProvider:
    name: workers
    clusterRef: { name: prod }
    resources: { cpu: 4, ramGB: 16, diskGB: 120 }
    nodeCount: 2
  managementPolicies: ["*"]
```

No more `k8s-promo-1-rub-1999`-style slugs — type the resources you want.

## 3. ContainerRegistry now lives in the kubernetes group

The group changed (breaking). Re-apply registry manifests under the new apiVersion:

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1   # was containerregistry.m.timeweb.crossplane.io
kind: ContainerRegistry
metadata: { name: my-registry, namespace: default }
spec:
  forProvider:
    name: my-registry
    initialSizeGB: 5
  managementPolicies: ["*"]
```

Behavior is unchanged — only the group/apiVersion. The old `containerregistry.m.timeweb.crossplane.io` group is removed.

## Switching sizing variant

Flipping a live resource between `presetName` and `resources` is rejected with
`Synced=False, reason=SizingSwitchRequiresRecreate` — delete and recreate to change sizing variant (no in-place resize in this feature).

## What's NOT in this feature

In-place resize, dedicated-server sizing, and controller-side cleanup of the network-less-cluster auto-VPC orphan (documented operator-side in `docs/kubernetes.md`).
