# Choosing a size — `initialSizeGB`

`ContainerRegistry` and `S3Bucket` size themselves via a single integer
field: `spec.forProvider.initialSizeGB`. The value is constrained to the
discrete tariff tiers Timeweb publishes for that service, so operators
never need to look up an opaque preset slug or configurator ID.

| MR kind                                  | Allowed `initialSizeGB` values | Source of truth                                |
|------------------------------------------|--------------------------------|------------------------------------------------|
| `kubernetes.m.timeweb.crossplane.io/ContainerRegistry` | `5`, `10`, `25`, `50`, `75`, `100` | Timeweb dashboard → Container Registry → "Фиксированная" tiers |
| `objectstorage.m.timeweb.crossplane.io/S3Bucket`              | `1`, `10`, `100`, `250`            | Timeweb dashboard → S3 Storage → tier picker   |

Both values are also encoded as a CEL `+kubebuilder:validation:Enum=…` on
the CRD, so applying an out-of-list value is rejected at admission time.

## Examples

### Smallest Container Registry (5 GB)

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: cr-team-a
  namespace: team-a
spec:
  forProvider:
    name: my-registry
    initialSizeGB: 5
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

### Smallest S3 bucket (1 GB)

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: artifacts
  namespace: team-a
spec:
  forProvider:
    name: my-bucket
    type: private
    storageClass: hot
    initialSizeGB: 1
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Optional `location`

If your account has presets in multiple Timeweb regions and the cheapest
match for your size is ambiguous, narrow with `spec.forProvider.location`:

```yaml
spec:
  forProvider:
    initialSizeGB: 5
    location: ru-1
```

Leave it empty (the default) when your account has a single region — the
controller picks the only matching preset.

## What the controller does

At reconcile time the controller fetches the relevant catalog endpoint
(`/api/v1/container-registry/presets` or `/api/v1/presets/storages`) via
the in-controller resolver (cached per `(ProviderConfig, dimension)` with
a 5-minute TTL), filters entries by `(initialSizeGB, location?,
storageClass?)`, and resolves to a single upstream `preset_id`. That ID is
recorded in `status.atProvider.lockedPresetID` so re-resolution survives
upstream catalog rotations.

When no preset matches the requested combination, the MR transitions to:

```
Synced=False reason=ReconcileError
Message: resolver: preset not found: slug "size=5GB,location=\"<your-loc>\",storageClass=\"\"" in dimension "ContainerRegistryPreset" does not match any upstream entry (valid: 5GB/ru-1, 10GB/ru-1, 25GB/ru-1, 50GB/ru-1, 75GB/ru-1, 100GB/ru-1)
```

The `(valid: …)` suffix enumerates every size/location/storageClass combo
the resolver saw on the most recent catalog fetch — copy-paste a valid
combination back into the manifest.

## What about the dashboard's "Произвольная" (Custom) path?

Container Registry's dashboard exposes a custom-size path that starts at
100 GB. That path requires an internal `configurator.id` value that
Timeweb doesn't expose via any public API endpoint, so the controller
can't drive it programmatically. Operators who need a non-tier size today
must provision the registry through the dashboard and import it (set
`crossplane.io/external-name` on a fresh CR to the upstream id).

A TODO marker in `internal/controller/containerregistry/credentials.go`
tracks the future per-registry credential / configurator API; the
`initialSizeGB` enum will expand when Timeweb ships it.

## Container Registry credentials

Docker login uses:

| Field    | Value                                |
|----------|--------------------------------------|
| Registry | `<registry-name>.registry.twcstorage.ru` |
| Username | `<registry-name>`                    |
| Password | Your Timeweb API token               |

The controller derives all three from the registry name + the same token
the operator supplied in the `ProviderConfig`'s Secret, and publishes
them to the connection-Secret as both individual keys
(`endpoint`/`username`/`password`) and the standard
`.dockerconfigjson` blob.

When Timeweb ships per-registry deploy tokens, the controller will
switch to that endpoint and the operator's account token will no longer
appear in connection Secrets. Until then, treat the connection Secret as
account-token-equivalent — share it only with workloads you trust with
the full Timeweb scope.

## ProviderConfig resolution

Every MR references its credentials via `spec.providerConfigRef.{kind, name}`:

- `kind: ProviderConfig` — namespaced; the controller looks up a PC of
  this name in the MR's own namespace. The PC's `secretRef.namespace`
  may be omitted (defaults to the PC's namespace). Cross-namespace
  Secret references on this kind are rejected.
- `kind: ClusterProviderConfig` — cluster-scoped; the controller looks
  up a cluster PC of this name. `secretRef.namespace` is required.
- `kind:` omitted — the crossplane-runtime v2 default of
  `ClusterProviderConfig` applies.

The controller **hard-switches** on `kind` — there is no silent fallback
between the two. Operator-side mistakes (unknown kind, missing PC of the
declared kind, namespaced PC pointing at a Secret in a different
namespace, ClusterProviderConfig with empty `secretRef.namespace`)
surface as `Synced=False` with a typed `InvalidProviderConfigRef`
message. The crossplane-runtime overrides the reason to `ReconcileError`
on the condition itself but preserves the message verbatim (same
behavior as `PresetNotFound`).

## Locked-preset semantics

After the first successful Create:

- `status.atProvider.lockedPresetID` holds the resolved upstream ID.
- Editing `spec.forProvider.initialSizeGB` on a live MR causes the
  controller to re-resolve the new tier and PATCH the upstream resource
  (within-account tier moves are supported by Timeweb).
- Editing `spec.forProvider.location` similarly re-resolves.
- Deleting the CR with the default `managementPolicies: ['*']` cascades
  upstream Delete. Set `managementPolicies: [Observe]` to keep the
  upstream resource alive (e.g. for the `Project` import pattern).
