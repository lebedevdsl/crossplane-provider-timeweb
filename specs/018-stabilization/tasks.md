# Tasks: Stabilization round 2 — v0.9.0 slice

**Input**: specs/018-stabilization/ (plan, spec, research, data-model)
**Tests**: unit four-case for changed paths + targeted credential/limiter tests.
**Release**: v0.9.0 (non-breaking). No CRD/secret-key changes.

## Phase 1: Plan-phase verification (cheap, unblocks design)

- [X] T001 VP-1: verify the pinned crossplane-runtime treats empty `ConnectionDetails`
      from Observe/Update as a no-op (no Secret wipe). Record in research.md; if MERGE,
      note the option to publish non-secret keys on Update.
- [ ] T002 [P] VP-2/VP-3 (env-gated, uses e2e token): confirm `GET /storages/users/{id}`
      omits the secret key, and that an S3User grant resolves the primary bucket's region
      via the referenced S3Bucket. Fallbacks specified; non-blocking.

## Phase 2: P1 — shared rate budget (US1)

- [X] T003 In `internal/clients/timeweb/client.go`: add a process-global, per-host
      `rate.Limiter` + shared base `http.Transport` (package-level, lazily initialised);
      `New()` reuses them instead of building its own. Per-client `authTransport` still
      wraps the shared transport with the request bearer token (no token bleed).
- [X] T004 Unit test: multiple `New()` constructions share ONE limiter/transport for the
      same host; two different tokens produce isolated auth but shared budget.
- [X] T005 Verify all 9 `*/connector.go` compile unchanged against the new `New()` (the
      signature stays source-compatible); adjust any that pass a custom transport.

## Phase 3: P1 — S3User credential integrity (US2)

- [X] T006 In `internal/controller/s3user/external.go`: Observe and Update return EMPTY
      `ConnectionDetails{}` (create-only publish, spec Q1=B); Create keeps full details.
- [X] T007 Derive singular `endpoint`/`region` in `buildConnection` from the primary
      granted bucket's region (resolve via the referenced S3Bucket); remove the hardcoded
      `dataEndpoint` default. Multi-region grant → primary bucket wins (documented).
- [X] T008 Adopted user whose secret key is unobtainable → set a clear condition
      (`ReasonSecretMissing`-family) and DO NOT publish; never a blank Secret.
- [X] T009 Unit tests: steady-state Observe/Update publish no `secret_key`; Create
      publishes full set incl. non-empty `secret_key`; region reflects the granted bucket;
      adopted-no-key path surfaces the condition.

## Phase 4: P1 — uniform capped requeue (US3)

- [X] T010 Add `WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()})`
      to `SetupNetwork`, `SetupFloatingIP` (network/controller.go) and `SetupCluster`
      (kubernetes/controller.go); confirm parity across all Setup funcs.

## Phase 5: P3 — dedup (US5)

- [X] T011 Hoist `deriveAdminKeys` → `internal/controller/shared` (preserve the
      never-cache contract); rewire `s3bucket/external.go` + `s3user/connector.go` to it;
      remove the two copies.
- [ ] T012 [P] Scan for other byte-duplicated patterns named in 014 FR-019 (Observe
      skeleton, ref-resolution sentinels, condition-record helper, number formatting);
      consolidate the true duplicates behaviour-preservingly; note any behaviour fix.

## Phase 6: P3 — docs & examples (US4) + record hygiene (US5)

- [X] T013 `make validate-examples`: fix any example failing server-side dry-run and any
      comment naming a nonexistent field; fix `k8sVersion` explain example to the live
      `vX.Y.Z+k0s.N` format.
- [X] T014 Author `docs/conditions.md` — every Ready/Synced reason the controllers emit
      (grep `shared.Reason*` + inline), meaning + remediation, incl. the terminal-reason→
      `ReconcileError` override gotcha. Regenerate the printcolumns reference from the
      generated CRDs and diff clean.
- [X] T015 Record hygiene: tick 009 tasks; mark 011/012/013 specs complete; retire
      superseded `specs/_next-*.preface.md` seeds whose work shipped; backfill the plural
      `buckets` connection-Secret key into the 012 spec; refresh CLAUDE.md pointers.

## Phase 7: verify & release

- [ ] T016 `make reviewable`-level checks (build, lint via go run, tests, generate-diff
      clean, validate-examples); fix findings.
- [ ] T017 Optional light staging smoke: many resources reconciling — confirm no 429
      status-freeze; S3User Secret keys byte-stable across restarts. Skip if staging is
      down (P1s are unit-verified).
- [ ] T018 Release v0.9.0: notes (house style — call out the rate-limiter + credential
      fixes and the non-breaking guarantee), commit/push/tag per repo git conventions.

## Dependencies
T001→T006 (publish surface); T002→T007; T003→T004/T005; P2/P3/P4/P5/P6 largely independent
after their verifies; T016 after all code; T017 after T016; T018 after T017.
