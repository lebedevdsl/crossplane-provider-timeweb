# Quickstart: S3 storage classes (feature 028, v0.14.0)

## Order a bucket in the class the vendor sells

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: media
  namespace: team-a
spec:
  forProvider:
    name: team-a-media
    type: public
    storageClass: standard   # Standard (recommended); hot = Premium; cold = pay-as-you-go, 1 GB only
    initialSizeGB: 10
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

Expect `Ready=True`, `Synced=True`, `status.atProvider.storageClass: standard`
within a few minutes.

| class | panel name | sizes (GB) | note |
|---|---|---|---|
| `standard` | Standard | 1, 10, 100, 250 | what the panel sells today |
| `hot` | Premium | 1, 10, 100, 250 | `409 … sold out` since 2026-10-14 (support ticket open) |
| `cold` | Cold pay-as-you-go | 1 | |

## What a sold-out class looks like

```
Synced=False reason=ReconcileError
  create failed: timeweb transient (Conflict: Storage class hot is sold out)
```

Nothing exists upstream; the provider keeps retrying at its capped pace.
Switch `storageClass` (no recreate needed — the bucket was never created).

## Day-2 edits never move the tier

Editing `description`, `type` or `projectID` sends no tier selection.
Editing `initialSizeGB` within the same class re-resolves and resizes
(`lockedPresetID` is refreshed).

## The vendor moved my bucket to another class

After 2026-10-13 every bucket bought as `hot` before 2026-09 reports
`standard`. The provider shows it, and does not touch it:

```
status.conditions:
- type: StorageClassDrift
  status: "True"
  reason: UpstreamReclassified
  message: declared "hot", upstream reports "standard"; set spec.forProvider.storageClass to "standard" ...
```

`Ready`/`Synced` stay `True`. To clear it, align the manifest:

```yaml
    storageClass: standard
```

No request is sent (the class is not part of the up-to-date check); the
condition flips to `False/Aligned` on the next observation.

Until aligned, any other edit on that bucket is refused:

```
Synced=False  message: storage class mismatch: spec declares "hot" but upstream reports "standard"; set spec.forProvider.storageClass to "standard" ...
Event Warning StorageClassMismatch
```

Cross-class moves on an existing bucket are not supported by the provider.

## Rollout

1. Tag the release → release workflow → published package.
2. GitOps repo: bump the Provider package → sync → `make preflight` (CRD enum
   must list `standard`).
3. Switch the incident manifest to `storageClass: standard` (nothing exists
   upstream yet, so no recreate).
4. Align the legacy manifests to `standard`; confirm `StorageClassDrift=False`
   and 0 PATCH calls in provider logs. A bucket whose upstream class is still
   `hot` stays `hot`.
