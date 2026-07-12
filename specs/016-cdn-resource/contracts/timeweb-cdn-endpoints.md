# Contract: Timeweb CDN endpoints (undocumented surface)

Captured live 2026-07-12 (browser devtools, panel host; unauthenticated existence probes on
both hosts). NOT present in the published OpenAPI spec or `/api-docs`. Envelope convention:
underscore (`http_resource`, `http_resource_configuration`) + `response_id`.

Base: `https://api.timeweb.cloud` (401-verified to exist; token acceptance = probe P-1) —
panel host `https://timeweb.cloud` proven working and available as fallback (015 precedent).

## POST /api/v1/cdn/http-resources → 201

```json
{
  "name": "Ambitious Jackdaw",
  "description": "",
  "project_id": 2277851,
  "preset_id": 3807,
  "server": { "host": "origin.inyan.pro", "port": 443 },
  "use_https": true
}
```

- `preset_id` REQUIRED — enumerate via `GET /api/v1/cdn/presets` (shape = probe P-5).
- Bucket origin: `storage_id: <bucket-id>` instead of `server` (seen on PATCH; create-with-
  storage_id = probe P-4, expected symmetric).
- Response: full `http_resource` (below), `status: "processing"`, `cdn_domain` already
  assigned.

## GET /api/v1/cdn/http-resources/{id} → 200

```json
{
  "http_resource": {
    "id": 22209, "name": "Ambitious Jackdaw", "description": "",
    "status": "processing",
    "source": "inyan-static",
    "cdn_domain": "cz02dfkcda.cdn.twcstorage.ru",
    "preset_id": 3807, "project_id": 2277851,
    "storage_id": 528009,
    "avatar_link": null, "traffic_limit_bytes": null,
    "traffic_usage": { "requests": 0, "outgoing_traffic": 0, "cache_ratio": 0 }
  },
  "response_id": "..."
}
```

- NO settings here. `source` flattens the origin (bucket name for storage origins, host for
  server origins). `status` values: `processing` seen; terminal serving value = probe P-6.
- List `GET /api/v1/cdn/http-resources` exists (401-probe) — envelope presumably
  `http_resources[]`; needed for by-name adoption (probe P-1).

## GET /api/v1/cdn/http-resources/{id}/configuration → 200 — SECRET-BEARING

```json
{
  "http_resource_configuration": {
    "access":  { "allowed_methods": null },
    "cache": {
      "cdn": { "ttl": { "2xx": 3600 } },
      "browser": null, "always_online": null, "query_args": null
    },
    "delivery": {
      "http3": false, "gzip": false,
      "large_files": false, "slice_size": null,
      "image_optimization": false, "packaging": { "mp4": null }
    },
    "domains": { "aliases": ["cz02dfkcda.cdn.twcstorage.ru"] },
    "http_headers": { "request": { "HEAD": "HEADER" }, "cors": null },
    "origin": {
      "servers": [ { "host": "inyan-static.s3.twcstorage.ru", "port": 443 } ],
      "use_https": true,
      "aws": { "access_key": "<REDACTED>", "secret_key": "<REDACTED>" }
    },
    "robots": { "type": "deny" },
    "security": { "redirect": false, "certificate_id": null, "secure_token": null }
  },
  "response_id": "..."
}
```

- **`origin.aws` carries the account's live S3 keys in PLAINTEXT.** Client must never log the
  raw response; controller must never mirror the `aws` block (spec FR-011).
- Unset sections/leaves are `null` ⇒ "feature off / not configured".
- Edge TTL keyed per status class (`ttl."2xx"`); other classes may exist (probe P-3).
- `domains.aliases` INCLUDES the technical domain on read although the panel writes
  `aliases: []` — read/write-asymmetric; do not own (research R-9).
- Bucket origins materialize as a synthesized `servers[]` entry
  (`<bucket>.s3.twcstorage.ru:443`) — `source of truth` for origin identity remains
  `storage_id` on the resource read.

## PATCH /api/v1/cdn/http-resources/{id} → 200

```json
{
  "storage_id": 528009,
  "config": { "domains": { "aliases": [] } }
}
```

- Partial update: top-level identity/origin fields + a `config` object holding any subset of
  the configuration sections (write-side mirror of the GET schema above; per-key
  partial-vs-replace semantics = probe P-3).
- 200 does NOT imply applied — settings apply is asynchronous (panel: "Применяются
  настройки"); converge by re-observation only (research R-5).
- AWS auto-wire hypothesis: PATCH with `storage_id` and NO `aws` still yields populated
  `origin.aws` on the next configuration read (research R-3 / probe P-4).

## POST /api/v1/cdn/http-resources/{id}/clear-cache → 204

```json
{ "purge_type": "full",    "paths": [] }
{ "purge_type": "partial", "paths": ["/folder"] }
```

- Both variants live-verified. Paths are root-relative. No response body.

## DELETE /api/v1/cdn/http-resources/{id}

- Not yet exercised — probe P-2 (status code, second-call behavior). Controller treats 404 as
  success regardless.

## GET /api/v1/cdn/presets

- Exists (401-probe); shape unknown — probe P-5. Expected: preset list incl. id 3807; used at
  Create to resolve `preset_id` (lowest-price pick, locked in status).

## GET /api/v1/cdn/certificates

- Exists (401-probe). OUT of v1 scope (SSL deferred); inventoried for the follow-up feature.

## Probe results (2026-07-12, session 2 — resource `22219` on `api.timeweb.cloud`)

| # | Question | Result |
|---|---|---|
| P-1 | token on api host; list envelope | ✅ token works; list = `{"http_resources": [...], "meta", "response_id"}` |
| P-2 | DELETE semantics + idempotency | ✅ DELETE → 204; repeat DELETE → 404; GET after → 404 (matches the 404-tolerant delete) |
| P-3 | remaining write shapes | ✅ ALL resolved — see verified schema below |
| P-4 | aws auto-wire on storage_id create | ✅ POST accepts `storage_id` directly (live gate); aws auto-wire confirmed via 22209's configuration read; data-plane check pending first real use |
| P-5 | presets envelope / count | ✅ `{"http_resource_presets": [{"id":3807,"service_name":"cdn_1","cost":1,"rate_cost":0.6}]}` — single preset |
| P-6 | terminal status; apply visibility | ✅ terminal = **`created`** (settles after hours); `processing` covers provisioning AND settings applies; every PATCH re-enters it |

### Session-2 verified wire facts (supersede the provisional notes above)

- **Create validates origin DNS**: a non-resolvable `server.host` → **500**
  `{"status_code":500,"error_code":"internal_server_error"}` with NO explanatory message
  (ticket-worthy quirk: an operator typo surfaces as a retried transient, not a terminal
  4xx). `project_id` optional-looking but keep sending it (panel does).
- **`config.cache` sub-features are objects-or-explicit-null** (null = disabled; a written
  cache section must carry all four keys, no omitempty):
  - `cdn: {"ttl": {"2xx": N}} | null`
  - `browser: {"ttl": N} | null` (plain int — NOT a status-class map)
  - `always_online: {"stale_conditions": ["error","timeout", …]} | null` (enum-validated;
    panel shows the set as "Типы ошибок" tags)
  - `query_args: {"mode": "all"} | null` (`mode` enum; "all" valid, "none"/"ignore"/"disabled" rejected)
- **`config.robots` custom**: `{"type":"custom","content":"…"}` write-ACCEPTED, but the
  configuration read echoes `{"type":"custom"}` only — `content` is write-only ⇒ the
  controller diffs robots by `type` alone and writes `content` through.
- **`config.http_headers`**: `request` name→value map and `cors: {"domains":[…],
  "always":bool}` confirmed exactly as modeled.
- **PATCH during `processing`** is accepted (200) and re-triggers the apply; the provider
  still skips mid-apply pushes by policy (R-5).
- **`clear-cache` while freshly provisioning → 500**; additionally the endpoint is
  **rate-limited**: repeated purges return `429 rate_limit_exceeded` (panel-observed
  2026-07-12). Both classify as transient — the controller retries with backoff, purge
  annotation retained, so a purge eventually lands exactly once per annotation set.
- Validation errors are self-describing (`message` is an array of per-field complaints) —
  useful for future field additions.

Bug-grade quirks for a support ticket (RU) per `feedback_capture_upstream_quirks`:
DNS-validation-as-500 on create, and clear-cache-as-500 on a processing resource.
