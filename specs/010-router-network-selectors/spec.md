# Feature Specification: Router Multi-Network Attachment & Selectors

**Feature Branch**: `010-router-network-selectors`

**Created**: 2026-06-22

**Status**: Draft

**Input**: User description: "router multi-network attachment and selectors"

## User Scenarios & Testing *(mandatory)*

The `Router` kind already accepts a list of network attachments, where each entry
names exactly one network by reference (`networkRef`) or by raw upstream id
(`networkID`). This feature adds **label-selector–based attachment**: an operator
labels their networks and the router attaches *every* network whose labels match,
with the set tracked continuously as networks are created, labeled, unlabeled, or
deleted. This turns a static, hand-maintained attachment list into a declarative,
self-converging membership rule.

### User Story 1 - Attach every matching network by label (Priority: P1)

An operator labels a group of networks (e.g. `router-attach: "true"`) and declares
a single selector attachment on the router. The router attaches all matching
networks. When the operator later creates another network with the same label, the
router attaches it automatically — without anyone editing the Router resource.

**Why this priority**: This is the core of the feature and the reason multi-network
attachment needs selectors. It removes the need to enumerate and continuously
maintain a network list by hand, which is the main day-2 toil with the current
ref/id-only model. Delivers value on its own.

**Independent Test**: Create three networks bearing a shared label, declare one
router with a single selector attachment, and confirm all three are attached. Then
create a fourth network with the same label and confirm the router attaches it on
the next reconcile without any change to the Router. Remove the label from one and
confirm it is detached.

**Acceptance Scenarios**:

1. **Given** three Ready networks labeled `app=true` and a router with one
   attachment using `networkSelector: {matchLabels: {app: "true"}}`, **When** the
   router reconciles, **Then** all three networks are attached and the router
   reports them in its status.
2. **Given** the router from (1), **When** a fourth Ready network labeled
   `app=true` is created, **Then** the router attaches it within one reconcile
   interval with no edit to the Router resource.
3. **Given** the router from (1), **When** the `app` label is removed from one
   network (or that network is deleted), **Then** the router detaches it within one
   reconcile interval.
4. **Given** a network that matches the selector but is not yet `Ready`, **When**
   the router reconciles, **Then** that network is not attached until it becomes
   `Ready`, and the router does not report an error solely because the network is
   still provisioning.

---

### User Story 2 - Mix selector and explicit attachments (Priority: P2)

An operator combines a selector attachment (for a fleet of like-purposed networks)
with one or more explicit `networkRef`/`networkID` attachments (for specific
networks that need distinct settings, e.g. NAT). The router attaches the union of
both with no duplicates.

**Why this priority**: Real routers mix a managed fleet with a few special-cased
networks. Without clean union/dedup semantics, operators would be forced to choose
selectors *or* explicit entries, undermining adoption. Builds on US1.

**Independent Test**: Declare one selector attachment matching two networks and one
explicit `networkRef` to a third, where the third *also* carries the selector
label. Confirm the router attaches exactly three networks (no duplicate for the
overlapping one) and that the explicit entry's per-attachment settings win for the
overlapping network.

**Acceptance Scenarios**:

1. **Given** a router with one selector attachment matching networks A and B and an
   explicit `networkRef` to network C, **When** it reconciles, **Then** A, B, and C
   are all attached.
2. **Given** a network is matched by a selector entry *and* named by an explicit
   entry, **When** the router reconciles, **Then** it is attached exactly once, and
   the explicit entry's per-attachment settings (e.g. DHCP) take precedence over the
   selector entry's defaults.
3. **Given** two selector entries whose match sets overlap, **When** the router
   reconciles, **Then** each network in the overlap is attached exactly once.

---

### User Story 3 - Safe convergence and clear blocking (Priority: P3)

An operator relying on selector attachments gets clear, safe behavior at the
boundaries: the router is never left with zero networks, mistakes surface as
conditions rather than silent drift, and NAT remains explicit.

**Why this priority**: The dynamic match set introduces failure modes the static
list never had (a selector that matches nothing, a fleet that drains to empty).
These must be safe and observable, but they are refinements on top of US1/US2.

**Independent Test**: Declare a router whose only attachment is a selector that
matches zero Ready networks; confirm it does not create/converge a router with zero
networks and instead reports a blocking condition. Then label one network to match
and confirm the router converges.

**Acceptance Scenarios**:

1. **Given** a router whose declared attachments would resolve to zero networks,
   **When** it reconciles, **Then** it does not attempt to create or converge a
   router with zero attached networks and surfaces a clear not-ready/blocked reason
   (rather than letting the upstream reject it with
   `router_must_have_at_least_one_network`).
2. **Given** a healthy router with several selector-matched networks, **When** the
   match set would shrink to zero (all labels removed at once), **Then** the
   provider does not detach the final network in a way that leaves the upstream
   router with zero networks; it surfaces a blocking reason instead.
3. **Given** a selector attachment, **When** the operator also declares a NAT
   floating-IP on that same selector entry, **Then** the request is rejected at
   admission with a clear message (NAT requires an explicit single network).

---

### Edge Cases

- **Selector matches nothing**: A selector entry that matches zero Ready networks
  contributes zero attachments; the entry itself is not an error, but if the *router
  as a whole* would have zero resolved attachments it is blocked (US3-1). Note this
  is distinct from the declared-entry count: a single selector entry satisfies the
  "at least one declared attachment" rule yet can still resolve to zero networks.
- **Matched-but-not-Ready**: Networks matching the labels but not yet `Ready` (no
  upstream id, or `Ready≠True`) are excluded until they become Ready; this is a
  transient wait, not a failure.
- **Overlap / duplicates**: A network reachable via multiple entries (two
  selectors, or a selector plus an explicit ref/id) is attached exactly once.
- **Precedence**: When a network is reachable via both a selector entry and an
  explicit entry, the explicit entry's per-attachment settings apply.
- **Draining to empty**: Removing all matching labels at once must not detach the
  last upstream network into an invalid zero-network router.
- **NAT on a selector entry**: Rejected at admission — a single floating IP cannot
  serve an unbounded, changing set of networks.
- **Label churn / flapping**: Rapid add/remove of labels should converge to the
  current match set without thrashing beyond normal reconcile cadence.
- **Cross-namespace**: Selectors match only networks in the router's own namespace
  (consistent with existing reference resolution).

## Clarifications

### Session 2026-06-22

- Q: How should the provider handle attaching/detaching a large selector match set, given the upstream Qrator burst-ban hazard? → A: Pace/throttle attach & detach calls (reuse the existing rate limiter) and converge the match set incrementally over multiple reconciles; no hard cap on match size.
- Q: How should an empty / constraint-less network selector (matches every network in the namespace) be handled? → A: Reject it at admission; a selector attachment must specify at least one label constraint.

### Validation findings (live e2e on twc-staging, 2026-06-22)

- The non-empty-selector CEL rule (FR-015) exceeded the Kubernetes apiserver's
  **CEL cost budget** (1.7×) because it calls `size()` on the selector's
  unbounded `matchLabels`/`matchExpressions`, multiplied by the unbounded
  `networks` array. Fix: bound the **declared** attachment entries with
  `MaxItems=64` on `networks`. This does NOT cap the resolved set (FR-014) — a
  single selector entry still matches an unbounded number of networks.
  (`crossplane beta validate` does not run the cost estimator and missed this; it
  surfaced only on a live `kubectl apply`.)
- Feature verified end-to-end: a single selector attached both networks at create
  (formulation: create networks → wait Ready → create router), dynamically
  attached a third when labelled (`AttachedNetwork` event), and detached one when
  unlabelled (`DetachedNetwork` event), with the unlabelled Network surviving
  (detach ≠ delete).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A router network attachment MUST support selecting networks by label
  selector, as a third alternative to selecting by reference name or by raw upstream
  id. Exactly one of the three selection modes MUST be used per attachment entry,
  enforced at admission.
- **FR-002**: A selector attachment MUST resolve to **every** network in the
  router's namespace whose labels match the selector and that is provisioned
  (`Ready`, with a known upstream id), producing one attached network per match
  ("to-many" expansion).
- **FR-003**: The router MUST continuously converge its attached set to the current
  selector match set: networks that newly match (created or newly labeled, once
  Ready) MUST be attached, and networks that stop matching (unlabeled or deleted)
  MUST be detached, without any edit to the Router resource.
- **FR-004**: New or changed selector matches MUST be picked up automatically within
  one reconcile interval; the provider MUST observe relevant network changes rather
  than requiring manual re-trigger.
- **FR-005**: The router's effective attachment set MUST be the de-duplicated union
  across all declared attachment entries (selector and explicit); a network reachable
  via more than one entry MUST be attached exactly once.
- **FR-006**: When a network is reachable via both a selector entry and an explicit
  reference/id entry, the explicit entry's per-attachment settings (DHCP, and any
  other per-attachment options) MUST take precedence over the selector entry's
  defaults.
- **FR-007**: A network matching a selector but not yet `Ready` MUST be excluded
  until Ready, and this transient state MUST NOT by itself mark the router failed.
- **FR-008**: The provider MUST NOT create or converge a router with zero *resolved*
  attached networks, even when the declared-entry count satisfies the existing
  "at least one declared attachment" rule. If the declared attachments resolve to
  zero networks, the router MUST be marked not-ready with a clear, actionable reason
  and MUST NOT detach the final network out of an existing healthy router. (The
  underlying upstream constraint — verified in feature 006 as a `400
  router_must_have_at_least_one_network` — MUST be pre-empted as a blocking
  condition, not surfaced as a raw API failure.)
- **FR-009**: A NAT floating-IP MUST NOT be combinable with a selector attachment;
  such a configuration MUST be rejected at admission with a clear message. NAT
  continues to be supported only on explicit single-network (ref/id) attachments.
- **FR-010**: Selector resolution MUST be scoped to the router's own namespace,
  consistent with existing cross-resource reference behavior.
- **FR-011**: The router's status MUST reflect the actual set of currently attached
  networks (including those brought in by selectors), so an operator can see what a
  selector resolved to without inspecting the upstream API.
- **FR-012**: Selector-driven attachment MUST be additive and backward compatible:
  existing routers using only `networkRef`/`networkID` attachments MUST continue to
  behave exactly as before, with no migration required. The existing CRD-level
  "at least one declared attachment" guard remains unchanged.
- **FR-013**: All per-attachment behavior that exists today for explicit entries
  (DHCP toggle; create-only gateway/reserved-IP handling) MUST apply equally to the
  networks brought in by a selector entry, using that entry's declared defaults.
- **FR-014**: The provider MUST pace attach and detach operations rather than
  issuing them in an unbounded burst, to avoid tripping the upstream's burst-based
  ban (Qrator DDoS protection). A large selector match set MAY converge
  incrementally across multiple reconciles; partial convergence MUST be a normal,
  non-error progress state with no upper limit imposed on how many networks a
  selector may ultimately match.
- **FR-015**: A selector attachment MUST specify at least one label constraint; an
  empty or constraint-less selector (which would match every network in the
  namespace) MUST be rejected at admission with a clear message, so a match-all
  attachment cannot be created by accident.
- **FR-016** (added during implementation — observability): Because the selector
  changes the attached set WITHOUT a spec edit, the provider MUST emit a
  user-visible event when it attaches or detaches a network on a live router
  (`AttachedNetwork` / `DetachedNetwork`, naming the network), so operators can
  see selector-driven changes via `kubectl describe` / `kubectl get events`.
- **FR-017** (added during implementation — observability): An operator MUST be
  able to correlate those events to a `Network` resource from `kubectl get`
  output — i.e. the upstream network id (`network-<hex>`, which equals the event
  message and the `Network` external-name) MUST be a default print column on the
  `Network` kind.

### Key Entities *(include if feature involves data)*

- **Router**: The NAT/DHCP router appliance. Carries the list of network attachment
  entries. This feature extends each entry with an optional label-selector selection
  mode and makes the resolved attachment set dynamic.
- **Router network attachment (entry)**: One declared rule for attaching networks.
  Today it names one network (ref or id) plus per-attachment options (DHCP, NAT
  floating-IP, create-only gateway/reserved IPs). This feature adds a label-selector
  mode that expands to many networks; NAT is disallowed in that mode.
- **Network**: The VPC being attached. Referenced, not modified by this feature.
  Its labels are the selector match surface; its Ready state and upstream id gate
  attachment.
- **Floating IP**: The NAT address for an explicit attachment. Unchanged by this
  feature; remains usable only on explicit (non-selector) entries.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can attach an arbitrary, growing set of networks to a
  router using a single labeled selector, without editing the Router resource when
  the set changes.
- **SC-002**: When a single matching network is added (or becomes Ready), the router
  attaches it within one reconcile interval, with zero manual action on the Router.
  When many networks change at once, the router converges the full set without manual
  action and without tripping the upstream burst-ban, even if convergence spans
  several reconciles.
- **SC-003**: Removing the matching label from a network, or deleting it, detaches
  it from the router; a single such change takes effect within one reconcile
  interval, and bulk changes converge incrementally under the same pacing.
- **SC-004**: A router declaring both a selector entry and overlapping explicit
  entries attaches the exact de-duplicated union — verifiable by counting attached
  networks against the distinct set of matched + named networks (no duplicates, none
  missing).
- **SC-005**: A router whose attachments resolve to zero networks never reaches a
  Ready state with zero networks attached; it reports a clear blocking reason, and
  recovers automatically once at least one matching network becomes Ready.
- **SC-006**: 100% of existing router manifests that use only reference/id
  attachments behave identically after this feature ships (no regression, no
  migration).
- **SC-007**: An operator can determine the full set of networks a selector
  resolved to by reading the Router's status alone.

## Assumptions

- **Selector cardinality is to-many** (confirmed with the requester): one selector
  entry attaches every matching network, rather than resolving to a single network.
  Multi-network attachment is therefore expressed both by listing multiple entries
  and by a single selector that matches many networks.
- **The ≥1-network rule is real and upstream-verified, but operates at two levels.**
  Feature 006's live probe confirmed the Timeweb API rejects a zero-network router
  with `400 router_must_have_at_least_one_network`, which is why the declared-entry
  list carries an "at least one declared attachment" admission guard. That guard is
  necessary but *not sufficient* once selectors exist: a single selector entry
  satisfies it yet may resolve to zero networks. This feature therefore adds a
  separate runtime guard on the *resolved* count (FR-008). The declared-entry guard
  itself is unchanged.
- **NAT stays explicit**: per-network NAT requires a single, stable network→IP
  mapping, which a changing selector match set cannot provide; NAT-on-selector is
  rejected rather than guessed. Operators needing NAT for specific networks use
  explicit ref/id entries (the existing path).
- **Floating-IP label-selectors are out of scope** for this feature. The NAT
  floating-IP continues to be chosen by reference or raw address on explicit
  entries; adding a label-selector for floating IPs (and the N-networks↔M-IPs
  pairing question it raises) is deferred.
- **Namespace-scoped resolution**: selectors match only networks in the router's
  namespace, matching the provider's existing reference-resolution behavior; no
  cross-namespace selection.
- **Readiness gating reuses existing semantics**: a network is eligible for
  attachment only when it is `Ready` with a known upstream id — the same gate the
  current `networkRef` resolver applies.
- **Convergence is observe-driven**: the router already re-observes and converges
  its attachment set every reconcile; selector matches are recomputed on the same
  cadence, and network changes trigger reconciliation.
