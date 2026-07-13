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

## 2b. Secure token / signed URLs (panel-documented, wire shape pending)

Panel form: secret key + "ограничение доступа по IP-адресу" checkbox. Signing
algorithm (panel docs, captured 2026-07-13): token = urlsafe-b64(md5(
`<secret><path><ip><expires>`)) with `=` stripped, `+`→`-`, `/`→`_`; URL form
`https://<cdn-domain>/md5(<token>,<expires>)/<path>`; ip/expires omitted from
the string when their checks are off; the CDN domain is not part of the
signature. Spec surface: `security.secureToken: {secretRef, bindClientIP}`
(key from a Secret, never in spec/status). BLOCKED on one devtools capture:
the save-PATCH body for `config.security.secure_token`.

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
