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
| `endpoint` | `<name>.cr.twcstorage.ru` (derived from registry name; verify per deployment) |
| `username` | First storage-user's `access_key` (see "Credentials" below) |
| `password` | First storage-user's `secret_key` — **sensitive** |

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
    image: demo-prod.cr.twcstorage.ru/mygroup/myimage:v1
```

### Credentials caveat

The Timeweb OpenAPI document does not expose a registry-specific auth
endpoint. The controller's default implementation reads the first
storage-user (`GET /api/v1/storages/users`) and uses its `access_key`/
`secret_key` as the docker username/password. This is a best-effort
default; verify against your account's actual registry credentials.

If `GET /api/v1/storages/users` returns 403 or an empty list, the registry
CR reaches `Synced=True, Ready=False, reason=CredentialsPending` —
operators can either:
- Open an issue describing the actual mechanism, or
- Patch the connection Secret out-of-band with the real credentials
  (subsequent reconciles will overwrite if the API later becomes available).

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `PresetNotFound`, `APIError`, `RateLimited`. |
| `Ready` | Registry exists upstream AND credentials are usable. | `CredentialsPending`, `RegistryNotFound`, `Reconciling`. |

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
| Observe | `GET /api/v1/container-registry/{id}` + `GET /api/v1/storages/users` | The latter is used for the connection Secret. |
| Create | `POST /api/v1/container-registry` | Resolves `initialSizeGB` → `preset_id` first. |
| Update | `PATCH /api/v1/container-registry/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/container-registry/{id}` | 404 treated as success. |
