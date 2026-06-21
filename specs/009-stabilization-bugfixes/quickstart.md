# Quickstart — Stabilization & Bugfixes

## Operator: reading accurate state

After this round, `kubectl get`/`describe` reflect reality:

```
# Nodepool: CLUSTER populated, PUBLIC is a clear boolean
kubectl get kubernetesclusternodepools
NAME              READY  SYNCED  CLUSTER   PRESET           PUBLIC  DESIRED  OBSERVED  AGE
e2e-k8s-workers   True   True    1091306   k8s-1-2-30-1679  <unset> 2        2         12m

# Server: resolved availability zone visible (preset may override the request)
kubectl describe server e2e-server | grep -A2 'At Provider'
#   Availability Zone:  spb-3        # even if spb-1 was requested (preset-driven)

# Network-less cluster: the auto-created VPC id is recorded for cleanup
kubectl get kubernetescluster e2e-k8s -o jsonpath='{.status.atProvider.autoCreatedNetworkID}'
#   network-de7774e5...            # operator can delete this manually if desired
```

Auto-created networks are **not** deleted or swept by the provider — the id is
recorded so you can clean them up deliberately.

## Operator: custom sizing

```
# Custom-sized server/worker pick a standard configurator and provision:
resources: { cpu: 1, ramGB: 1, diskGB: 15 }   # in an orderable region (e.g. ru-3/msk-1)

# In a region with only promo/legacy configurators you get a clear error:
#   "no orderable configurator for ru-1/1-1-15 (only promo/legacy: discount35, ssd_2022, …)"
```

## Maintainer: running e2e reliably

```
source ~/.tw                         # token (do not echo)
source test/e2e/presets.local.env    # seeds TWE_LOCATION=ru-3 TWE_AZ=msk-1 + presets

# Serial (default) — context-flake no longer aborts bundles:
make e2e.test E2E_KUBECONTEXT=twc-staging KUTTL_TEST=12-k8s-cluster-lifecycle

# Opt-in parallel — split tiers; watch ACCOUNT QUOTAS (not request rate):
make e2e.test E2E_KUBECONTEXT=twc-staging KUTTL_TEST=09-server-lifecycle &
make e2e.test E2E_KUBECONTEXT=twc-staging KUTTL_TEST=18-router-lifecycle &
# k8s tier separately (heavier on quota):
make e2e.test E2E_KUBECONTEXT=twc-staging KUTTL_TEST=12-k8s-cluster-lifecycle
```

To target a different account/region: override `TWE_LOCATION` / `TWE_AZ` (and the
matching presets).

## Maintainer: release readiness

```
# 1. Diagnostic logging OFF
grep -n -- '--debug' deploy/deploymentruntimeconfig.yaml   # must be absent

# 2. Clean release version (not a dev iteration tag)
make xpkg.push VERSION=v0.1.0 ...     # dev-<ts> tags are for iteration only

# 3. Private-cluster path validated once on the release build
TIMEWEB_E2E_PRIVATE=1 make e2e.test E2E_KUBECONTEXT=twc-staging KUTTL_TEST=19-private-cluster
```

## Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| `CLUSTER` column empty on a Ready nodepool | clusterID not mirrored on Observe | fixed (FR-001); if seen, check the parent ref resolved |
| Custom create "Preset with id: 0 not found" | omitted `gpu` (server) / region promo-only | server fixed (008); region → use an orderable region |
| Bundle aborts: "context does not exist" | transient kubeconfig read | fixed (FR-005 retry); re-run |
| preset-not-found for a hardcoded region | bundle pinned wrong region | fixed (FR-006); ensure `TWE_LOCATION`/`TWE_AZ` set |
| Leftover `192.168.0.0/24` VPCs | network-less cluster auto-VPC | expected; id is in the owner's status — delete manually |
