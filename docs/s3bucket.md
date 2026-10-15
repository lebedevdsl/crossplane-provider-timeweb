# `S3Bucket` (v1alpha1) — Timeweb Cloud S3-compatible bucket

> Stuck? Start at [docs/troubleshooting.md](troubleshooting.md) — the get→describe→events→logs path, then the [condition reference](conditions.md).

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
    # Storage class by panel name: standard (recommended), hot (Premium), cold (1 GB only).
    storageClass: standard
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
| `initialSizeGB` | integer | yes | within class | Tariff tier by disk size. Valid values: 1, 10, 100, 250 (`cold`: 1 only). A change re-resolves the tier and PATCHes it; see [Tier changes](#tier-changes). |
| `storageClass` | enum | yes | **no** (create-time) | `standard` (panel "Standard", recommended), `hot` (panel "Premium" — `409 sold out` since 2026-10-14), `cold` (pay-as-you-go, 1 GB only). If upstream reports a different class, updates are refused until the spec is aligned — see [Vendor reclassification](#vendor-reclassification-storageclassdrift). |
| `location` | string | no | no | Region code (e.g. `ru-1`). Narrows preset resolution when the account has multiple regions. |
| `description` | string | no | yes | Free-form comment. |
| `projectID` | integer | no | yes | Assign bucket to a Timeweb project. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb bucket ID. |
| `hostname` | string | S3 endpoint URL. |
| `location` | string | Geographic region (read-only — Timeweb decides). |
| `storageClass` | string | Upstream-reported class: `hot`, `standard` or `cold`. May differ from `spec` after a vendor reclassification (surfaced by `StorageClassDrift`). |
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
| `Synced` | Reconciliation reached upstream cleanly. | `ImmutableFieldChange`, `StorageClassMismatch`, `PresetNotFound`, `APIError`, `RateLimited`. |
| `Ready` | Bucket exists upstream and is not quarantined. | `BucketNotFound`, `BucketQuarantined`, `PaymentRequired`, `Reconciling`. |
| `StorageClassDrift` | (additional type) upstream reports a different class than `spec` — reason `UpstreamReclassified`. | `Aligned` once they match again; absent on a bucket that never drifted. |

> Runtime gotcha: when the controller also returns an error (`ImmutableFieldChange`,
> `StorageClassMismatch`, `PresetNotFound`), crossplane-runtime overwrites the
> `Synced` reason with `ReconcileError` but keeps the message; the real reason is
> on the Warning Event — see [`docs/conditions.md`](./conditions.md).

## Tier changes

| Edit | Effect |
| ---- | ------ |
| `description`, `type`, `projectID` | PATCHed as-is. **No tier is sent** and no catalog lookup happens — the bucket's class and size are untouched (feature 028). |
| `initialSizeGB` (same class) | The controller re-resolves `(initialSizeGB, storageClass, location?)`, PATCHes `preset_id`, and refreshes `status.atProvider.lockedPresetID`. The size is compared against the upstream-observed disk, never against the locked preset. |
| `storageClass` ≠ upstream class | **Update refused fail-closed.** Nothing is sent; `Synced=False` with the message `storage class mismatch: spec declares "hot" but upstream reports "standard"; set spec.forProvider.storageClass to "standard" …` and a Warning Event `StorageClassMismatch`. Align the spec to the upstream value; the pending edit then applies. Cross-class moves on an existing bucket are not supported — delete and recreate, or change the class in the panel and align the spec. |
| `name` | Rejected: `Synced=False reason=ImmutableFieldChange`, Warning Event, upstream untouched. |

## Vendor reclassification (`StorageClassDrift`)

Timeweb can move a bucket to another storage class without any API call from
the provider (2026-10-13: every bucket bought as `hot` before 2026-09 became
`standard`; `hot` now means the Premium tier). The provider shows it and does
not touch it:

```yaml
status:
  atProvider:
    storageClass: standard      # upstream truth
  conditions:
  - type: StorageClassDrift
    status: "True"
    reason: UpstreamReclassified
    message: declared "hot", upstream reports "standard"; set spec.forProvider.storageClass to "standard" to align (no automatic re-tiering — a class change is a paid move)
```

- `Ready` and `Synced` stay `True` — the bucket serves; GitOps health is unaffected.
- No automatic re-tiering: the class is not part of the up-to-date check, so no
  PATCH is sent for the mismatch alone.
- To clear it, set `spec.forProvider.storageClass` to the upstream value. No
  request results; the condition flips to `False` / `Aligned` on the next
  observation.
- Until aligned, any other edit of that bucket is refused with
  `StorageClassMismatch` (see [Tier changes](#tier-changes)).

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/storages/buckets/{id}` | Populates `atProvider` + Connection Secret. |
| Create | `POST /api/v1/storages/buckets` | Body: name, type, preset_id OR configurator, description?, project_id?. |
| Update | `PATCH /api/v1/storages/buckets/{id}` | Mutable subset only. |
| Delete | `DELETE /api/v1/storages/buckets/{id}` | 404 treated as success. |
