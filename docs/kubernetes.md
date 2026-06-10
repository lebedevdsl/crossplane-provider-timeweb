# Managed Kubernetes — operator guide

Covers the three `kubernetes.m.timeweb.crossplane.io/v1alpha1` kinds:
`KubernetesCluster`, `KubernetesClusterNodepool`, `KubernetesClusterAddon`.
Assumes the provider is installed and a `ProviderConfig` named `default`
resolves to a valid Timeweb token. All location/AZ codes are **API values**
(`msk-1`, `spb-3`, `ams-1`, `fra-1`), never dashboard labels.

## 1. Minimum cluster + worker pool

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata: { name: demo, namespace: default }
spec:
  forProvider:
    name: demo
    k8sVersion: "1.31.2"        # an EXACT entry from /api/v1/k8s/k8s-versions
    networkDriver: cilium        # kuberouter | calico | flannel | cilium
    availabilityZone: msk-1      # spb-3 | msk-1 | ams-1 | fra-1
    presetName: <smallest-master-slug>
    masterNodesCount: 1          # 3 for an HA control plane (immutable)
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
kubectl wait --for=condition=Ready kubernetescluster/demo --timeout=20m
kubectl wait --for=condition=Ready kubernetesclusternodepool/demo-workers --timeout=15m
kubectl get secret demo-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > demo.kubeconfig
kubectl --kubeconfig demo.kubeconfig get nodes        # lists the 2 workers
```

The Nodepool waits for the cluster to be `Ready=True` before creating the
worker group (you'll see `Synced=False, reason=Reconciling` naming the cluster
until then). Preset slugs are derived from the catalog `description_short`
(resolved against `/api/v1/presets/k8s`, filtered to `type=master` /
`type=worker`); an unknown slug surfaces `Synced=False` listing valid values.

> **Ambiguous preset slugs.** K8s presets carry no location, and Timeweb ships
> several identically-named tiers (e.g. four masters all named "K8S Promo (1
> Rub)"). When a `presetName` matches more than one upstream preset the
> controller reports `Synced=False, reason=PresetAmbiguous` with the exact
> disambiguator to use — append the upstream id, e.g.
> `presetName: k8s-promo-1-rub-1999`.

### Custom sizing (configurators)

To sidestep ambiguous preset slugs entirely, size the cluster and/or nodepool
by `resources` (cpu/ramGB/diskGB) instead of `presetName`:

```yaml
# KubernetesCluster.spec.forProvider
    resources: { cpu: 4, ramGB: 8, diskGB: 80 }     # masters; no presetName
# KubernetesClusterNodepool.spec.forProvider
    resources: { cpu: 4, ramGB: 16, diskGB: 120 }   # workers; optional gpu
```

- Exactly one of `presetName` / `resources` per kind (admission-enforced);
  presets remain supported.
- `ramGB`/`diskGB` are normalized to the upstream MB units and emitted as the
  `configuration` block; `status.atProvider.lockedConfiguratorID` records the
  resolved configurator.
- Unsatisfiable → `reason=NoConfiguratorAvailable`; flipping the sizing variant
  on a live resource → `reason=SizingSwitchRequiresRecreate`.
- Configurator resolution is **location-first and role-aware**: the cluster's
  sizing resolves against the master-family configurators of the region your
  `availabilityZone` maps to, and a nodepool resolves against the
  worker-family configurators of its parent cluster's region (the upstream
  does not validate this pairing itself — a mismatched id strands the cluster
  in AMS-1 and it fails to provision, so the provider never sends one).
- The K8s catalog's bounds are tighter than the cloud-server one, and master
  nodes have higher minimums than workers (e.g. Moscow: masters from 4 CPU /
  8 GB RAM / 60 GB disk, workers from 2 CPU / 2 GB RAM / 40 GB disk) — a
  sizing that works for a `Server`, or for a nodepool, may yield
  `NoConfiguratorAvailable` for a cluster. The error names the location and
  the bound that rejected it.
- A cluster whose upstream provisioning dies is surfaced as
  `Ready=False, reason=UpstreamFailed` (it will not recover on its own —
  delete and recreate).

### Worker node networking

Worker nodes come up with **public IPs by default** — that is the upstream
behavior and this provider does not change it. There is no per-nodepool
public-IP toggle in the Timeweb API. For private-only nodes, the cluster's
network must sit behind a Timeweb **Router** providing NAT egress (the
dashboard's router → private networks → NAT flow). The Router product is not
yet modeled by this provider — planned as its own kind in a future feature;
until then, private-only setups are arranged in the dashboard and the cluster
is attached to the routed network via `networkRef`/`networkID`.

## 2. Scale a worker pool

```bash
kubectl patch kubernetesclusternodepool/demo-workers --type merge \
  -p '{"spec":{"forProvider":{"nodeCount":4}}}'
```

The controller adds/removes nodes via relative deltas (the group is never
recreated) and `status.atProvider.observedNodeCount` converges. While a delta
is in flight the pool reports `Ready=False, reason=Reconciling`.

Autoscaling instead of manual scaling (`minSize`/`maxSize` ≥ 2):

```yaml
    autoscaling: { enabled: true, minSize: 2, maxSize: 6 }
    autohealing: true
```

When autoscaling is on the controller stops reconciling `nodeCount` — the
upstream autoscaler owns the count.

## 3. Private network + project

```yaml
    networkRef: { name: shared-vpc }     # a feature-003 Network MR, Ready=True
    projectRef: { name: team-a-project } # a feature-001 Project MR
```

Use `networkID: <vpc-id>` / `projectID: <id>` to attach externally-managed
resources without an MR. The provider does **no** client-side AZ/region
pre-check — an incompatible cluster-AZ / VPC-region pairing is rejected by the
upstream API and surfaced as `Synced=False, reason=ReconcileError`.

> **Attach an explicit network for production.** A cluster created without a
> `networkRef`/`networkID` makes Timeweb **auto-create a default VPC**, and that
> auto-created VPC is **not** removed when the cluster is deleted (it lingers as
> an orphan you must clean up manually). Always attach your own `Network` so its
> lifecycle is GitOps-managed alongside the cluster.

> **Promo tiers are one-per-account.** Timeweb caps promo presets (e.g.
> "K8S Promo (1 Rub)") at a single cluster per account; a second promo create
> returns `Synced=False, reason=… (Conflict: Promo cluster already exists on
> account)`. Use a standard (non-promo) preset for anything you'll recreate.

## 4. Upgrade the Kubernetes version

```bash
kubectl patch kubernetescluster/demo --type merge \
  -p '{"spec":{"forProvider":{"k8sVersion":"1.32.0"}}}'   # newer, catalog-valid
```

A forward bump triggers the in-place upstream upgrade (transient
`Ready=False, reason=Upgrading`, then back to `Ready=True`). A downgrade or
non-catalog version is rejected with `Synced=False, reason=ReconcileError`; no
upstream call is made.

## 5. Install an addon

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterAddon
metadata: { name: demo-ingress, namespace: default }
spec:
  forProvider:
    clusterRef: { name: demo }
    type: <addon-type-from-addons-configs>
    version: <addon-version>
    # yamlConfig: "<override>"   # optional; defaults to the catalog config
  managementPolicies: ["*"]
```

`type`+`version` are validated against the cluster's available-addons catalog
(`/api/v1/k8s/clusters/{id}/addons-configs`). Deleting the MR removes the
addon; the cluster stays `Ready=True`.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Cluster `ReconcileError`, message lists versions | `k8sVersion` not an exact catalog entry | copy a value from `/api/v1/k8s/k8s-versions` |
| Cluster `ReconcileError`, message lists slugs | `presetName` not a valid master slug | use a slug from the error (`type=master`) |
| Cluster `Ready=False, reason=PaymentRequired` | account `no_paid` (billing/quota) | top up the account; not a controller bug |
| Cluster `ReconcileError` on create with `networkRef`/`networkID` | upstream rejected the cluster-AZ / VPC-region pairing | pick an `availabilityZone` compatible with the VPC's region |
| Nodepool stuck `Synced=False, reason=Reconciling` | parent cluster not `Ready=True` yet | wait; check `clusterRef.name` |
| Nodepool `nodeCount` change ignored | `autoscaling.enabled` is true | disable autoscaling to scale manually |
| Cluster edit rejected `ImmutableFieldChange` | changed `networkDriver`/`availabilityZone`/`presetName`/`masterNodesCount` | delete + recreate; only `k8sVersion`/`name`/`description` are mutable |
| Addon `ReconcileError`, lists valid types | `type`/`version` not in the cluster's catalog | use a catalog entry |

## What's NOT in v0.x

Custom-configurator sizing (`cpu`/`ram`/`disk`), `is_ingress`/`is_k8s_dashboard`
toggles, OIDC provider, maintenance windows, custom pod/service CIDR, in-place
nodepool label/autoscaling mutation, and per-addon config PATCH. Each is a
follow-up extending `kubernetes.m.timeweb.crossplane.io`.
