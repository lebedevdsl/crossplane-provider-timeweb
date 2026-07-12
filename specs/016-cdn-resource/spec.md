# Feature Specification: CDN Resource

**Feature Branch**: `016-cdn-resource`

**Created**: 2026-07-12

**Status**: Draft

**Input**: User description: "Implement timeweb CDN https://timeweb.cloud/docs/cdn docs entrypoint as resource"

## Clarifications

### Session 2026-07-12

- Q: How much of the CDN surface should v1 of the `Cdn` kind cover? → A: **Core + settings** —
  origin, technical delivery domain, and all day-2 panel settings (cache, HTTPS redirect,
  performance, CORS, request headers) with drift reversion. Custom delivery domains, SSL
  certificate management, and secure-token are deferred.
- Q: How should the CDN origin be modeled — typed cross-resource refs or opaque values? → A:
  **`bucketRef` + plain strings** — typed `bucketRef` to `S3Bucket` (primary use case, mirrors
  the existing `bucketRef` idiom), plus plain `domain` / `ip` strings for external origins;
  exactly one of the three.
- Q: Should cache purge (an imperative action) be in scope for v1? → A: **Annotation-triggered**
  (refined 2026-07-12 after the purge-endpoint capture): a self-clearing
  `cdn.timeweb.crossplane.io/purge` annotation whose value is either the literal `all` (full
  purge) or a comma-separated list of root-relative paths, each with a leading `/` (e.g.
  `/,/path,/dir,/index.html`) — the leading `/` disambiguates a path from the `all` keyword.
  The controller emits a Kubernetes Event, calls the upstream purge (full or partial), and
  **removes the annotation** on success — removal is the one-shot guarantee. Both full and
  selective purge are in v1.
- Q: How should v1 handle AWS auth for `bucketRef` origins (private buckets can't serve
  without it; the panel auto-wires it)? → A: **Auto-wire for bucketRef** — when the origin is
  a `bucketRef`, the controller enables AWS auth itself, deriving the account S3 keys at
  runtime the way feature 012 already does (never cached, never in manifest/status). No
  operator-facing AWS surface in v1.
- Q (live-gate session, 2026-07-12): the upstream `status` stays `processing` for HOURS on
  resources that serve content, apply PATCHes (immediate readback), and purge (204) — the
  panel's "Применяются настройки" badge and purge spinner track the same stuck field. Gate
  Ready/updates/purge on it? → A: **Ignore `processing`** — Ready = exists && not
  suspended-family (raw state mirrored in status); updates always allowed, paced one PATCH
  per reconcile, converging via the configuration readback; purge ungated (an early-lifecycle
  500 retries with the annotation retained). Quirk recorded for an RU support ticket.

### Live probe findings (2026-07-12)

The CDN API is absent from the published OpenAPI spec and the public api-docs, but exists —
probe-verified today:

- Unauthenticated `GET` returns **401 (exists)** on BOTH hosts (`timeweb.cloud` and
  `api.timeweb.cloud`) for: `/api/v1/cdn/http-resources`, `/api/v1/cdn/http-resources/{id}`,
  `/api/v1/cdn/presets`, `/api/v1/cdn/certificates`. Control: `/api/v1/ssh-keys` → 401,
  non-existent paths → 404.
- Panel-captured create (2026-07-12): `POST https://timeweb.cloud/api/v1/cdn/http-resources` →
  **201** with request body:

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

  and response envelope (underscore envelope, per project convention):

  ```json
  {
    "http_resource": {
      "id": 22209,
      "name": "Ambitious Jackdaw",
      "traffic_usage": { "requests": 0, "outgoing_traffic": 0, "cache_ratio": 0 },
      "description": "",
      "status": "processing",
      "source": "origin.inyan.pro",
      "cdn_domain": "cz02dfkcda.cdn.twcstorage.ru",
      "preset_id": 3807,
      "project_id": 2277851,
      "storage_id": null,
      "avatar_link": null,
      "traffic_limit_bytes": null
    },
    "response_id": "ae0c5629-51dd-4854-88b2-bc88517d6c29"
  }
  ```

  Notable: origin is `server: {host, port}` + `use_https` on the write side but flattens to
  `source` on the read side; a **`preset_id`** is required (CDN billing preset — enumeration
  via `/cdn/presets`, to research); `cdn_domain` is the technical delivery domain;
  `storage_id: null` suggests S3-bucket origins are wired by storage id rather than host
  (to verify); initial `status` is `processing` (terminal active value to verify);
  `traffic_usage` and `traffic_limit_bytes` are read-only telemetry.
- Panel observation of the created resource (screenshot, 2026-07-12): management page groups
  the settings as *Источник и домены раздачи* / *Кэширование* / *Безопасность* (HTTP→HTTPS
  redirect + Secure token) / *AWS-авторизация* / *Контент и подключение* (HTTP/3, gzip,
  large-file acceleration, **content optimization** — a three-way mode `Выключено`/`Видео`/
  `Изображения` NOT covered by the public docs, plus robots.txt with `Не индексировать`/
  `Проксировать`/`Свой` + inline content) / *HTTP-заголовки* (request headers + CORS) /
  traffic limit / project, plus an *SSL-сертификаты* tab. After saving, the resource shows a
  transient **"Применяются настройки"** (settings being applied) state — settings application
  is asynchronous.
- Panel-captured update (2026-07-12): `PATCH https://timeweb.cloud/api/v1/cdn/http-resources/22209`
  → **200**, body (switching the origin to an S3 bucket):

  ```json
  {
    "storage_id": 528009,
    "config": { "domains": { "aliases": [] } }
  }
  ```

  Confirms: the update verb is **PATCH on the resource path** (the feature-015 nodepool
  pattern); S3-bucket origins attach by top-level **`storage_id`** (the bucket's upstream
  numeric id — so `bucketRef` resolves to an id the `S3Bucket` controller already knows, not
  an endpoint host); settings are nested under a **`config`** object (`config.domains.aliases`
  = custom delivery domains, empty here) that the plain GET envelope above did not include.
- Panel-captured purge (2026-07-12):
  `POST https://timeweb.cloud/api/v1/cdn/http-resources/22209/clear-cache` → **204 No Content**.
  Both variants verified: selective `{"purge_type": "partial", "paths": ["/folder"]}`
  (root-relative paths) and full `{"purge_type": "full", "paths": []}`.
- Caching drawer (screenshot, 2026-07-12): CDN caching is a **toggle + TTL seconds** pair
  (browser caching likewise), plus *Всегда онлайн* (always online) and an undocumented
  **"Учитывать query string"** toggle (whether query parameters join the CDN cache key).
  The same session shows AWS-авторизация flipped to *Включено* for the bucket origin —
  the panel auto-wires S3 credentials for bucket origins (v1 mirrors this; see
  Clarifications).
- Panel-captured configuration read (2026-07-12):
  `GET https://timeweb.cloud/api/v1/cdn/http-resources/22209/configuration` →
  `http_resource_configuration` envelope — the full settings schema (writes go as partial
  `config` objects in the PATCH above; reads come from this sub-path):

  ```json
  {
    "http_resource_configuration": {
      "access":  { "allowed_methods": null },
      "cache": {
        "cdn":   { "ttl": { "2xx": 3600 } },
        "browser": null,
        "always_online": null,
        "query_args": null
      },
      "delivery": {
        "http3": false, "gzip": false,
        "large_files": false, "slice_size": null,
        "image_optimization": false,
        "packaging": { "mp4": null }
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
    }
  }
  ```

  Notable: edge cache TTL is keyed **per status class** (`ttl.{"2xx"}`); "content
  optimization" decomposes into `delivery.image_optimization` (bool) +
  `delivery.packaging.mp4` (video); `origin.servers` is an **array** (multi-origin capable);
  a `storage_id`-wired bucket origin still materializes as a plain
  `<bucket>.s3.twcstorage.ru:443` server entry; `domains.aliases` **includes the technical
  domain** on read although the panel PATCHed `aliases: []` (quirk — the alias set is not
  round-trip symmetric); `http_headers.request` is a name→value map; unset sections read as
  `null`. **AWS-auth S3 credentials are returned in PLAINTEXT by this GET** — the provider
  must treat the configuration response as secret-bearing (never log it, never mirror
  `origin.aws` into status).
- Still to capture in the plan-phase probe: PATCH partial-vs-full semantics per `config` key
  (incl. the `aliases` read/write asymmetry), the IP-origin create shape, `access.allowed_methods`
  value shape, delete semantics, terminal `status` values, and preset enumeration.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Provision a CDN resource in front of an origin (Priority: P1)

A platform operator declares a `Cdn` managed resource pointing at an origin — an `S3Bucket`
managed by this provider (by reference), an external domain, or an IP address — applies it, and
gets a working CDN: the upstream resource is created, the auto-assigned technical delivery
domain (`*.cdn.twcstorage.ru`) is surfaced in status, and the resource reports Ready when the
CDN is active and serving.

**Why this priority**: This is the feature — without provisioning there is nothing else.
Fronting the provider's own S3 buckets with a CDN is the primary use case in this environment.

**Independent Test**: Apply a `Cdn` manifest with a domain origin to a live account; verify the
upstream resource appears, status carries the technical domain, Synced+Ready go True, and a
file fetched through the technical domain returns origin content.

**Acceptance Scenarios**:

1. **Given** a valid `Cdn` manifest with `origin.domain` set, **When** it is applied, **Then**
   the upstream CDN resource is created with the declared name/description/project, the
   external-name annotation records the upstream id, and status shows the technical domain.
2. **Given** a `Cdn` manifest with `origin.bucketRef` naming a Ready `S3Bucket` in the same
   namespace, **When** it is applied, **Then** the controller resolves the bucket to the origin
   and creates the CDN resource against it with AWS auth enabled, so a private bucket serves
   content without any credential fields in the manifest.
3. **Given** a `Cdn` manifest with `origin.bucketRef` naming a missing or not-Ready bucket,
   **When** it is applied, **Then** the resource does not attempt creation and reports a clear
   condition explaining the unresolved reference.
4. **Given** more than one of `bucketRef`/`domain`/`ip` set (or none), **When** the manifest is
   applied, **Then** admission rejects it with a message naming the exactly-one constraint.

---

### User Story 2 - Day-2 settings management with drift reversion (Priority: P2)

The operator declares CDN behavior in the manifest — cache TTLs (edge + browser), always-online,
HTTP→HTTPS redirect, HTTP/3, gzip compression, large-file slicing, robots.txt mode, CORS
origins, and custom request headers to the origin — and the provider keeps the upstream matched
to the declaration: changes to the manifest are pushed up, and out-of-band panel edits to
declared fields are reverted (single-writer).

**Why this priority**: Settings are where CDN operational value lives (cache correctness, TLS
policy); panel-only settings would leave the resource half-managed and drift-prone.

**Independent Test**: Change `cache.edgeTTLSeconds` in the manifest and verify upstream follows;
flip a declared setting in the panel and verify the controller reverts it on the next reconcile.

**Acceptance Scenarios**:

1. **Given** a Ready `Cdn` with declared settings, **When** a settings field is changed in the
   manifest, **Then** the upstream is updated to match and the resource returns to Synced+Ready.
2. **Given** a Ready `Cdn`, **When** a declared setting is changed out-of-band in the panel,
   **Then** the controller detects the diff on Observe and reverts it in one Update pass.
3. **Given** a `Cdn` whose manifest omits an optional settings block, **When** the resource is
   reconciled, **Then** upstream values for the omitted fields are left untouched (not fought
   over) and mirrored read-only in status.
4. **Given** an update rejected upstream, **When** the controller retries, **Then** the resource
   reports an `UpstreamFailed`-style condition with the upstream message, without tight-looping.

---

### User Story 3 - Annotation-triggered cache purge (Priority: P3)

The operator forces a cache purge by annotating the `Cdn` resource with
`cdn.timeweb.crossplane.io/purge`: the literal value `all` requests a full purge, and a
comma-separated list of root-relative paths, each starting with `/` (e.g.
`/,/path,/dir,/index.html`), requests a selective purge of those paths — the leading `/`
keeps a file named "all" (`/all`) unambiguous from the keyword. The controller emits a
Kubernetes Event describing the purge, calls the upstream purge
endpoint, records `status.atProvider.lastPurgedAt`, and removes the annotation on success —
so the annotation's presence is the request, its removal is the acknowledgment, and status
retains a durable trace of the last purge; repeated reconciles never re-purge.

**Why this priority**: Purge is the one imperative CDN operation operators need routinely
(deploys that change static assets); without it they must context-switch to the panel.

**Independent Test**: Annotate a Ready `Cdn` with `purge: full`; verify exactly one upstream
purge fires, an Event records it, and the annotation disappears; repeat with a path list and
verify a partial purge of exactly those paths.

**Acceptance Scenarios**:

1. **Given** a Ready `Cdn`, **When** the annotation is set to `all`, **Then** a full purge is
   requested upstream exactly once, an Event records it, `lastPurgedAt` advances, and the
   annotation is removed.
2. **Given** a Ready `Cdn`, **When** the annotation is set to a comma-separated path list,
   **Then** a partial purge of exactly those paths is requested, recorded in an Event, and the
   annotation is removed.
3. **Given** an upstream purge call that fails (e.g. the fresh-resource 500 window),
   **When** the controller retries on subsequent reconciles, **Then** the annotation remains
   in place until a purge succeeds, a warning Event explains each failure, and the resource's
   own Synced/Ready conditions are unaffected.
4. **Given** a not-yet-Ready `Cdn` with the annotation set, **When** it becomes Ready, **Then**
   the pending purge is honored.

---

### User Story 4 - Deletion and lifecycle states (Priority: P4)

The operator deletes the `Cdn` resource and the upstream CDN resource is removed. If the
upstream enters a non-serving state out-of-band (suspended for traffic-limit overrun, paused,
or deleted in the panel), the resource surfaces it: Ready goes False with a reason, and a
panel-deleted upstream is re-created (declared state wins).

**Why this priority**: Required for a complete lifecycle but exercised least often.

**Independent Test**: Delete the MR and verify the upstream disappears and the finalizer
releases; separately, suspend/delete upstream in the panel and observe the condition/recreate.

**Acceptance Scenarios**:

1. **Given** a Ready `Cdn`, **When** the MR is deleted, **Then** the upstream resource is
   deleted, and the finalizer releases even if the origin `S3Bucket` was already deleted first.
2. **Given** an upstream deleted out-of-band, **When** the resource is reconciled, **Then**
   Observe reports it missing and the controller re-creates it.
3. **Given** an upstream in a suspended/paused state, **When** observed, **Then** Ready=False
   with a reason distinguishing billing/limit suspension from transient provisioning.

---

### Edge Cases

- Undocumented API: response shapes may shift without notice; 2xx ≠ converged — every mutation
  is verified by re-observation, and quirks are recorded (support ticket + `_next` preface).
- `preset_id` semantics unknown: single fixed preset vs. selectable tiers — if enumerable via
  `/cdn/presets`, resolution must not require the operator to hard-code a numeric id.
- Origin bucket deleted while the CDN still references it: reference resolution must not wedge
  the `Cdn` finalizer (deletion proceeds without re-resolving).
- Purge requested while the upstream is mid-provisioning or suspended: defer, don't fail the
  reconcile permanently.
- Upstream `status` sticks at `processing` indefinitely (live-verified) while the resource
  serves normally: Ready and update/purge behavior MUST NOT key on it (see Clarifications);
  only the suspended family gates.
- Settings convergence: PATCHed values appear in the configuration readback immediately
  (live-verified), so the diff clears on the next Observe; pacing (one PATCH per reconcile)
  is the only push throttle needed.
- The 3-delivery-domain limit and custom domains are out of scope, but the configuration read
  returns `domains.aliases` (including the technical domain, asymmetrically with what the
  panel writes) — status mirrors must tolerate the array without owning it, and drift logic
  must not fight it.
- The configuration read is secret-bearing (plaintext AWS-auth S3 keys under `origin.aws`):
  it must never be logged verbatim and the `aws` block must never be mirrored into status.
- Rapid create/delete cycles against a Qrator-protected API: reconciliation stays within the
  provider's existing conservative rate limits.
- Adoption: an existing panel-created CDN resource with matching external-name is adopted, not
  duplicated.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The provider MUST offer a new namespaced managed resource kind **`Cdn`** in group
  **`cdn.m.timeweb.crossplane.io`**, version `v1alpha1`, managing one Timeweb Cloud CDN
  resource (`/api/v1/cdn/http-resources` surface).
- **FR-002**: The spec MUST accept identity fields: display `name` (defaulted from the MR name
  if omitted), optional `description`, and the standard project assignment used by the
  provider's other kinds.
- **FR-003**: The spec MUST model the origin as exactly one of: `bucketRef` (typed reference to
  an `S3Bucket` in the same namespace), `domain` (hostname string), or `ip` (IPv4 string) —
  enforced at admission — plus an `https` toggle (default true) selecting the origin scheme,
  and an optional origin `port` (defaulted by scheme).
- **FR-004**: `bucketRef` resolution MUST derive the bucket's upstream storage id
  (probe-verified: bucket origins attach by `storage_id`), MUST wait (with a clear condition)
  while the bucket is missing or not Ready, and MUST NOT block deletion of the `Cdn` when the
  bucket is already gone.
- **FR-005**: The spec MUST accept optional cache settings: edge (CDN) caching as an
  enable + TTL-seconds pair, browser caching likewise, always-online (serve stale when origin
  is down), and a query-string cache-key toggle (panel-verified, undocumented).
- **FR-006**: The spec MUST accept an optional security setting: forced HTTP→HTTPS redirect on
  delivery domains.
- **FR-007**: The spec MUST accept optional performance/content settings: HTTP/3, gzip
  compression, large-file slicing with block size 1–1024 MB, content optimization mode
  (`off` (default) / `video` / `images` — panel-verified, undocumented), and robots.txt mode
  (`deny` (default) / `proxy` / `custom` with inline directives).
- **FR-008**: The spec MUST accept optional CORS configuration: list of allowed origins (exact,
  wildcard-subdomain, dot-prefixed, or regex forms as upstream supports) and an
  always-add-header toggle.
- **FR-009**: The spec MUST accept an optional list of custom request headers (name/value)
  forwarded to the origin, bounded by MaxItems within the CRD CEL cost budget.
- **FR-010**: The controller MUST be the single writer for all declared fields: Observe diffs
  declared vs. upstream state as sole authority, Update pushes owned fields only, and
  out-of-band edits to declared fields are reverted; fields omitted from the manifest are left
  untouched upstream and mirrored read-only in `status.atProvider`.
- **FR-011**: `status.atProvider` MUST expose at least: upstream id, technical delivery domain
  (`cdn_domain`), upstream state, `lastPurgedAt`, and mirrors of the observed settings; the
  external-name annotation MUST record the upstream id. Secret-bearing configuration fields (the AWS-auth
  key pair) MUST NOT appear in status, conditions, events, or logs.
- **FR-012**: A `cdn.timeweb.crossplane.io/purge` annotation MUST trigger exactly one upstream
  cache purge. Value grammar (unambiguous because upstream paths are root-relative): the exact
  literal `all` → full purge (upstream `purge_type: full`); otherwise the value MUST be a
  comma-separated list where every entry starts with `/` → selective purge of those paths (a
  file literally named `all` is addressed as `/all`). The controller MUST emit a Kubernetes Event describing what was
  purged, record the completion time as `status.atProvider.lastPurgedAt`, remove the
  annotation only after the upstream call succeeds (removal is the one-shot/idempotency
  guarantee), keep the annotation and retry with backoff on failure (warning Event), and on a
  value matching neither form (empty, or any entry without a leading `/`) emit a warning Event
  naming the bad entry and remove the annotation without purging.
- **FR-013**: Deletion MUST remove the upstream resource, treat upstream-not-found as success,
  and release the finalizer without requiring reference resolution.
- **FR-014**: The resource MUST follow the provider's established condition vocabulary
  (Synced/Ready, `UpstreamFailed` on rejected mutations) and map upstream suspended/paused
  states to Ready=False with a distinguishing reason.
- **FR-015**: Every mutation MUST be verified by re-observation before the resource reports
  converged (2xx is not trusted), consistent with the provider's Timeweb API handling.
- **FR-016**: Validation MUST include a kuttl e2e bundle for the new kind and a live gate that
  provisions a CDN against a real origin, verifies content delivery via the technical domain,
  exercises one settings drift-reversion and one purge, and deletes cleanly.
- **FR-017**: When the origin is a `bucketRef`, the controller MUST enable upstream AWS auth
  automatically using account S3 keys derived at runtime (the feature-012 mechanism: fetched
  per reconcile, never cached, never logged, never surfaced in manifest or status), so private
  buckets serve through the CDN with zero operator-facing credential plumbing; for
  `domain`/`ip` origins the controller MUST NOT touch the upstream AWS-auth block.

### Key Entities

- **Cdn (managed resource)**: The declared CDN — identity (name, description, project), origin
  (bucket reference | domain | IP, scheme, port), settings groups (cache, security,
  performance, CORS, request headers), and observed state (id, technical domain, upstream
  status, settings mirror, last purge time).
- **Upstream CDN resource (`http_resource`)**: The Timeweb Cloud entity created per `Cdn`;
  numeric id, `source` origin, auto-assigned `cdn_domain`, lifecycle `status`, `preset_id`,
  optional `storage_id` linkage for bucket origins, read-only traffic telemetry.
- **CDN preset**: Upstream billing/tier object referenced at create (`preset_id`); enumerable
  via the presets endpoint; not operator-facing unless research shows multiple orderable tiers.
- **S3Bucket (existing kind)**: Optional origin target referenced by `bucketRef`; its storage
  id / endpoint is the derived origin.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Applying a valid `Cdn` manifest yields a Ready resource with the technical
  delivery domain in status within 5 minutes, with no panel interaction.
- **SC-002**: A file present on the origin is retrievable through the technical delivery domain
  once the resource is Ready (live gate check).
- **SC-003**: A declared-field change made in the panel is reverted by the controller within
  2 reconcile cycles, and a manifest settings change reaches upstream within 1.
- **SC-004**: Setting the purge annotation results in exactly one upstream purge per
  annotation set (full or the exact declared paths), an Event recording it, `lastPurgedAt`
  updated in status, and the annotation's removal, with zero duplicate purges across 10
  subsequent reconciles.
- **SC-005**: Deleting the `Cdn` removes the upstream resource with no orphaned CDN entities in
  the account, including when the origin bucket was deleted first.
- **SC-006**: The kuttl e2e bundle for the kind passes in the standard harness; all invalid
  manifests (origin oneof violations, out-of-range slicing size) are rejected at admission with
  actionable messages.

## Assumptions

- The undocumented `/api/v1/cdn/*` surface is reachable with the provider's existing bearer
  token on `api.timeweb.cloud` (401-confirmed to exist there; token acceptance to be verified
  in the plan-phase probe) and is stable enough to build on, as `routers` was in feature 006.
- The plan phase includes an authenticated probe to capture what devtools has not yet shown:
  the full `config` schema and how it is read back (the GET envelope lacks `config`), PATCH
  partial-vs-full semantics per key, the IP-origin create shape, the purge endpoint, delete
  semantics, preset enumeration, and the terminal `status` values — findings land in
  `research.md` and, where they are quirks, in a support ticket / `_next` preface per project
  practice.
- A single default CDN preset is assumed orderable (`preset_id` resolvable at runtime, not
  hard-coded by the operator); if multiple tiers exist, v1 picks the base tier and defers
  operator-facing tier selection.
- Custom delivery domains, SSL certificate management (`/cdn/certificates`), secure-token
  signed URLs, traffic-limit configuration, and pause/resume are OUT of scope for v1 and are
  natural follow-ups; status must not fight upstream values for these.
- CDN billing (≈1₽/month per resource plus per-GB traffic) is accepted on the live account used
  for the e2e gate; live-gate resources are created fresh and deleted (no touching
  `inyan-infra`/`cloud-infra`; the probe resource `22209` "Ambitious Jackdaw" is the user's to
  clean up).
- The existing e2e harness, ProviderConfig, error-classification, and rate-limiting
  conventions carry over unchanged; no selector-based origin references in v1 (consistent with
  the project's refs-first, selectors-stubbed posture).
