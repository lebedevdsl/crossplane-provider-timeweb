# `ContainerRegistry` (v1alpha1) — Timeweb-hosted Docker registry

A managed Docker registry on Timeweb. The controller publishes a
`kubernetes.io/dockerconfigjson` connection Secret operators can drop into
workloads as `imagePullSecrets`.

| Property | Value |
| -------- | ----- |
| API group | `kubernetes.m.timeweb.crossplane.io` |
| Kind | `ContainerRegistry` |
| Scope | Namespaced |
| External-name format | stringified Timeweb registry ID |
| Connection Secret | `kubernetes.io/dockerconfigjson` (keys: `.dockerconfigjson`, `endpoint`, `username`, `password`) |

## Manifest

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: demo-prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-prod
    description: "Production registry"
    # Pick the tier by disk size. Valid values: 5, 10, 25, 50, 75, 100 (GB).
    initialSizeGB: 5
    # Optional: narrow preset resolution when the account has multiple regions.
    # location: ru-1
  writeConnectionSecretToRef:
    name: demo-prod-pull
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | 3–48 chars, lowercase alphanumeric + hyphen. Immutable. |
| `description` | string | no | yes | Free-form note. |
| `initialSizeGB` | integer | yes | no | Tariff tier by disk size. Valid values: 5, 10, 25, 50, 75, 100. Immutable post-create — delete + recreate to change. |
| `location` | string | no | no | Region code (e.g. `ru-1`). Narrows preset resolution when the account has multiple regions. |
| `projectID` | integer | no | yes | Assign to a Timeweb project. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb registry ID. |
| `lockedPresetID` | integer | Resolved preset_id recorded at first successful create; survives upstream catalog rotations. |
| `projectID` | integer | Project assignment. |
| `diskStats.sizeGB` | integer | Tariff disk capacity. |
| `diskStats.usedGB` | integer | Used disk. |
| `createdAt`, `updatedAt` | string (RFC3339) | Upstream timestamps. |

## Connection Secret (type `kubernetes.io/dockerconfigjson`)

| Key | Source |
| --- | ------ |
| `.dockerconfigjson` | Marshaled docker config: `{"auths":{"<endpoint>":{"username":"…","password":"…","auth":"<base64>"}}}` |
| `endpoint` | `<name>.registry.twcstorage.ru` (derived from the registry name) |
| `username` | The registry name |
| `password` | The operator's Timeweb API token — **sensitive** |

### Using the Secret as an `imagePullSecret`

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app
  namespace: timeweb-prod
spec:
  imagePullSecrets:
  - name: demo-prod-pull
  containers:
  - name: app
    image: demo-prod.registry.twcstorage.ru/mygroup/myimage:v1
```

### Credentials caveat

Timeweb has no separate credential API for container registries — the
dashboard shows that docker login uses the **registry name as the username
and the account API token as the password**, and the controller synthesizes
the Secret from exactly that pair (no upstream lookup). Confirmed against the
dashboard's registry detail page.

Consequence: the Secret embeds the same API token the provider itself uses.
Anyone who can read the pull Secret can call the Timeweb API with the
account's rights — scope access to the Secret's namespace accordingly. When
Timeweb ships per-registry credentials, the controller will switch to
fetching those instead.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `PresetNotFound`, `APIError`, `RateLimited`. |
| `Ready` | Registry exists upstream. | `RegistryNotFound`, `Reconciling`. |

## Immutable-field handling

`name` and `initialSizeGB` (the tariff tier) are immutable. Editing either
triggers reject-and-surface:

1. Controller detects the diff against the live upstream.
2. `Synced=False, reason=ImmutableFieldChange` with a message naming the field.
3. Kubernetes Event (type `Warning`).
4. Upstream is NOT modified.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/container-registry/{id}` | Connection Secret is synthesized locally (name + API token). |
| Create | `POST /api/v1/container-registry` | Resolves `initialSizeGB` → `preset_id` first. |
| Update | `PATCH /api/v1/container-registry/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/container-registry/{id}` | 404 treated as success. |
