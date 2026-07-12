# Research: CDN Resource (016)

Phase 0 findings. Wire evidence is from the 2026-07-12 devtools capture session against live
resource `22209` ("Ambitious Jackdaw") — see spec.md "Live probe findings" for raw payloads.
Items marked **OPEN(probe)** need one authenticated probe before/at implementation start; each
carries a ready-to-run command. None blocks Phase 1 design — fallbacks are specified.

> **Probe session 2 (2026-07-12, implementation day, token-authenticated on
> `api.timeweb.cloud`, throwaway resource `22219` "probe-016-cdn"):** P-1, P-3, P-5, P-6
> RESOLVED, P-2 run (see additions inline). Highlights: create **500s (bare
> `internal_server_error`) when the origin host does not resolve in public DNS**; presets
> envelope is `http_resource_presets` with `cost`/`rate_cost` (single preset 3807); cache
> sub-features are **objects-or-explicit-null** (`browser: {ttl: int}`,
> `always_online: {stale_conditions: [error|timeout|…]}`, `query_args: {mode: "all"}`);
> robots custom `content` is write-accepted but NOT echoed by the configuration read
> (type-only diff); CORS `{domains, always}` and `request` map confirmed exactly;
> `clear-cache` on a still-`processing` resource → **500** (the controller's purge-defer
> avoids this); API `processing` == the panel's "Применяются настройки" badge (one
> transitional state for provisioning AND settings applies). P-4 (bucket-origin aws
> auto-wire) remains for the live gate.

## R-1 — Endpoint inventory & host

**Decision**: Use `https://api.timeweb.cloud/api/v1/cdn/...` with the existing Bearer token.

**Evidence**: All CDN paths return 401 (exists) unauthenticated on BOTH `timeweb.cloud` and
`api.timeweb.cloud`; control endpoint behaves identically. Captured working calls (panel host):

| Verb | Path | Status | Notes |
|------|------|--------|-------|
| POST | `/api/v1/cdn/http-resources` | 201 | create; `http_resource` envelope |
| GET | `/api/v1/cdn/http-resources/{id}` | 200 | resource read (no settings) |
| PATCH | `/api/v1/cdn/http-resources/{id}` | 200 | partial update; `config` nesting |
| GET | `/api/v1/cdn/http-resources/{id}/configuration` | 200 | full settings; SECRET-BEARING |
| POST | `/api/v1/cdn/http-resources/{id}/clear-cache` | 204 | `{purge_type: full|partial, paths}` |
| GET | `/api/v1/cdn/presets` | 401→exists | shape unknown (R-4) |
| GET | `/api/v1/cdn/certificates` | 401→exists | out of v1 scope |

**OPEN(probe) P-1**: token acceptance on the public host + list endpoint (adoption needs it):
`curl -s -H "Authorization: Bearer $TWC_TOKEN" https://api.timeweb.cloud/api/v1/cdn/http-resources | head -c 2000`

**OPEN(probe) P-2**: DELETE semantics (status code, idempotency on second call):
`curl -s -X DELETE -H "Authorization: Bearer $TWC_TOKEN" https://api.timeweb.cloud/api/v1/cdn/http-resources/22209 -w '%{http_code}'`
(run on the probe resource when done with it; run twice to observe the 404/behavior.)

**Rationale**: Public-host residency matches every other kind; panel host is a fallback the
provider already proved viable for nodepool PATCH (015).

## R-2 — Configuration schema ↔ spec mapping

**Decision**: One `config` wire struct in the hand-written client, mapped field-for-field:

| Spec field | Wire (`http_resource_configuration`) |
|---|---|
| `cache.edgeTTLSeconds` | `cache.cdn.ttl."2xx"` (+ `cache.cdn` presence = enabled) |
| `cache.browserTTLSeconds` | `cache.browser` (enable+TTL pair; exact key TBD at impl from PATCH echo) |
| `cache.alwaysOnline` | `cache.always_online` |
| `cache.queryStringInCacheKey` | `cache.query_args` |
| `security.forceHTTPS` | `security.redirect` |
| `performance.http3` | `delivery.http3` |
| `performance.gzip` | `delivery.gzip` |
| `performance.largeFileSlicingMB` | `delivery.large_files` + `delivery.slice_size` |
| `performance.contentOptimization` | `off`→both false; `images`→`delivery.image_optimization`; `video`→`delivery.packaging.mp4` |
| `performance.robots.mode/custom` | `robots.type` (`deny`/…) + custom payload key (TBD) |
| `cors.origins` / `cors.alwaysAddHeader` | `http_headers.cors` (null when off; exact populated shape TBD) |
| `requestHeaders[]` | `http_headers.request` map (name→value) |
| `origin.*` | top-level `server`/`storage_id` + `use_https` on write; `origin.servers[]`+`use_https`+`aws` on read |

Unset sections read as `null` ⇒ the differ treats `nil` section = "not owned, don't diff"
(matches spec FR-010: omitted blocks untouched + mirrored).

**OPEN(probe) P-3** (low-risk, fills the TBD cells): flip each remaining panel toggle once and
save, capturing the PATCH body — browser-cache pair, robots custom, CORS populated shape,
allowed_methods. Pure payload observation; can also be done during implementation against the
probe resource.

**Rationale**: The captured GET is authoritative for key names; the three TBD leaf shapes are
write-side details that don't affect the CRD schema, only the client marshaling.

## R-3 — AWS auth for bucket origins: upstream auto-wire hypothesis

**Decision**: Treat AWS auth as **upstream-automatic** for `storage_id` origins; controller
sends no keys. Fallback (if P-4 disproves): controller PATCHes `config.origin.aws` with
runtime-derived account keys via `deriveAdminKeys` (hoisted to a shared helper — currently
duplicated in `internal/controller/s3bucket/external.go:171` and
`internal/controller/s3user/connector.go:101`).

**Evidence**: The captured origin-switch PATCH carried only `{storage_id, config:{domains:...}}`
— no credentials — yet the subsequent configuration GET shows `origin.aws` populated with the
account's S3 keys, and the panel showed AWS-авторизация flipping Выключено→Включено across
that switch. Strong signal the backend wires keys itself when `storage_id` is set.

**OPEN(probe) P-4**: create a fresh resource with `{storage_id: <bucket-id>}` via API (no aws
field), then GET `/configuration` and check `origin.aws`. Decides auto-wire vs controller-set.

**Controller invariant either way**: the `aws` block is never diffed as an owned field for
`domain`/`ip` origins, never logged, never mirrored to status (spec FR-011/FR-017).

## R-4 — `preset_id` resolution

**Decision**: Resolve at Create by listing `/api/v1/cdn/presets` and picking the single/base
(lowest-price) preset; lock the chosen id in `status.atProvider.lockedPresetID` (S3Bucket
`LockedPresetID` idiom). No operator-facing tier field in v1; no new resolver dimension —
a one-endpoint lookup in the external client is enough (contrast: DimRouterPreset was needed
for sizing choice; CDN has no evident sizing).

**Evidence**: create required `preset_id: 3807`; docs price CDN flat (1₽/mo + traffic),
suggesting a single orderable preset.

**OPEN(probe) P-5**: `curl -s -H "Authorization: Bearer $TWC_TOKEN" https://api.timeweb.cloud/api/v1/cdn/presets`
— envelope + whether >1 preset exists. If multiple tiers appear, v1 still auto-picks base
(spec Assumption) and a `_next` note seeds tier selection.

## R-5 — Async settings apply & convergence

**Decision (REVISED at the live gate, 2026-07-12 — supersedes the original skip-guard)**:
the upstream `status` field is unreliable — it sticks at `processing` for hours on resources
that serve content (200 via cdn_domain), apply PATCHes (values in the configuration readback
immediately), and purge (204). The original design (Ready gated on leaving `processing`,
updates skipped while transitional) made Ready unreachable (kuttl bundle 23 run 2 timed out
on the Ready wait) and would starve day-2 updates. Final model, user-confirmed: **ignore
`processing`** — Ready = exists && not suspended-family (raw state mirrored in
`status.atProvider.state`); Update always allowed, paced one PATCH per reconcile, convergence
via the immediate readback (still "2xx ≠ converged": the DIFF decides, just not the status
field); purge ungated (early-lifecycle 500 → warning Event + retry, annotation retained).

**P-6 outcome (final, live gate session 3)**: the terminal state is **`created`** —
resources settle `processing` → `created` after an extended period (hours observed on 22209
and 22225). The ignore-processing model needs no change: unknown/`created` classify as
serving. Purge on a FRESH resource: refused with 500s only for the first ~2 minutes
(two poll-paced `PurgeFailed` retries), then `CachePurged` landed and the annotation
self-cleared — live-verified end-to-end on the bucketRef resource.

**Live-gate closure (2026-07-12, inyan-staging, dev-1783876993)**: bucketRef leg green —
OriginNotReady gate held Create while the bucket provisioned (Create reordered afterward so
gated retries stay API-silent), the S3Bucket watch unblocked it in <1 min, POST create with
`storage_id` accepted directly (P-4 create shape confirmed), source mirrored as the bucket
name, Ready/Synced True, purge landed, both MRs deleted cleanly (upstream list confirmed
empty of e2e resources; orphan 22225 removed via external-name adoption + delete). P-4 aws
auto-wire: confirmed indirectly (22209's configuration read shows upstream-populated
`origin.aws` nobody sent); the direct data-plane fetch through the fresh technical domain was
inconclusive (edge TLS/DNS not provisioned within the test window) — first real-world use
will settle it.

## R-6 — Purge annotation mechanics

**Decision**: Handle in the external client at the top of the post-existence Observe flow:
parse `cdn.timeweb.crossplane.io/purge` (`all` → `purge_type=full, paths=[]`; else split on
`,`, require every entry to start with `/` → `purge_type=partial`); on valid + upstream
serving: `POST clear-cache`, emit Event (`CachePurged`, listing scope), set
`status.atProvider.lastPurgedAt`, then remove the annotation with a direct `e.kube.Update`
(metadata-only) — removal AFTER the 204 is the one-shot guarantee; a conflict on the kube
Update is benign (next reconcile sees 204-already-done? no — it would re-purge; therefore
remove-annotation FIRST retry semantics are wrong; keep order POST→remove and accept the rare
double purge on kube-update conflict — purges are idempotent content-wise and harmless).
Invalid value (empty / entry without `/`): warning Event naming the bad entry + annotation
removal, no POST. Not-yet-serving resource: leave annotation, no Event spam (single
`PurgeDeferred` event on first sighting).

**Rationale**: crossplane-runtime persists metadata changes only via late-init on Observe;
a direct kube Update (pattern already used by ref-resolution controllers) is explicit and
testable. Grammar validation is pure-Go → unit-testable without HTTP.

## R-7 — Client strategy

**Decision**: Hand-write `internal/clients/timeweb/cdn.go` on the `doV2` plumbing
(`storages_users_v2.go` / `firewall.go` precedent): typed request/response structs with
underscore JSON tags (`http_resource`, `http_resource_configuration` envelopes), pointer
fields for PATCH partials so `omitempty` yields true partial updates. NOT regenerating from
`docs/openapi-timeweb.json` — the surface is absent from the published spec and hand-patching
~7 operations + ~10 schemas would dwarf the client file while inviting regen churn
(`project_openapi_handpatched_superset`).

**Alternatives considered**: hand-patch openapi + oapi-codegen (013 already rejected this
trade for a smaller gap); a separate `internal/clients/cdn` package (rejected — same host,
same token, same limiter ⇒ same package as firewall).

## R-8 — `bucketRef` resolution & Watches

**Decision**: `bucketRef` (namespace-local, same-ns) → `client.Get` the `S3Bucket`, require
`GetCondition(Ready).Status == True` AND `status.atProvider.id != nil`, use that id as
`storage_id`. Gate skipped when the Cdn has a `deletionTimestamp`
(`project_ref_gate_must_not_block_delete`). Add `Watches(&S3Bucket{}, mapper)` in SetupCdn
mapping bucket events to Cdn MRs whose `spec.forProvider.origin.bucketRef.name` matches
(nodepool parent-watch idiom, 60s-capped rate limiter) so bucket readiness unblocks creation
promptly instead of waiting a poll.

**Evidence**: S3Bucket external-name == upstream bucket id == the `storage_id` (528009) the
captured PATCH used.

## R-9 — `domains.aliases` read/write asymmetry

**Decision**: The controller NEVER writes `config.domains` (custom domains are out of scope)
and NEVER diffs it; `status.atProvider.domains` mirrors the observed aliases read-only.

**Evidence**: panel PATCHed `aliases: []` while the configuration GET returns
`aliases: ["<tech-domain>"]` — the alias set is not round-trip symmetric; owning it risks a
revert-fight that could detach the technical domain. Quirk recorded here per
`feedback_capture_upstream_quirks`; ticket-worthy only if it bites during live gate.

## R-10 — e2e design

**Decision**:
- **kuttl bundle 23** (no live API): CRD admission cases — origin oneof (0 and 2 origins
  rejected), slicing range, robots enum, purge-annotation is NOT admission-validated (runtime
  grammar) — plus create/assert-pending lifecycle against the fake-less local env as bundles
  do today (`kubectl wait --for=condition=...` asserts only, never positional).
- **Live gate** (operator-run, pinned context, after `e2e.up`+`e2e.deploy`): manifest with
  (1) `S3Bucket` `initialSizeGB: 10` (`project_timeweb_one_1gb_bucket_per_account`) +
  (2) `Cdn` with `bucketRef` + settings block. Checks: Ready + `technicalDomain` populated;
  upload an object, `curl https://<technicalDomain>/<object>` == content (SC-002); flip one
  setting in panel → reverted ≤2 reconciles (SC-003); annotate `purge: all` → Event +
  `lastPurgedAt` + annotation gone (SC-004); delete both → upstream empty (SC-005). Fresh
  resources only; `inyan-infra`/`cloud-infra` untouched.

**Rationale**: bundle/live split matches 013/015 practice; bucket origin in the live gate
exercises FR-017/R-3 end-to-end.
