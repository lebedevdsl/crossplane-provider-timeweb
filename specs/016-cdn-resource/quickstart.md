# Quickstart: CDN Resource (016)

Operator walkthrough for the `Cdn` kind. Assumes a working `ProviderConfig` (`default`).

## 1. CDN in front of an S3 bucket

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata: { name: site-assets, namespace: web }
spec:
  forProvider: { name: site-assets, type: private, storageClass: hot, initialSizeGB: 10 }
  providerConfigRef: { name: default }
---
apiVersion: cdn.m.timeweb.crossplane.io/v1alpha1
kind: Cdn
metadata: { name: assets, namespace: web }
spec:
  forProvider:
    origin:
      bucketRef: { name: site-assets }
      https: true
    cache: { edgeTTLSeconds: 86400 }
    security: { forceHTTPS: true }
    performance: { gzip: true, http3: true }
  providerConfigRef: { name: default }
```

The Cdn waits (`Ready=False reason=OriginNotReady`) until the bucket is Ready, then creates
the upstream resource. AWS auth for the private bucket is wired automatically — no credential
fields anywhere.

```sh
kubectl -n web wait cdn/assets --for=condition=Ready --timeout=5m
kubectl -n web get cdn assets -o jsonpath='{.status.atProvider.technicalDomain}'
# → cz02dfkcda.cdn.twcstorage.ru — serve content from https://<that domain>/<key>
```

## 2. External origins

```yaml
    origin: { domain: origin.example.com, https: true }        # or:
    origin: { ip: 203.0.113.10, https: false, port: 8080 }
```

Exactly one of `bucketRef` / `domain` / `ip` — admission rejects anything else.

## 3. Day-2 settings

Edit the manifest; the controller PATCHes upstream and re-observes until it matches. Panel
edits to declared fields are reverted (single-writer). Blocks you omit (`cors`, `cache`, …)
are left panel-managed and only mirrored under `status.atProvider.observedSettings`.

## 4. Cache purge

```sh
kubectl -n web annotate cdn/assets cdn.timeweb.crossplane.io/purge=all
# or selective (every entry starts with /):
kubectl -n web annotate cdn/assets cdn.timeweb.crossplane.io/purge=/,/img,/index.html
```

The controller purges once, emits a `CachePurged` Event, stamps
`status.atProvider.lastPurgedAt`, and removes the annotation. A malformed value gets a
`PurgeInvalid` warning Event and is removed without purging.

## 5. Deletion

`kubectl delete cdn/assets` removes the upstream resource (safe even if the bucket is already
gone). Deleting only the bucket does NOT delete the Cdn — it keeps serving the last origin
until you update or delete it.

## Troubleshooting

| Symptom | Likely cause | Check / fix |
|---|---|---|
| `Ready=False OriginNotReady` | bucket missing / not Ready | `kubectl get s3bucket <name>`; wait or fix bucket |
| `status.atProvider.state` stuck at `processing` | upstream quirk — the field never settles; the CDN serves regardless | ignore it; Ready/updates/purges don't key on it |
| Stuck admission error on apply | origin oneof / range violated | message names the CEL rule |
| `Synced=False APIError` retrying on create | origin `domain` does not resolve in public DNS (upstream 500s on it) | fix the origin hostname |
| `Ready=False Suspended` | traffic limit / paused in panel | panel → resource state; billing/limit is upstream-side |
| Purge annotation not disappearing | upstream purge failing or resource not serving | Events (`PurgeFailed`/`PurgeDeferred`); annotation is retried until success |
| Setting keeps reverting in panel | that block is declared in the manifest | single-writer by design — remove the block from spec to hand it back to the panel |
| New field vanishes from spec | stale controller image | `project_stale_binary_prunes_new_fields`: strings-grep deployed image |

## Live-gate note (operators running e2e)

Fresh bucket (`initialSizeGB: 10`) + fresh Cdn only; pinned kubectl context; verify SC-002 by
uploading an object and fetching it through `technicalDomain`; leave `inyan-infra`/
`cloud-infra` untouched.
