# Implementation Plan: Stabilization round 2 — v0.9.0 slice

**Branch**: `018-stabilization` | **Date**: 2026-07-13 | **Spec**: [spec.md](./spec.md)

**Release target**: **v0.9.0** (non-breaking)

## Summary

A non-breaking hardening slice of the 014 review round, in three P1 fixes + P3 hygiene:

- **Shared rate budget (FR-001/002/003)**: the `timeweb.Client` (and its `rate.Limiter`
  + HTTP transport) is built **per-Connect** in all 9 controller connectors, so N
  concurrent reconciles get N independent 2 r/s budgets — the multiplication that trips
  Timeweb's 429 / egress ban (the reproduced CDN status-freeze incident). Fix: one
  process-global, per-host limiter + shared transport, injected into every per-reconcile
  client; per-request bearer auth stays isolated.
- **S3User credential integrity (FR-004/005/006)**: `buildConnection` runs on Observe (121),
  Create (149), **and** Update (188); the upstream GET used by Observe/Update does not
  return the secret key, so steady-state republishes a **blank** `secret_key` — data loss.
  Fix (clarified: create-only): publish connection details only from Create; Observe/Update
  return empty details. Derive the singular `endpoint`/`region` from the primary granted
  bucket's region (kill the hardcoded `dataEndpoint` default). Adopted user without an
  obtainable key → clear condition, never a blank Secret.
- **Uniform capped requeue (FR-007)**: `SetupNetwork`, `SetupFloatingIP`, `SetupCluster`
  lack `WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()})` (all
  other Setup funcs have it). Add it.
- **Hygiene (FR-008..012)**: examples pass server-side dry-run + no phantom-field comments;
  `kubectl explain` examples use live formats (`k8sVersion`); docs match code (kind
  spellings, printcolumns regenerated, an operator conditions-reason reference incl. the
  `ReconcileError`-override gotcha); dedup `deriveAdminKeys` (duplicated in s3bucket +
  s3user) and other recurring patterns to one implementation each; project-record hygiene
  (mark 009/011/012/013 complete, retire superseded `_next` seeds, backfill `buckets` into
  the 012 spec).

## Technical Context

**Language/Version**: Go (latest stable); Crossplane v2 namespaced MR model.

**Primary Dependencies**: `crossplane-runtime/v2`, `golang.org/x/time/rate` (already used).
No new third-party deps.

**Testing**: `go test` four-case discipline for changed controller paths; a focused
S3User credential-integrity test (steady-state Observe/Update publish NO secret_key /
empty details, Create publishes full); a rate-limiter sharing test (one budget across
constructions); `make validate-examples` for FR-008; regenerate-and-diff for printcolumns.
Live gate is OPTIONAL and light (the P1 fixes are unit-verifiable; a staging smoke re-run
confirms no 429 under load).

**Target Platform**: Linux provider pod; existing e2e harness.

**Project Type**: Crossplane provider (single Go module).

**Constraints**: NON-BREAKING — no CRD schema or connection-secret key-name changes.
Shared limiter must not leak bearer tokens across ProviderConfigs. Dedup is
behaviour-preserving (any divergence found is an explicit fix, surfaced not absorbed).

**Scale/Scope**: touches `internal/clients/timeweb/client.go` (+ a shared limiter/transport
holder), all 9 `*/connector.go` (inject shared budget), 3 Setup funcs (network×2,
kubernetes×1), `internal/controller/s3user/external.go` (create-only + region), a hoisted
`internal/controller/shared` admin-key helper, docs, examples, and spec records.

## Open verification (plan-phase, cheap)

- **VP-1 RESOLVED** (empirical test vs crossplane-runtime v2.3.1): empty
  `ConnectionDetails` from Observe does NOT wipe the Secret (merge patch + `data,omitempty`).
  Strict create-only is safe — guarded by `vp1_runtime_test.go`.
- **VP-2 (single-user GET secret-key)**: confirm the live `GET /storages/users/{id}` omits
  the secret key (motivates create-only). Recorded, not blocking.
- **VP-3 (per-bucket region source)**: confirm S3User grants can resolve the primary
  bucket's region (via the referenced S3Bucket's status) for FR-006; endpoint host is the
  shared `s3.twcstorage.ru` (region is the metadata field).

## Constitution Check

- **§I CRD Contract Stability — PASS.** No `apis/` schema change (non-breaking round).
- **§II Idempotent Reconciliation — PASS.** Create-only publishing REDUCES steady-state
  writes; shared limiter is transparent; capped requeue only changes backoff timing. No
  external-name/identity change.
- **§III Test Discipline — PASS.** Changed controller paths keep four-case coverage; new
  targeted tests for credential-integrity + limiter sharing.
- **Provider Constraints — PASS.** Tokens still from ProviderConfig; the shared transport/
  limiter carries NO token (auth is per-request), so no token bleed; never-cache admin-key
  contract preserved through the hoist.
- **Observability — PASS.** Adopted-user-no-key surfaces a clear condition; conditions
  reference doc added (FR-010).

No violations → Complexity Tracking empty.

## Project Structure

```text
specs/018-stabilization/  plan.md, research.md, data-model.md, quickstart.md, tasks.md
internal/clients/timeweb/client.go        # shared process-global limiter + transport;
                                          #   New() draws from it instead of building its own
internal/controller/*/connector.go (×9)   # pass the shared budget/transport into timeweb.New
internal/controller/network/controller.go # + RateLimiter on SetupNetwork, SetupFloatingIP
internal/controller/kubernetes/controller.go # + RateLimiter on SetupCluster
internal/controller/s3user/external.go    # create-only publish; primary-bucket region;
                                          #   adopted-no-key condition
internal/controller/shared/               # hoisted deriveAdminKeys (+ any other dedup)
internal/controller/s3bucket/external.go  # use the hoisted helper
docs/                                     # conditions reference; printcolumns regen; fixes
examples/                                 # dry-run clean; comment fixes
CLAUDE.md, specs/00x, specs/_next-*       # record hygiene
docs/release-notes/v0.9.0.md
```

**Structure Decision**: Keep the per-reconcile external-client construction (it carries the
per-request token) but move the rate limiter + HTTP transport to a process-global singleton
that `timeweb.New` reuses — smallest change that fixes the multiplication without disturbing
per-config auth. All other work is in-place edits + doc/record hygiene.

## Complexity Tracking

No violations — section intentionally empty.
