# `ContainerRegistryPreset` (v1alpha1, observe-only)

Catalog data: a single Timeweb Container Registry tariff plan, reflected as
a Kubernetes resource. Operators read these and reference one by Kubernetes
name in `ContainerRegistry.spec.forProvider.presetRef.name`.

| Property | Value |
| -------- | ----- |
| API group | `containerregistry.m.timeweb.crossplane.io` |
| Kind | `ContainerRegistryPreset` |
| Scope | Namespaced (lives in the provider's `--preset-target-namespace`; default `crossplane-system`) |
| External-name | none — Preset CRs are named by the catalog poller |
| Connection Secret | none |

## Operator workflow

```bash
# Browse the catalog populated by the provider:
kubectl get -n crossplane-system containerregistrypresets

# Reference one in a ContainerRegistry manifest:
cat <<EOF | kubectl apply -f -
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: my-registry
  namespace: timeweb-prod
spec:
  forProvider:
    name: my-registry
    presetRef:
      name: cr-starter-5gb-1939   # ← the Preset CR's Kubernetes name
  providerConfigRef:
    name: default
EOF
```

## How the catalog is populated

The provider runs a timer-based **PresetReconciler** alongside the
controller-runtime manager. On startup (after a ~15s grace period) and
every `--preset-sync-interval` thereafter (default `30m`) the reconciler:

1. Uses the `default` ProviderConfig to fetch
   `GET /api/v1/container-registry/presets`.
2. Upserts a `ContainerRegistryPreset` per upstream catalog entry in
   `--preset-target-namespace` (default `crossplane-system`). The Kubernetes
   name is derived from `description_short` + the numeric preset ID
   (e.g. `cr-starter-5gb-1939`).
3. Prunes presets that disappeared from the upstream catalog.
4. On transient errors (rate-limit, 5xx), keeps existing CRs untouched and
   retries on the next tick.

## Read-only enforcement

A `ValidatingAdmissionPolicy` shipped with the provider package
(`deploy/policies/preset-readonly.yaml`) rejects every `CREATE`/`UPDATE`
on `containerregistrypresets` whose user is not the provider's
ServiceAccount. Operators cannot edit Preset specs — the catalog is the
source of truth.

## Field reference

### `spec`

Intentionally empty. Operators do NOT author Presets.

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `presetID` | integer | The upstream Timeweb `preset_id` — referenced by `ContainerRegistry.spec.forProvider.presetRef`. |
| `description` | string | Long description from the catalog (may be Russian). |
| `descriptionShort` | string | Short label. |
| `diskGB` | integer | Included disk capacity. |
| `location` | string | Region (when advertised by upstream). |
| `prices` | list of `{amount, currency, period}` | Price list. |
| `lastObservedAt` | string (RFC3339) | Timestamp of the most-recent catalog poll. |

## Cross-namespace reference

`ContainerRegistry.spec.forProvider.presetRef.name` resolves a Preset by
**Kubernetes name** in the provider's target namespace
(`--preset-target-namespace`). One Preset CR serves every namespace's
ContainerRegistries without duplication.

## Tearing one down manually

If you `kubectl delete containerregistrypreset/<name>`, the next catalog
poll will recreate it (assuming the underlying upstream preset still
exists). Treat this as a "force-refresh" knob.
