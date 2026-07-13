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
| Connection Secret | `technical_domain`, `url` (public delivery endpoint — handy for app/DNS wiring via `writeConnectionSecretToRef`) |

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

## Custom delivery domains

Up to 2 subdomains alongside the immutable technical domain:

```yaml
domains: [cdn.example.com, static.example.com]
```

CNAME each to `status.atProvider.technicalDomain` at your DNS provider (on
Timeweb NS it updates automatically). The provider owns the alias set when
declared and never touches the technical domain.

## SSL — one certificate per resource

```yaml
ssl:
  mode: letsEncrypt          # none | letsEncrypt | custom
  # certificateSecretRef:    # required iff mode=custom
  #   name: example-tls      # kubernetes.io/tls: tls.crt = full chain, tls.key
```

> **letsEncrypt mode** (verified end-to-end 2026-07-13): CNAME your custom
> domain to `technicalDomain` FIRST — the provider attaches the domain, then
> requests issuance and binds the certificate when it materializes (the
> platform auto-binds). Issuance can fail transiently **with no error reason**
> while DNS propagates to Let's Encrypt's resolvers; the provider retries on a
> budget (max 4 attempts ≥15 min apart) and, if spent, sets
> `status.atProvider.ssl.state: exhausted` — reset with a domains/ssl spec
> edit or `kubectl annotate cdn/X cdn.timeweb.crossplane.io/retry-ssl=now`.

- `custom`: no retry budget (upload is deterministic). The chain must
  terminate in a system-trusted root — self-signed certificates are rejected
  (`422 cert_add_root_not_trusted`), surfacing as `ssl.state: failed` + an
  `SSLUploadFailed` Event; the same certificate is NOT re-uploaded until the
  Secret changes. `issueAttempts` applies to Let's Encrypt only.
- `custom`: rotation is declarative — update the TLS Secret and the provider
  uploads the new certificate, rebinds, and deletes its old one. No SAN
  validation is performed (upstream accepts any certificate; coverage is your
  responsibility).
- `none`: unbinds, and deletes the certificate only if the provider created
  it (`status.atProvider.managedCertificate`); panel-uploaded certificates
  are never destroyed.
- Block absent: the certificate slot stays panel-managed, mirrored in
  `status.atProvider.certificate`.

## Secure token (signed URLs)

```yaml
security:
  secureToken:
    secretRef: { name: cdn-signing }   # Secret key "secret" by default
    restrictByIP: true
```

The signing key never appears in spec or status. Removing the block disables
the feature upstream. Rotating the Secret's key propagates automatically
(the platform echoes the current key, so the change is detected).

## Outbound traffic limit

```yaml
trafficLimitGBPerMonth: 3000   # GiB upstream; 0 = explicitly no limit
```

Exceeding it SUSPENDS the resource (Ready=False reason=Suspended) with up to
2 h of upstream lag — traffic keeps billing during that window.

## External private S3 origins

```yaml
origin:
  domain: bucket.external-s3.example
  https: true
  awsAuthSecretRef: { name: ext-s3-keys }   # keys: access_key / secret_key
```

Only for external origins — in-account `bucketRef` buckets are wired by the
platform automatically and reject this field at admission.

## Query-string cache key

One setting, `queryStringCacheKeyMode`:

```yaml
cache:
  queryStringCacheKeyMode: all         # all | whitelist | blacklist
  # queryStringCacheKeyParams: ["utm_source", "ref", "v"]  # whitelist/blacklist only
```

`all` puts every query parameter in the cache key; `whitelist` keys the cache
only on the listed parameters; `blacklist` on all except them.

## Signed URLs — the signing algorithm (app-side)

Signing, for app-side use:
`token = urlsafe_b64(md5("<secret><path><ip><expires>"))` with `=` stripped,
`+`→`-`, `/`→`_`; fetch as
`https://<cdn-domain>/md5(<token>,<expires>)/<path>`. Omit `<ip>`/`<expires>`
from the string when those checks are disabled; the domain is never signed.
Invalid signatures return 403; EXPIRED links return 410 Gone (upstream
platform docs — the CDN is a CDNvideo white-label).

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
