# Data Model: CDN Resource (016)

## Kind: `Cdn` — `cdn.m.timeweb.crossplane.io/v1alpha1`

Namespaced managed resource. External-name = upstream `http_resource.id` (numeric, encoded
via `shared.EncodeID`).

## Spec (`CdnParameters`)

| Field | Type | Constraints | Wire |
|---|---|---|---|
| `name` | string, optional | 1–255; defaults to MR name | `name` |
| `description` | *string, optional | ≤255 | `description` |
| `projectID` / `projectRef` | provider-standard | existing idiom | `project_id` |
| `origin` | `CdnOrigin`, required | CEL: exactly one of bucketRef/domain/ip | see below |
| `cache` | `*CdnCache`, optional | nil ⇒ not owned | `config.cache` |
| `security` | `*CdnSecurity`, optional | nil ⇒ not owned | `config.security` |
| `performance` | `*CdnPerformance`, optional | nil ⇒ not owned | `config.delivery` + `config.robots` |
| `cors` | `*CdnCors`, optional | nil ⇒ not owned | `config.http_headers.cors` |
| `requestHeaders` | `[]CdnRequestHeader`, optional | MaxItems=32; unique names (CEL) | `config.http_headers.request` (map) |

### `CdnOrigin`

| Field | Type | Constraints | Wire (write) |
|---|---|---|---|
| `bucketRef` | `*LocalRef{name}` | same-namespace `S3Bucket` | `storage_id` (bucket upstream id) |
| `domain` | `*string` | hostname pattern | `server: {host, port}` |
| `ip` | `*string` | IPv4 pattern | `server: {host, port}` |
| `https` | `*bool`, default `true` | origin scheme | `use_https` |
| `port` | `*int32`, optional | 1–65535; default 443/https, 80/http | `server.port` |

CEL (spec root, bounded — `project_cel_cost_budget_crd`):
- `exactly one of origin.bucketRef / origin.domain / origin.ip is set`
- `origin.port` only meaningful for `domain`/`ip` (reject with `bucketRef`)

Read-side origin: `configuration.origin.servers[] + use_https + aws` — mirrored (minus `aws`).

### `CdnCache`

| Field | Type | Constraints | Wire (probe-verified 2026-07-12) |
|---|---|---|---|
| `edgeTTLSeconds` | `*int64` | ≥0; 0 = disabled | `cache.cdn = {ttl:{"2xx":N}} \| null` |
| `browserTTLSeconds` | `*int64` | ≥0; 0 = disabled | `cache.browser = {ttl:N} \| null` (plain int) |
| `alwaysOnline` | `*bool` | presence-only diff | `cache.always_online = {stale_conditions:[…]} \| null` (enable writes `[error,timeout]`, existing set preserved) |
| `queryStringInCacheKey` | `*bool` | presence-only diff | `cache.query_args = {mode:"all"} \| null` |

Cache section writes are full-section replaces with explicit nulls for
disabled sub-features (no omitempty on the inner keys).

### `CdnSecurity`

| Field | Type | Wire |
|---|---|---|
| `forceHTTPS` | `*bool` | `security.redirect` |

(`security.certificate_id` / `secure_token` are deliberately absent — deferred; never diffed.)

### `CdnPerformance`

| Field | Type | Constraints | Wire |
|---|---|---|---|
| `http3` | `*bool` | | `delivery.http3` |
| `gzip` | `*bool` | | `delivery.gzip` |
| `largeFileSlicingMB` | `*int64` | 1–1024; unset = off | `delivery.large_files` + `slice_size` |
| `contentOptimization` | `*string` | enum `off|video|images`, default `off` | `image_optimization` / `packaging.mp4` |
| `robots` | `*CdnRobots` | | `robots` |

`CdnRobots`: `mode` enum `deny|proxy|custom` (default `deny`); `custom` *string required iff
mode=custom (CEL), ≤4096.

### `CdnCors`

| Field | Type | Constraints | Wire |
|---|---|---|---|
| `origins` | `[]string` | MinItems=1, MaxItems=16 | `http_headers.cors` |
| `alwaysAddHeader` | `*bool` | | (same block) |

### `CdnRequestHeader`

`name` (HTTP token pattern, ≤128) + `value` (≤1024). List⇄map conversion at the client edge
(wire is a JSON object; CEL uniqueness on `name` keeps the map bijective).

## Status (`CdnObservation`)

| Field | Type | Source |
|---|---|---|
| `id` | *int | `http_resource.id` |
| `technicalDomain` | *string | `http_resource.cdn_domain` |
| `state` | *string | `http_resource.status` (`processing` seen; terminal TBD R-5/P-6) |
| `source` | *string | `http_resource.source` |
| `lockedPresetID` | *int64 | chosen at Create (R-4) |
| `lastPurgedAt` | *metav1.Time | set on purge success (FR-012) |
| `domains` | []string | `configuration.domains.aliases` (read-only mirror, R-9) |
| `observedSettings` | `*CdnSettingsMirror` | read-only mirror of cache/security/delivery/robots/cors/request-header **names** (values elided for headers that may carry tokens); NEVER `origin.aws` |
| `trafficUsage` | `*CdnTrafficUsage{requests,outgoingTraffic,cacheRatio}` | `http_resource.traffic_usage` |

## Annotations (contract surface)

- `crossplane.io/external-name` — upstream id (managed by runtime).
- `cdn.timeweb.crossplane.io/purge` — operator-set, controller-cleared. Grammar: literal
  `all` | comma-separated entries each starting with `/`. See contracts/cdn-v1alpha1.md.

## State machine

```
(absent) --Create(POST)--> processing --upstream--> <serving/active (P-6)>
                                                   |  PATCH settings --> transient apply --> serving
serving --purge annotation--> POST clear-cache --> Event + lastPurgedAt + annotation removed
serving --panel suspend/limit--> Ready=False (reason=Suspended|LimitReached family)
(any) --Delete--> DELETE upstream (404-tolerant) --> finalizer released
upstream gone out-of-band --Observe--> ResourceExists=false --> re-Create
```

Ready gates on upstream serving state (NOT create 2xx); Synced on diff-clean observation.

## Relationships

```
Cdn.spec.forProvider.origin.bucketRef ──(same-ns Get, Ready+id gate,
     │                                   skipped when deletionTimestamp set)
     ▼
S3Bucket.status.atProvider.id ══ external-name ══ upstream storage_id
     ▲
     └── Watches(S3Bucket → Cdn) requeue mapping (R-8)
```

No selector support in v1 (project posture). No connection secret in v1 (`technicalDomain` is
public data in status; nothing credential-like to publish).

## Diff/ownership rules (Observe)

- Owned: identity (`name`, `description`), origin (`storage_id` | `server`+`use_https`), and
  every **non-nil** settings block, compared field-by-field against the configuration read.
- Not owned (never diffed, never written): `domains`, `security.certificate_id`,
  `secure_token`, `origin.aws` (subject to R-3 fallback), `access.allowed_methods`,
  traffic limit, nil settings blocks.
- Update payload: single PATCH containing ONLY the dirty owned subset (pointer structs +
  `omitempty`), one PATCH per reconcile (paced).
```
