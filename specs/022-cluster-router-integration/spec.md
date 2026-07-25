# Feature Specification: Declarative cluster↔router integration (routerRef)

**Feature Branch**: `022-cluster-router-integration`

**Created**: 2026-07-25

**Status**: Draft

**Input**: User description: Part 3 RESOLVED — cluster↔router integration is an explicit first-class op on the cluster (`PATCH /api/v1/k8s/clusters/{id} {"virtual_router_id": "<router-uuid>"}` → 200; panel: cluster → Сеть → «Интеграция с роутерами»). Provider gains `KubernetesCluster.spec.forProvider.routerRef`; target minor release **v0.11.0**.

**Source**: `specs/_next-router-nat-bind.preface.md` (Part 3 RESOLVED section, panel-captured and effect-verified live 2026-07-25). This supersedes the entire "frozen linkage" model: the state is NOT unfixable, integration is day-2, no recreation ever needed.

## Verified upstream facts (2026-07-25, live)

- `PATCH /api/v1/k8s/clusters/{clusterId}` with `virtual_router_id: <router uuid>`
  integrates a LIVE cluster with a router. Effect-verified: private worker
  groups that had failed for a day
  (`router_required_for_worker_groups_without_public_ip` /
  `router_must_have_nat_ip…` / `…dhcp…` chain) began creating within seconds
  of the PATCH, with no other change.
- Integration is NEVER automatic at cluster create — not even for a cluster
  created on a network wired via the router CREATE body (verified: cluster
  1101699 born "Не подключен").
- The cluster GET does NOT echo the field. The readback is the ROUTER's
  `parent_services` gaining `{id: <clusterId>, type: "k8s"}` — observed to
  appear immediately. (The provider already mirrors `parentServices` in
  Router status.)
- The per-nodepool `virtual_router_id` seen in the panel's group-create body
  is secondary/optional once the cluster itself is integrated.
- The 2026-07-24 snapshot-desync probes (`router_must_have_nat_ip…` even with
  NAT on) were run WITHOUT cluster integration — whether that trap still
  exists post-integration must be RE-VERIFIED at the live gate before any
  trap-guard text claims it.

## Clarifications

### Session 2026-07-25

- Q: Behavior when `routerRef` is removed from the cluster spec? → A: **Detach (Option B, confirmed)** — the upstream accepts nulling the field (`virtual_router_id: null`), so removal issues the detach and the integration follows declared state symmetrically; changing the ref re-PATCHes to the new router. Both verified at the live gate; readback stays the router's parent services.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Declare the router on the cluster; integration converges (Priority: P1)

The operator declares which Router a KubernetesCluster integrates with:

```yaml
spec:
  forProvider:
    networkRef: {name: app-net}
    routerRef:  {name: edge}     # NEW — the integration declaration
```

After the cluster exists upstream, the provider performs the integration op
and keeps it converged: if the linkage is observed absent while declared
(e.g. detached in the panel), it re-integrates. The linkage state is read
back through the referenced Router's parent services and mirrored on the
cluster (`status.atProvider.routerIntegrated`).

**Why this priority**: this is THE missing piece for private clusters — today
integration exists only as a manual panel action nobody can see in git; every
private-nodepool rollout depends on it.

**Independent Test**: cluster + router + NAT'd network declared; after
convergence the router's `parentServices` contains the cluster and
`routerIntegrated: true`; a private nodepool then creates successfully.

**Acceptance Scenarios**:

1. **Given** a Ready Router and a KubernetesCluster declaring `routerRef`,
   **When** the cluster is created and reconciled, **Then** the router's
   parent services gain the cluster and the cluster mirrors
   `routerIntegrated: true` — no manual steps.
2. **Given** the integrated pair, **When** the integration is removed
   out-of-band (panel), **Then** the provider re-integrates on observation
   (single-writer drift repair).
3. **Given** a cluster declaring `routerRef` toward a Router that is not yet
   Ready (or not yet attached to the cluster's network with NAT), **When**
   reconciled, **Then** the integration waits with a clear condition and
   converges automatically when the router side is ready — never a hot loop.
4. **Given** a pre-existing cluster (created before this feature, e.g. the
   production cluster already integrated manually), **When** `routerRef` is
   added to its spec, **Then** the provider observes the existing linkage and
   reports `routerIntegrated: true` without issuing a redundant op (or issues
   an idempotent one).

---

### User Story 2 - Private nodepool failures point at the real, FIXABLE cause (Priority: P2)

When a private (`publicIP: false`) nodepool fails with the
`router_required_for_worker_groups_without_public_ip` family, the nodepool's
condition explains: the cluster is not router-integrated — set
`spec.forProvider.routerRef` on the KubernetesCluster (or integrate via the
panel). No text anywhere may claim recreation is required — that model is
disproven.

**Why this priority**: the raw 400 loop cost a full day live; the correct
one-line remedy turns it into a minutes-fix.

**Acceptance Scenarios**:

1. **Given** a non-integrated cluster, **When** a private nodepool create
   fails with that error family, **Then** the condition names the missing
   integration and the `routerRef` remedy.
2. **Given** the operator sets `routerRef`, **When** integration converges,
   **Then** the same nodepool proceeds with no further edits.

---

### User Story 3 - Ordering safety without blocking legitimate creates (Priority: P3)

A cluster declaring `routerRef` whose router does not yet NAT the cluster's
network simply WAITS (requeue-style condition) for the router side to
converge — the v0.10.0 Router features (bind-then-NAT, staticRoutes) make
that self-resolving in GitOps. Clusters without `routerRef` are entirely
unaffected: no preconditions, no warnings, byte-identical behavior to
v0.10.0.

**Acceptance Scenarios**:

1. **Given** `routerRef` toward a router whose attachment of the cluster
   network has no NAT yet, **When** reconciled, **Then** the integration op is
   deferred with a clear condition and fires once NAT is observed.
2. **Given** a cluster with no `routerRef`, **When** reconciled, **Then**
   behavior is unchanged from v0.10.0.

---

### Edge Cases

- **Un-declaring / changing the router** (clarified): removal detaches
  (`virtual_router_id: null` — upstream-confirmed); a changed ref
  re-integrates with the new router. Both follow declared state and verify by
  parent-services readback at the live gate. A detach must never fire merely
  because the ref is temporarily unresolvable (deleted-Router race) — only an
  explicit spec removal detaches.
- Integration op while the cluster is still provisioning: attempt when the
  cluster is observable; upstream rejections classify as transient and the
  op retries on the normal cadence.
- The router readback (`parent_services`) is on a DIFFERENT resource than the
  declaration — observation must tolerate the router being temporarily
  unreadable without failing the cluster's Observe.
- `routerRef` naming a Router in a different namespace / nonexistent: standard
  ref-gate behavior (wait with not-ready condition; never wedge deletion —
  the gate is skipped while deleting).
- Snapshot-desync re-verification: if the post-attach trap still bites even
  when integrated, the nodepool classification must keep naming it (with the
  wire-via-create-body exit) — gate evidence decides the final text.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: KubernetesCluster MUST accept an optional router declaration
  (`routerRef` reference, plus the conventional flat `routerID` escape hatch)
  identifying the Router to integrate with. Additive, NON-BREAKING.
- **FR-002**: When declared and the cluster exists upstream, the provider
  MUST perform the integration op and MUST keep it converged (re-integrate
  when the declared linkage is observed absent). Removing the declaration
  MUST detach (null the integration); changing it MUST re-integrate with the
  new router. Detach fires ONLY on explicit spec removal, never on a
  transiently unresolvable ref.
- **FR-003**: The linkage MUST be observed via the router's parent services
  (the only readback that exists) and mirrored as
  `status.atProvider.routerIntegrated` on the cluster; router-side read
  failures MUST NOT fail the cluster's Observe.
- **FR-004**: Integration MUST wait (typed requeue-style condition, no hot
  loop) while the referenced Router is not Ready or does not yet NAT the
  cluster's network; it MUST fire automatically once observed ready.
- **FR-005**: The nodepool `router_required_…` error family MUST be
  classified into a condition naming the missing integration and the
  `routerRef` remedy; no provider text may present recreation as the fix.
- **FR-006**: Clusters without the declaration MUST behave byte-identically
  to v0.10.0 (no new reads, no warnings, no preconditions).
- **FR-007**: Pre-existing integrations (panel-made) MUST be recognized:
  declaring `routerRef` over one converges without breaking it.
- **FR-008**: Unit tests per Constitution III (integrate-on-create, drift
  re-integrate, wait-on-not-ready, idempotence over existing linkage,
  classification, no-ref untouched); live e2e: full private-cluster ordering
  (network → router+NAT → cluster+routerRef → private nodepool Ready) plus
  the detach/move probes.
- **FR-009**: Docs: kubernetes.md private-cluster section rewritten around
  `routerRef` (the "order matters" warning softens to the wait-condition
  description); trap-guard texts updated to the fixable model;
  `specs/_next-router-features.preface.md` trimmed accordingly.

### Key Entities

- **KubernetesCluster**: gains `routerRef`/`routerID` (spec, optional) and
  `routerIntegrated` (status mirror).
- **Router**: unchanged; its `parentServices` mirror is the integration
  readback.
- **KubernetesClusterNodepool**: unchanged schema; gains the corrected error
  classification.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A fully-declared private cluster (network, NAT'd router,
  `routerRef`, private nodepool) converges end-to-end from empty namespace to
  nodepool Ready with ZERO panel/API interventions.
- **SC-002**: Panel-detaching the integration is repaired within one poll
  cycle; `routerIntegrated` reflects reality throughout.
- **SC-003**: On a non-integrated cluster, the private-nodepool condition
  names the `routerRef` remedy; following it (one spec edit) unblocks the
  nodepool with no other action.
- **SC-004**: Existing clusters without `routerRef` show zero behavior change
  (regression suites pass unchanged).
- **SC-005**: Removing `routerRef` detaches within one reconcile (router's
  parent services lose the cluster; `routerIntegrated: false`); changing it
  moves the integration — both verified live.

## Assumptions

- The integration op targets the documented cluster PATCH with an
  undocumented field — extend the client per the established body-extension /
  hand-patch convention; no full regen required.
- Readback-on-router means the cluster controller performs one routers-list
  read per reconcile ONLY when `routerRef` is declared (FR-006 protects
  everyone else); the shared rate limiter covers it.
- The v0.9.2/v0.10.0 Router mechanics (bind-then-NAT, staticRoutes, release)
  are the substrate that makes US3's wait-until-NAT self-resolving.
- The old Part 2 "create-precondition" is NOT revived as a blocker — ordering
  is handled by the wait-condition on the integration itself.
- Target release: minor **v0.11.0** (additive spec surface; SemVer per release-maintainer verdict — repo precedent classifies new fields as MINOR, urgency is a scheduling
  timing — the production cluster's manual integration should become declared
  state ASAP).
