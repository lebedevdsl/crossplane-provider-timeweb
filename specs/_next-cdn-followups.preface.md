# _next: CDN follow-ups (post-016 / v0.7.0)

Seeds for the next CDN round, collected from the 016 live gate (2026-07-12).

## 1. Query-string cache-key modes (panel: Разрешенные / Запрещенные)

The panel's "Учитывать query string" has THREE modes — Все (all) / Разрешенные
(allowed-list) / Запрещенные (forbidden-list) — with a query-parameter list for
the latter two (screenshots in session; e.g. `utm_source,ref,v`). v0.7.0 models
only the bool (`queryStringInCacheKey` → `query_args: {mode:"all"} | null`).

- Wire shape UNPROBED for the list modes: `mode` values (likely
  `whitelist`/`blacklist`-style; only `"all"` verified valid, `none`/`ignore`/
  `disabled` rejected) and the params key. One PATCH probe with validation-error
  reading (the API self-describes) settles it.
- CRD design: additive — keep the bool, add e.g.
  `queryStringCacheKeyMode: all|allowed|forbidden` + `queryStringCacheKeyParams: []`
  with CEL (params required iff mode != all; bool and mode mutually exclusive),
  OR deprecate the bool in a v1alpha2. Decide at spec time.

## 2. Deferred v1 scope (spec 016 Clarifications)

Custom delivery domains (`config.domains.aliases` — mind the read/write
asymmetry, R-9), SSL certificates (`/api/v1/cdn/certificates` exists;
Let's Encrypt + custom), secure token (`security.secure_token`), traffic limits
(`traffic_limit_bytes`), selective purge exposure beyond the annotation,
operator-facing AWS-auth for EXTERNAL private S3 origins — in-account buckets
auto-wire upstream (officially: "Для приватных бакетов Timeweb Cloud ключи
подставляются автоматически", panel AWS-auth drawer). External form =
access/secret key pair → will need a secretRef surface.

## 2a. Custom delivery domains (panel-verified 2026-07-13 on 22209)

Up to **2 custom subdomains** + the immutable technical domain (3 total).
Operator sets a CNAME → technical domain at their DNS provider (auto-updated
when the domain is on Timeweb NS). Live example: `s3.inyan-rolly.ru`,
`s3.volpepizza.ru` on 22209. Wire: `config.domains.aliases` — BUT the earlier
capture showed the panel WRITING `aliases: []` while the read includes the
technical domain (R-9 asymmetry). OPEN: capture the domains-drawer save-PATCH
with customs present — does the write carry just the customs, or tech domain
too? Spec surface: `domains: [{name}]` (MaxItems=2) + per-domain sslStatus in
status; pairs with SSL (Let's Encrypt needs the CNAME live first).
Synergy: as of v0.7.2 the Cdn publishes `technical_domain`/`url` to its
connection Secret — a future **DNS record kind** can consume that Secret to
manage the CNAME, closing the chain Cdn → DNS CNAME → custom domain → LE SSL
with zero manual DNS steps.

## 2b. Secure token / signed URLs (panel-documented, wire shape pending)

Panel form: secret key + "ограничение доступа по IP-адресу" checkbox. Signing
algorithm (panel docs, captured 2026-07-13): token = urlsafe-b64(md5(
`<secret><path><ip><expires>`)) with `=` stripped, `+`→`-`, `/`→`_`; URL form
`https://<cdn-domain>/md5(<token>,<expires>)/<path>`; ip/expires omitted from
the string when their checks are off; the CDN domain is not part of the
signature. Spec surface: `security.secureToken: {secretRef, bindClientIP}`
(key from a Secret, never in spec/status). WRITE shape captured 2026-07-13
(request body): `config.security.secure_token = {"secret_key": "<key>",
"restrict_by_ip": bool}` (null = off, per the cache-subfeature convention).
Disable captured too (request body 2026-07-13):
`config.security = {"redirect": false, "secure_token": null}` — explicit null
per the cache-subfeature convention, and the panel replaces the security
SECTION wholesale (`redirect` included) ⇒ implementation must switch
`CDNConfigSecurity` to full-section-replace semantics (drop omitempty on its
keys, mirror the cache write struct). Readback UNKNOWN: whether the
configuration GET echoes `secret_key` (like `origin.aws`) or masks it —
decides the diff strategy (default: presence + `restrict_by_ip` diff, key
written through like robots `content`; never logged/mirrored either way).
READY to implement.

## 2c. Outbound traffic limit (panel-verified 2026-07-13)

Panel: enable toggle + limit in **GB/month**; on exceeding, the resource is
SUSPENDED — with up to a **2-hour delay during which traffic keeps billing**
(panel warning; ops-relevant). Wire: `traffic_limit_bytes` on the resource
(null = off; read-mirrored in status already). Spec surface:
`trafficLimitGBPerMonth *int64` (nil = off) → bytes on write; the provider's
`Ready=False reason=Suspended` mapping already covers the exceeded state.
WRITE shape captured 2026-07-13 (request body): top-level PATCH
`{"traffic_limit_bytes": N}` —
panel "ГБ/мес" is GiB (3000 ГБ → 3221225472000 = 3000×2^30). READY to
implement.

## 2d. Platform identified: CDNvideo (2026-07-13)

The technical domain CNAMEs to `*.a.trbcdn.net` — Timeweb's CDN is a
white-label of **CDNvideo** (ООО «СДН-видео»). Their public docs
(doc.cdnvideo.ru) explain several observed behaviors: settings propagation
"up to 15 minutes" (the processing/apply badge mechanism — Timeweb's status
mapping holds it much longer), query-string modes all/whitelist/blacklist
(one list max), the FULL stale_conditions vocabulary (errors, timeouts,
invalid responses, 500/502/503/504, `updating`), the signed-link algorithm
incl. **410 Gone for expired links** (Timeweb docs claim only 403), custom
certificate renewal being MANUAL upstream, image optimization ≤2 MB, WebP
conversion async, 100 resources/account. CDNvideo's own API may document
purge limits etc. — a useful secondary reference for future rounds.
Their open-source Terraform provider
(github.com/opensource-cdnvideo/terraform-provider-cdnvideo, resource
`cdnvideo_http`) documents the ENGINE's full schema — stale_conditions set,
tokenized access, geo/IP/referer/UA limitations, ssl_protocols, SNI, cookie
cache keys. **REFERENCE ONLY (operator directive 2026-07-13): Timeweb's
facade differs from CDNvideo's native API (e.g. `query_args {mode,list}` vs
`consider_args`/`args_whitelist`) and the integration depth is unknown —
NEVER derive wire shapes or design from these docs; first-party captures
remain the sole authority.**

## 2e. SYSTEMIC: per-reconcile rate limiter (not CDN-specific)

`timeweb.New` is called in every controller's Connect (20 sites) → each
reconcile gets its OWN 2 r/s limiter, so concurrent/looping reconciles blow
past Timeweb's server-side limit → 429 (observed on the CDN 017 error-loop,
which froze status mirrors). Fix (future, all-kinds): a PROCESS-GLOBAL limiter
shared across reconciles/controllers (build the timeweb.Client once in main,
inject; or a package-level limiter). Contradicts the aspirational memory note
that claimed a global limiter already exists.

## 3. Upstream quirks to track (RU support tickets FILED — Qrator/CDN batch
2026-07-13, incl. LE-fails-with-correct-CNAME on resource 22209)

- `status` sticks at `processing` for hours before settling to `created`;
  panel badge + purge spinner track it.
- `clear-cache`: 500 in the first ~2 minutes after create, 429 on repeats.
- Create with a non-DNS-resolvable origin host → bare 500 (should be 4xx).
- `POST /cdn/certificates` returns 204 with NO body — the created id is never
  disclosed (and ids are RECYCLED), forcing identity-based ownership tracking.
- Certificate upload rejects any field beyond `{certificate, private_key}`
  ("property resource_id should not exist") yet the certificate is
  resource-scoped — no documented way to target the resource in the request.
- LE certificate tasks (`/cdn/certificates/tasks`) carry NO failure reason —
  `status: failed` with no message/error field; undiagnosable via API.
  Successful tasks VANISH from the list, yet `/issue` can still 409 with
  `cert_issue_task_already_exists` even after the certificate was deleted —
  an invisible/cached task record the API neither shows nor expires promptly.
- Qrator IP bans triggered by error-loop traffic (two incidents 2026-07-12);
  provider now keeps purge retries poll-paced and origin-gated creates
  API-silent — keep that discipline in future controllers.

## 4. e2e note

kuttl bundle 23's purge assert accepts the retry posture (PurgeFailed +
annotation retained) because purge completion on a fresh resource exceeds the
240s kuttl ceiling (~2 min refusal window + poll pacing). A matured-resource
purge landed live (bucketRef leg). Revisit if kuttl timeouts become tunable
per-step.

## 5. CDN 404 shape — verify the canonical not-found envelope (feature 019 FR-014)

Feature 019 (v0.9.1) narrowed `timeweb.Classify`: a 404 is "deleted" only when it
carries the canonical error envelope (`error_code` present); envelope-less 404s
(HTML/empty) are transient → requeue, never recreate. All **documented** endpoints
reference the envelope via `components/responses/404` → `not-found`, but **CDN**
(`/api/v1/cdn/*`, hand-written `cdn.go`) is **absent from the OpenAPI spec**, so its
genuine delete-404 shape is unverified.

- Conservative default is SAFE: envelope-absent → transient means a CDN never gets
  wrongly recreated. The only downside if CDN returns a *bare* 404 on real deletion:
  the CR requeues instead of recognizing the deletion (drift-adoption stalls; Ready
  isn't gated on CDN upstream status anyway).
- ACTION: at the next CDN live gate, delete a CDN resource out-of-band and capture
  the raw 404 (headers + body). If enveloped → FR-014 closed. If bare → add per-type
  corroboration for CDN (second read / list before concluding deleted), keyed off the
  same rule. Not run in the 019 e2e (the token got Qrator-banned mid-run).
