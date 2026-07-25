# Feature Specification: Router peering — static routes, multi-router networks, NAT release

**Feature Branch**: `021-router-peering-routes`

**Created**: 2026-07-25

**Status**: Draft

**Input**: User description: "let's discuss the spec preface next router features, and let's decide together what we take into this spec, minimum is network multi attach to two routers and routers static routes nexthop + vuln fix, anything else is subject to discussion" — scope settled in-session: preface items 1–5 + 7 (static routes, multi-router attachment, x/text vuln bump, clusterNetworkCIDR, NAT-IP declarative release, k8sVersion drift message). Deferred to a later round: nodepool `virtual_router_id` + trap guards (blocked on the live probe matrix), OpenAPI base-spec refresh.

**Source**: `specs/_next-router-features.preface.md` (probe findings 2026-07-24/25, timeweb-infra#132/#135 rollout). Target release **v0.10.0** (feature, NON-BREAKING — additive spec fields only).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Declarative static routes between peered routers (Priority: P1)

The production fleet peers the `shared` and `production` routers through a
transit network; the static routes that make the peering work are hand-managed
in the panel today. The operator declares them on the Router resource instead:
each route is a destination subnet plus a nexthop. Two declaration forms:

```yaml
    staticRoutes:
      - subnet: 10.12.0.0/24
        nexthop: 10.13.0.3            # literal address
      - subnet: 10.14.0.0/24
        via:                          # by reference — nexthop resolved from
          routerRef: {name: shared}   # the neighbor router's observed gateway
          networkRef: {name: transit} # in the common network
```

The by-reference form matters because gateways are platform-assigned and
unknowable in git at authoring time. Routes converge like network attachments:
added routes are created, removed routes are deleted, out-of-band panel
deletions are re-created (drift repair), all under the existing mutation
pacing.

**Why this priority**: this is the live production need the feature exists for
(timeweb-infra#132); until it lands, every peering change is a manual panel
step invisible to GitOps.

**Independent Test**: two routers sharing a common network; declare a route on
each (one literal, one by-reference); both appear upstream and repair after a
manual panel deletion.

**Acceptance Scenarios**:

1. **Given** a Ready Router and a declared `staticRoutes` entry with a literal
   nexthop, **When** the provider reconciles, **Then** the route exists
   upstream and is mirrored in the Router's status.
2. **Given** a declared by-reference route whose neighbor Router is Ready with
   an observed gateway in the common network, **When** reconciled, **Then** the
   route is created with that gateway as nexthop.
3. **Given** a declared by-reference route whose neighbor Router is not yet
   Ready (or its gateway not yet observed), **When** reconciled, **Then** the
   route waits with a clear not-ready condition and converges automatically
   once the neighbor is usable — never a hot loop.
4. **Given** a converged route deleted in the panel, **When** the provider
   re-observes, **Then** the route is re-created (single-writer drift repair).
5. **Given** a route entry removed from the spec, **When** reconciled, **Then**
   the upstream route is deleted.

---

### User Story 2 - One network on several routers, without drift fights (Priority: P1)

A Network attached to multiple routers simultaneously is the platform's
official peering mechanism (verified live: the transit network sits on both
`shared` and `production`). Two Router resources may therefore reference the
same Network. Each Router's controller must scope every attach / detach /
DHCP / NAT decision strictly to its OWN router — the other router's attachment
of the same network is not drift and must never be "fixed".

**Why this priority**: without strict scoping the two Router CRs would fight
over the shared network (attach/detach flapping), breaking live peering — the
second half of the minimum scope.

**Independent Test**: common network declared on two Routers; both converge
Ready and stay quiet (no attach/detach events after convergence); deleting one
router leaves the network attached to the survivor.

**Acceptance Scenarios**:

1. **Given** one Network declared as an attachment on two Router resources,
   **When** both reconcile repeatedly, **Then** both are Ready/Synced and
   neither emits attach/detach operations after convergence.
2. **Given** the shared network and one router being deleted, **When** deletion
   completes, **Then** the network remains attached to the surviving router
   and the survivor shows no reconcile flapping.
3. **Given** stale reserved addresses leaked on the network by a past detach
   (platform wart, verified live), **When** a Router reconciles, **Then** the
   leaked reservations are not treated as provider-owned drift.

---

### User Story 3 - NAT floating IP released declaratively (Priority: P2)

Removing `natFloatingIP` from an attachment currently disables NAT but leaves
the address attached to the router — reclaiming it for another consumer needs
a manual unbind (disliked; v0.9.2 shipped thin). Now the release is part of
the same declarative transition: when the provider disables NAT because the
declaration was removed (or moved to another address), it also unbinds that
address from the router — provided no other attachment of this router declares
it and the address is not serving a DNAT rule (checked upstream before the
unbind; skipped with an explanatory event if in use). The provider still never
sweeps idle addresses it merely finds on the router, and never-steal stays
absolute.

**Why this priority**: closes the "unbind manually to reuse" wart with the
narrowest safe semantics — only IPs whose NAT transition the provider itself
is executing.

**Independent Test**: enable NAT on an attachment (bind happens), remove the
declaration, verify NAT disabled AND the address unbound; a panel-attached
idle address on the same router stays untouched.

**Acceptance Scenarios**:

1. **Given** an attachment whose NAT declaration is removed, **When**
   reconciled, **Then** NAT is disabled and the address is unbound from the
   router.
2. **Given** the same address declared as NAT on ANOTHER attachment of the
   same router, **When** one declaration is removed, **Then** the address
   stays bound.
3. **Given** an address serving a DNAT rule upstream, **When** its NAT
   declaration is removed, **Then** NAT is disabled, the unbind is skipped,
   and an event explains why.
4. **Given** an idle address attached to the router out-of-band (panel),
   **When** the provider reconciles, **Then** the address is never unbound.

---

### User Story 4 - Distinct pod/service CIDRs per cluster (Priority: P2)

The operator declares `clusterNetworkCIDR` (pods and services subnets,
create-only) on a KubernetesCluster so each cluster gets distinguishable
internal ranges — making cross-cluster debugging (Hubble, logs) unambiguous.
The production cluster shipped on defaults in July because the field was
missing; the NEXT cluster must not.

**Independent Test**: create a cluster declaring both CIDRs; upstream echoes
them; editing either post-create is rejected as immutable.

**Acceptance Scenarios**:

1. **Given** a cluster declared with pods/services CIDRs, **When** created,
   **Then** the upstream cluster uses those ranges (observed status mirror).
2. **Given** an existing cluster, **When** either CIDR is edited, **Then** the
   change is rejected at admission (immutability).
3. **Given** a cluster without the field, **When** created, **Then** behavior
   is exactly as today (platform defaults).

---

### User Story 5 - Version-drift errors name the valid versions (Priority: P3)

The upstream Kubernetes version list drifts (v1.35.4 vanished between
IaC-writing and create, v1.35.6 stayed). When a declared `k8sVersion` is no
longer offered, the pre-create validation error must name the currently valid
versions so the operator can fix the spec in one step instead of probing the
API.

**Acceptance Scenarios**:

1. **Given** a cluster declaring a version absent from the upstream list,
   **When** reconciled, **Then** the condition message lists the versions the
   upstream currently offers.

---

### Edge Cases

- Static routes: route identity for convergence is the destination subnet —
  the same subnet with a changed nexthop converges by replace (delete +
  create); duplicate subnets in one spec are rejected at admission.
- Static routes: exactly one of `nexthop` / `via` per entry (admission-
  enforced); a `via` pair naming a network the neighbor router is NOT attached
  to is a spec error surfaced as a typed condition, not a retry loop.
- Static routes: the upstream "available nexthops" listing is known-unreliable
  (empty in all probes) and MUST NOT be used for validation; invalid nexthops
  surface as classified upstream errors.
- Static routes: bounded list (admission cap) so route sets converge within
  the existing per-reconcile mutation pacing; deleting the Router deletes its
  routes with it (upstream-cascaded — verify, don't assume).
- Multi-router: the never-detach-last guard is per router, not per network —
  a network detachable from router A while it remains router B's last network
  must still respect B's invariant.
- NAT release: unbind failure after a successful NAT disable is best-effort —
  event emitted; the address then looks panel-attached and is deliberately
  not retried (documented residue, strictly better than today's always-bound).
- NAT release: NAT moved from address X to address Y in one edit — Y follows
  the bind-then-enable flow (v0.9.2), X follows the disable-then-unbind flow;
  both under pacing, no interleaving hazard.
- clusterNetworkCIDR: overlapping pods/services ranges rejected at admission;
  malformed CIDR rejected by schema.
- Version message: the upstream version list read failing transiently must not
  turn into a misleading "invalid version" message.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Router MUST support declared static routes (destination subnet +
  nexthop), converging additions, removals, and out-of-band deletions
  (single-writer drift repair) under the existing mutation pacing, with the
  route set mirrored in status.
- **FR-002**: Each static route MUST accept exactly one nexthop form: a
  literal address, or a reference pair (neighbor Router + common Network)
  resolved from the neighbor's observed gateway; an unresolvable reference
  waits with a typed not-ready condition and converges automatically.
- **FR-003**: Static-route validation MUST NOT depend on the upstream
  "available nexthops" listing; upstream rejections surface as classified
  errors with the upstream message.
- **FR-004**: All per-attachment operations (attach/detach/DHCP/NAT/routes)
  MUST be scoped to the CR's own router; another router's attachment of the
  same network is never treated as drift.
- **FR-005**: Deleting one of several routers sharing a network MUST leave the
  network attached to the survivors, with no reconcile flapping on their CRs.
- **FR-006**: Leaked per-attachment reservations on a network (post-detach
  platform wart) MUST NOT be treated as provider-owned drift.
- **FR-007**: When the provider disables NAT because the declaration was
  removed or moved, it MUST also unbind that address from the router — unless
  the address is declared on another attachment of the router or serves a
  DNAT rule upstream (skip + explanatory event). The provider MUST NOT unbind
  addresses outside a NAT transition it is executing; never-steal remains
  absolute.
- **FR-008**: KubernetesCluster MUST accept optional create-only
  `clusterNetworkCIDR` pods/services ranges (immutable post-create, admission-
  enforced; absent ⇒ platform defaults; observed values mirrored in status).
- **FR-009**: The pre-create Kubernetes version validation MUST name the
  currently valid upstream versions in its error message; a transient list
  failure is classified transient, never reported as an invalid version.
- **FR-010**: Dependency hygiene: the module MUST be free of known reachable
  vulnerabilities at release (GO-2026-5970 / `golang.org/x/text` ≥ v0.39.0);
  the CI vulnerability gate on main is green again.
- **FR-011**: All changes are NON-BREAKING: new spec fields are optional and
  additive; existing manifests behave identically; CRDs/generated artifacts
  regenerate in the same PR (Constitution I).
- **FR-012**: Every new external-client path ships fake-client unit tests
  (success, not-found, transient, terminal — Constitution III); live e2e
  covers the two-router peering arrangement end-to-end (common network, routes
  via both forms, survivor deletion, NAT release).

### Key Entities

- **Router**: gains `staticRoutes` (list of {subnet, nexthop | via{routerRef,
  networkRef}}) and the transition-scoped NAT unbind behavior; attachment
  semantics become explicitly multi-router-safe.
- **Network**: unchanged schema; now legitimately referenced by several
  Routers.
- **FloatingIP**: unchanged schema; its router binding now also releases
  declaratively on NAT removal.
- **KubernetesCluster**: gains create-only `clusterNetworkCIDR`
  (pods/services); version-validation message upgraded.
- **Static route**: subnet-keyed declarative entry on a Router; upstream
  object with platform-assigned id, mirrored in Router status.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The production peering arrangement (two routers, transit
  network, routes both directions) is fully declared in git: zero
  panel-managed static routes remain on the peered routers after migration.
- **SC-002**: A route deleted in the panel is restored automatically within
  one poll cycle; a route removed from the spec disappears upstream within one
  reconcile.
- **SC-003**: Two Router CRs sharing a network run a sustained soak (≥30 min)
  with zero attach/detach events after initial convergence; deleting one
  leaves the survivor Ready with the network attached.
- **SC-004**: Removing a NAT declaration frees its address for a new consumer
  with zero manual panel/API steps (end-to-end: remove on router A, declare on
  server/router B, B converges).
- **SC-005**: A cluster created with declared pod/service CIDRs shows those
  exact ranges upstream; post-create edits are rejected at admission.
- **SC-006**: A stale `k8sVersion` fails with a message naming ≥1 currently
  valid version; no API probing needed to correct the spec.
- **SC-007**: The vulnerability gate (`govulncheck`) passes on main; v0.10.0
  releases with zero known reachable vulnerabilities.
- **SC-008**: Existing suites (router lifecycle, NAT bind from v0.9.2,
  private-cluster, selector bundles) pass unchanged.

## Assumptions

- Static-route API surface as probed 2026-07-25: list/create/delete of
  {subnet, nexthop} sub-resources; no update op (replace = delete + create);
  nexthop must be a neighbor router's gateway in a common network or a cloud
  server in an own network (managed-k8s nodes do not qualify). Routes and
  their quirks: `specs/_next-router-features.preface.md` §3.
- NAT release ships as the DEFAULT behavior of the removal transition (no new
  opt-in spec field): the mechanism only ever touches the address whose NAT
  the provider is actively transitioning, which keeps the blast radius at
  declared state; the DNAT read guards the one known invisible use. (Option
  analysis in preface §6.)
- The v0.9.2 never-steal + bind-then-enable semantics are unchanged; this
  feature only adds the symmetric release.
- `clusterNetworkCIDR` upstream field shape per preface §5 (create-only in the
  cluster create body); no day-2 mutability exists upstream.
- Deferred out of scope: nodepool `virtual_router_id`, post-attach trap
  guards, cluster-create wiring precondition rework (blocked on the live
  probe matrix, preface "Live probe matrix wanted"), OpenAPI base-spec
  refresh, DNAT rule management as a spec surface (only READ for the release
  guard).
- Target release: v0.10.0.
