# `S3Bucket` (v1alpha1) — Timeweb Cloud S3-compatible bucket

An S3-compatible object-storage bucket. The controller publishes an Opaque
connection Secret with `endpoint`, `bucket`, `region`, `access_key`, and
`secret_key` — the values needed to talk to the bucket from any S3 client
library.

| Property | Value |
| -------- | ----- |
| API group | `objectstorage.m.timeweb.crossplane.io` |
| Kind | `S3Bucket` |
| Scope | Namespaced |
| External-name format | stringified Timeweb bucket ID |
| Connection Secret | `Opaque` (keys: `endpoint`, `bucket`, `region`, `access_key`, `secret_key`) |

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
    presetID: 100
    description: "User-uploaded images for example.com"
  writeConnectionSecretToRef:
    name: demo-images-creds
  providerConfigRef:
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | Globally unique. 3–63 chars, `^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`. Immutable upstream. |
| `type` | enum | yes | yes | `private` or `public`. |
| `presetID` | integer | one of `presetID`/`configuration` | within-axis only | Tariff plan ID. Mutually exclusive with `configuration`. |
| `configuration.id` | integer | when `configuration` is set | within-axis only | Custom configurator ID. |
| `configuration.diskMB` | integer | when `configuration` is set | within-axis only | Disk size in MB. |
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

## Connection Secret (type `Opaque`)

| Key | Source | Notes |
| --- | ------ | ----- |
| `endpoint` | `atProvider.hostname` | S3 endpoint URL. |
| `bucket` | upstream `name` | Bucket name (matches `spec.forProvider.name`). |
| `region` | `atProvider.location` | Geographic region. |
| `access_key` | upstream `access_key` | S3 access key — **sensitive**. |
| `secret_key` | upstream `secret_key` | S3 secret key — **sensitive**. |

Reference the Secret via `envFrom` to make all five available as env vars,
or via `volumeMounts.secretName` to mount as files. The standard
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_ENDPOINT_URL_S3`
environment variables your S3 SDK expects can be remapped from these keys
via `Secret.data` mapping or a one-line `subPath`.

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `APIError`, `RateLimited`. |
| `Ready` | Bucket exists upstream and is not quarantined. | `BucketNotFound`, `BucketQuarantined`, `Reconciling`. |

## Immutable-field handling (FR-017)

Editing `spec.forProvider.name`, or switching between `presetID` and
`configuration` (the sizing axis), triggers reject-and-surface:

1. Controller GETs the upstream bucket, detects the diff.
2. `Synced` flips to `False` with `reason=ImmutableFieldChange` naming the field.
3. A Kubernetes Event (type `Warning`, reason `ImmutableFieldChange`) is emitted.
4. Upstream is NOT modified.

Within-axis changes (different `presetID` value, or different `configuration.id`/
`configuration.diskMB` while staying on the configurator axis) are mutable.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/storages/buckets/{id}` | Populates `atProvider` + Connection Secret. |
| Create | `POST /api/v1/storages/buckets` | Body: name, type, preset_id OR configurator, description?, project_id?. |
| Update | `PATCH /api/v1/storages/buckets/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/storages/buckets/{id}` | 404 treated as success. |
