# Feature Specification: Router NAT-on-update bind fix (v0.9.2 — Part 1 only)

**Feature Branch**: `020-fix-router-nat-update`

**Created**: 2026-07-24

**Status**: Draft

**Input**: User description: "Fix Router NAT-on-update 'IP not found' infinite retry (timeweb-infra#135, target patch v0.9.2, NON-BREAKING bugfix). Enabling NAT on a network attachment of an EXISTING Router loops forever with CannotUpdateExternalResource 'IP not found'. The create path attaches declared NAT floating IPs in the router create request, but the update path issues the NAT enable directly without any IP-attach step — the router doesn't own the IP, upstream returns 404 ip_not_found, and the controller retries indefinitely."

**Source incident**: `infra/timeweb-infra#135` (found activating the production network on the shared router, 2026-07-24). The network attach and DHCP change converged; the NAT enable never did. **Part 2** (same rollout): the production cluster was created on a router-attached but NAT-less network → the cluster↔router linkage never formed and every private nodepool create fails `router_required_for_worker_groups_without_public_ip`. Handoff for both: `specs/_next-router-nat-bind.preface.md`.

## Clarifications

### Session 2026-07-24

- Q: Is there really no API to attach a floating IP to an existing router (issue-comment probe said `/bind` rejects `resource_type=router`)? → A: **Correction — there is.** `POST /api/v1/floating-ips/{fip_uuid}/bind` with body `{"resource_type": "router", "resource_id": "<router UUID>"}` succeeds, despite the documented enum omitting `router` (stale enum, same precedent as the firewall `ResourceType`). Live-verified 2026-07-24 on the incident router: after this one bind call, the provider's existing NAT write converged on the next reconcile with no further intervention (router owns 201.24.116.171, NAT active on the production network, MR Ready/Synced). The fix is therefore **auto-bind in the update path**, not a panel-attach degraded condition; the earlier "panel-only" constraint section is superseded.
- Q: When the declared NAT IP is bound to another resource, may the provider force the bind (unbind from the holder)? → A: **Never steal** — degraded condition naming the IP and current holder; converge automatically once the IP is freed. Freeing a binding is always a deliberate operator/other-MR action.
- Q: On NAT disable / NAT moved off an IP, should the provider unbind the IP from the router? → A: **No — leave it bound** (per the investigation handoff): the FIP may be shared intent or re-enabled later; unbinding changes panel-visible state; leaving it bound matches how create-path routers look after a NAT toggle-off. Documented as expected behavior. *(Revisited 2026-07-25: the manual-unbind-to-reuse step is disliked; declarative release options — symmetric transition unbind with DNAT guard, `releaseOnDisable` opt-in, tracked ownership — are specced in `specs/_next-router-features.preface.md` §6 for v0.10.0. 0.9.2 ships thin with leave-bound.)*
- Q: (scope extension) → A: ~~Part 2 in scope for this patch~~ — superseded 2026-07-25, see below.

### Session 2026-07-25

- Q: The handoff gained Parts 3–6 + a design philosophy ("fix provider gaps, guard platform traps"); Part 3's panel capture shows the REAL nodepool↔router mechanism is an explicit undocumented `virtual_router_id` per node-group create (so "recreate the cluster is the only remedy" is false), and even NAT'd post-attach networks get rejected by the k8s service (snapshot desync chain). Disposition? → A: **v0.9.2 ships the minimal bugfix only — Part 1** (the #135 incident bug: bind-before-NAT, never-steal, blocked-row condition, loop isolation; ours, verified, tested). The implemented Part 2 pieces (cluster create-precondition, `routerLinked`, nodepool recreate-only classification) are REVERTED — their premises are invalidated. Everything router-related not shipping in 0.9.2 (Parts 2–6 rework: `virtual_router_id`, trap-guard conditions per the new philosophy, `staticRoutes`, multi-router scoping, `clusterNetworkCIDR`, k8sVersion drift) is respecced for **v0.10.0** in `specs/_next-router-features.preface.md`.

## Upstream surface (probed 2026-07-24)

**Working attach path**: `POST /api/v1/floating-ips/{fip_uuid}/bind` accepts
`resource_type: "router"` with the router **UUID** as `resource_id` — undocumented
enum value (documented enum lists only `server|balancer|database|network`; the
readback `bound_to` already reports `router` bindings, UUID-keyed — feature 006
F-5). Live-verified on the incident router; NAT converged automatically
afterwards. Unbind is the symmetric documented `/unbind` operation.

Paths that do **not** work (probed, for the record):

- `POST /routers/{id}/ips` does not exist (404 page not found, both body forms).
- The router edit operation carries only `name`/`comment`.
- The NAT enable operation rejects both an unowned IP address and a floating-IP
  UUID with 404 `ip_not_found` — it only accepts an address the router already
  owns; it never attaches.

The stale documented bind enum MUST be captured as an upstream quirk per project
convention (docs are wrong, API is capable).

## Descoped (2026-07-25): Part 2 and beyond → v0.10.0

The originally-in-scope Part 2 (cluster↔router linkage guard: create-
precondition, `routerLinked`, nodepool classification) was implemented and
then REVERTED in this branch: the handoff's later findings (Part 3 —
`virtual_router_id` is the real per-nodepool router binding; post-attach
networks rejected by the k8s service even with NAT/DHCP on) invalidate its
premises. User stories US6–US8, FR-012..FR-016 and SC-005..SC-007 below the
line are retained for history but are NOT part of v0.9.2 — they are respecced
(reshaped by the trap-guard philosophy and the `virtual_router_id` mechanism)
in `specs/_next-router-features.preface.md` (target v0.10.0).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - NAT added to an existing router converges automatically (Priority: P1)

An operator manages a shared Router that already serves several attached
networks. They declare NAT egress on one attachment (new or existing), pointing
at a FloatingIP the router does not yet own. Today the resource enters an
endless `CannotUpdateExternalResource … IP not found` retry loop. Instead, the
provider must recognize — before issuing the NAT write — that the declared NAT
address is not among the router's owned addresses, bind the floating IP to the
router itself (the same declarative outcome the create path achieves via its
IP list), and then converge NAT. End to end, declaring NAT on an existing
router must work exactly like declaring it at router creation: hands-free.

**Why this priority**: This is the incident bug (#135). It blocked production
network activation, produced a misleading error, and burned reconcile cycles on
a write that could never succeed without the missing bind step.

**Independent Test**: Against a router whose upstream mirror lacks the declared
NAT address (address known free), run reconciles: the provider issues the bind,
then the NAT enable, and the attachment converges with no manual steps and no
`ip_not_found` errors.

**Acceptance Scenarios**:

1. **Given** an existing Ready Router and an attachment newly declared with
   `nat` pointing at a free FloatingIP the router does not own, **When** the
   provider reconciles, **Then** it binds the IP to the router and subsequently
   enables NAT, and the attachment converges without operator intervention.
2. **Given** that flow, **When** reconciles repeat during convergence, **Then**
   no `CannotUpdateExternalResource … IP not found` errors are emitted and the
   NAT enable is only issued once observation confirms the router owns the
   address (verify-by-reobservation).
3. **Given** a fresh Router created with NAT declared from the start, **When**
   it is created, **Then** behavior is unchanged (IPs attached at create, NAT
   converges as today).

---

### User Story 2 - Bind failure surfaces a clear condition, not a retry storm (Priority: P2)

The bind step can fail — most notably when the declared FloatingIP is currently
bound to another resource. The provider must not silently hot-loop: the
resource surfaces a clear degraded condition (established upstream-failure
vocabulary) identifying the attachment, the IP, and why the bind cannot
proceed, and requeues at the normal paced/poll cadence. Once the blocker is
removed upstream (IP freed), convergence resumes automatically.

**Why this priority**: The fallback path is what prevents a #135-style
undiagnosable loop when auto-bind cannot succeed; it also guards other
services' bindings from being broken.

**Independent Test**: Declare NAT with an IP bound to another resource; verify
no bind-steal is attempted, the degraded condition appears with an actionable
message, no hot loop occurs, and freeing the IP upstream unblocks convergence
with no MR edits.

**Acceptance Scenarios**:

1. **Given** a declared NAT IP that is bound to another resource, **When** the
   provider reconciles, **Then** it never forces the bind away from the current
   holder (no unbind-steal under any circumstances), reports a degraded
   condition naming the IP and holder, and requeues paced.
2. **Given** the blocked state, **When** the IP becomes free upstream, **Then**
   a later reconcile binds it and NAT converges with no manual resource poking.

---

### User Story 3 - One blocked NAT does not wedge the rest of the router (Priority: P2)

A shared router carries several attachments. One attachment's NAT is blocked
(bind failing). The blocked attachment must be skipped — not abort the update
pass — so every other attachment continues to converge normally
(attach/detach/DHCP/other NAT ops).

**Why this priority**: The incident router is shared infrastructure; today the
NAT error aborts the per-attachment convergence loop, so a single blocked
attachment can freeze unrelated changes on the same router.

**Independent Test**: Declare two changes in one reconcile — a blocked NAT on
attachment A and a DHCP change on attachment B — and verify B converges while A
reports the condition.

**Acceptance Scenarios**:

1. **Given** attachment A blocked on an unbindable NAT IP and attachment B with
   drifted DHCP, **When** the provider reconciles, **Then** B's DHCP is
   corrected and A reports the degraded condition.

---

### User Story 4 - Lifecycle: IPs no longer used for NAT stay bound (Priority: P3)

When an operator removes a NAT declaration (or moves NAT to a different IP),
the provider disables NAT (existing behavior) and **leaves the IP bound to the
router** — no unbind. Rationale (investigation handoff): the FIP may be shared
intent or re-enabled later; unbinding mutates panel-visible state; and this
matches how create-path routers look after a NAT toggle-off. The leftover
binding is documented so operators know to unbind manually (or delete the
FloatingIP MR) when the address should be freed for reuse.

**Why this priority**: Symmetry must be deliberate, not implied; the
documented leftover prevents "why is my IP still attached" confusion without
adding a destructive-adjacent write to a shared production router.

**Independent Test**: Remove a NAT declaration; verify NAT is disabled, the IP
remains in the router's observed IP set, and no unbind call is issued.

**Acceptance Scenarios**:

1. **Given** an attachment whose NAT declaration is removed, **When** the
   provider reconciles, **Then** NAT is disabled, no unbind is issued, and the
   IP remains bound to the router.

---

### User Story 5 - The behavior and quirk are documented (Priority: P3)

An operator reading the Router documentation learns that NAT on an existing
router auto-binds the referenced FloatingIP, what the bind-failure condition
looks like, and how to unblock it. The stale documented bind enum (missing
`router`) is captured as an upstream quirk/support note per project convention.

**Independent Test**: Router kind docs and the conditions reference describe
the auto-bind behavior, the condition reason, and the unblock procedure; the
quirk capture exists.

**Acceptance Scenarios**:

1. **Given** the shipped docs, **When** an operator looks up the degraded
   condition reason, **Then** they find the bind-failure explanation and the
   remedy.

---

### User Story 6 - A cluster can never be created into the unlinkable state (Priority: P1)

An operator declares a KubernetesCluster on a network. If that network is
attached to a router but has **no NAT** at create time, the provider refuses to
create the cluster: a clear condition explains that a cluster created now would
never link to the router, private nodepools would fail
`router_required_for_worker_groups_without_public_ip`, and NAT must be enabled
first. The block is a requeue, not terminal — the moment NAT appears on the
network (e.g. Part 1 converges it), the create proceeds automatically. A
network with no router at all stays allowed (public-only clusters are
legitimate) but the provider emits a Warning event noting private nodepools
will not be possible.

**Why this priority**: This is the core Part 2 fix — it makes the
unrecoverable-without-recreate state unreachable. The production cluster hit
exactly this; recovery is delete + recreate, which only gets more expensive
with time.

**Independent Test**: Declare a cluster on a router-attached NAT-less network →
create blocked with the explicit condition; enable NAT → create proceeds on a
later reconcile with no MR edits. Declare a cluster on a router-less network →
create proceeds, Warning event emitted.

**Acceptance Scenarios**:

1. **Given** a network attached to a router without NAT, **When** a
   KubernetesCluster targeting it is reconciled, **Then** no upstream create is
   issued and the CR carries a condition naming the network, the consequence,
   and the remedy (enable NAT first).
2. **Given** that blocked state, **When** NAT is enabled on the network,
   **Then** a later reconcile creates the cluster without manual intervention.
3. **Given** a network with no router, **When** the cluster is reconciled,
   **Then** the create proceeds and a Warning event notes that private
   nodepools will require a NAT'd router wired before cluster creation.

---

### User Story 7 - Private nodepool failures name the real cause (Priority: P2)

An operator adds a private (no-public-IP) nodepool to a cluster whose
cluster↔router linkage is missing (e.g. a pre-fix cluster). Instead of a raw
`400 router_required_for_worker_groups_without_public_ip` retry loop, the
nodepool CR surfaces a condition explaining the actual situation: the
cluster→router linkage is missing and is frozen at cluster-create time; check
the router's `parentServices`; recreating the cluster is the only remedy.

**Why this priority**: Pre-fix clusters (production included) still exist; the
raw 400 sends operators chasing phantom router/NAT config when nothing they can
change will help.

**Independent Test**: Reconcile a private nodepool against a cluster without
linkage; assert the condition message names the frozen linkage and remedy
rather than the raw error string.

**Acceptance Scenarios**:

1. **Given** a cluster with no router linkage, **When** a private nodepool
   create fails with `router_required_for_worker_groups_without_public_ip`,
   **Then** the nodepool CR condition explains the missing frozen linkage and
   the recreate-only remedy.

---

### User Story 8 - Linkage state is visible on the cluster CR (Priority: P3)

An operator inspects a KubernetesCluster and sees whether the cluster↔router
linkage exists (`status.atProvider.routerLinked`) without any API spelunking —
making the broken state (and the healthy state) directly observable in
`kubectl describe`/GitOps tooling.

**Why this priority**: Diagnosis today requires manual router API reads; the
status mirror is cheap because the router observation surface already exposes
`parentServices`.

**Independent Test**: A linked cluster shows `routerLinked: true`; a cluster on
a router-less network shows `routerLinked: false` (with the Warning from US6);
a pre-fix broken cluster shows `routerLinked: false`.

**Acceptance Scenarios**:

1. **Given** any KubernetesCluster on a router-wired network, **When** it is
   observed, **Then** `status.atProvider.routerLinked` reflects whether the
   cluster appears in the router's parent services.

---

### Edge Cases

- Declared NAT IP is owned by the router but NAT is currently on a *different*
  owned IP (change case): must converge as today — the precondition is
  ownership of the *declared* address; no bind needed.
- Declared NAT IP is already bound to this router (e.g. operator pre-attached
  it): no bind issued; NAT proceeds directly (idempotence).
- Declared NAT IP does not resolve to any known FloatingIP: degraded condition
  (nothing to bind).
- Bind succeeds but the subsequent NAT enable still 404s (races, stale
  observation): treated as a normal classified error for that reconcile; must
  not recur persistently once observation catches up.
- NAT *disable* must keep working unchanged; the IP stays bound to the router
  afterwards (US4 — no unbind).
- Router create with NAT: unchanged; the bind step applies to the update path
  only.
- Pacing interplay: the bind is a mutation and must respect the per-reconcile
  mutation budget; a skipped/blocked bind must not consume budget or flap the
  condition between paced reconciles.
- (Part 2) Cluster on a router-attached network whose NAT is declared but not
  yet converged (Part 1 in flight): create stays blocked-requeued and proceeds
  automatically once NAT lands — the ordering race the precondition exists for.
- (Part 2) Network attached to a router where the router observation is
  transiently unavailable: the precondition check failure is transient —
  requeue, never proceed on unknown wiring state.
- (Part 2) Adopted/pre-existing clusters: `routerLinked` is observational only;
  the precondition applies to creates, never to Observe/Update of existing
  clusters.
- (Part 2) Public nodepools on an unlinked cluster keep working — only private
  (no-public-IP) pools hit the upstream 400; the friendly classification must
  not fire for unrelated 400s.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Before issuing a NAT enable/change for an attachment, the update
  path MUST verify the declared NAT address is among the addresses the router
  currently owns (as observed upstream).
- **FR-002**: When the declared NAT address is not owned by the router and the
  referenced FloatingIP is free, the provider MUST bind the FloatingIP to the
  router (router-UUID-keyed bind) and converge NAT once observation confirms
  ownership — no operator intervention.
- **FR-003**: The provider MUST NOT issue a NAT enable for an address the
  router does not own (the doomed write of #135 is eliminated, not retried).
- **FR-004**: The provider MUST NEVER unbind the declared IP from another
  holder to claim it (no bind-steal). When the bind cannot proceed (IP bound
  elsewhere, unresolvable, or bind rejected), the provider MUST surface a
  degraded condition using the
  established upstream-failure vocabulary — identifying the attachment/network,
  the IP, and the reason — and requeue at the normal paced/poll cadence (no hot
  loop, no repeated `CannotUpdateExternalResource` events for the same cause).
- **FR-005**: A blocked NAT on one attachment MUST NOT abort convergence of
  other pending work on the same router (attach/detach/DHCP/other NAT ops in
  the same or later reconciles).
- **FR-006**: Once the blocker clears upstream (IP freed or otherwise becomes
  bindable/owned), convergence MUST resume automatically and the degraded
  condition MUST clear, with no manual changes to the managed resource.
- **FR-007**: The create path (IPs declared at router creation) and the NAT
  disable path MUST be unchanged; NAT disable MUST NOT unbind the IP from the
  router (US4).
- **FR-011**: The bind step MUST resolve the declared NAT address to its
  FloatingIP identity for both declaration forms — a referenced FloatingIP MR
  (via its recorded external identity) and a raw address (via upstream
  floating-IP listing lookup).
- **FR-012** (Part 2): Before creating a KubernetesCluster, the provider MUST
  verify the target network's router wiring: if the network is attached to a
  router WITHOUT NAT, the create MUST be refused with an explicit condition
  (naming the network, the never-links consequence including
  `router_required_for_worker_groups_without_public_ip`, and the enable-NAT
  remedy) and requeued — never issued. Once NAT is present, the create MUST
  proceed automatically.
- **FR-013** (Part 2): A cluster network with NO router attachment MUST remain
  creatable (public-only clusters are legitimate), with a Warning event noting
  that private nodepools will not be possible on the resulting cluster.
- **FR-014** (Part 2): The nodepool controller MUST classify upstream
  `router_required_for_worker_groups_without_public_ip` into a condition
  message naming the real cause (cluster↔router linkage missing, frozen at
  cluster-create; check the router's `parentServices`; cluster recreation is
  the only remedy) instead of surfacing the raw 400 retry loop.
- **FR-015** (Part 2): KubernetesCluster MUST surface
  `status.atProvider.routerLinked` (whether the cluster appears in its
  network's router parent services), populated from the existing router
  observation surface. Status-only, additive — no spec-field change.
- **FR-016** (Part 2): An e2e covers the canonical ordering (network → router
  attach + NAT → cluster → private nodepool) and the negative case (NAT-less
  router-attached network → cluster create blocked by the precondition).
- **FR-008**: A regression test MUST cover "NAT added to an already-existing
  router": unowned-free IP → bind issued then NAT converges; unowned-bound-
  elsewhere IP → no bind, degraded condition; owned IP → NAT direct.
- **FR-009**: The Router kind documentation and the conditions reference MUST
  record the auto-bind behavior, the bind-failure condition reason, and the
  unblock procedure; the stale bind-enum quirk MUST be captured for upstream
  support per project convention.
- **FR-010**: The fix is NON-BREAKING: no schema changes to spec fields; only
  status/condition and behavior changes.

### Key Entities

- **Router**: existing managed resource; its network attachments declare
  optional NAT via a floating-IP address; upstream mirror includes the set of
  IPs the router owns; the Router is the single owner of bind/unbind
  side-effects for IPs it declares for NAT (consuming-MR-owns-binding
  convention, as with Server).
- **FloatingIP**: existing pure-allocation managed resource the NAT
  declaration references; may be free, bound to this router, or bound to
  another resource; its status mirrors the binding for diagnostics only.
- **Degraded condition**: status-only signal (established upstream-failure
  vocabulary) carrying the bind-blocked reason and remedy.
- **KubernetesCluster** (Part 2): gains the create-precondition on its
  network's router wiring and the additive `status.atProvider.routerLinked`
  mirror; spec surface unchanged.
- **KubernetesClusterNodepool** (Part 2): gains the friendly classification of
  the linkage-missing upstream error; no schema change.
- **Cluster↔router linkage**: upstream `router.parent_services` entry of type
  `k8s`; formed only at cluster create on a NAT'd router-wired network; never
  mutable afterwards.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Reproducing the #135 scenario (NAT declared on a network of an
  existing shared router, free IP not yet attached) converges hands-free —
  bind + NAT — within the normal reconcile cadence, with zero `ip_not_found`
  update errors.
- **SC-002**: With the declared IP bound to another resource, the resource
  reports exactly one clear degraded condition, emits no repeated update
  errors over a sustained soak (≥10 minutes), and converges automatically
  after the IP is freed.
- **SC-003**: On a shared router with one blocked attachment, all other
  declared attachment changes still converge.
- **SC-004**: Existing Router e2e/regression suites (fresh create with NAT,
  DHCP convergence, NAT disable) pass unchanged.
- **SC-005** (Part 2): A KubernetesCluster declared on a router-attached
  NAT-less network is never created upstream; it carries the explicit
  precondition condition, and converges automatically (blocked → created →
  private nodepool Ready) once NAT is enabled, with no MR edits.
- **SC-006** (Part 2): On a linkage-less cluster, a private nodepool's
  condition names the frozen-linkage cause and remedy — the raw
  `router_required_for_worker_groups_without_public_ip` string no longer
  appears as the only diagnostic.
- **SC-007** (Part 2): `routerLinked` on the cluster CR matches the router's
  observed parent services for linked, router-less, and broken clusters.

## Assumptions

- The referenced FloatingIP already exists (typically as its own managed
  resource, per the existing NAT reference trio).
- The undocumented `router` bind target is stable API surface (it is what
  the panel-visible state reflects and the readback already reports router
  bindings); the stale documented enum is a docs gap, not a beta flag —
  captured as a quirk regardless.
- The existing error-classification behavior (404 `ip_not_found` with the
  canonical envelope → not-found classification, per feature 019) is correct
  and unchanged; this fix adds the missing bind step and precondition, not
  reclassification.
- Recreating the router is never needed for this flow and remains out of
  scope as an automatic behavior (shared infrastructure; incident #124
  sensitivity).
- (Part 2) No public API re-links an existing cluster to a router; remediating
  the already-broken production cluster (recreate) is an operations task, OUT
  of scope for the provider — the provider's job is making the state
  unreachable, diagnosable, and honestly reported.
- (Part 2) `routerLinked` and the create-precondition read the existing router
  observation surface (router networks / parent services); no new upstream
  endpoints are assumed.
- Target release: patch v0.9.2 (both parts ship together; all changes
  NON-BREAKING — behavior, conditions, and additive status only).
