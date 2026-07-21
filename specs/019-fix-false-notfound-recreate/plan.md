# Implementation Plan: Fix false not-found → resource recreation

**Branch**: `019-fix-false-notfound-recreate` | **Date**: 2026-07-21 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/019-fix-false-notfound-recreate/spec.md`

## Summary

A single flaky/edge HTTP 404 from the Timeweb API is currently classified as "resource
deleted" by the shared classifier (`internal/clients/timeweb/errors.go`) purely on the HTTP
status code, driving `Observe` → `ResourceExists:false` → `Create` and recreating a live
resource (work item #124: production VPC orphaned, empty duplicate created). The fix narrows
the 404 branch of `Classify` to return the not-found sentinel **only** when the response
carries the canonical Timeweb error envelope (`error_code` present, per the documented
`not-found` response schema whose required fields are `status_code`/`error_code`/`response_id`);
any envelope-less 404 (HTML, empty, non-envelope JSON — the shape edge/Qrator/gateway 404s
take) becomes a `TransientError` → requeue, never recreation. Because every controller routes
its 404 through this one function, the fix eliminates the bug class across all managed kinds in
one change. The bug class is then documented durably (Constitution principle + `docs/`
reference) and locked with two tests (a classifier 404 contract test and a source-scan guard
that fails if any controller derives "absent" from a raw status), with the general rule stated
so non-Timeweb clients that classify precisely by their own canonical signal (`rgwiam` /
`NoSuchEntity`) remain compliant and unchanged.

## Technical Context

**Language/Version**: Go (module tracks latest stable per project policy)

**Primary Dependencies**: crossplane-runtime (managed reconciler), hand-written Timeweb client
(`internal/clients/timeweb`), `rgwiam` AWS IAM Query client (unchanged)

**Storage**: N/A (stateless reconciler)

**Testing**: `go test` with fake Timeweb client (`internal/clients/timeweb/fake.go`); kuttl
e2e bundles against a live Timeweb account from the staging cluster

**Target Platform**: Linux controller image; published as a Crossplane `.xpkg`

**Project Type**: Crossplane provider (single Go module)

**Performance Goals**: No change; the fix adds one body-parse already performed by
`readErrorDetail` in the same branch (net zero extra reads)

**Constraints**: Non-breaking (no CRD/API change); centralized fix — no per-controller
classification change permitted (FR-004); must not weaken the `rgwiam` path (FR-013)

**Scale/Scope**: One shared function changed; ~12 controllers benefit; 40 `ResourceExists:false`
sites protected transitively

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. CRD Contract Stability** — No `apis/` change, no CRD/DeepCopy regen. **PASS.**
- **II. Idempotent, Side-Effect-Aware Reconciliation** — This is the principle the bug
  violated in spirit: Principle II already requires errors be "explicitly classified as
  transient or terminal … NEVER silently swallowed", but a bare 404 was being treated as a
  definitive deletion rather than an ambiguous signal. The fix realigns with II and **amends
  II** with an explicit not-found sub-rule (a MINOR constitution bump per governance policy —
  materially expanded normative guidance within an existing principle). Delivering the
  amendment is FR-010. **PASS (strengthens II).**
- **III. Controller Test Discipline** — Adds a classifier contract test (the "resource not
  found" path, now split into enveloped vs. bare) plus a bypass-guard test; uses the fake
  client, no live HTTP. **PASS.**
- **Provider Constraints / Observability** — Reclassified-404 surfaces through the standard
  transient path (descriptive `Synced` reason + structured requeue), no ad-hoc printing.
  **PASS.**

No gate violations. No complexity-tracking entries required.

## Project Structure

### Documentation (this feature)

```text
specs/019-fix-false-notfound-recreate/
├── plan.md              # This file
├── research.md          # Phase 0 — envelope audit, body-lifecycle, guard approach
├── data-model.md        # Phase 1 — classifier decision model (no persisted entities)
├── quickstart.md        # Phase 1 — reproduce + verify + e2e walkthrough
├── contracts/
│   └── classifier-404-contract.md   # Classify 404-branch behavioral contract
├── checklists/
│   └── requirements.md  # from /speckit-specify
└── tasks.md             # /speckit-tasks output (next)
```

### Source Code (repository root)

```text
internal/clients/timeweb/
├── errors.go            # CHANGE: Classify 404 branch → envelope-gated not-found
└── errors_test.go       # CHANGE/ADD: 404 contract cases (enveloped/bare/HTML/empty)

internal/controller/
└── notfound_guard_test.go   # ADD: source-scan bypass guard (no raw-404→absent in controllers)

.specify/memory/constitution.md   # CHANGE: amend Principle II (not-found sub-rule); v1.0.0→1.1.0
docs/
└── error-classification.md      # ADD: reference doc for the bug class + canonical-not-found rule
```

No changes under `apis/`, `cmd/`, or generated clients. The `rgwiam` package is untouched.

## Phase 0: Research (complete)

See [research.md](./research.md). Resolved:

- **R-1 Envelope is defined and universally referenced.** `components/responses/not-found`
  (`required: [status_code, error_code, response_id]`) is aliased by
  `components/responses/404`, which all **256** documented 404 endpoints reference. So every
  documented Timeweb GET the controllers call carries the envelope on a genuine 404.
- **R-2 Discriminator.** Envelope-present ⇔ the body decodes to the error shape with a
  non-empty `error_code` (generic `not_found` for VPC per the spec). Bare/HTML/empty → absent
  envelope → transient.
- **R-3 Body lifecycle.** Controllers call the **raw** generated client methods (e.g.
  `c.GetVPC`) that return `*http.Response` with an intact body; `Classify` only reads the body
  on error branches, and on a 404 the controller does not subsequently `DecodeBody`. Reading
  the body to test the envelope is therefore safe and already happens (`readErrorDetail`).
- **R-4 FR-014 per-type audit.** All managed kinds except **CDN** map to documented endpoints
  that reference the envelope. **CDN** (`internal/clients/timeweb/cdn.go`, `/cdn/*`) is absent
  from the OpenAPI spec → its real 404 shape is unverified → flagged for a live 404 capture;
  the conservative default (envelope-absent → transient) is safe (never wrongly recreates) and
  low-risk for CDN (Ready is not gated on its upstream status anyway).
- **R-5 `rgwiam` already compliant.** `classifyQueryError` returns `ErrNoSuchEntity` only on an
  exact `NoSuchEntity` code (`sigv4.go:204`); no status-alone path. Left unchanged (FR-013).
- **R-6 Guard mechanism.** A Go source-scan test (standard ecosystem, no new lint dep) walking
  `internal/controller/**` and asserting no `http.StatusNotFound` / `== 404` / raw-status →
  `ResourceExists:false` pattern, with the generated client and classifier packages excluded.

## Phase 1: Design & Contracts (complete)

- **[data-model.md](./data-model.md)** — the classifier decision model: inputs (status, body
  shape) → outputs (`nil` / `ErrNotFound` / `TransientError` / `APIError`); the 404 sub-decision
  table; no persisted entities.
- **[contracts/classifier-404-contract.md](./contracts/classifier-404-contract.md)** — the
  behavioral contract for `Classify`'s 404 branch and the enumerated test cases (maps FR-001,
  FR-002, FR-003, FR-006, FR-008).
- **[quickstart.md](./quickstart.md)** — reproduce the false-recreate in a unit test, verify
  the fix, and the staging e2e plan (bundles to run + CDN live-capture note).

## Phase 2 note

`/speckit-tasks` will decompose this into ordered tasks. Rollout target: a **prerelease**
(non-semver dev tag, e.g. `dev-<epoch>`) for the staging e2e gate before any real v0.9.x tag,
per the project's dev-tag convention.
