# Quickstart: Scale-to-zero worker pools (feature 026)

## Prerequisite (upstream requirement — not enforced by the provider)

The cluster needs **at least two permanently active nodes in other pools** to
host system components — a `minSize: 0` pool cannot be the cluster's only
capacity. If the prerequisite isn't met the pool works normally, it just never
drains to zero. See the official doc:
`https://timeweb.cloud/docs/k8s/kubernetes-autoscaling/autoscaling-to-zero-nodes`.

## Declare a scale-to-zero pool

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata:
  name: ci
  namespace: infra
spec:
  forProvider:
    name: ci
    clusterRef: {name: main-cluster}
    resources: {cpu: 4, ramGB: 8, diskGB: 60}
    nodeCount: 1                # required; ignored while autoscaling is on
    autoscaling: {enabled: true, minSize: 0, maxSize: 3}
    taints:
      - {key: inyan.pro/ci, value: "true", effect: NoSchedule}
    labels: {inyan.pro/pool: ci}
  managementPolicies: ["*"]
  providerConfigRef: {name: default}
```

When no workload targets the pool, the upstream autoscaler removes all nodes
(default idle window ~5 min). The nodepool stays `Ready=True` at zero nodes —
that IS the desired state:

```console
$ kubectl get kubernetesclusternodepool ci
NAME  READY  SYNCED  ...  DESIRED  OBSERVED
ci    True   True    ...  1        0
```

`status.atProvider.autoscaling` mirrors the upstream bounds; `nodes` is empty
while drained.

## Day-2: drop an existing floor to zero

```console
$ kubectl patch kubernetesclusternodepool ci --type=merge \
    -p '{"spec":{"forProvider":{"autoscaling":{"enabled":true,"minSize":0,"maxSize":3}}}}'
```

Converges in place via the group PATCH (no recreate). Verify with
`kubectl wait --for=condition=Ready` and the status mirror.

## What to expect

- **Scale-up from zero** is the upstream autoscaler's job: pods must target the
  pool explicitly via `nodeSelector`/`nodeAffinity` (group id or the pool's
  labels) — a pending pod without that targeting will NOT wake the pool. Allow
  node-provision time (upstream default budget 600s). While provisioning, the
  pool reports Ready=False `Reconciling` — normal.
- **Drain to zero can be blocked by the workload**: pods annotated
  `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"`, restrictive
  PodDisruptionBudgets, or pods without a controller keep the last node alive.
  Drained nodes get a `DeletionCandidateOfClusterAutoscaler` taint and are
  deleted ~2 minutes later (idle window ~5 min before that).
- **Ready=True at 0 nodes happens only when** the declaration is converged,
  autoscaling is enabled, and `minSize: 0` is declared. A pool with
  `minSize >= 1` observed at 0 nodes is transitional and reports not-Ready
  until the autoscaler restores it.
- **The provider never fights the autoscaler**: no count writes while
  autoscaling is declared on — including no "repair" scale-up at zero.
- Out-of-band bounds edits (panel) are reverted to the declaration on the next
  reconcile (single-writer, as for all owned fields).

## Troubleshooting

| Symptom | Meaning |
|---------|---------|
| Rejected at apply: `maxSize must be >= minSize` | bounds inverted |
| Rejected at apply: `minSize ... greater than or equal to 0` / `maxSize ... 1` | negative min / zero max |
| Pool drained but Ready=False `Creating` | declaration not converged or `minSize > 0` — check `status.atProvider.autoscaling` vs spec |
| Pods pending, no node appears within ~10 min | pods likely lack `nodeSelector`/`nodeAffinity` targeting this pool; else upstream autoscaler issue — check the Timeweb panel; the provider does not provision autoscaled counts |
| Pool never drains to zero | fewer than 2 permanently active nodes in other pools (prerequisite), or a drain blocker (safe-to-evict:"false", PDB, controller-less pod) |

## Ops note (this account)

The `ci` pool from the 2026-08-07 wire capture (cluster 1096397 = the
**inyan-staging** cluster, group 117127) is panel-managed — no
`KubernetesClusterNodepool` MR declares it, so its `min_size: 0` stands and
nothing fights it. If it is ever brought under management, declare it with
`autoscaling: {enabled: true, minSize: 0, maxSize: 3}` to match; declaring any
other bounds would revert the panel setting (single-writer).
