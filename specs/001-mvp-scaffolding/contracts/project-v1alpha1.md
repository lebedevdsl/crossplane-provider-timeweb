# Contract: `Project` (v1alpha1)

**Group/Version**: `project.m.timeweb.crossplane.io/v1alpha1`
**Kind**: `Project` | **Scope**: `Namespaced`
**Short name**: `tw-project`

A Timeweb project (resource-grouping container). All fields are mutable; no
immutable-field rejection paths apply.

## Manifest

```yaml
apiVersion: project.m.timeweb.crossplane.io/v1alpha1
kind: Project
metadata:
  name: prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: "Demo Production"
    description: "Production environment for example.com"
  providerConfigRef:
    name: default
```

## Validation contract

- `spec.forProvider.name`: required, length 1–255.
- `spec.forProvider.description`: optional, ≤255 chars, may be `null`.
- `spec.forProvider.avatarID`: optional, ≤255 chars; carried for upstream parity.
- `spec.providerConfigRef.name`: required.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Latest reconciliation reached upstream cleanly. | `ProviderConfigInvalid`, `APIError`, `RateLimited` (transient — usually clears next poll). |
| `Ready` | Upstream project exists and matches spec. | `ProjectNotFound`, `Reconciling`. |

## External-name

Stringified Timeweb project ID. Set by the controller on first successful Create. May
be set manually by the operator for import (the controller will then `Observe` the
existing project instead of creating a new one).

## Connection Secret

None — projects have no consumable connection info.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/projects/{id}` | 404 → `ResourceNotFound`. |
| Create | `POST /api/v1/projects` | Body: `create-project` schema. |
| Update | `PATCH /api/v1/projects/{id}` | Body: `update-project` schema. All current fields mutable. |
| Delete | `DELETE /api/v1/projects/{id}` | 404 treated as success. |
