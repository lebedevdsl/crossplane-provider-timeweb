# Contract — S3Bucket redesign v1alpha1 (feature 012)

Changes to the **existing** `S3Bucket` kind (`objectstorage.m.timeweb.crossplane.io`). Spec
`forProvider` is **unchanged**; the deltas are in the connection Secret and `status.atProvider`.

## Connection Secret — BREAKING change

| Key | Before (≤ v0.3.x) | After (feature 012) |
|---|---|---|
| `endpoint` | ✅ present | ✅ present (S3 data host) |
| `bucket` | ✅ present | ✅ present |
| `region` | ✅ present | ✅ present |
| `access_key` | ✅ **account-admin key** | ❌ **removed** |
| `secret_key` | ✅ **account-admin key** | ❌ **removed** |

**Migration**: consumers that read `access_key`/`secret_key` from an `S3Bucket` Secret MUST switch
to an `S3User` (create one granting the needed level on the bucket; consume its scoped Secret).
This is the feature's core security outcome (SC-008). Acceptable as a breaking change because the
kind is `v1alpha1` (Constitution §I permits breaking changes pre-`v1beta1`).

## Status (`atProvider`) — additive

| Field | Type | Notes |
|---|---|---|
| `attachedUsers` | list | read-only mirror: `{name, accessLevel}` of users granted on this bucket |

`attachedUsers` is **observational only** — `S3Bucket` never writes, owns, or is a source of truth
for any grant (the `S3User` is the sole writer). Derived best-effort during `Observe` from the IAM
host; population MUST NOT block bucket readiness, and any truncation under rate limits MUST be
logged (no silent caps).

## Print columns (candidate)

Add `ATTACHED` = `len(status.atProvider.attachedUsers)` (priority=1), alongside the existing
`READY`/`SYNCED`/`SIZE-GB`/`CLASS`/`STATE`/`ID`/`AGE`.

## Conditions

Unchanged from the current `S3Bucket` contract (the redesign does not alter bucket reconciliation
semantics — only what the connection Secret and status carry).
