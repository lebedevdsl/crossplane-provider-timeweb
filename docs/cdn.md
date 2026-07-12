# `Cdn` (v1alpha1) — Timeweb Cloud CDN resource

One resource = one Timeweb CDN resource: a single origin served through an
auto-assigned technical delivery domain, with the declared settings blocks
kept in sync (single-writer — dashboard edits to declared fields are reverted
on the next reconcile).

| Property | Value |
| -------- | ----- |
| API group | `cdn.m.timeweb.crossplane.io` |
| Kind | `Cdn` |
| Scope | Namespaced |
| External-name format | upstream numeric resource id |
| Connection Secret | none |

## Manifest

See `examples/cdn.yaml` for the full annotated form.

```yaml
apiVersion: cdn.m.timeweb.crossplane.io/v1alpha1
kind: Cdn
metadata:
  name: site-assets
  namespace: timeweb-prod
spec:
  forProvider:
    origin:
      bucketRef: { name: site-assets }   # or domain: / ip: (exactly one)
      https: true
    cache: { edgeTTLSeconds: 86400 }
    security: { forceHTTPS: true }
    performance: { gzip: true, http3: true }
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Origin

Exactly one of (CEL-enforced at admission):

- `bucketRef` — an `S3Bucket` in the same namespace. The Cdn waits
  (`Ready=False reason=OriginNotReady`) until the bucket is Ready, then
  attaches by upstream storage id. AWS auth for private buckets is
  upstream-wired — no credential fields anywhere.
- `domain` — external hostname (no scheme/path). MUST resolve in public DNS:
  the upstream validates it at create and fails a non-resolvable host with a
  bare 500 (surfaces as a retrying `Synced=False APIError` — check the origin
  spelling first).
- `ip` — external IPv4.

`https` picks the origin scheme (default true); `port` (domain/ip only)
defaults by scheme.

## Settings ownership

A settings block that is declared is owned wholesale: its absent leaves mean
"disabled", and out-of-band panel edits are reverted. A block that is omitted
is never written and only mirrored under `status.atProvider.observedSettings`.
Custom delivery domains, SSL certificates, secure tokens, and traffic limits
are out of scope in v1 (panel-managed; never touched).

Note on the upstream `status` field: it commonly sticks at `processing` for
hours while the CDN serves, applies changes, and purges normally, before
eventually settling to `created` (platform quirk). The provider therefore mirrors it in `status.atProvider.state` but
does NOT gate Ready, updates, or purges on it — only a suspended state does.

## Cache purge (annotation)

```sh
kubectl annotate cdn/site-assets cdn.timeweb.crossplane.io/purge=all
kubectl annotate cdn/site-assets cdn.timeweb.crossplane.io/purge=/,/img,/index.html
```

`all` = full purge; otherwise a comma-separated list where every entry starts
with `/` (a file literally named "all" is `/all`). The controller purges once,
emits a `CachePurged` Event, stamps `status.atProvider.lastPurgedAt`, and
removes the annotation. Invalid values get a `PurgeInvalid` warning Event and
are removed without purging; upstream failures keep the annotation and retry on later reconciles (fresh
resources refuse purges with 500s for several minutes — expected).

## Conditions

| Ready reason | Meaning |
| ------------ | ------- |
| `Available` | upstream exists and is not suspended (`processing` included — the CDN serves through it) |
| `OriginNotReady` | bucketRef target missing / not Ready |
| `Suspended` | upstream paused (traffic limit / billing) — resolve in panel |
| `UpstreamFailed` | mutation rejected upstream (see message) |

## Upstream surface

Undocumented `/api/v1/cdn/http-resources` API (devtools-verified 2026-07-12) —
inventory, payloads, and quirks in
`specs/016-cdn-resource/contracts/timeweb-cdn-endpoints.md`. The configuration
read is secret-bearing (`origin.aws`): the provider never logs it and never
mirrors the aws block into status.
