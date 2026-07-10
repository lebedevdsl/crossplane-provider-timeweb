# Quickstart: Dedicated (tainted) worker pools

Operator walkthrough for `KubernetesClusterNodepool.spec.forProvider.taints`
(feature 015). Assumes a Ready `KubernetesCluster` and a `ProviderConfig`.

## 1. Declare a dedicated pool

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata:
  name: ingress-pool
  namespace: infra
spec:
  managementPolicies: ["*"]
  forProvider:
    name: ingress-pool
    clusterRef:
      name: prod-cluster
    resources:
      cpu: 2
      ramGB: 4
      diskGB: 40
    nodeCount: 2
    labels:
      role: ingress
    taints:
      - key: dedicated
        value: ingress
        effect: NoSchedule
  providerConfigRef:
    name: default
    kind: ProviderConfig
```

Nodes join the cluster already tainted — workloads without the matching
toleration never land on them, from the first second.

## 2. Tolerate it in the dedicated workload

```yaml
spec:
  tolerations:
    - key: dedicated
      operator: Equal
      value: ingress
      effect: NoSchedule
  nodeSelector:
    role: ingress
```

## 3. Day-2 changes

Edit the manifest and re-apply — the pool converges in place (no node
recreation):

- add/remove/modify entries under `taints:` or `labels:`
- remove the whole block to clear all taints/labels upstream

`kubectl get kubernetesclusternodepool ingress-pool -n infra` shows
`SYNCED=True` once the upstream group reports the declared sets. Edits made
out-of-band (panel/API) are reverted on the next reconcile — the manifest is
the single writer.

## 4. Troubleshooting

| Symptom | Meaning |
|---------|---------|
| apply rejected: `Unsupported value: "NoScheduleTypo"` | effect must be NoSchedule / PreferNoSchedule / NoExecute |
| apply rejected: `taints must not repeat the same key+effect pair` | duplicate identity; same key needs distinct effects |
| `SYNCED=False` with an upstream error after a taint edit | PATCH rejected upstream — see the CR events; drift will be retried |
| a value-less taint shows on the nodepool but not on the node objects | platform quirk: taints with an empty value persist on the group but are not applied to nodes — give the taint a value |

Scheduling semantics are standard Kubernetes:
https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/
