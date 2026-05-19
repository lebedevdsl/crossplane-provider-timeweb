# Contract: `SshKey` (v1alpha1)

**Group/Version**: `sshkey.timeweb.crossplane.io/v1alpha1`
**Kind**: `SshKey` | **Scope**: `Namespaced`
**Short name**: `tw-sshkey`

A Timeweb SSH key registered on the account. Its `body` (the public key material) is
immutable; rotating the key requires recreating the resource.

## Manifest

```yaml
apiVersion: sshkey.timeweb.crossplane.io/v1alpha1
kind: SshKey
metadata:
  name: mariadb-admin
  namespace: timeweb-prod
spec:
  forProvider:
    name: "mariadb-admin"
    body: "ssh-ed25519 AAAAC3Nz... admin@example.com"
    isDefault: false
  providerConfigRef:
    name: default
```

## Validation contract

- `spec.forProvider.name`: required, 1–255 chars.
- `spec.forProvider.body`: required, MUST match
  `^(ssh-rsa|ssh-ed25519|ssh-dss|ecdsa-sha2-[a-z0-9]+) `.
- `spec.forProvider.isDefault`: optional boolean, defaults to `false`.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange` (terminal — stays False until reverted or recreated), `ProviderConfigInvalid`, `APIError`, `RateLimited`. |
| `Ready` | Upstream key exists and matches spec. | `SshKeyNotFound`, `Reconciling`. |

## Immutable-field handling (FR-017)

If `spec.forProvider.name` or `spec.forProvider.body` is edited after creation:
- The controller MUST NOT call the upstream PATCH endpoint.
- The MR transitions to `Synced=False, reason=ImmutableFieldChange,
  message="<field> is immutable; revert the change or delete and recreate the
  resource."`.
- A Kubernetes `Event` (type `Warning`, reason `ImmutableFieldChange`) is emitted.
- The operator's options: revert the spec, or delete the MR and create a new one with
  the new value.

## External-name

Stringified SSH key ID.

## Connection Secret

None — public key is in `spec`/`status`, not a credential.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/ssh-keys/{id}` | 404 → `ResourceNotFound`. |
| Create | `POST /api/v1/ssh-keys` | Body: `{name, body, is_default}`. |
| Update | `PATCH /api/v1/ssh-keys/{id}` | Only `is_default` is mutable. Body restricted to mutable fields. |
| Delete | `DELETE /api/v1/ssh-keys/{id}` | 404 treated as success. |
