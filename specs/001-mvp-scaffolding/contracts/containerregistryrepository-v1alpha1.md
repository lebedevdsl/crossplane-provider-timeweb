# Contract: `ContainerRegistryRepository` (v1alpha1)

**Group/Version**: `containerregistry.timeweb.crossplane.io/v1alpha1`
**Kind**: `ContainerRegistryRepository` | **Scope**: `Namespaced`
**Short name**: `tw-cr-repo`

A repository inside a `ContainerRegistry`. Repositories are created implicitly upstream
by `docker push`; this CRD provides declarative *lifecycle* control (notably deletion).
The MVP supports Observe + Delete; Update is a no-op for v0.1.

## Manifest

```yaml
apiVersion: containerregistry.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistryRepository
metadata:
  name: demo-prod-backend
  namespace: timeweb-prod
spec:
  forProvider:
    registryRef:
      name: demo-prod                     # must reference an existing ContainerRegistry
                                           # in the same namespace
    name: mygroup/backend                    # repository path within the registry
  providerConfigRef:
    name: default                          # must equal the parent registry's
```

## Validation contract

- `forProvider.name`: required, MUST match Docker repository name segment rules
  (`^[a-z0-9]+([._\-][a-z0-9]+)*(/[a-z0-9]+([._\-][a-z0-9]+)*)*$`).
- `forProvider.registryRef.name`: required, MUST resolve to an existing
  `ContainerRegistry` in the same namespace.
- `providerConfigRef.name`: MUST equal the parent registry's `providerConfigRef.name`
  (admission-level CEL rule). Prevents accidental cross-tenant references.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ParentNotFound`, `ProviderConfigInvalid`, `APIError`, `RateLimited`. |
| `Ready` | Repository exists upstream. | `RepositoryNotPushed` (info — operator must `docker push` first), `Reconciling`. |

## Immutable fields (FR-017)

- `registryRef.name` (cannot move a repository between registries).
- `forProvider.name` (renaming would mean creating a new repository).

## External-name

`<parent-registry-name>/<repository-name>` (R-2 composite encoding).

## Connection Secret

None — the parent registry's Secret already serves the auth needs.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/container-registry/{registry_id}/repositories` and filter by name | The endpoint returns a list; the controller filters client-side. |
| Create | n/a | Repositories are created by `docker push` on the client side. The MVP controller does NOT call any upstream "create repository" endpoint; the `Ready` condition reaches True only after the operator has pushed at least one image. |
| Update | n/a (MVP) | Repository fields are all immutable. Reserved for v0.2+ when policies (e.g. immutable tags) may become editable. |
| Delete | `DELETE /api/v1/container-registry/{registry_id}/repositories/{repository_name}` | 404 treated as success. |
