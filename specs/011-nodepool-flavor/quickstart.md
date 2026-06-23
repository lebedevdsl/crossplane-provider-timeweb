# Quickstart: Nodepool Worker Flavor

## Small / cheap worker pool (default — general family)

No `flavor` needed; omitting it means `standard` (Premium NVMe / general), which allows low
RAM-per-CPU sizing:

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
spec:
  forProvider:
    name: workers
    clusterRef: { name: my-cluster }
    publicIP: false
    resources:
      cpu: 2
      ramGB: 2        # valid on general; would be rejected on dedicated-cpu
      diskGB: 40
      # flavor: standard   # implicit default
```

Expected: resolves a `k8s_configurator_general` worker configurator; pool reaches Ready.

## Dedicated-CPU worker pool

```yaml
    resources:
      cpu: 2
      ramGB: 8        # dedicated-cpu floor ≈ 4 GB/cpu ⇒ 2 cpu needs ≥ 8 GB
      diskGB: 40
      flavor: dedicated-cpu
```

Expected: resolves a `k8s_configurator_dedicated_cpu` configurator; pool reaches Ready.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Synced=False`, error naming `dedicated-cpu` + a ram constraint | sizing below the dedicated family's ~4 GB/cpu floor | raise `ramGB` (≥ 4×cpu) or switch to `flavor: standard` |
| Upstream `invalid_configuration_ram` (pre-feature behavior) | provider auto-picked dedicated via tightest-fit | upgrade to the flavor-aware build; default `standard` now selects general |
| `flavor` rejected at apply | typo / unsupported value | use exactly `standard` or `dedicated-cpu` |
| Existing pool unchanged after adding `flavor` | configurator is locked at create | flavor affects new pools; recreate the pool to change family |

## Validate

```sh
# the resolved family shows up as the locked configurator id on status
kubectl get kubernetesclusternodepool <name> -o jsonpath='{.status.atProvider.lockedConfiguratorID}'
# general (e.g. id 59 in ru-3) for standard; dedicated (e.g. 69) for dedicated-cpu
```
