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

## 2b. Secure token / signed URLs (panel-documented, wire shape pending)

Panel form: secret key + "ограничение доступа по IP-адресу" checkbox. Signing
algorithm (panel docs, captured 2026-07-13): token = urlsafe-b64(md5(
`<secret><path><ip><expires>`)) with `=` stripped, `+`→`-`, `/`→`_`; URL form
`https://<cdn-domain>/md5(<token>,<expires>)/<path>`; ip/expires omitted from
the string when their checks are off; the CDN domain is not part of the
signature. Spec surface: `security.secureToken: {secretRef, bindClientIP}`
(key from a Secret, never in spec/status). BLOCKED on one devtools capture:
the save-PATCH body for `config.security.secure_token`.

## 2c. Outbound traffic limit (panel-verified 2026-07-13)

Panel: enable toggle + limit in **GB/month**; on exceeding, the resource is
SUSPENDED — with up to a **2-hour delay during which traffic keeps billing**
(panel warning; ops-relevant). Wire: `traffic_limit_bytes` on the resource
(null = off; read-mirrored in status already). Spec surface:
`trafficLimitGBPerMonth *int64` (nil = off) → bytes conversion on write; the
provider's `Ready=False reason=Suspended` mapping already covers the exceeded
state. OPEN: one save-PATCH capture to confirm the write field name/units.

## 3. Upstream quirks to track (RU support ticket drafted 2026-07-12)

- `status` sticks at `processing` for hours before settling to `created`;
  panel badge + purge spinner track it.
- `clear-cache`: 500 in the first ~2 minutes after create, 429 on repeats.
- Create with a non-DNS-resolvable origin host → bare 500 (should be 4xx).
- Qrator IP bans triggered by error-loop traffic (two incidents 2026-07-12);
  provider now keeps purge retries poll-paced and origin-gated creates
  API-silent — keep that discipline in future controllers.

## 4. e2e note

kuttl bundle 23's purge assert accepts the retry posture (PurgeFailed +
annotation retained) because purge completion on a fresh resource exceeds the
240s kuttl ceiling (~2 min refusal window + poll pacing). A matured-resource
purge landed live (bucketRef leg). Revisit if kuttl timeouts become tunable
per-step.
