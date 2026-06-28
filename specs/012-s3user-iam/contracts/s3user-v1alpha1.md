# Contract — S3User v1alpha1 (`objectstorage.m.timeweb.crossplane.io`)

Operator-facing CRD contract. Field semantics in `../data-model.md`; behaviour in `../spec.md`.

## Spec (`forProvider`)

| Field | Type | Required | Mutable | Notes |
|---|---|---|---|---|
| `name` | string | ✅ | ❌ | IAM user name; 1–250 chars; immutable after create |
| `bucketAccess` | list | — | ✅ | ≤64 items; each bucket at most once (duplicates → `Synced=False`) |
| `bucketAccess[].bucketRef` | reference | one-of | ✅ | references an `S3Bucket` in the same namespace |
| `bucketAccess[].bucketName` | string | one-of | ✅ | direct bucket name (unmanaged buckets) |
| `bucketAccess[].accessLevel` | enum | ✅ | ✅ | `read` \| `read-write` \| `admin` |
| `projectID` | int | — | ✅ | optional Timeweb project assignment |

Exactly one of `bucketRef` / `bucketName` per item. Empty `bucketAccess` ⇒ identity with only the
account-wide `s3:ListAllMyBuckets` permission (valid; "revoked from every bucket").

## Status (`atProvider`)

| Field | Type | Notes |
|---|---|---|
| `id` | string | upstream IAM user UUID (= external-name) |
| `status` | string | upstream user status (e.g. `active`) |
| `accessKeyID` | string | non-secret access key id (secret key only in the connection Secret) |
| `policyHash` | string | stable hash of rendered desired policy (drift signal) |
| `resolvedBuckets` | list | `{bucketName, accessLevel}` actually applied |

SC contract: the upstream id, user status, applied grants, and access key id are answerable from
status alone; the secret key never appears in status.

## Connection Secret (`writeConnectionSecretToRef`)

`access_key`, `secret_key` (scoped — never account-admin), `endpoint` (S3 data host), `bucket`.

## Conditions

| Situation | Synced | Ready | Reason |
|---|---|---|---|
| Created, policy converged | True | True | Available |
| Awaiting `bucketRef` (missing / not Ready) | False | False | `ParentNotReady` |
| Duplicate resolved bucket | False | — | `InvalidConfiguration` |
| Immutable `name` change attempted | False | — | `ImmutableFieldChange` |
| Upstream terminal 4xx (e.g. malformed policy) | False | — | `APIError` |
| Transient (5xx/429/timeout/Qrator) | unchanged | unchanged | requeue, no flap |
| `deletionTimestamp` set | False | False | Deleting |

## Events

Emitted on: create (identity + policy attach), grant add/change/remove (`PutUserPolicy`),
immutable-change rejection, parent-not-ready, terminal upstream error, delete.
