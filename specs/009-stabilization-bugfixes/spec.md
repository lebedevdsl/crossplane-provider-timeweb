# Feature Specification: Stabilization & Bugfixes (live-e2e hardening round)

**Feature Branch**: `009-stabilization-bugfixes`

**Created**: 2026-06-21

**Status**: Complete (shipped)

**Input**: User description: "`specs/_next-008-followups.md` add to spec" — the
consolidated findings from the 008 packaging + live-e2e work on the Timeweb
staging cluster. Stabilization, observability, and bugfixes only; two adjacent
new features (server SSH-key runtime management, dataplane delete-guard
annotations) are explicitly OUT of scope and keep their own prefaces.

## Clarifications

### Session 2026-06-21

- Q: Is cost-aware configurator selection in scope this round? → A: In scope, but
  only **prefer non-promo standard-family** configurators over promo/legacy ones —
  classification by family/tag, **no real price math** (FR-010, US3 scenario 3).
- Q: How should the provider handle auto-created networks (auto-VPCs) on owner
  delete? → A: **Report in owner status only** — record the auto-created network
  id for traceability; do NOT auto-delete it, and do NOT add it to the orphan
  sweep (FR-011; the earlier sweep-enumeration requirement was dropped).
- Q: How should e2e bundles pick their region? → A: **Parameterize** via
  `TWE_LOCATION` / `TWE_AZ` env (seeded), threaded through all bundles (FR-006).
- Q: Enable parallel e2e this round? → A: **Enable opt-in** concurrent bundle
  execution; document account resource quota as the ceiling (FR-007).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Accurate at-a-glance resource state (Priority: P1)

An operator runs `kubectl get`/`describe` on the Timeweb managed resources and
sees correct, meaningful columns and status for every kind — the parent cluster
of a nodepool, whether nodes are public, and each resource's resolved
placement — without empty or mislabeled fields.

**Why this priority**: Operators rely on `kubectl` output to understand and
troubleshoot live infrastructure. Empty (`CLUSTER`), mislabeled (`PUBLIC-IP` as
a boolean), and missing (resolved availability zone, node public IPs) fields
erode trust and hide real state. These are the most visible, lowest-risk wins
and unblock confident day-2 operation.

**Independent Test**: Apply a cluster + nodepool + server, then assert each
kind's columns and `status.atProvider` reflect reality (parent cluster id
populated, public-node flag correctly named, resolved AZ present, node IPs
complete) — verifiable purely by inspecting the resources.

**Acceptance Scenarios**:

1. **Given** a nodepool bound to a cluster, **When** the operator runs
   `kubectl get` on it, **Then** the parent-cluster column shows the cluster
   identity (not empty) once the reference is resolved.
2. **Given** a nodepool, **When** the operator inspects its public-addressing
   setting, **Then** the column is named as the boolean intent it represents
   (not as if it were an IP address).
3. **Given** a worker node that has a public address, **When** the operator
   inspects node status, **Then** both the private and the public address are
   visible (or, if no public address exists, that absence is unambiguous).
4. **Given** a server whose effective availability zone was decided by its
   preset, **When** the operator inspects its status, **Then** the resolved
   availability zone is recorded so placement drift is observable.

---

### User Story 2 - Trustworthy live end-to-end validation (Priority: P1)

A maintainer runs the live e2e suite against the Timeweb staging cluster and it
runs to completion without losing test bundles to harness flakiness, with every
bundle targeting a region the account can actually fulfill, so a green run is a
trustworthy release gate.

**Why this priority**: The 008 runs repeatedly lost bundles to a transient
context-existence flake (3 occurrences across 2 runs) and to bundles hardcoded
to a region whose catalog the account cannot order from — masking the real
result and blocking the release gate (T018/SC-007). Without a reliable suite,
no other fix can be confidently validated.

**Independent Test**: Run the full billable suite twice; every selected bundle
either passes or fails for a real reason — none aborts on a precheck flake, and
none fails purely because of a region/catalog mismatch.

**Acceptance Scenarios**:

1. **Given** the configured kube context exists, **When** a bundle starts,
   **Then** a momentary context-lookup hiccup does not abort the bundle (the
   check tolerates transient failure).
2. **Given** the account's seeded presets/configurators are for a specific
   region, **When** any bundle provisions a resource, **Then** it targets a
   region the account can fulfill (no preset-not-found / non-orderable-catalog
   failures from a hardcoded region).
3. **Given** the full suite is selected, **When** it completes, **Then** every
   bundle reports a real pass/fail and the run is reproducible on a single
   provider build.
4. **Given** multiple bundles are run concurrently, **When** they execute,
   **Then** they do not trip the upstream anti-abuse protection (because request
   rate is bounded provider-side), and account resource quotas are the only
   stated ceiling.

---

### User Story 3 - Custom-sizing works and degrades gracefully (Priority: P2)

An operator sizes a server or Kubernetes worker pool by explicit CPU/RAM/disk
(custom configurator) and it provisions correctly; when a region offers no
orderable configurator, they get a clear, actionable error instead of a
misleading one.

**Why this priority**: Custom server sizing was silently broken (a missing
field made the platform fall back to a non-existent preset, surfaced as a
confusing "preset 0" error). The server path is fixed and live-validated; the
Kubernetes worker path uses the same shape and is **not yet verified**, and the
platform still surfaces a misleading error when a region only lists
non-orderable (promo/legacy) configurators.

**Independent Test**: Provision a custom-sized server and a custom-sized worker
pool in an orderable region (both reach Ready); attempt a custom size in a
region with only non-orderable configurators and confirm the error names the
real cause.

**Acceptance Scenarios**:

1. **Given** a custom-sized Kubernetes worker pool in an orderable region,
   **When** it is created, **Then** it provisions to Ready (no preset-fallback
   failure). Masters, which never take accelerators, are unaffected.
2. **Given** a region whose configurator catalog is entirely non-orderable,
   **When** a custom-sized resource is requested, **Then** the operator receives
   an error that names "no orderable configurator in <region>" rather than a
   phantom-preset error.
3. **Given** a custom size that several configurators satisfy, **When** one is
   selected, **Then** the selection prefers a non-promo standard-family
   configurator over a promo/legacy one (no price computation — just family/tag
   preference).

---

### User Story 4 - Auto-created networks are traceable (Priority: P2)

When the platform auto-creates a private network for a network-less
cluster/server, that network's identity is recorded in the owner's status so an
operator can see what was created and clean it up deliberately — the provider
does NOT auto-delete it and does NOT sweep for it.

**Why this priority**: The live sweep found multiple leftover auto-created
`192.168.0.0/24` networks from deleted clusters. The platform does not tie their
lifecycle to the owner, and (per the never-blind-delete principle) the provider
should not silently delete networks. The actionable, conservative fix is
**traceability**: surface the auto-created network id on the owner so cleanup is
informed and manual.

**Independent Test**: Create a network-less cluster; confirm the auto-created
network's id appears in the cluster's status. (Cleanup remains an explicit,
operator-driven action — not automated.)

**Acceptance Scenarios**:

1. **Given** a network-less cluster that caused an auto-network to be created,
   **When** the operator inspects the cluster's status, **Then** the
   auto-created network's id is recorded there for traceability.
2. **Given** an auto-created network whose owner has been deleted, **When** the
   operator decides to clean it up, **Then** they can identify it from the
   (now-deleted owner's) recorded id — the provider neither auto-deletes nor
   sweeps for it.

---

### User Story 5 - Release readiness (Priority: P3)

A maintainer cuts the public release only from a clean, debug-free build that has
passed the full live suite — including the private-cluster (NAT) path — so the
first public artifact is trustworthy.

**Why this priority**: Final hygiene before the public milestone: diagnostic
logging must be off, the released version must come from the validated tree (not
a debug iteration), and the one capability the standard suite skips (private,
no-public-IP nodes behind a NAT router) must be validated at least once.

**Independent Test**: Inspect the release build's runtime configuration (no
diagnostic flag), confirm its version is a clean release tag, and confirm the
private-cluster bundle has passed on that build.

**Acceptance Scenarios**:

1. **Given** the release build, **When** its runtime is inspected, **Then**
   diagnostic logging is disabled.
2. **Given** the release artifact, **When** its version is checked, **Then** it
   is a clean release version, not a development iteration tag.
3. **Given** the private-cluster arrangement (NAT router + nodes without public
   IPs), **When** it is exercised once on the release build, **Then** it
   provisions to Ready.

---

### Edge Cases

- A nodepool whose parent cluster reference has not yet resolved (parent-cluster
  column legitimately empty until resolution) vs. resolved-but-not-mirrored (the
  bug) — the two must be distinguishable.
- A worker node that genuinely has no public address (e.g. a private nodepool)
  vs. one whose public address simply isn't surfaced — node status must not
  imply "private" when a public address exists.
- A preset whose fixed zone conflicts with an operator-requested availability
  zone — the effective placement must be observable (and ideally flagged).
- The live-API sweep run from a network that the upstream anti-abuse layer has
  rate-banned — must degrade to a clear "cannot reach API" rather than report
  "no orphans".
- Concurrent e2e bundles exceeding the account's resource quota — must fail with
  a quota error, not be mistaken for a code defect.

## Requirements *(mandatory)*

### Functional Requirements

**Observability (US1)**
- **FR-001**: A nodepool MUST surface its resolved parent-cluster identity in
  status on every reconcile (not only at creation), so the parent-cluster column
  is populated in steady state.
- **FR-002**: The nodepool's public-addressing column MUST be named to reflect
  that it is a boolean intent (whether nodes get public addresses), not an
  address value.
- **FR-003**: **RESOLVED** (R-2): the upstream node API (`NodeOut`) exposes only
  `node_ip` (the cluster-network/private address) — there is **no per-node public
  IPv4** field, even though nodes are `public_ip_enabled`. So node status keeps
  showing `node_ip`; docs MUST clarify that per-node public reachability is
  governed by the nodepool `publicIP` flag, not a per-node public address. No new
  status field.
- **FR-004**: A server's status MUST record its resolved/effective availability
  zone, so placement decided by a preset (which may override a requested zone) is
  observable; the platform SHOULD signal when a preset's zone conflicts with the
  requested one.

**E2E reliability (US2)**
- **FR-005**: The e2e harness's context-existence precheck MUST tolerate
  transient failure (retry with backoff) so a momentary lookup hiccup does not
  abort a bundle.
- **FR-006**: E2E bundle region/zone MUST be **parameterized** (`TWE_LOCATION` /
  `TWE_AZ`, seeded once and threaded through all bundles) so the suite runs
  against any account's fulfillable region — never hardcoded to a region the
  account cannot fulfill.
- **FR-007**: The suite MUST support **opt-in** concurrent execution of multiple
  bundles without tripping the upstream anti-abuse protection (request rate is
  already bounded provider-side); the serial path remains the default and
  documentation MUST state that account resource quotas (not request rate) are
  the parallelism ceiling.

**Custom sizing (US3)**
- **FR-008**: **RESOLVED** (R-1): custom-sized Kubernetes worker pools provision
  successfully without an explicit accelerator field — the worker nodegroup
  endpoint does NOT enforce it like `/servers` did (live-confirmed). No fix
  needed; masters keep no accelerator field.
- **FR-009**: When no orderable configurator exists for a requested
  region/size, the platform MUST surface an error naming the real cause (no
  orderable configurator), not a misleading phantom-preset error.
- **FR-010**: Among configurators that satisfy a requested size, selection MUST
  prefer a **non-promo standard-family** configurator over a promo/legacy one,
  using only family/tag classification (NO price computation). This also reduces
  hitting the non-orderable promo configurators (FR-009).

**Lifecycle / traceability (US4)**
- **FR-011**: When the platform auto-creates an auxiliary network for a
  network-less cluster/server, the owner's status MUST record that network's id
  for traceability. The provider MUST NOT auto-delete the auto-created network
  and MUST NOT add it to the orphan sweep (never blind-delete; cleanup stays an
  explicit operator action informed by the recorded id).

**Release hygiene (US5)**
- **FR-012**: The released build MUST have diagnostic logging disabled.
- **FR-013**: The released artifact MUST be built from the validated tree and
  carry a clean release version (development iteration tags MUST NOT be released).
- **FR-014**: The private-cluster arrangement (NAT router + nodes without public
  IPs) MUST be validated at least once on the release build.

**Upstream quirks (tracking)**
- **FR-015**: Upstream API quirks discovered (misleading "preset 0" on omitted
  accelerator field; non-orderable promo configurators offered in the catalog;
  anti-abuse egress ban on request bursts) MUST be captured for a Timeweb support
  request and/or documented, per the project's quirk-capture practice.

### Key Entities *(include if feature involves data)*

- **Nodepool status**: must carry resolved parent-cluster identity + node
  addresses (private and public) — currently missing/partial.
- **Server status**: must carry resolved availability zone — currently missing.
- **Configurator catalog entry**: carries orderability signals (promo/legacy
  tags) and an accelerator-count requirement that the create body must echo.
- **Auto-created network**: the private network the platform creates for
  network-less clusters/servers; its id must be recorded in the owner's status
  for traceability (the provider does not manage its lifecycle).
- **E2E bundle**: must declare an account-fulfillable region; the harness must
  start it reliably.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of managed-resource kinds show non-empty, correctly-labeled
  columns and status for state that exists (no empty parent-cluster column, no
  mislabeled public-addressing column, resolved zone present).
- **SC-002**: The full live e2e suite completes with **zero** bundles aborted by
  harness flakiness across at least two consecutive runs.
- **SC-003**: 100% of e2e bundles provision in a region the account can fulfill
  (zero region/catalog-mismatch failures).
- **SC-004**: Custom-sized servers and Kubernetes worker pools both reach Ready
  in an orderable region; a non-orderable region yields an error that names the
  real cause.
- **SC-005**: Every auto-created auxiliary network is traceable from its owner's
  recorded status (100% of network-less clusters/servers record the auto-created
  network id).
- **SC-006**: The release build is debug-free, carries a clean release version,
  and the private-cluster path has passed on it — i.e. the release gate
  (full billable suite green) is met.

## Assumptions

- The provider's client-side request rate limiting (added in 008) already bounds
  total API pressure regardless of concurrency, so parallel e2e is safe from the
  anti-abuse layer; account resource quotas are the real parallelism limit.
- The account's seeded presets/configurators target one region (currently
  ru-3/msk-1); bundles should standardize on or parameterize to that region.
- Custom server sizing is already fixed and live-validated (008); only the
  Kubernetes worker path remains to verify.
- Kubernetes masters never take accelerators (no accelerator field needed — by
  design), so only the worker custom path is in question.
- Orphan handling follows the project rule: investigate before cleanup; never
  blind-delete; a 403/boundary is not a bug.
- Server SSH-key runtime management + inline keys (`_next-server-ssh-keys`) and
  dataplane delete-guard annotations (`_next-extra-annotations`) are separate new
  features and OUT of scope here.
- The location/AZ↔preset unification preface (`_next-location-az-presets`) is
  IN scope and should be folded into this effort (it underlies the region
  findings).
