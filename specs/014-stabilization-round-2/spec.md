# Feature Specification: Stabilization round 2 — review-findings fix round

**Feature Branch**: `014-stabilization-round-2`

**Created**: 2026-07-02

**Status**: Draft

**Input**: User description: "Stabilization round 2 — fix findings from a full four-way
codebase review (code correctness, reconcile efficiency, user-facing surface, spec/docs
hygiene) performed against main at b6fddb5 + the merged 013-firewall-api. All changes are
fixes/hardening, no new kinds." (33 enumerated findings; inventory table below.)

## User Scenarios & Testing *(mandatory)*

A platform operator runs this provider in production against the Timeweb Cloud API. The
provider must never destroy credentials it issued, must stay under the upstream's
DDoS-protection thresholds at any fleet size, and must present a consistent, honest
declarative surface — what admission accepts must work, what documentation says must match
what ships. This round fixes review findings in those areas; it adds no new kinds.

### User Story 1 - Issued S3 credentials stay intact and correct (Priority: P1) 🎯

An operator provisions a scoped object-storage user (`S3User`) whose access/secret keys are
delivered via a connection Secret. Those credentials remain valid and present in the Secret
for the resource's whole life — steady-state reconciliation never blanks or corrupts them —
and the Secret's `endpoint`/`region` point at the region the granted buckets actually live
in, not a hardcoded default.

**Why this priority**: A reconcile pass that wipes the only copy of a secret key breaks
every consumer of that Secret irrecoverably (the key cannot be re-fetched). This is the one
data-loss-class finding in the review.

**Independent Test**: Create an S3User, record the published Secret, let the provider idle
past several poll intervals, and compare: every credential key is byte-identical. Repeat
via the adoption path (pre-existing upstream user). Verify against the live API whether
the single-user GET returns the secret key at all, and that the published endpoint/region
match the referenced bucket's region.

**Acceptance Scenarios**:

1. **Given** a Ready S3User with a published connection Secret, **When** N steady-state
   reconciles pass (including provider restart), **Then** `access_key`/`secret_key` in the
   Secret are unchanged and non-empty.
2. **Given** an upstream scoped user adopted by name (external-name match), **When** the
   resource becomes Ready, **Then** the Secret is never published with an empty
   `secret_key` — if the key is unobtainable, the resource reports a clear condition
   instead of publishing a blank credential.
3. **Given** an S3User granting a bucket in any supported region, **When** the connection
   Secret is published, **Then** its `endpoint` and `region` match that bucket's region.
4. **Given** the live upstream policy store, **When** the declared grants already match
   upstream, **Then** repeated reconciles issue no policy writes (semantic diff converges —
   verified against real round-tripped policy documents, including any upstream
   statement/resource merging).

---

### User Story 2 - Provider stays under upstream rate limits at any scale (Priority: P1)

An operator runs the full provider (14 controllers, many resources) against the Timeweb
API, which silently bans bursty egress IPs at its DDoS-protection layer. Aggregate request
rate across all controllers respects one shared, host-wide budget, and steady-state
reconciliation of converged resources costs the minimum number of upstream calls.

**Why this priority**: The current per-reconcile limiter multiplies the intended budget by
the number of concurrent reconciles — the exact failure mode (silent egress ban, all
resources stuck) the limiter exists to prevent. Risk grows with fleet size.

**Independent Test**: Under a synthetic load of many resources reconciling concurrently,
measure aggregate egress request rate: it never exceeds the configured host-wide budget.
Count upstream calls per steady-state reconcile for each kind before/after.

**Acceptance Scenarios**:

1. **Given** many resources across kinds reconciling concurrently, **When** aggregate
   requests to the upstream host are measured, **Then** the rate never exceeds the single
   configured host-wide budget (no per-controller multiplication).
2. **Given** a long-Ready, converged nodepool, **When** it is re-observed, **Then** the
   per-node list is not fetched (only the group state).
3. **Given** a Ready S3Bucket, **When** it is re-observed, **Then** the attached-users
   status mirror does not cost one signed IAM call per account user per bucket per poll
   (gated, cached, or cheapened).
4. **Given** a Server with N bound floating IPs (or a Router with attachments), **When**
   one Observe→Update cycle runs, **Then** binding/attachment state is fetched once, not
   re-fetched by Update.

---

### User Story 3 - Dependent resources survive parent turbulence (Priority: P2)

An operator upgrades a Kubernetes cluster (or the cluster transiently flips non-Ready).
Existing nodepools and addons keep reconciling — observation and updates of
already-created dependents never require the parent to be Ready; only creation does.
Invalid day-2 changes (e.g. a version downgrade) settle into one clear terminal condition
instead of an endless error loop.

**Why this priority**: Wedged dependents during routine cluster maintenance is an
operational hazard, but not data loss; churn loops waste rate budget and pollute Events.

**Independent Test**: Flip a parent cluster non-Ready while its nodepool is Ready; the
nodepool continues to Observe/Update normally. Declare a k8s version downgrade; the
cluster reports one terminal condition and stops retrying.

**Acceptance Scenarios**:

1. **Given** a Ready nodepool whose parent cluster turns transiently non-Ready, **When**
   the nodepool reconciles, **Then** Observe/Update proceed using the recorded cluster
   identity (no connect-time Ready gate for non-create paths).
2. **Given** a declared `k8sVersion` lower than or lateral to the running version,
   **When** reconciled, **Then** the resource reports a single terminal condition with an
   actionable message and does not loop.
3. **Given** a cluster or nodepool deleted upstream between Observe and Update, **When**
   Update runs, **Then** the not-found is classified so the runtime can recreate, matching
   Observe/Delete behavior.

---

### User Story 4 - The declarative surface is honest and consistent (Priority: P2)

An operator explores the provider with `kubectl explain`, `kubectl get`, and the examples.
Everything admission accepts actually works: unimplemented selector fields are rejected at
admission with an actionable message (and their docs say so); fields documented as
immutable are enforced immutable; project references work the same way on every kind;
printcolumns, status field names, and connection-secret key names follow one convention.

**Why this priority**: The selector trap (valid-looking manifest, green admission,
perpetually stuck resource) is the #1 new-operator footgun found in the review; the rest
are consistency debts that compound with every new kind.

**Independent Test**: Apply a manifest using each declared-but-unimplemented selector —
each is rejected at admission with a message naming the working alternative. Patch every
documented-immutable field — each is rejected. Diff printcolumns/status/secret key names
across all 14 kinds against the documented convention.

**Acceptance Scenarios**:

1. **Given** a manifest using any unimplemented `*Selector` field, **When** applied,
   **Then** admission rejects it with a message pointing to the `*Ref`/`*ID` alternative,
   and the field's `kubectl explain` text discloses the limitation.
2. **Given** any field whose documentation claims immutability (`location`,
   `availabilityZone`, `isDDoSGuard`, `presetName`, `os`, …), **When** patched post-create,
   **Then** admission rejects the change.
3. **Given** any kind that can belong to a project (Server, KubernetesCluster, Router,
   S3Bucket, S3User, ContainerRegistry), **When** the operator assigns a project, **Then**
   the same reference shape (typed ref to a Project MR + raw ID escape hatch, same integer
   width) is available on all of them.
4. **Given** `kubectl get` across all kinds, **Then** every kind shows a `STATE` column,
   external IDs are wide-output only (`priority=1`), and long kind names have shortNames.

---

### User Story 5 - Documentation and examples match what ships (Priority: P2)

An operator following the README, examples, and reference docs succeeds on the first try:
example manifests apply cleanly against the shipped schemas, field-comment examples use
accepted formats, kind names are spelled correctly, the printcolumns reference matches the
generated CRDs, and every Ready-condition reason the controllers emit is documented in one
operator-facing reference.

**Why this priority**: Each drift item is small, but every one costs a new operator a
failed first attempt; several were live-confirmed wrong (nonexistent `projectRef` in the
S3Bucket example, wrong `k8sVersion` format, `SshKey` vs `SSHKey`).

**Independent Test**: Apply every file in `examples/` against the packaged CRDs (dry-run) —
zero schema rejections. Regenerate the printcolumns doc from code and diff — zero
mismatch. Grep controllers' condition reasons against the conditions reference — zero
undocumented reasons.

**Acceptance Scenarios**:

1. **Given** the shipped examples, **When** server-side dry-run applied, **Then** all pass
   schema validation and contain no comments referencing nonexistent fields.
2. **Given** the `k8sVersion` field's `kubectl explain` output, **When** an operator copies
   its example value, **Then** the value resolves against the live catalog format.
3. **Given** any Ready-condition reason a controller can set, **When** the operator
   consults the docs, **Then** the reason is listed with meaning and remediation, including
   the runtime's error-override gotcha (terminal reasons surfacing as `ReconcileError`).

---

### User Story 6 - One idiom per pattern in the codebase (Priority: P3)

A contributor adding the next kind finds exactly one implementation of each recurring
pattern — the Observe skeleton, cross-MR ref resolution, condition recording, admin-key
derivation — instead of choosing which of several copies to imitate. Behavior is unchanged.

**Why this priority**: Pure maintenance; no operator-visible behavior change, but every
copy is a divergence point (the review found copies already drifting: missing rate
limiters, inconsistent not-found handling, misleading variable bindings).

**Independent Test**: The full test suite passes unchanged; duplicated helpers exist in
exactly one shared location; controllers previously missing the capped rate limiter or
ParentNotReady classification now match their siblings.

**Acceptance Scenarios**:

1. **Given** the refactor, **When** the full unit + e2e suites run, **Then** all pass with
   no behavior deltas beyond those specified in US1–US5.
2. **Given** the shared helpers, **When** grepping for the old duplicated bodies, **Then**
   each pattern (Observe skeleton, ref resolution, condition triple, admin-key derivation)
   has exactly one implementation.

---

### User Story 7 - Project records reflect reality (Priority: P3)

A maintainer (or agent) reading the project's spec artifacts and instructions sees the true
state: completed features marked complete, shipped task lists checked off, superseded seed
files retired, and the one unspecced shipped behavior (the plural `buckets` connection-
Secret key) backfilled into its feature spec.

**Why this priority**: Stale records repeatedly mislead planning (the review itself had to
re-derive feature status from git); cheapest to fix, lowest urgency.

**Independent Test**: Read specs/ and the project instructions: no feature that shipped is
marked Draft/pending; no retired seed file describes work that already exists; the 012
spec covers the shipped Secret key set.

**Acceptance Scenarios**:

1. **Given** the updated artifacts, **When** statuses are compared to git history, **Then**
   009/011/012/013 are marked complete and their shipped tasks checked.
2. **Given** the 012 spec, **When** compared to the shipped S3User connection Secret,
   **Then** every published key (including plural `buckets`) is specified.

---

### Edge Cases

- **Upstream GET omits credentials**: if the live single-user GET (or list) returns no
  secret key, the provider must still never publish an empty credential — carry forward or
  publish only from the creation response. The live probe decides which mechanism applies.
- **Upstream merges policy statements**: if the round-tripped policy document merges
  same-action resources, semantic equality must still converge (no perpetual re-writes).
- **Existing Secrets with old key names**: if connection-secret key casing is standardized,
  already-published Secrets carry old keys — the round must define what happens on the
  next publish (full replace ⇒ old keys vanish; consumers must be warned in release notes).
- **Existing manifests using selector fields**: adding admission-time rejection makes
  previously-accepted (but broken) manifests invalid — acceptable for alpha, but release
  notes must call it out.
- **CEL immutability on fields that were mutable-in-schema**: existing resources whose
  documented-immutable fields were already drifted must not be bricked by the new rules —
  rules must only reject *changes*, not existing values.
- **Rate-limiter consolidation under multi-token configs**: different ProviderConfigs
  (tokens) still target the same host/egress IP — the shared budget must be per-host, not
  per-token.
- **Parent gate removal vs deletion ordering**: relaxing the connect-time Ready gate must
  not reintroduce the known finalizer-wedge class (ref-gate must never block delete).

## Requirements *(mandatory)*

### Functional Requirements

**Credential integrity & correctness (P1)**

- **FR-001**: Steady-state observation MUST never overwrite a published connection-Secret
  credential with an empty or missing value; credentials are published only from sources
  that authoritatively contain them. The behavior of the live single-user GET and LIST
  endpoints regarding secret keys MUST be verified against the real API and the chosen
  mechanism recorded.
- **FR-002**: When credentials cannot be obtained for an adopted upstream user, the
  resource MUST surface a clear condition rather than publish a Secret with blank keys.
- **FR-003**: The S3User connection Secret MUST carry an `endpoint` and `region` derived
  from the referenced bucket's actual region — no hardcoded region default.
- **FR-004**: Grant reconciliation MUST converge to zero policy writes when declared grants
  match upstream, verified against live round-tripped policy documents (including any
  upstream statement/resource merging); the drift indicator exposed in status MUST reflect
  observed state or be relabeled to what it actually is.

**Rate-limit safety & reconcile efficiency (P1/P2)**

- **FR-005b (018 follow-up, DevOps review 2026-07-13)**: the `rgwiam` IAM client
  (`internal/clients/rgwiam/sigv4.go`, host `panel.s3.twcstorage.ru`) uses a bare
  `http.Client` with NO shared limiter — a large S3User fleet could trip an egress ban on
  that host independently of the API host. If that host is Qrator-fronted, bring it under
  a shared per-host budget too (probe first). Low volume today.
- **FR-005**: All requests to the upstream API host MUST share one process-wide rate
  budget regardless of controller, reconcile, or ProviderConfig token; transport/connection
  reuse MUST survive across reconciles.
- **FR-006**: Steady-state observation of converged resources MUST NOT perform per-item
  fan-out calls that only matter during convergence: nodepool per-node listing gated on
  non-convergence; S3Bucket attached-users mirror gated/cached/cheapened; Observe results
  carried to Update for Server floating-IP bindings and Router attachments instead of
  re-fetching.
- **FR-007**: All kinds MUST use the same capped requeue/backoff configuration (the kinds
  currently missing the 60s-capped rate limiter get it).

**Dependent-resource resilience (P2)**

- **FR-008**: Cross-MR parent readiness MUST be required only for creation; observation,
  update, and deletion of an existing dependent MUST proceed from recorded identity when
  the parent is not Ready or absent.
- **FR-009**: A declared version downgrade/lateral change MUST produce a single terminal
  condition with an actionable message, not a perpetual reconcile-error loop.
- **FR-010**: Update paths MUST classify upstream not-found the same way Observe and
  Delete do, so a resource deleted out-of-band between calls is recreated, not errored.

**Honest, consistent declarative surface (P2)**

- **FR-011**: Every declared-but-unimplemented selector field MUST be rejected at admission
  with a message naming the working `*Ref`/`*ID` alternative, and its schema documentation
  MUST disclose the limitation.
- **FR-012**: Every field documented as immutable MUST be admission-enforced immutable;
  enforcement MUST NOT invalidate existing resources (reject changes only).
- **FR-013**: Project assignment MUST use one reference convention across all kinds that
  support it (typed ref to the Project MR + raw ID escape hatch, one integer width);
  bespoke one-off reference types are replaced by the standard idiom.
- **FR-014**: Printcolumns MUST follow the established convention on every kind: a `STATE`
  column present and correctly named, external IDs at wide-output priority, and shortNames
  for long kind names. Status observation field naming (external id field) and JSON casing
  MUST follow one convention.
- **FR-015**: Connection-secret key naming MUST follow one documented convention across all
  kinds; the change and its consumer impact MUST be called out in release notes. This
  round ALSO redesigns the S3User connection Secret to carry a per-bucket structure (each
  granted bucket's name/region/endpoint) and DEPRECATE/REMOVE the singular
  `endpoint`/`region`/`bucket` (breaking — 018 shipped only the non-breaking
  primary-bucket derivation as an interim).

**Documentation & examples (P2)**

- **FR-016**: All shipped example manifests MUST pass server-side schema validation and
  MUST NOT reference nonexistent fields in comments.
- **FR-017**: Schema field examples visible via `kubectl explain` MUST use values the live
  API accepts (k8s version format).
- **FR-018**: All operator-facing docs MUST match the shipped artifacts: kind spellings,
  the printcolumns reference regenerated from code (including S3User and Firewall), and an
  operator-facing reference documenting every Ready-condition reason the controllers emit,
  including the runtime's reason-override gotcha.

**Maintenance dedup (P3)**

- **FR-019**: Each recurring implementation pattern MUST exist exactly once: shared Observe
  skeleton, shared cross-MR ref-resolution helpers and sentinels, shared condition-record
  helper, shared admin-key/storage-user client methods (preserving the never-cache
  contract), standard-library number formatting, and corrected misleading bindings; the
  missing ParentNotReady classification on repository observation is aligned with siblings.

**Record hygiene (P3)**

- **FR-020**: Project records MUST reflect shipped reality: 009 tasks checked, 011/012/013
  specs marked complete, project instructions refreshed, superseded seed files retired, and
  the plural `buckets` connection-Secret key backfilled into the 012 spec.

### Findings inventory (traceability)

| FR | Review findings (input items) |
|---|---|
| FR-001/002 | 1 |
| FR-003 | 2 |
| FR-004 | 4, 5 |
| FR-005 | 6 |
| FR-006 | 3, 7, 9, 10 |
| FR-007 | 30 |
| FR-008 | 8 |
| FR-009 | 11 |
| FR-010 | 12 |
| FR-011 | 13 |
| FR-012 | 15 |
| FR-013 | 14, 19 |
| FR-014 | 16, 18 |
| FR-015 | 17 |
| FR-016 | 20 |
| FR-017 | 21 |
| FR-018 | 22, 23, 24 |
| FR-019 | 25, 26, 27, 28, 29, 31, 32 |
| FR-020 | 33 |

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Zero credential loss: across ≥10 steady-state reconcile cycles plus a
  provider restart, every published connection-Secret credential key is byte-identical to
  its value at creation (create and adoption paths).
- **SC-002**: Aggregate upstream request rate under concurrent multi-kind load never
  exceeds the single configured host budget — no egress ban events during a full e2e run.
- **SC-003**: Steady-state upstream calls per converged resource per poll drop measurably:
  nodepool 2→1; S3Bucket observe no longer scales with account user count; Server/Router
  Observe+Update cycle no longer re-fetches binding state.
- **SC-004**: A dependent resource (nodepool/addon) continues reconciling normally while
  its parent cluster is non-Ready; a declared version downgrade settles to one terminal
  condition within one reconcile and generates no further error Events.
- **SC-005**: 100% of unimplemented selector usages are rejected at admission with an
  actionable message; 100% of documented-immutable fields reject post-create changes.
- **SC-006**: 100% of shipped examples pass server-side dry-run against the packaged CRDs;
  the printcolumns reference diffs clean against generated CRDs; every controller-emitted
  condition reason appears in the operator docs.
- **SC-007**: Full unit + e2e suites pass; live e2e (bundles incl. S3, k8s, firewall)
  re-verifies the P1 fixes by re-observation.

## Assumptions

- Alpha API status permits the breaking surface changes (connection-secret key renames,
  selector admission rejection, project-ref unification, printcolumn/JSON casing fixes);
  each break is called out in release notes. This matches the precedent set by the 012
  S3Bucket Secret redesign.
- Project-reference unification standardizes on typed ref + raw ID (`*int64`); no new
  selector fields are introduced in this round (selectors remain the disclosed-unsupported
  pattern of FR-011 until a future feature implements them).
- The two live-verify items (single-user GET secret-key behavior; upstream policy
  statement merging) are Phase-0 research for this round — their outcomes choose between
  pre-agreed mechanisms (carry-forward vs create-only publishing; canonicalization scope)
  without changing this spec's requirements.
- The host-wide rate budget keeps the current conservative default (2 r/s, small burst);
  making it configurable is in scope only if it falls out naturally — tuning is not a goal.
- Dedup refactors (FR-019) are behavior-preserving; any behavior change discovered during
  extraction is surfaced, not silently absorbed.
- The e2e harness and packaging flow from 008/009 are reused unchanged; no new
  infrastructure.

## Out of Scope (v1 of this round)

- Implementing the stubbed `*Selector` fields (only disclose + reject at admission).
- Server SSH-key runtime management (`specs/_next-server-ssh-keys.preface.md`).
- Dataplane delete-guard annotations (`specs/_next-extra-annotations.preface.md`).
- Any new managed kinds or upstream API surface.
- Marketplace listing.
- Rate-limit budget tuning/benchmarking beyond fixing the sharing defect.
