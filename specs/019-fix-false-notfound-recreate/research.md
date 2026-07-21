# Phase 0 Research: Fix false not-found → resource recreation

## R-1 — Is the canonical not-found envelope defined and referenced?

**Decision**: Yes. Treat presence of the canonical Timeweb error envelope as the sole
discriminator for "genuinely deleted" on a 404.

**Evidence** (`docs/openapi-timeweb.json`, audited 2026-07-21):
- `components/responses/not-found` defines the JSON error body with
  `required: [status_code, error_code, response_id]` and properties
  `status_code`, `message`, `error_code`, `response_id`.
- `components/responses/404` is a `$ref` alias to `not-found`.
- **256** endpoints reference `components/responses/404` for their 404 (every documented GET a
  controller calls: `/api/v2/vpcs/{id}`, `/api/v1/servers/{id}`, `/api/v1/k8s/clusters/{id}`,
  `/api/v1/routers/{id}`, `/api/v1/firewall/groups/{id}`, `/api/v1/ssh-keys/{id}`,
  `/api/v1/projects/{id}`, storage/registry surfaces, …).

**Rationale**: A genuine upstream not-found always carries the envelope; an edge/Qrator/gateway
404 arrives as HTML or an empty body with no envelope. The industry standard (ACK, Upjet, KCC,
crossplane-runtime — see work item #124 notes) is to trust a single *well-classified* not-found,
not a bare status.

**Alternatives considered**: (a) match a resource-specific code like ACK's `NoSuchBucket` —
rejected: Timeweb emits a generic `not_found` for all 404s, so there is no per-kind code to key
on; envelope-presence is the available signal. (b) list/second-read corroboration before
concluding deleted — deferred: above industry norm, reserved for a type whose real deletion is
found to return a bare 404 (see R-4).

## R-2 — What exactly discriminates envelope-present?

**Decision**: Envelope-present ⇔ the 404 body decodes as JSON into the error shape with a
non-empty `error_code`. Absent (HTML, empty, non-JSON, or JSON lacking `error_code`) → transient.

**Rationale**: `error_code` is a required field of the documented envelope and is populated
(`not_found`) on real 404s; it is the most robust single marker. `response_id` corroborates but
`error_code` alone is sufficient and simplest. This reuses the existing `errorResponseBody`
decode already performed by `readErrorDetail`.

## R-3 — Is the response body still readable when Classify runs?

**Decision**: Yes — reading the body in the 404 branch is safe.

**Evidence**: Controllers call the **raw** generated client method (e.g.
`e.tw.GetVPC(ctx, id)` → `generated.Client.GetVPC`) which returns `*http.Response` with an
un-consumed body; they do **not** call the `ParseGetVPCResponse` wrapper (whose
`StatusCode == 404` decoders were the only raw-404 reads found outside `errors.go`). `Classify`
returns `nil` immediately on 2xx *without* touching the body, and the controller only calls
`DecodeBody(resp.Body, …)` after a `nil` classification. On a 404, `Classify` reads the body and
the controller does not read it again. No double-read, no consumed-body hazard.

## R-4 — FR-014 per-type audit (documented vs. undocumented surfaces)

**Decision**: All managed kinds inherit the envelope rule via documented endpoints **except
CDN**, which is flagged for a live 404 capture.

**Evidence**: Grep of `docs/openapi-timeweb.json` — `cdn` paths: **none**. CDN is served by the
hand-written `internal/clients/timeweb/cdn.go` on the undocumented `/api/v1/cdn/*` surface, so
its genuine 404 shape is not contract-guaranteed. Router *is* present (`/api/v1/routers*` with
the 404 envelope ref) in the hand-patched spec, so it inherits the rule at the contract level.

**Consequence**: The conservative default (envelope-absent → transient) is **safe** for CDN —
it can never wrongly recreate a live CDN resource; the only downside is that a genuine external
CDN deletion returning a bare 404 would requeue instead of being recognized, which is low-impact
(CDN Ready is not gated on upstream status; drift adoption stalls rather than corrupts). Action:
capture a real CDN-delete 404 at the e2e gate and, if it lacks the envelope, add per-type
corroboration for CDN in a follow-up. Documented in spec Assumptions + FR-014.

## R-5 — Does any non-Timeweb classifier need changing?

**Decision**: No. `rgwiam` is already compliant and is left unchanged (FR-013).

**Evidence**: `internal/clients/rgwiam/sigv4.go:204` — `classifyQueryError` returns
`ErrNoSuchEntity` **only** when `env.Error.Code == "NoSuchEntity"` (exact AWS IAM code), else a
`*QueryError` (terminal, transient-aware). There is no status-code-alone not-found path. The
S3User controller treats `ErrNoSuchEntity` on the grant/policy read as drift (re-put), not as a
resource deletion. This is exactly the "canonical signal, never status-alone" rule instantiated
for a different API.

## R-6 — How to guard against future reintroduction?

**Decision**: A Go source-scan test (`internal/controller/notfound_guard_test.go`) — standard
ecosystem, no new lint dependency.

**Approach**: Walk `internal/controller/**/*.go` (excluding `_test.go`) and fail if any file
references `http.StatusNotFound` or a literal `404` status comparison, or otherwise derives a
not-found decision from a raw status rather than routing through the shared classifier
(`errors.Is(err, timeweb.ErrNotFound)` / `errors.Is(err, rgwiam.ErrNoSuchEntity)`). The
generated client package and `errors.go`/`rgwiam` are out of scope for the scan. This directly
enforces FR-004/FR-012 and permits any client with its own canonical signal (FR-009).

**Alternatives considered**: golangci-lint `forbidigo` rule — equivalent effect but adds lint
config surface; the self-contained test is preferred and runs in the existing `go test` gate
(Constitution III).

## R-7 — Observability of a reclassified 404 (FR-007)

**Decision**: Surface it via a descriptive `TransientError.Reason` (e.g. "404 without canonical
error envelope — suspected upstream flap, not treating as deleted") carried on the standard
requeue path (Synced-condition reason + structured provider log), rather than adding a
per-controller Kubernetes Event.

**Rationale**: Keeps the fix centralized (FR-004) — a per-controller Event would touch all 12
controllers. The transient reason is already surfaced by the managed reconciler on requeue,
closing the silent-requeue gap the postmortem called out, at the shared layer.
