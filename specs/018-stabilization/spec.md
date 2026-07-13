# Feature Specification: Stabilization round 2 — v0.9.0 slice (rate-limit safety, S3User credential integrity, hygiene)

**Feature Branch**: `018-stabilization-p1-hygiene`

**Created**: 2026-07-13

**Status**: Draft

**Input**: The non-breaking P1 + P3-hygiene slice carved from
`specs/014-stabilization-round-2/spec.md`. Ships as **v0.9.0**. Breaking P2 items
(connection-secret key renames, selector admission-rejection, project-ref
unification, printcolumn/JSON casing, immutability CEL) and the other P2
resilience/efficiency items remain in 014 for a later round.

## Provenance & scope

This round implements a subset of the 014 review findings, chosen because they are
either **actively harmful right now** (P1) or **cheap, non-breaking hygiene** (P3):

| This spec | 014 FR | Priority |
|---|---|---|
| Shared process-wide rate budget | 014 FR-005 | P1 |
| S3User credential integrity | 014 FR-001/002 | P1 |
| S3User endpoint/region from bucket region | 014 FR-003 | P1 (rides with US2) |
| Uniform 60s-capped requeue | 014 FR-007 | P1 |
| Examples pass dry-run; no phantom-field comments | 014 FR-016 | P3 |
| kubectl-explain examples use live-accepted formats | 014 FR-017 | P3 |
| Docs match shipped artifacts + conditions reference | 014 FR-018 | P3 |
| Dedup recurring patterns to one implementation each | 014 FR-019 | P3 |
| Project-record hygiene | 014 FR-020 | P3 |

Explicitly OUT (stay in 014): FR-004 (grant semantic-diff / drift-label), FR-006
(per-item observe fan-out), FR-008 (parent-not-ready gate), FR-009 (version-downgrade
terminal), FR-010 (Update not-found), FR-011 (selector rejection), FR-012
(immutability CEL), FR-013 (project-ref unification), FR-014 (printcolumn/JSON casing),
FR-015 (secret-key renaming).

## Motivating incident

While shipping the CDN kind (016/017), a settings-write bug produced a fast
reconcile-error loop on one resource. Because the upstream client's rate limiter is
built **per reconcile**, the aggregate egress rate across concurrent reconciles blew
past Timeweb's server-side limit and returned **429 (Too Many Requests) on Observe** —
which then **froze that resource's status mirror** (Observe could not complete to
persist fresh `atProvider`). This is exactly the failure mode 014 FR-005 predicted.
The rate-limiter fix here is not theoretical; it addresses a reproduced production
incident.

## Clarifications

### Session 2026-07-13

- Q: S3User "never-blank" credential mechanism (the upstream GET likely omits the
  secret key, yet `buildConnection` runs on every Observe/Update)? → A:
  **Create-only publish** — connection details (incl. `secret_key`) are published
  ONLY from Create; Observe and Update return empty connection details, so the
  runtime leaves the Create-published Secret untouched in steady state. (Standard
  crossplane behaviour: empty connection details from Observe is a no-op, not a
  wipe — this MUST be verified for the runtime version in the plan-phase, and the
  adopted-user path still needs a source for the key on first publish — see FR-005.)
- Q: S3User endpoint/region when granted buckets span regions (the singular
  `endpoint`/`region` can only carry one)? → A: **Minimal fix only in 018** — derive
  the singular `endpoint`/`region` from the PRIMARY granted bucket's region (removing
  the hardcoded default); the singular pair reflects the primary bucket only. A proper
  per-bucket connection-secret structure (each bucket's name/region/endpoint) is a
  KNOWN LIMITATION deferred to the breaking connection-secret round (014 FR-015), since
  it changes the Secret shape and 018 is non-breaking.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Provider stays under the upstream rate limit at any fleet size (Priority: P1) 🎯

An operator runs the full provider (all controllers, many resources) against the
Timeweb API, whose DDoS-protection layer silently bans bursty egress IPs and returns
429s. The provider's total request rate to the upstream host respects **one shared,
process-wide budget** — it does not scale up with the number of concurrent reconciles,
controllers, or ProviderConfigs.

**Why this priority**: The current per-reconcile limiter multiplies the intended
budget by the number of concurrent reconciles — the exact failure mode (429 storms,
silent egress ban, frozen resources) the limiter exists to prevent. It has already
caused a production status-freeze; risk grows with every added resource.

**Independent Test**: Drive many resources across kinds reconciling concurrently (or a
single kind in a tight error loop) and observe that aggregate egress request rate never
exceeds the single configured host budget, and that no resource's Observe is starved
into a frozen-status state by 429s.

**Acceptance Scenarios**:

1. **Given** many resources across kinds reconciling concurrently, **When** aggregate
   requests to the upstream host are measured, **Then** the rate stays within the single
   configured host-wide budget — no per-controller/per-reconcile multiplication.
2. **Given** two ProviderConfigs with different tokens targeting the same upstream host,
   **When** both reconcile, **Then** they draw from the **same** host budget (the ban is
   per egress IP, not per token).
3. **Given** a resource stuck in a reconcile-error loop, **When** it retries, **Then**
   other resources' Observes are not starved into stale/frozen status by rate exhaustion,
   and the loop itself is bounded by the uniform capped backoff (US3).
4. **Given** repeated reconciles, **Then** the upstream HTTP transport/connection pool is
   reused across reconciles rather than rebuilt each time.

---

### User Story 2 - Issued S3 credentials stay intact and correct (Priority: P1)

An operator provisions a scoped object-storage user (`S3User`) whose access/secret keys
are delivered via a connection Secret. Those credentials remain valid and present for
the resource's whole life — steady-state reconciliation never blanks or corrupts them —
and the Secret's `endpoint`/`region` point at the region the granted buckets actually
live in.

**Why this priority**: A reconcile pass that overwrites the only copy of a secret key
with a blank value breaks every consumer irrecoverably — the key cannot be re-fetched
upstream. This is the single data-loss-class finding in the review and it is latent in
the shipped provider.

**Independent Test**: Create an S3User, record its published Secret, let the provider
idle past several poll intervals and restart, then compare: every credential key is
byte-identical and non-empty. Repeat via the adoption path (pre-existing upstream user).
Confirm the published `endpoint`/`region` match the granted bucket's region.

**Acceptance Scenarios**:

1. **Given** a Ready S3User with a published connection Secret, **When** N steady-state
   reconciles pass (including a provider restart), **Then** `access_key`/`secret_key` in
   the Secret are unchanged and non-empty.
2. **Given** an upstream scoped user adopted by name, **When** its credentials cannot be
   authoritatively obtained, **Then** the resource reports a clear condition rather than
   publishing a Secret with a blank `secret_key`.
3. **Given** an S3User granting a bucket in any supported region, **When** the connection
   Secret is published, **Then** its `endpoint` and `region` match that bucket's region
   (no hardcoded region default).

---

### User Story 3 - Reconcile-error loops are bounded on every kind (Priority: P1)

An operator hits a transient or misconfiguration error on any kind. The controller
retries with a **capped** backoff (the same 60s ceiling used by the mature controllers),
so a stuck resource cannot hammer the upstream at the runtime default's much faster
early cadence — the amplifier that turned one CDN bug into a 429 storm.

**Why this priority**: Cheap, non-breaking, and directly reduces the blast radius of any
future error loop (including ones the shared limiter now also bounds). The two must-fix
P1s (US1, US3) together close the incident's full causal chain.

**Independent Test**: For each kind, induce a reconcile error and observe the requeue
interval never drops below the mature-controller cadence nor exceeds the 60s cap.

**Acceptance Scenarios**:

1. **Given** any kind currently missing the capped requeue configuration, **When** it
   error-loops, **Then** its backoff matches its siblings (capped at 60s), not the
   uncapped runtime default.

---

### User Story 4 - Documentation and examples match what ships (Priority: P3)

An operator following the README, examples, and reference docs succeeds on the first
try: example manifests apply cleanly against the shipped schemas with no comments
referencing nonexistent fields; `kubectl explain` field examples use values the live API
accepts; kind names are spelled correctly; the printcolumns reference matches the
generated CRDs; and every Ready-condition reason the controllers emit is documented in
one operator-facing reference — including the runtime gotcha where a terminal reason can
surface as `ReconcileError`.

**Why this priority**: Each drift item is small, but every one costs a new operator a
failed first attempt; several were live-confirmed wrong in the review.

**Independent Test**: Server-side dry-run every file in `examples/` against the packaged
CRDs (zero rejections); regenerate the printcolumns doc from code and diff (zero
mismatch); grep every controller's condition reasons against the conditions reference
(zero undocumented reasons).

**Acceptance Scenarios**:

1. **Given** the shipped examples, **When** server-side dry-run applied, **Then** all pass
   schema validation and contain no comments referencing nonexistent fields.
2. **Given** a schema field's `kubectl explain` example (e.g. `k8sVersion`), **When** an
   operator copies it, **Then** the value resolves against the live catalog format.
3. **Given** any Ready-condition reason a controller can set, **When** the operator
   consults the docs, **Then** it is listed with meaning and remediation, including the
   `ReconcileError`-override gotcha.

---

### User Story 5 - One idiom per pattern; records reflect reality (Priority: P3)

A contributor adding the next kind finds exactly one implementation of each recurring
pattern instead of several drifting copies; and a maintainer reading the project's spec
artifacts sees the true state (completed features marked complete, shipped tasks checked,
superseded seed files retired, the one unspecced shipped behaviour backfilled).

**Why this priority**: Pure maintenance; no operator-visible behaviour change, but each
duplicate is a divergence point (the review already found copies drifting — a missing
rate limiter, inconsistent not-found handling), and stale records repeatedly mislead
planning.

**Independent Test**: Full test suite passes unchanged; grep shows each deduplicated
pattern in exactly one location; specs/ statuses match git history.

**Acceptance Scenarios**:

1. **Given** the refactor, **When** the full unit + e2e suites run, **Then** all pass with
   no behaviour deltas beyond US1–US4.
2. **Given** the shared helpers, **When** grepping for the old duplicated bodies, **Then**
   each pattern (Observe skeleton, cross-MR ref resolution + sentinels, condition-record
   helper, admin-key derivation preserving the never-cache contract, number formatting)
   has exactly one implementation.
3. **Given** the updated artifacts, **When** statuses are compared to git history, **Then**
   009/011/012/013 are marked complete with shipped tasks checked, superseded seed files
   are retired, and the plural `buckets` connection-Secret key is backfilled into the 012
   spec.

---

### Edge Cases

- **Upstream GET omits the secret key**: create-only publishing sidesteps this — since
  Observe/Update never (re)publish credentials, a GET that omits the secret key cannot
  blank the Secret. The residual risk is the runtime wiping the Secret when Observe
  returns empty details; the plan verifies this is a no-op for the target runtime, with
  carry-forward as the documented fallback.
- **Adopted user with unobtainable key**: an upstream user adopted by name may have no
  retrievable secret key at all — the resource surfaces a clear condition instead of a
  blank Secret; it does not fabricate or blank the credential.
- **Rate-limiter consolidation under multi-token configs**: different ProviderConfigs
  (tokens) still target the same host/egress IP — the shared budget MUST be per-host, not
  per-token.
- **Limiter consolidation must not reintroduce credential mixing**: a shared limiter/
  transport must not leak one ProviderConfig's bearer token onto another's requests — auth
  stays per-request/per-client while only the rate budget (and connection reuse to the
  shared host) is shared.
- **Dedup must be behaviour-preserving**: any behaviour change discovered while extracting
  a shared helper (e.g. a copy that silently lacked a rate limiter or not-found handling)
  is surfaced as an explicit fix, not silently absorbed.
- **Region derivation with multiple granted buckets**: an S3User may grant buckets in more
  than one region — in 018 the singular `endpoint`/`region` reflects the PRIMARY bucket
  only (documented); the correct per-bucket representation is deferred (014 FR-015). This
  must be called out in the S3User docs so operators understand the singular pair is
  primary-bucket-scoped.

## Requirements *(mandatory)*

### Functional Requirements

**Rate-limit safety (P1)**

- **FR-001**: All requests to the upstream API host MUST draw from **one process-wide rate
  budget**, independent of controller, reconcile, or ProviderConfig token. The
  per-reconcile limiter multiplication MUST be eliminated.
- **FR-002**: The shared budget MUST be keyed by upstream **host/egress**, not by token, so
  multiple ProviderConfigs targeting the same host share it. Per-request bearer-token
  authentication MUST remain correct and isolated (no token bleed across configs).
- **FR-003**: The upstream HTTP transport/connection pool MUST be reused across reconciles
  rather than rebuilt per reconcile.

**S3User credential integrity (P1)**

- **FR-004**: Credentials MUST be published ONLY from the Create path (which
  authoritatively contains the secret key). Observe and Update MUST return empty
  connection details so steady-state reconciliation never overwrites the published
  Secret. The plan-phase MUST verify, for the target crossplane-runtime version, that
  Observe returning empty connection details is a no-op (does not wipe the Secret);
  if that does not hold, carry-forward is the fallback. The live single-user GET/LIST
  secret-key behaviour is recorded as Phase-0 research.
- **FR-005**: When credentials cannot be authoritatively obtained for an adopted user, the
  resource MUST surface a clear condition rather than publish a Secret with a blank
  `secret_key`.
- **FR-006**: The S3User connection Secret's `endpoint` and `region` MUST be derived from
  the granted bucket's actual region (no hardcoded default); the multi-region-grant tie-
  break MUST be defined and documented.

**Bounded backoff (P1)**

- **FR-007**: Every kind MUST use the same capped requeue/backoff configuration (60s ceiling
  — the mature-controller default); kinds currently missing it get it.

**Documentation & examples (P3)**

- **FR-008**: All shipped example manifests MUST pass server-side schema validation and MUST
  NOT reference nonexistent fields in comments.
- **FR-009**: Schema field examples visible via `kubectl explain` MUST use values the live
  API accepts (notably the `k8sVersion` format).
- **FR-010**: Operator-facing docs MUST match shipped artifacts: correct kind spellings, the
  printcolumns reference regenerated from code (including all current kinds), and an
  operator-facing reference documenting every Ready-condition reason the controllers emit,
  including the runtime's terminal-reason→`ReconcileError` override gotcha.

**Maintenance dedup (P3)**

- **FR-011**: Each recurring implementation pattern MUST exist exactly once: a shared Observe
  skeleton, shared cross-MR ref-resolution helpers and sentinels, a shared condition-record
  helper, shared admin-key/storage-user client methods (preserving the never-cache
  contract), and standard-library number formatting. Any copy found missing a rate limiter
  or not-found classification is aligned with its siblings. Behaviour is preserved except
  where a divergence is an explicit fix.

**Record hygiene (P3)**

- **FR-012**: Project records MUST reflect shipped reality: 009 tasks checked; 011/012/013
  specs marked complete; project instructions (`CLAUDE.md`) refreshed; superseded `_next`
  seed files retired; and the plural `buckets` connection-Secret key backfilled into the
  012 spec.

### Key Entities

- **Upstream API client / rate budget**: the shared, per-host request-rate budget and
  reusable HTTP transport all controllers draw on; today constructed per reconcile, to
  become process-global while keeping per-request auth isolated.
- **S3User connection Secret**: the published `access_key`/`secret_key`/`endpoint`/`region`
  set whose integrity (never blanked) and correctness (region from bucket) this round
  guarantees.
- **Conditions reference**: the operator-facing catalogue of every Ready-condition reason
  the controllers emit, including the `ReconcileError`-override note.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Under concurrent multi-kind load (and a single-kind error loop), aggregate
  upstream request rate never exceeds the single configured host budget; no 429-induced
  status-freeze occurs, and no egress-ban event fires during a full e2e run.
- **SC-002**: Across ≥10 steady-state reconcile cycles plus a provider restart, every
  published S3User connection-Secret credential key is byte-identical to its creation value
  (create and adoption paths); an adopted user with an unobtainable key reports a condition
  and never publishes a blank key.
- **SC-003**: The primary granted bucket's region is reflected in the published singular
  `endpoint`/`region` for every supported region (no hardcoded default remains).
- **SC-004**: Every kind's reconcile-error backoff is capped at 60s (none uses the uncapped
  runtime default).
- **SC-005**: 100% of shipped examples pass server-side dry-run against the packaged CRDs;
  the printcolumns reference diffs clean against generated CRDs; every controller-emitted
  condition reason appears in the operator docs.
- **SC-006**: Full unit + e2e suites pass with no behaviour deltas beyond US1–US4; each
  deduplicated pattern has exactly one implementation; specs/ statuses match git history.

## Assumptions

- Alpha status permits the non-user-facing internal changes here; this slice is
  **non-breaking** to CRD schemas and connection-Secret **key names** (the breaking renames
  stay in 014). Existing manifests and Secrets keep working.
- Two live-verify items are Phase-0 research for this round: (a) whether the single-user
  GET/LIST returns the secret key, which chooses carry-forward vs create-only publishing;
  (b) the multi-region-grant endpoint/region tie-break. Outcomes are recorded without
  changing these requirements.
- The host-wide budget keeps the current conservative default (~2 r/s, small burst);
  making it configurable is in scope only if it falls out naturally — tuning/benchmarking
  is not a goal.
- Dedup refactors are behaviour-preserving; any behaviour change found during extraction is
  surfaced as an explicit fix, not silently absorbed.
- The 008/009 e2e harness and packaging flow are reused unchanged; no new infrastructure,
  no new kinds, no new upstream API surface.

## Out of Scope

- All breaking 014 P2 surface changes (connection-secret key renames, selector admission-
  rejection, project-ref unification, printcolumn/JSON casing, immutability CEL) — remain in
  014 for a later round. **The per-bucket S3User connection-secret structure (each granted
  bucket's name/region/endpoint) is explicitly part of that breaking round (014 FR-015),
  not 018.**
- The other 014 P2 resilience/efficiency items (FR-004 grant semantic-diff, FR-006 observe
  fan-out reduction, FR-008 parent-not-ready gate, FR-009 version-downgrade terminal, FR-010
  Update not-found) — remain in 014.
- Rate-budget tuning/benchmarking beyond fixing the sharing defect.
- Any new managed kinds, upstream API surface, SSH-key runtime management, dataplane
  delete-guard annotations, or marketplace listing.
