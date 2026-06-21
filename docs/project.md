# `Project` (v1alpha1) — Timeweb Cloud project

A logical grouping container in Timeweb Cloud. Resources (servers, buckets,
container registries, …) can be assigned to a project via their own
`spec.forProvider.projectID` field once it's published.

| Property | Value |
| -------- | ----- |
| API group | `project.m.timeweb.crossplane.io` |
| Kind | `Project` |
| Scope | Namespaced |
| Short name | `tw-project` |
| External-name format | stringified Timeweb project ID |
| Connection Secret | none |

## Manifest

```yaml
apiVersion: project.m.timeweb.crossplane.io/v1alpha1
kind: Project
metadata:
  name: demo
  namespace: timeweb-prod
spec:
  forProvider:
    name: "Demo Production"
    description: "Demo environment for example.com"
  providerConfigRef:
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | yes | Display name. 1-255 characters. |
| `description` | string | no | yes | Free-form description. Up to 255 characters. |
| `avatarId` | string | no | yes | Carried for upstream parity. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb project ID. Also encoded in the external-name annotation. |
| `accountId` | string | Upstream account identifier (e.g. `cp00001`). |
| `isDefault` | boolean | True when Timeweb marks this project as the account's default. |

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `APIError`, `RateLimited` (transient — clears next poll). |
| `Ready` | Project exists upstream and matches the spec. | `ProjectNotFound`, `Reconciling`. |

## Immutable fields

**None.** All `forProvider` fields are mutable on the upstream API. The
controller `Update` method PATCHes the changes in place; the immutable-field
reject-and-surface flow never fires for Project.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/projects/{id}` | 404 → `ResourceExists=false`. |
| Create | `POST /api/v1/projects` | Body: `{name, description, avatar_id}`. |
| Update | `PATCH /api/v1/projects/{id}` | Body: `{name?, description?, avatar_id?}`. |
| Delete | `DELETE /api/v1/projects/{id}` | 404 treated as success. |

## Import existing project

Set `metadata.annotations["crossplane.io/external-name"]` to the project's
numeric Timeweb ID before applying. The controller will Observe the existing
resource on the next reconciliation instead of creating a new one.

```yaml
apiVersion: project.m.timeweb.crossplane.io/v1alpha1
kind: Project
metadata:
  name: imported
  namespace: timeweb-prod
  annotations:
    crossplane.io/external-name: "12345"
spec:
  forProvider:
    name: "Existing Project"
  providerConfigRef:
    name: default
```
