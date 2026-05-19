# Contract: `ContainerRegistryPreset` (v1alpha1, observe-only)

**Group/Version**: `containerregistry.timeweb.crossplane.io/v1alpha1`
**Kind**: `ContainerRegistryPreset` | **Scope**: `Namespaced`
**Short name**: `tw-cr-preset`

Observe-only Kubernetes representation of a Timeweb Container Registry tariff plan.
The provider reconciles these CRDs from `/api/v1/container-registry/presets` on a
schedule (default every 30 minutes — R-6); operators **read** them and reference one
by name in `ContainerRegistry` manifests. Operator edits to `spec` are rejected.

## What operators see

```yaml
apiVersion: containerregistry.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistryPreset
metadata:
  name: cr-starter-5gb
  namespace: crossplane-system     # provider's namespace by default;
                                   # cross-namespace reference is permitted
spec:
  forProvider: {}                  # empty — provider-owned
status:
  atProvider:
    presetID: 1939
    displayName: "Starter (5GB)"
    disk: 5
    price:
      amount: "200"
      currency: RUB
    location: ru-1
    lastObservedAt: "2026-05-18T10:24:01Z"
  conditions:
    - type: Synced
      status: "True"
      reason: CatalogObserved
```

## Read-only enforcement

- A `ValidatingAdmissionPolicy` shipped with the provider package rejects every PATCH/UPDATE
  that mutates `spec`. The rejection message: `"ContainerRegistryPreset is observe-only;
  spec is controller-owned and cannot be edited"`.
- Operators may delete a `ContainerRegistryPreset` CR; the provider re-creates it on the
  next catalog poll if the preset still exists upstream.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Last catalog poll observed this preset and updated `status.atProvider`. | `CatalogPollFailed` (transient — last successful values are retained), `Stale` (last poll older than 2 × `--preset-sync-interval`). |

Note: `Ready` is not surfaced — presets have no readiness concept.

## External-name

Stringified `atProvider.presetID`. Set by the controller; operators do not author it.

## Cross-namespace reference

`ContainerRegistry.spec.forProvider.presetRef.name` resolves a preset by Kubernetes
name within the **provider's target namespace** (`--preset-target-namespace`, defaults
to the provider's own namespace). This lets one preset CR serve every namespace's
`ContainerRegistry` resources without duplication.

## Lifecycle

The controller does NOT implement the standard `external` client interface for this
kind. Instead, a dedicated `CatalogReconciler` runs on a timer:

```
Every --preset-sync-interval (default 30m):
  presets ← GET /api/v1/container-registry/presets
  for each preset in presets:
      upsert ContainerRegistryPreset/<slug(preset.name)>
  delete CRs for presets that disappeared upstream
  on transient error → keep existing CRs, surface Synced=False on each
```

The first reconcile runs at controller start; subsequent reconciles run on the timer
and on a `force-sync` channel signaled by the provider's healthz endpoint when an
operator runs the diagnostic command (`kubectl exec` + `curl localhost:8081/force-preset-sync`).
