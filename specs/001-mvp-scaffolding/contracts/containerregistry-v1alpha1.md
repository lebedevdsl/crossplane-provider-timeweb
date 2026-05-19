# Contract: `ContainerRegistry` (v1alpha1)

**Group/Version**: `containerregistry.timeweb.crossplane.io/v1alpha1`
**Kind**: `ContainerRegistry` | **Scope**: `Namespaced`
**Short name**: `tw-cr`

A Timeweb-hosted Docker registry. Operators reference a `ContainerRegistryPreset` by
Kubernetes name (the controller resolves to the upstream numeric `preset_id`), so
manifests stay readable across catalog churn.

## Manifest

```yaml
apiVersion: containerregistry.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: demo-prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-prod                       # 3-48 chars, lowercase alnum + hyphen
    description: "Production registry for example.com images"
    presetRef:
      name: cr-starter-5gb                 # references a ContainerRegistryPreset
    # OR
    # configuration: { id: 7, disk: 20 }
    projectID: 12345                       # optional
  writeConnectionSecretToRef:
    name: demo-prod-pull
    # namespace defaults to MR's namespace
  providerConfigRef:
    name: default
```

## Validation contract

- `name`: required, MUST match `^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$` (3–48 chars).
- Exactly one of `presetRef` or `configuration` MUST be set (CEL rule).
- `presetRef.name`: when set, MUST resolve to a `ContainerRegistryPreset` reconciled by
  the provider. Resolution failure → `Synced=False, reason=PresetReferenceNotFound`.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `PresetReferenceNotFound`, `ProviderConfigInvalid`, `APIError`, `RateLimited`. |
| `Ready` | Registry exists upstream and connection Secret is populated. | `RegistryNotFound`, `CredentialsPending` (when the credential lookup, see R-1, hasn't completed yet), `Reconciling`. |

## Immutable fields (FR-017 reject-and-surface)

- `name`
- The chosen sizing axis (`presetRef` vs `configuration`). Values within the same axis
  are mutable (e.g. switching from `cr-starter-5gb` to `cr-team-20gb` is permitted).

## External-name

Stringified registry ID.

## Connection Secret (type `kubernetes.io/dockerconfigjson`)

| Key | Source |
| --- | ------ |
| `.dockerconfigjson` | Marshaled docker config: `{"auths":{"<endpoint>":{"username":"…","password":"…","auth":"<base64>"}}}` |
| `endpoint` | Registry URL (see R-1) |
| `username` | From credentials lookup (see R-1) |
| `password` | From credentials lookup (see R-1, sensitive) |

> **Note**: The credentials source is documented in [research.md §R-1](../research.md#r-1-container-registry-credential-model)
> as an open item: the OpenAPI does not expose a registry-specific auth endpoint. The
> controller will use the storage-users endpoint by default; the implementation PR
> updates [docs/resources/containerregistry.md](../../../docs/resources/containerregistry.md)
> with the live behaviour.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/container-registry/{id}` | |
| Create | `POST /api/v1/container-registry` | Body: `RegistryIn` (with resolved `preset_id`). |
| Update | `PATCH /api/v1/container-registry/{id}` | Body: `RegistryEdit`. Mutable: description, preset_id within axis, configuration within axis. |
| Delete | `DELETE /api/v1/container-registry/{id}` | |
