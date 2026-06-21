# `ContainerRegistryRepository` (v1alpha1, observe-only)

A repository (image namespace) inside a parent `ContainerRegistry`.

| Property | Value |
| -------- | ----- |
| API group | `kubernetes.m.timeweb.crossplane.io` |
| Kind | `ContainerRegistryRepository` |
| Scope | Namespaced |
| External-name format | `<parent-registry-name>/<repository-name>` (composite, R-2) |
| Connection Secret | none — use the parent registry's Secret |

## API-level constraint: observe-only

Timeweb's API exposes **only** `GET /api/v1/container-registry/{id}/repositories`
— there is no per-repository CRUD endpoint. Consequently, this managed
resource is **observe-only**:

- **Create**: a no-op upstream. The CR's external-name annotation is set
  immediately so Crossplane considers the resource "created"; the upstream
  repository materializes when an operator runs `docker push` against the
  parent registry. Until then the CR reports `Ready=False, reason=RepositoryNotPushed`.
- **Update**: a no-op (repositories have no mutable fields anyway).
- **Delete (CR)**: removes the Kubernetes object but does NOT touch the
  upstream repository. A Kubernetes Event with reason `DeleteNoOp` is
  emitted to make the asymmetry explicit. Use `docker rmi` against the
  registry endpoint or the Timeweb dashboard for image-level cleanup.

## Manifest

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistryRepository
metadata:
  name: backend
  namespace: timeweb-prod
spec:
  forProvider:
    registryRef:
      name: demo-prod
    name: mygroup/backend
  providerConfigRef:
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `registryRef.name` | string | yes | **no** | Parent ContainerRegistry in the same namespace. |
| `name` | string | yes | **no** | Repository path (e.g. `mygroup/myimage`). Matches what you `docker push` to. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `tags` | list of `{tag, digest, sizeBytes}` | All tags currently published in the repository. |
| `tagCount` | integer | Convenience for `kubectl get`. |

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `APIError`, `RateLimited`. |
| `Ready` | Upstream repository exists. | `RepositoryNotPushed` (run `docker push` first), `ParentNotReady`. |
