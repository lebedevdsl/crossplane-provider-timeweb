# Error classification: the canonical-not-found rule

This provider reconciles declared Kubernetes state against the Timeweb Cloud API. The single
most dangerous mistake a controller can make is to conclude a resource is **deleted** when it
is merely temporarily unreachable — because under a Create-enabled management policy, `Observe`
returning `ResourceExists=false` makes the managed reconciler immediately **recreate** the
resource. If the "gone" signal was a false alarm, the result is a duplicate cloud resource and
an orphaned live one.

This is not hypothetical. On **2026-07-20** a single flaky HTTP 404 for a live VPC (the Timeweb
API sits behind Qrator DDoS protection and intermittently returns edge 404s) was read as a
deletion; the provider recreated the network as an empty duplicate and orphaned the production
VPC holding all of staging (postmortem: work item #124).

## The rule (Constitution Principle II)

> A resource is reported absent (`Observe` → `ResourceExists=false`) **only** on a canonical,
> precisely-classified not-found signal for that API — **never on the HTTP status code alone**.
> An ambiguous 404 without the canonical signal is classified **transient** and requeued, never
> treated as a deletion.

"When in doubt, retry — never recreate." Safety (never destroying a live resource) is preferred
over liveness (promptly noticing a genuine deletion).

## Canonical not-found signal, per API

| API / client | Canonical not-found signal | Where |
|--------------|---------------------------|-------|
| **Timeweb** (`internal/clients/timeweb`) | the documented error **envelope**: `error_code` present (the `not-found` response requires `status_code`/`error_code`/`response_id`) | `Classify` 404 branch, `errors.go` |
| **RGW / AWS IAM** (`internal/clients/rgwiam`) | the exact error code **`NoSuchEntity`** | `classifyQueryError`, `sigv4.go` |
| **future clients** | that API's documented canonical not-found | must follow this rule |

A genuine Timeweb 404 always carries the envelope (generic `error_code: "not_found"`). An
edge/Qrator/gateway 404 arrives as an HTML page, an empty body, or non-envelope JSON — no
`error_code` — and is therefore classified transient.

## What this means when you add a resource

- Route every upstream 404 through the shared classifier. In a Timeweb controller that is
  `errors.Is(err, timeweb.ErrNotFound)`; in the S3User grant path it is
  `errors.Is(err, rgwiam.ErrNoSuchEntity)`. **Do not** inspect `resp.StatusCode` /
  `http.StatusNotFound` yourself and map it to `ResourceExists:false`.
- The guard test `internal/controller/notfound_guard_test.go` fails the build if a controller
  references the 404 status directly. A client that classifies not-found by its own canonical
  signal (like `rgwiam`) is fine — the rule forbids the status-alone shortcut, not precise
  classification.
- **Undocumented endpoints** (e.g. the CDN `/cdn/*` surface, absent from
  `docs/openapi-timeweb.json`): confirm by a **live 404 capture** that a genuine deletion returns
  the envelope before relying on envelope-presence. If a real deletion returns a bare 404 for
  some type, add a per-type corroboration step (a second read / list lookup) for that type — the
  conservative default (requeue) is already safe in the meantime.

## Test coverage

- Classifier contract: `internal/clients/timeweb/errors_test.go` (enveloped 404 → `ErrNotFound`;
  empty / HTML / non-envelope 404 → transient; deletion detail preserved).
- Bypass guard: `internal/controller/notfound_guard_test.go` (no controller derives absent from
  a raw status).

See `specs/019-fix-false-notfound-recreate/` for the full spec, research, and the industry
survey (ACK / Upjet / KCC / crossplane-runtime all trust a single well-classified not-found,
never a bare status).
