# Contract: `S3Bucket` (v1alpha1)

**Group/Version**: `objectstorage.timeweb.crossplane.io/v1alpha1`
**Kind**: `S3Bucket` | **Scope**: `Namespaced`
**Short name**: `tw-s3`

A Timeweb S3-compatible object storage bucket. Several create-time fields are
immutable (region, name, storage class, sizing axis); the description, type
(private/public), auto-upgrade flag, and project assignment can be updated.

## Manifest

```yaml
apiVersion: objectstorage.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: demo-images
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-images
    location: ru-1
    type: public
    storageClass: standard
    presetID: 1939                # OR `configuration: {id, disk}` — not both
    description: "User-uploaded images for example.com"
    isAllowAutoUpgrade: false
    projectID: 12345              # optional
  writeConnectionSecretToRef:
    name: demo-images-creds
    # namespace defaults to the MR's namespace
  providerConfigRef:
    name: default
```

## Validation contract

- `name`: required, MUST match `^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`.
- `location`: required, non-empty string (catalog values such as `ru-1`).
- `type`: required, enum `["private", "public"]`.
- `storageClass`: required, enum `["cold", "standard"]`.
- Exactly one of `presetID` or `configuration` MUST be present (CEL validation on the
  CRD; admission rejects when both or neither are set).
- `configuration.id`, `configuration.disk` both required when `configuration` is set.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `ProviderConfigInvalid`, `APIError`, `RateLimited`. |
| `Ready` | Bucket exists upstream and is not quarantined. | `BucketNotFound`, `BucketQuarantined` (when `atProvider.movedInQuarantineAt` is non-null), `Reconciling`. |

## Immutable fields (FR-017 reject-and-surface)

- `name`
- `location`
- `storageClass`
- The chosen sizing axis (`presetID` vs `configuration`) — values within the same axis
  are mutable; switching axes is rejected.

## External-name

Stringified bucket ID.

## Connection Secret (type `Opaque`)

| Key | Source |
| --- | ------ |
| `endpoint` | `atProvider.hostname` |
| `bucket` | `atProvider.name` |
| `region` | `forProvider.location` |
| `access_key` | upstream `bucket.access_key` (sensitive) |
| `secret_key` | upstream `bucket.secret_key` (sensitive) |

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/storages/buckets/{id}` | |
| Create | `POST /api/v1/storages/buckets` | |
| Update | `PATCH /api/v1/storages/buckets/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/storages/buckets/{id}` | |
