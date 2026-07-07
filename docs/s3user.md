# `S3User` (v1alpha1) — scoped object-storage credentials

A scoped, least-privilege object-storage IAM user. This is the credentials
counterpart to [`S3Bucket`](./s3bucket.md): the bucket kind provisions storage
and publishes non-secret metadata; the `S3User` grants access to specific
buckets and publishes the keys. It replaces the account-admin keys `S3Bucket`
handed out before v0.4.0.

| Property | Value |
| -------- | ----- |
| API group | `objectstorage.m.timeweb.crossplane.io` |
| Kind | `S3User` |
| Scope | Namespaced |
| External-name format | upstream IAM user UUID |
| Connection Secret | `Opaque` (keys: `access_key`, `secret_key`, `endpoint`, `bucket`, `buckets`) |

## Manifest

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3User
metadata:
  name: app-data-rw
  namespace: timeweb-prod
spec:
  forProvider:
    name: app-data-rw            # IMMUTABLE — the IAM user name
    bucketAccess:
      - bucketRef:
          name: demo-images      # S3Bucket in this namespace
        accessLevel: read-write
      - bucketName: shared-assets   # or a direct name (unmanaged bucket)
        accessLevel: read
  writeConnectionSecretToRef:
    name: app-data-rw-creds
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | IAM user name, 1–250 chars. Immutable after create. |
| `bucketAccess` | list | no | yes | ≤64 items; each bucket at most once (duplicates → `Synced=False`). Empty list = identity with only `s3:ListAllMyBuckets` ("revoked from every bucket"). |
| `bucketAccess[].bucketRef` | reference | one-of | yes | An `S3Bucket` in the same namespace. |
| `bucketAccess[].bucketName` | string | one-of | yes | Direct bucket name — for buckets not managed by Crossplane. |
| `bucketAccess[].accessLevel` | enum | yes | yes | `read`, `read-write`, or `admin`. |
| `projectID` | integer | no | yes | Optional Timeweb project assignment. |

Exactly one of `bucketRef` / `bucketName` per item. Grant changes apply in
place — the issued keys do **not** rotate.

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | string | Upstream IAM user UUID (= external-name). |
| `status` | string | Upstream user status (e.g. `active`). |
| `accessKeyID` | string | Non-secret access key id (the secret key lives only in the connection Secret). |
| `policyHash` | string | Stable hash of the rendered desired policy (drift signal). |
| `resolvedBuckets` | list | `{bucketName, accessLevel}` actually applied. |

## Connection Secret (type `Opaque`)

| Key | Notes |
| --- | ----- |
| `access_key` | Scoped S3 access key — **sensitive**. Never account-admin. |
| `secret_key` | Scoped S3 secret key — **sensitive**. |
| `endpoint` | S3 data endpoint. |
| `bucket` | The primary (first, sorted) granted bucket — single-bucket consumers read this. |
| `buckets` | Comma-separated, sorted list of **every** granted bucket (since v0.4.1). The same key pair authorizes all of them: `IFS=, read -ra BUCKETS <<<"$buckets"`. |

## How grants work

All of a user's grants render to **one merged inline policy**
(`iam-user-policy`) — the same convention the Timeweb panel reads and writes,
so panel and provider stay interoperable. Drift detection is semantic
(Sid- and order-insensitive): dashboard edits are reverted to the declared
state on the next reconcile (single-writer). The bucket side is mirrored
read-only at `S3Bucket.status.atProvider.attachedUsers`.

## Conditions

| Situation | Synced | Ready | Reason |
| --------- | ------ | ----- | ------ |
| Created, policy converged | True | True | `Available` |
| Awaiting `bucketRef` (missing / not Ready) | False | False | `ParentNotReady` |
| Duplicate resolved bucket | False | — | `InvalidConfiguration` |
| Immutable `name` change attempted | False | — | `ImmutableFieldChange` |
| Upstream terminal 4xx | False | — | `APIError` |
| Transient (5xx/429/timeout) | unchanged | unchanged | requeue, no flap |
| Deletion in progress | False | False | `Deleting` |

Deletion does not deadlock when the referenced `S3Bucket` is removed first:
the `bucketRef` gate is relaxed during deletion, so the finalizer clears and
the identity + policy still delete by external-name.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v2/storages/users/{id}` + IAM `GetUserPolicy` | Semantic policy diff. |
| Create | `POST /api/v2/storages/users` + IAM `PutUserPolicy` | Identity, then merged policy. |
| Update | IAM `PutUserPolicy` (+ REST PATCH for identity fields) | In-place; keys unchanged. |
| Delete | IAM `DeleteUserPolicy` + `DELETE /api/v2/storages/users/{id}` | 404 treated as success. |

Grants go through the AWS IAM Query API (SigV4-signed against
`panel.s3.twcstorage.ru`); identity through the Timeweb REST API. Both
authenticate from the single `ProviderConfig` token: the IAM signing keys are
derived from it at runtime, so no additional credentials are configured.
