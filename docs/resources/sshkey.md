# `SSHKey` (v1alpha1) — Timeweb Cloud SSH key

An SSH public key registered on the Timeweb account. The key body and display
name are immutable; key rotation is delete-and-recreate.

| Property | Value |
| -------- | ----- |
| API group | `sshkey.m.timeweb.crossplane.io` |
| Kind | `SSHKey` |
| Scope | Namespaced |
| External-name format | stringified Timeweb SSH key ID |
| Connection Secret | none (public key is in `spec`/`status`) |

## Manifest

```yaml
apiVersion: sshkey.m.timeweb.crossplane.io/v1alpha1
kind: SSHKey
metadata:
  name: demo-key
  namespace: timeweb-prod
spec:
  forProvider:
    name: "demo-key"
    body: "ssh-ed25519 AAAAC3Nz... demo@example.com"
    isDefault: false
  providerConfigRef:
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | Display name. 1–255 chars. Immutable upstream. |
| `body` | string | yes | **no** | OpenSSH public key body. Pattern: starts with `ssh-rsa`, `ssh-ed25519`, `ssh-dss`, or `ecdsa-sha2-*`. Immutable upstream. |
| `isDefault` | boolean | no | yes | Mark as the account's default for new servers. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb SSH key ID. |
| `createdAt` | string (RFC3339) | Upstream creation timestamp. |
| `usedBy` | list of `{id, name}` | Servers currently referencing this key. |

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange` (terminal — stays False until reverted or recreated), `APIError`, `RateLimited`. |
| `Ready` | Upstream key exists and matches spec. | `SSHKeyNotFound`, `Reconciling`. |

## Immutable-field handling (FR-017)

Editing `spec.forProvider.name` or `spec.forProvider.body` after the resource
is `Ready=True`:

1. Controller GETs the upstream key and detects the diff.
2. `Synced` flips to `False` with `reason=ImmutableFieldChange` and a message
   naming the offending field.
3. A Kubernetes `Event` of type `Warning` with `reason=ImmutableFieldChange`
   is emitted on the SSHKey resource.
4. The upstream key is NOT modified.

To proceed, either revert the spec back to the original value, or
`kubectl delete` the resource and re-apply it with the new values.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/ssh-keys/{id}` | |
| Create | `POST /api/v1/ssh-keys` | Body: `{name, body, is_default}`. |
| Update | `PATCH /api/v1/ssh-keys/{id}` | Body restricted to `is_default` (the only mutable field). |
| Delete | `DELETE /api/v1/ssh-keys/{id}` | 404 treated as success. |

## Import an existing key

Set `metadata.annotations["crossplane.io/external-name"]` to the SSH key's
numeric Timeweb ID before applying. The controller observes the existing
resource on the next reconciliation instead of creating a new one.

Use `spec.managementPolicies: [Observe]` for read-only adoption when the
provider token lacks SSH-key write permission.
