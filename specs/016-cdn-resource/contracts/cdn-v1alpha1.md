# Contract: Cdn v1alpha1 CRD

Kind `Cdn`, group `cdn.m.timeweb.crossplane.io`, version `v1alpha1`, namespaced, categories
`crossplane,managed,timeweb`.

## Canonical manifest

```yaml
apiVersion: cdn.m.timeweb.crossplane.io/v1alpha1
kind: Cdn
metadata:
  name: assets
  namespace: default
  annotations:
    cdn.timeweb.crossplane.io/purge: "all"        # optional, one-shot, controller-cleared
    # cdn.timeweb.crossplane.io/purge: "/,/img,/index.html"
spec:
  forProvider:
    name: assets-cdn                # optional, defaults to MR name
    description: static assets      # optional
    origin:                         # exactly one of bucketRef | domain | ip (CEL)
      bucketRef:
        name: site-assets           # S3Bucket, same namespace
      # domain: origin.example.com
      # ip: 203.0.113.10
      https: true                   # default true
      # port: 8443                  # domain/ip only
    cache:
      edgeTTLSeconds: 86400
      browserTTLSeconds: 3600
      alwaysOnline: true
      queryStringInCacheKey: false
    security:
      forceHTTPS: true
    performance:
      http3: true
      gzip: true
      largeFileSlicingMB: 8
      contentOptimization: "off"    # off | video | images
      robots:
        mode: deny                  # deny | proxy | custom
        # custom: |
        #   User-agent: *
        #   Disallow: /private
    cors:
      origins: ["*.example.com"]
      alwaysAddHeader: true
    requestHeaders:
      - name: X-Origin-Auth
        value: token123
  providerConfigRef:
    name: default
  managementPolicies: ["*"]
```

## Admission rules (CEL / schema)

| Rule | Message shape |
|---|---|
| exactly one of `origin.bucketRef`/`origin.domain`/`origin.ip` | "exactly one origin must be set" |
| `origin.port` forbidden with `bucketRef` | "port applies to domain/ip origins only" |
| `largeFileSlicingMB` ∈ [1,1024] | schema min/max |
| `contentOptimization` ∈ off/video/images; `robots.mode` ∈ deny/proxy/custom | schema enum |
| `robots.custom` required iff `robots.mode == custom` | CEL on performance.robots |
| `requestHeaders` MaxItems=32, unique `name` | CEL + MaxItems |
| `cors.origins` MinItems=1, MaxItems=16 | schema |

All list-bearing parents carry MaxItems (CEL cost budget — apiserver-enforced, not just
`crossplane beta validate`).

## Purge annotation contract

`cdn.timeweb.crossplane.io/purge`:

| Value | Action |
|---|---|
| `all` | full purge (`purge_type: full`) |
| `/a,/b/c,...` (every entry starts with `/`) | partial purge of exactly those paths |
| anything else (empty, entry w/o leading `/`) | NO purge; warning Event `PurgeInvalid` naming the bad entry; annotation removed |

Lifecycle: annotation present + upstream serving → `POST clear-cache`; on 2xx: Event
`CachePurged` (scope listed), `status.atProvider.lastPurgedAt` set, annotation removed (removal
= acknowledgment; one purge per annotation set). On upstream failure: warning Event
`PurgeFailed`, annotation retained, retried on the next reconcile (poll-paced; the
resource's Synced/Ready are unaffected — fresh resources refuse purges for minutes). Resource not yet serving: annotation
retained, single `PurgeDeferred` Event.

## Conditions

| Type | Reason | When |
|---|---|---|
| Synced | ReconcileSuccess / ReconcileError | runtime standard |
| Ready=True | Available | upstream serving state + no pending origin gate |
| Ready=False | Creating / Provisioning | POST issued / `status=processing` or settings applying |
| Ready=False | OriginNotReady | bucketRef target missing or not Ready (pre-create gate) |
| Ready=False | UpstreamFailed | mutation rejected upstream (message attached, token-free) |
| Ready=False | Suspended | upstream paused/limit-suspended state |
| Ready=False | Deleting | deletionTimestamp set (normal) |

## Status guarantees

- `status.atProvider.technicalDomain` populated as soon as create response returns.
- `status.atProvider.lastPurgedAt` monotonic, set only after upstream 2xx.
- Mirrors (`domains`, `observedSettings`, `trafficUsage`) are read-only; NEVER include
  `origin.aws` or any credential material (also excluded from Events and logs).
- External-name = upstream numeric id; by-name adoption guard on Create (Router idiom).

## Out of contract (v1)

Custom delivery domains, SSL certificates, secure token, traffic limits, pause/resume,
selectors for origin references. (Connection secret ADDED in v0.7.2: `technical_domain` +
`url` published to `writeConnectionSecretToRef` — public endpoint data, no credentials.)
