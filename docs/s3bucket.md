# `S3Bucket` (v1alpha1) — Timeweb Cloud S3-compatible bucket

An S3-compatible object-storage bucket. The controller publishes an Opaque
connection Secret with `endpoint`, `bucket`, and `region` — non-secret
metadata only. **Credentials are NOT emitted** (since v0.4.0): they were the
account-admin super-user keys with full access to every bucket. Scoped
credentials come from the [`S3User`](./s3user.md) kind instead.

| Property | Value |
| -------- | ----- |
| API group | `objectstorage.m.timeweb.crossplane.io` |
| Kind | `S3Bucket` |
| Scope | Namespaced |
| External-name format | stringified Timeweb bucket ID |
| Connection Secret | `Opaque` (keys: `endpoint`, `bucket`, `region`) |

## Manifest

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: demo-images
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-images
    type: public
    # Pick the tier by disk size. Valid values: 1, 10, 100, 250 (GB).
    initialSizeGB: 10
    # Storage class: hot (frequently accessed) or cold (archives). Immutable.
    storageClass: hot
    description: "User-uploaded images for example.com"
  writeConnectionSecretToRef:
    name: demo-images-creds
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | Globally unique. 3–63 chars, `^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`. Immutable upstream. |
| `type` | enum | yes | yes | `private` or `public`. |
| `initialSizeGB` | integer | yes | no | Tariff tier by disk size. Valid values: 1, 10, 100, 250. Immutable — delete + recreate to change. |
| `storageClass` | enum | yes | **no** | `hot` (frequent access) or `cold` (archives). Immutable. |
| `location` | string | no | no | Region code (e.g. `ru-1`). Narrows preset resolution when the account has multiple regions. |
| `description` | string | no | yes | Free-form comment. |
| `projectID` | integer | no | yes | Assign bucket to a Timeweb project. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb bucket ID. |
| `hostname` | string | S3 endpoint URL. |
| `location` | string | Geographic region (read-only — Timeweb decides). |
| `storageClass` | string | `cold` or `hot` (read-only — derived from preset/configurator). |
| `status` | string | Upstream status (`active`, etc). |
| `diskStats` | object | `sizeKB`, `usedKB`, `isUnlimited`. |
| `objectAmount` | integer | File count. |
| `movedInQuarantineAt` | string (RFC3339, optional) | Non-null when the bucket is quarantined. |
| `attachedUsers` | list | Read-only mirror of which `S3User`s hold a grant on this bucket and at what level (`{name, accessLevel}`). Observational only — `S3User` is the sole writer of grants. |

## Connection Secret (type `Opaque`)

| Key | Source | Notes |
| --- | ------ | ----- |
| `endpoint` | `atProvider.hostname` | S3 endpoint URL. |
| `bucket` | upstream `name` | Bucket name (matches `spec.forProvider.name`). |
| `region` | `atProvider.location` | Geographic region. |

**No `access_key`/`secret_key`.** Since v0.4.0 the bucket Secret carries only
non-secret metadata. To talk to the bucket, create an [`S3User`](./s3user.md)
with a `bucketAccess` grant and consume *its* connection Secret — it publishes
scoped `access_key`/`secret_key` (plus `endpoint`/`bucket`/`buckets`) that
authorize only the granted buckets.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `APIError`, `RateLimited`. |
| `Ready` | Bucket exists upstream and is not quarantined. | `BucketNotFound`, `BucketQuarantined`, `Reconciling`. |

## Immutable-field handling

Editing `spec.forProvider.name`, `initialSizeGB`, or `storageClass` triggers
reject-and-surface:

1. Controller GETs the upstream bucket, detects the diff.
2. `Synced` flips to `False` with `reason=ImmutableFieldChange` naming the field.
3. A Kubernetes Event (type `Warning`, reason `ImmutableFieldChange`) is emitted.
4. Upstream is NOT modified.

To change the tariff tier or storage class, delete and recreate the bucket.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/storages/buckets/{id}` | Populates `atProvider` + Connection Secret. |
| Create | `POST /api/v1/storages/buckets` | Body: name, type, preset_id OR configurator, description?, project_id?. |
| Update | `PATCH /api/v1/storages/buckets/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/storages/buckets/{id}` | 404 treated as success. |
