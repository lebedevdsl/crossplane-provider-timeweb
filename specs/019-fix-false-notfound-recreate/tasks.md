# Tasks: Fix false not-found → resource recreation

**Feature**: `019-fix-false-notfound-recreate` | **Branch**: `019-fix-false-notfound-recreate`

**Input**: plan.md, spec.md, research.md, data-model.md, contracts/classifier-404-contract.md,
quickstart.md

**Tests**: REQUESTED by the spec (FR-008 classifier contract test, FR-011, FR-012 bypass guard) —
TDD ordering applies for the classifier change.

## Conventions

- File paths are repo-relative.
- `[P]` = parallelizable (different file, no dependency on an incomplete task).
- Story labels map to spec user stories: [US1] flaky-404-never-recreates, [US2]
  genuine-deletion-recognized, [US3] flap-observable, [US4] future-proofing/docs+guard.

---

## Phase 1: Setup

- [x] T001 Confirm clean baseline: `go build ./...` and `go test ./internal/clients/timeweb/...`
  pass on branch `019-fix-false-notfound-recreate` before any change.

## Phase 2: Foundational

*(none — no shared scaffolding; the fix is a single-function change. Proceed to user stories.)*

---

## Phase 3: US1 — A flaky 404 never destroys a live resource (P1) 🎯 MVP

**Goal**: An envelope-less 404 (HTML/empty/non-envelope JSON) is classified transient, never
not-found, for every kind (shared `Classify`).

**Independent test**: `go test ./internal/clients/timeweb/...` — cases C2/C3/C4 assert
`ErrTransient`, not `ErrNotFound`.

- [x] T002 [US1] Add failing contract tests C1–C5 for `Classify`'s 404 branch in
  `internal/clients/timeweb/errors_test.go` per contracts/classifier-404-contract.md (enveloped
  → `ErrNotFound`; empty/HTML/no-`error_code` → `ErrTransient`). Keep existing cases (C6).
- [x] T003 [US1] Implement the fix in `internal/clients/timeweb/errors.go`: in the
  `StatusCode == 404` branch, parse the body once as `errorResponseBody`; return `ErrNotFound`
  (with existing message/`response_id` detail) ONLY when `error_code` is non-empty; otherwise
  return a `*TransientError{StatusCode:404, Reason:"404 without canonical error envelope —
  suspected upstream flap"}`. Preserve non-404 behavior. (FR-001, FR-002, FR-003, FR-006, FR-007)
- [x] T004 [US1] Run `go test ./internal/clients/timeweb/...`; confirm C1–C6 pass.

**Checkpoint**: US1 delivers the incident fix for all kinds via the shared classifier.

---

## Phase 4: US2 — A genuine deletion is still recognized (P2)

**Goal**: A canonical-envelope 404 still yields `ErrNotFound`; deletion/adoption unchanged.

**Independent test**: contract case C1/C5 pass; existing controller not-found tests still green.

- [x] T005 [P] [US2] Verify no controller regression: `go test ./internal/controller/...`
  (existing "resource not found" unit tests across kinds still pass with the enveloped-404 path).
- [x] T006 [P] [US2] Confirm `rgwiam` untouched and green: `go test ./internal/clients/rgwiam/...`
  (FR-013 — `NoSuchEntity` path byte-for-byte unchanged).

**Checkpoint**: deletion recognition + `rgwiam` compliance verified.

---

## Phase 5: US3 — Suspected flaps are observable (P3)

**Goal**: A reclassified 404 is visible to an operator via the transient reason on requeue.

**Independent test**: assert the `*TransientError.Reason` for an envelope-less 404 names the
cause (`err.Error()` contains "canonical error envelope").

- [x] T007 [US3] Add a test in `internal/clients/timeweb/errors_test.go` asserting the
  reclassified-404 transient error message is descriptive (FR-007). (Centralized reason; no
  per-controller Event per plan R-7.)

---

## Phase 6: US4 — A future resource cannot silently reintroduce the bug (P2)

**Goal**: Document the bug class durably and lock it with an automated bypass guard, stated as
the general canonical-not-found rule so `rgwiam` and future precise clients stay compliant.

**Independent test**: guard test fails on an injected raw-404→absent pattern and passes on the
current tree; Constitution + `docs/` state the rule.

- [x] T008 [P] [US4] Add `internal/controller/notfound_guard_test.go`: source-scan
  `internal/controller/**/*.go` (non-test) and fail on `http.StatusNotFound` / literal `== 404`
  / raw-status→`ResourceExists: false`; exclude generated client, `errors.go`, `rgwiam`. Assert
  it passes on the current tree. (FR-004, FR-012, FR-009)
- [x] T009 [P] [US4] Amend `.specify/memory/constitution.md` Principle II with the not-found
  sub-rule ("a resource is deleted only on a canonical, precisely-classified not-found signal
  for that API — never the HTTP status alone"); bump version 1.0.0 → 1.1.0, update Last Amended
  + Sync Impact Report. (FR-009, FR-010)
- [x] T010 [P] [US4] Add `docs/error-classification.md`: the false-not-found bug class, the
  canonical-envelope rule, the per-API signal table (Timeweb envelope / `rgwiam` `NoSuchEntity`),
  and the #124 incident as motivation. (FR-010)

---

## Phase 7: Polish & Validation

- [x] T011 Run full local gate: `make generate && git diff --exit-code` (no drift),
  `go test ./...`, `make reviewable` (lint/vet/vuln). (Constitution I/III)
- [x] T012 Build prerelease image + xpkg: `make xpkg.push VERSION=dev-$(date +%s)` (non-semver
  dev tag for the staging gate).
- [x] T013 Staging e2e per quickstart.md: pin the staging kube-context, install the prerelease
  (bump annotation to force re-pull), run bundles Network / Router+Network / Server /
  K8s cluster+nodepool / S3Bucket+S3User; assert `Synced` **and** `Ready` by condition type;
  scan provider logs for the reclassified-404 reason and for any unexpected Create.
- [ ] T014 CDN FR-014 live capture: delete a CDN resource out-of-band, record the raw 404
  (headers + body). If it carries the envelope → close FR-014; if bare → file a follow-up for
  CDN-specific corroboration (note the conservative default is already safe). (FR-014)
- [x] T015 Update `specs/019-fix-false-notfound-recreate/quickstart.md` / release notes with the
  e2e results and the CDN capture finding.

---

## Dependencies & Order

- T001 → T002 → T003 → T004 (TDD core; strict order, same file).
- US2 (T005–T006), US3 (T007), US4 (T008–T010) depend on T003 (the fix) but are independent of
  each other → parallelizable after T004.
- Polish: T011 after all code/doc tasks; T012 after T011; T013/T014 after T012 (need the
  deployed prerelease); T015 last.

## Parallel opportunities

- After T004: `[P]` T005, T006, T008, T009, T010 (distinct files) can run together; T007 shares
  `errors_test.go` with T002 so is sequential to it.
- T013 bundles may run in parallel per the harness's opt-in parallel e2e (separate namespaces),
  but the CDN capture (T014) is a manual out-of-band step.

## MVP scope

**US1 (T002–T004)** alone eliminates the incident across all kinds — that is the minimum
shippable fix. US2/US3/US4 add verification, observability, and durability; Polish covers the
prerelease + staging gate required by the goal.

**Total tasks**: 15 (US1: 3, US2: 2, US3: 1, US4: 3, Setup: 1, Polish: 5).
