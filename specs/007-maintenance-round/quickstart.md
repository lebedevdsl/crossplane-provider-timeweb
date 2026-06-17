# Operator Quickstart — Feature 007 Changes

**Date**: 2026-06-17 | **Branch**: `007-maintenance-round`

This document covers what changes for operators after 007 lands, including
before/after manifest examples, the new uniform `kubectl get` output, the
improved conditions and events surface, and the getting-started and auth-setup
outline for new operators.

---

## Part 1 — Auth Setup (New Operators)

### Step 1: Obtain your Timeweb API token

Log in to the Timeweb panel (panel.timeweb.cloud) → Profile → API tokens →
"Create token". Copy the token immediately (it is shown once).

### Step 2: Create the credential Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: timeweb-credentials
  namespace: crossplane-system
type: Opaque
stringData:
  token: "<paste your API token here>"
```

```bash
kubectl apply -f timeweb-credentials.yaml
```

### Step 3: Create a ProviderConfig

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: crossplane-system
spec:
  credentials:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: timeweb-credentials
      key: token
```

```bash
kubectl apply -f providerconfig.yaml
```

### Step 4: Provision your first resource

See Part 2 for placement syntax. A minimal Server example:

```yaml
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata:
  name: my-first-server
  namespace: default
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: my-first-server
    location: ru-1
    presetName: ssd-15     # simplified form (new in 007)
    osID: 76               # Ubuntu 22.04 LTS
```

```bash
kubectl apply -f server.yaml
kubectl get server my-first-server   # watch READY column
```

---

## Part 2 — Placement: Before vs. After

### What changed

- All regionally-placed resources now use the same `location` + optional
  `availabilityZone` pair.
- Router and KubernetesCluster previously required only `availabilityZone`;
  they now also require `location`.
- All 8 Timeweb regions are reachable (previously only 4 were supported for
  Router and KubernetesCluster).

### Router — Before (006 and earlier)

```yaml
spec:
  forProvider:
    name: my-router
    availabilityZone: spb-3   # required; only 4 zones accepted
    presetName: router-1x1-1gb-ru-1
```

### Router — After (007+)

```yaml
spec:
  forProvider:
    name: my-router
    location: ru-1            # required; all 8 regions work
    # availabilityZone: spb-3   # optional; omit for single-AZ regions
    presetName: router-1x1-1gb  # short form (new); long form still accepted
```

**Existing manifests that set only `availabilityZone`**: add `location` when
re-applying. Applied resources already in etcd are not re-validated until edited.

### KubernetesCluster — Before

```yaml
spec:
  forProvider:
    name: my-cluster
    availabilityZone: msk-1
    k8sVersion: v1.31.x+k0s.0
    presetName: k8s-system-medium-msk-1
```

### KubernetesCluster — After

```yaml
spec:
  forProvider:
    name: my-cluster
    location: ru-3            # Moscow (was only accessible via msk-1 AZ)
    k8sVersion: v1.31.x+k0s.0
    presetName: k8s-system-medium  # short form
```

### Single-AZ vs. Multi-AZ regions

| Region | AZ count | `availabilityZone` needed? |
|--------|---------|---------------------------|
| `ru-1` (St. Petersburg) | 5 | Yes — controller asks if omitted |
| `ru-2` (Novosibirsk) | 1 | No — auto-derived |
| `ru-3` (Moscow) | 1 | No — auto-derived |
| `nl-1` (Amsterdam) | 1 | No — auto-derived |
| `de-1` (Frankfurt) | 1 | No — auto-derived |
| `kz-1` (Almaty) | 1 | No — auto-derived |
| `us-4` (USA) | 1 | No — auto-derived |
| `pl-1` (Poland) | 1 | No — auto-derived |

For `ru-1` with multiple AZs, specify explicitly:
```yaml
location: ru-1
availabilityZone: spb-3   # pick from spb-1..spb-5
```

---

## Part 3 — Preset Names: Before vs. After

### What changed

- Preset names can be written without the location suffix you already declared
  in `location`.
- Wrong preset names now list only the presets valid for your declared location.
- Long-form slugs continue to work forever (backward-compatible).

### Before

```yaml
presetName: ssd-15-ru-1    # location repeated here and in spec.forProvider.location
```

### After (both forms accepted)

```yaml
presetName: ssd-15         # simplified: location derived from spec.forProvider.location
# OR (still valid):
presetName: ssd-15-ru-1   # long form
```

### Improved not-found error

**Before** (global list, hard to parse):
```
resolver: preset not found: slug "ssd-99" in dimension "DimServerPreset" does not match
any upstream entry (valid: ssd-15-ru-1, ssd-25-ru-1, ssd-50-ru-1, ssd-15-ru-2,
ssd-25-ru-2, ssd-15-ru-3, ssd-25-ru-3, ssd-15-nl-1, ...)
```

**After** (location-scoped, self-service):
```
resolver: preset not found: slug "ssd-99" in dimension "DimServerPreset" does not match
any upstream entry for location "ru-1"
(valid: ssd-15, ssd-25, ssd-50, ssd-100, ssd-250, ssd-500, ssd-1000)
```

---

## Part 4 — `kubectl get` Output: Before vs. After

### Uniform column order (all kinds)

All MR kinds now show: `READY | SYNCED | LOCATION | <kind-specific> | AGE`

Internal upstream IDs appear only with `-o wide` (moved to `priority=1`).

### Server

```
# before
NAME            READY   SYNCED   LOCATION   PRESET   PUBLIC-IP       STATE     AGE
my-server       True    True     ru-1       ssd-15   123.45.67.89    started   5m

# after (ID column added at priority=1; visible with -o wide)
NAME            READY   SYNCED   LOCATION   PRESET   PUBLIC-IP       STATE     AGE
my-server       True    True     ru-1       ssd-15   123.45.67.89    started   5m
```

### Network

```
# before
NAME         READY   SYNCED   CIDR           LOCATION   UPSTREAM-ID             AGE
my-network   True    True     10.0.0.0/24    ru-1       network-abc123def456    5m

# after (UPSTREAM-ID → ID at priority=1; STATE added)
NAME         READY   SYNCED   LOCATION   CIDR           STATE    AGE
my-network   True    True     ru-1       10.0.0.0/24    active   5m

# -o wide also shows:
NAME         READY   SYNCED   LOCATION   CIDR           STATE    ID                      AGE
my-network   True    True     ru-1       10.0.0.0/24    active   network-abc123def456     5m
```

### FloatingIP

```
# before (3 binding columns, inconsistent ID type)
NAME     READY   SYNCED   IP            BOUND-RES   BOUND-TO   BOUND-UUID         LOCATION   AGE
my-fip   True    True     123.45.6.7    router      <none>     abc-uuid-123       ru-1       5m

# after (single BOUND-TO; ID at priority=1)
NAME     READY   SYNCED   LOCATION   IP            BOUND-TO             AGE
my-fip   True    True     ru-1       123.45.6.7    router/abc-uuid-123  5m
```

### Router

```
# before
NAME        READY   SYNCED   AZ      TIER                    STATE     UPSTREAM-ID    AGE
my-router   True    True     spb-3   router-1x1-1gb-ru-1     started   abc-uuid       5m

# after
NAME        READY   SYNCED   LOCATION   TIER           STATE     AGE
my-router   True    True     ru-1       router-1x1-1gb started   5m
```

### KubernetesCluster

```
# before
NAME         READY   SYNCED   K8S-VERSION        AZ      PRESET              STATE    AGE
my-cluster   True    True     v1.31.x+k0s.0     msk-1   k8s-system-medium   active   10m

# after
NAME         READY   SYNCED   LOCATION   K8S-VERSION        PRESET              STATE    AGE
my-cluster   True    True     ru-3       v1.31.x+k0s.0     k8s-system-medium   active   10m
```

### KubernetesClusterNodepool

```
# before
NAME          READY   SYNCED   CLUSTER      PRESET        DESIRED   OBSERVED   AGE
my-nodepool   True    True     cluster-id   k8s-worker    3         3          8m

# after (PUBLIC-IP added; ID at priority=1)
NAME          READY   SYNCED   CLUSTER      PRESET        PUBLIC-IP   DESIRED   OBSERVED   AGE
my-nodepool   True    True     cluster-id   k8s-worker    True        3         3          8m
```

### ContainerRegistry

```
# before
NAME          READY   SYNCED   SIZE-GB   EXTERNAL-NAME   AGE
my-registry   True    True     10        123             5m

# after
NAME          READY   SYNCED   STATE    SIZE-GB   ENDPOINT                      AGE
my-registry   True    True     active   10        cr.timeweb.cloud/my-registry  5m
```

---

## Part 5 — Conditions and Events: Before vs. After

### What changed

- `kubectl describe` now shows distinct conditions for all failure modes.
- Events fire on condition **transitions** only (no per-reconcile spam).
- All kinds share the same vocabulary — no more guessing which controller uses
  which reason string.

### Checking resource health with `kubectl get`

```bash
# READY=True means upstream is healthy
kubectl get server my-server

# READY=False means something is wrong — check SYNCED and describe
kubectl describe server my-server
```

### Condition meanings (in `kubectl describe`)

```
Conditions:
  Ready=True    Reason: Available         # healthy
  Ready=False   Reason: Creating          # first provision in progress
  Ready=False   Reason: PaymentRequired   # Server only: account billing-blocked
  Ready=False   Reason: UpstreamFailed    # terminal upstream error (delete+recreate)
  Synced=False  Reason: PresetNotFound    # fix presetName in spec
  Synced=False  Reason: ParentNotReady    # waiting for parent resource
  Synced=False  Reason: ImmutableFieldChange  # revert or recreate
```

### Events (`kubectl describe` → Events section)

**Before**: mostly empty or noisy per-reconcile entries.

**After**: one Event per condition transition:

```
Events:
  Type     Reason           Age    Message
  ----     ------           ----   -------
  Normal   Provisioning     5m     Creating upstream router
  Normal   Provisioned      3m     Router reached started state
  Warning  PresetNotFound   1m     resolver: preset not found: slug "bad-preset" ...
```

### Diagnosing common failures

| Symptom | Check |
|---------|-------|
| `READY=False`, `Reason: Creating` for > 10 minutes | Check upstream Timeweb panel; may be a quota issue |
| `READY=False`, `Reason: PaymentRequired` | Top up account; controller will re-check on next reconcile |
| `READY=False`, `Reason: UpstreamFailed` | Upstream provisioning failed terminally; delete and recreate |
| `SYNCED=False`, `Reason: PresetNotFound` | Fix `presetName`; error message lists valid options for your location |
| `SYNCED=False`, `Reason: ParentNotReady` | Wait for parent resource to become Ready=True |
| `SYNCED=False`, `Reason: ImmutableFieldChange` | Revert the immutable field change, or delete and recreate |
| `SYNCED=False`, `Reason: CatalogTransient` | Upstream catalog is temporarily unavailable; auto-requeued |

---

## Part 6 — Troubleshooting Matrix

| What you observe | Root cause | Fix |
|-----------------|------------|-----|
| Router/Cluster fails with "no zone known for location ru-2" | Old `azLocation` bug (pre-007) | Upgrade provider; or set `availabilityZone: nsk-1` manually |
| FloatingIP created in wrong zone | Old `defaultAZByLocation` inversion (ru-2↔ru-3) | Upgrade provider; zones are now live-sourced |
| Preset error lists dozens of cross-location entries | Pre-007 global not-found list | Upgrade provider; error now scoped to declared location |
| kubectl get shows UPSTREAM-ID column | Pre-007 CRD | After upgrade, CRD regenerated; column moves to `-o wide` |
| Router/Cluster manifest rejected after 007 CRD install | Missing `location` field | Add `location: <region>` to existing manifests |
| Event flood in `kubectl describe` | Pre-007 per-reconcile events | Upgrade provider; events now transition-only |

---

## Part 7 — Getting-Started Doc Outline

*This section outlines the content that should be written as `docs/getting-started.md`
and `docs/auth-setup.md` (US5/FR-015). The quickstart above is the draft content;
the final docs should expand each section.*

### `docs/auth-setup.md` outline

1. **Prerequisites** — kubectl, a Timeweb account, the provider installed.
2. **Obtaining the API token** — panel.timeweb.cloud path, token scopes needed.
3. **Creating the credential Secret** — YAML + `kubectl apply`.
4. **ProviderConfig variants** — namespaced (`ProviderConfig`) vs. cluster-scoped
   (`ClusterProviderConfig`); when to use each.
5. **Verifying the connection** — `kubectl get providerconfig`; check conditions.
6. **Rotating the token** — update the Secret; provider picks it up on next reconcile.
7. **Troubleshooting** — `SecretMissing`, `SecretKeyEmpty`, `CatalogUnauthorized`.

### `docs/getting-started.md` outline

1. **Install the provider** — OCI package URL; `kubectl apply -f` the Provider CR;
   wait for `Healthy=True`.
2. **Auth setup** — link to `docs/auth-setup.md`.
3. **First resource: Server** — minimal manifest with `location` and short preset;
   `kubectl get server`; `kubectl describe server`.
4. **First private network** — Network + FloatingIP + Server with `floatingIPRefs`.
5. **First Kubernetes cluster** — KubernetesCluster + KubernetesClusterNodepool;
   accessing the kubeconfig via the connection Secret.
6. **Common day-2 ops** — checking placement, resizing (where supported), deleting.
7. **Next steps** — link to per-kind contracts in `specs/` and `docs/kubernetes.md`.
