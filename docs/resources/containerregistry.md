# `ContainerRegistry` (v1alpha1) — Timeweb-hosted Docker registry

A managed Docker registry on Timeweb. The controller publishes a
`kubernetes.io/dockerconfigjson` connection Secret operators can drop into
workloads as `imagePullSecrets`.

| Property | Value |
| -------- | ----- |
| API group | `containerregistry.m.timeweb.crossplane.io` |
| Kind | `ContainerRegistry` |
| Scope | Namespaced |
| External-name format | stringified Timeweb registry ID |
| Connection Secret | `kubernetes.io/dockerconfigjson` (keys: `.dockerconfigjson`, `endpoint`, `username`, `password`) |

## Manifest

```yaml
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: demo-prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-prod
    description: "Production registry"
    presetRef:
      name: cr-starter-5gb-1939
  writeConnectionSecretToRef:
    name: demo-prod-pull
  providerConfigRef:
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | 3–48 chars, lowercase alphanumeric + hyphen. Immutable. |
| `description` | string | no | yes | Free-form note. |
| `presetRef.name` | string | one of `presetRef`/`configuration` | within-axis | References a `ContainerRegistryPreset` by Kubernetes name. The controller resolves to the numeric `preset_id` at create time. Switching axes is immutable. |
| `configuration.id` | integer | one of `presetRef`/`configuration` | within-axis | Custom configurator ID. |
| `configuration.diskGB` | integer | when `configuration` is set | within-axis | Disk capacity (GB). |
| `projectID` | integer | no | yes | Assign to a Timeweb project. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb registry ID. |
| `presetID` | integer | Resolved preset_id snapshot. |
| `configuratorID` | integer | Resolved configurator_id snapshot. |
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

### Credentials — R-1 caveat

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
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `PresetReferenceNotFound`, `APIError`, `RateLimited`. |
| `Ready` | Registry exists upstream AND credentials are usable. | `CredentialsPending`, `RegistryNotFound`, `Reconciling`. |

## Immutable-field handling (FR-017)

`name` and the sizing axis (preset ↔ configuration) are immutable. Editing
either triggers reject-and-surface:

1. Controller detects the diff against the live upstream.
2. `Synced=False, reason=ImmutableFieldChange` with a message naming the field.
3. Kubernetes Event (type `Warning`).
4. Upstream is NOT modified.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/container-registry/{id}` + `GET /api/v1/storages/users` | The latter is used for the connection Secret. |
| Create | `POST /api/v1/container-registry` | Resolves `presetRef` → `preset_id` first. |
| Update | `PATCH /api/v1/container-registry/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/container-registry/{id}` | 404 treated as success. |
