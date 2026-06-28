# Quickstart — S3User scoped object-storage credentials

Operator walkthrough for feature 012. Assumes a working `ProviderConfig` (account token) and at
least one `S3Bucket` (or an existing bucket name).

## 1. Scoped read-write credential for one bucket (the dominant case)

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3User
metadata:
  name: app-data-rw
  namespace: app
spec:
  forProvider:
    name: app-data-rw
    bucketAccess:
      - bucketRef: { name: app-data }      # references an S3Bucket in this namespace
        accessLevel: read-write
  providerConfigRef: { name: default }
  writeConnectionSecretToRef:
    name: app-data-s3-creds
```

Verify:

```bash
kubectl -n app get s3user app-data-rw -o yaml   # status.conditions: Synced=True, Ready=True
kubectl -n app get secret app-data-s3-creds -o jsonpath='{.data}' | jq 'keys'
# → ["access_key","bucket","endpoint","secret_key"]   (scoped — NOT account-admin)
```

The credential can read/write objects in `app-data` and is denied on every other bucket.

## 2. One user, several buckets at different levels

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3User
metadata: { name: backup-agent, namespace: db }
spec:
  forProvider:
    name: backup-agent
    bucketAccess:
      - bucketRef:  { name: db-backups }
        accessLevel: read-write
      - bucketName: shared-assets          # an unmanaged bucket, by name
        accessLevel: read
  providerConfigRef: { name: default }
  writeConnectionSecretToRef: { name: backup-agent-s3-creds }
```

Verify the single merged policy applied:

```bash
kubectl -n db get s3user backup-agent -o jsonpath='{.status.atProvider.resolvedBuckets}'
# → [{db-backups read-write} {shared-assets read}]
```

## 3. Bucket-side view (read-only mirror)

```bash
kubectl -n db get s3bucket db-backups -o jsonpath='{.status.atProvider.attachedUsers}'
# → [{backup-agent read-write}]   (observational only; S3Bucket never writes grants)
```

## 4. Day-2 operations

- **Raise/lower a level**: edit `bucketAccess[].accessLevel`; the policy re-renders in place. The
  connection Secret's keys do **not** change — consumers need not re-read it.
- **Revoke one bucket**: remove its `bucketAccess` entry; access is dropped on the next reconcile,
  other grants intact. Reducing to an empty list leaves a working identity with no bucket access.
- **Rename**: `name` is immutable — a change is rejected (`ImmutableFieldChange`). Create a new
  `S3User` instead.
- **Delete**: `kubectl delete s3user …` removes the upstream identity and its policy; the issued
  credential stops authorizing.
- **Migrate off S3Bucket admin keys**: any consumer still reading `access_key`/`secret_key` from an
  `S3Bucket` Secret must move to an `S3User` Secret — those keys are no longer emitted by `S3Bucket`.

## Troubleshooting

| Symptom | Meaning | Action |
|---|---|---|
| `Ready=False ParentNotReady` | a `bucketRef` target is missing or not `Ready=True` | create/await the `S3Bucket`; provider re-enqueues on parent readiness |
| `Synced=False InvalidConfiguration` | same bucket listed twice in `bucketAccess` | declare each bucket once with one level (FR-016) |
| `Synced=False ImmutableFieldChange` | tried to change `name` | revert `name`, or create a new `S3User` |
| `Synced=False APIError` (malformed policy) | a raw/derived policy was rejected by RGW | check `bucketAccess` levels; report if persistent |
| connection Secret lacks `access_key`/`secret_key` on an **S3Bucket** | expected after feature 012 | use an `S3User` for credentials |
| intermittent SYN timeout / transient retries | Qrator pacing on the API/IAM host | normal — provider retries conservatively; do not hammer |
| `status.attachedUsers` looks stale/partial | mirror is best-effort, non-blocking | it converges on a later reconcile; truncation is logged |
