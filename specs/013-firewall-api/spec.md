# Feature Specification: Firewall — declarative Timeweb Cloud firewall rule groups

**Feature Branch**: `013-firewall-api`

**Created**: 2026-06-28

**Status**: Draft

**Input**: User description: "build firewall api" (with dashboard screenshots of the
Timeweb **Управление сетями → Firewall** surface: rule groups, per-group inbound/outbound
rules, and a connected-services tab).

## Clarifications

### Session 2026-06-28

- Q: Attachment direction & target model — which resource owns the firewall↔service link,
  and what are the attachable targets? → A: **Firewall-centric, single-writer**, using
  **opaque service references** (`{id, type}`) rather than typed cross-MR references. v1
  targets **load balancers** (this environment runs Kubernetes load balancers, not cloud
  servers; the dashboard's service picker lists only `k8s-lb_*` load balancers). The upstream
  enforces **1:1 exclusivity** — a service belongs to at most one rule group ("привязаны к
  другой группе правил"). Typed convenience refs (e.g. a `serverRef`) and additional target
  types are deferred until the attachable-service catalog is probe-verified during planning.
- Q: Should v1 also support non-inline rules (a separate `FirewallRule` managed resource that
  references a `Firewall`)? → A: **No — inline only for v1.** Rules live in
  `Firewall.spec.rules[]`, single-writer. A standalone `FirewallRule` kind (AWS
  `SecurityGroupRule`-style — feasible because Timeweb rules are individually addressable) is a
  **deferred, purely-additive** future option for the multi-team / shared-group case, to be
  introduced behind a `Firewall` opt-out mode so the inline model is unchanged. Keeping rules
  inline preserves the single-writer guarantee that avoids multi-writer contention on one group.

## User Scenarios & Testing *(mandatory)*

A platform operator manages Timeweb infrastructure declaratively (GitOps/Crossplane) and
wants the cloud firewall to be part of that declared state instead of hand-clicked in the
dashboard. A **firewall rule group** is an allow-list: it holds a set of rules; any traffic
not matched by a rule is blocked. A group is attached to one or more **services** (e.g.
servers) for its rules to take effect.

### User Story 1 - Declare a firewall rule group (Priority: P1) 🎯 MVP

An operator declares a firewall rule group with a name, an optional comment, and a set of
**inbound** allow rules — each rule naming a source address (a CIDR, or "all addresses"), a
protocol, and a port or port range. The system provisions the group and its rules upstream
and reports it as ready, mirroring the upstream identity and the effective rule set.

**Why this priority**: The group + its rules is the core unit of value and the smallest
shippable slice; everything else (attaching to services, outbound rules, day-2 edits) builds
on a group existing. Even with nothing attached yet, the operator gets a versioned,
reviewable firewall definition.

**Independent Test**: Apply a firewall group with several inbound TCP rules (e.g. allow
`10.10.0.0/22:3306`, `100.64.0.0/10:22`, `all:443`). It reaches Synced + Ready; the dashboard
shows the group with exactly those rules and "remaining traffic blocked"; status reflects the
upstream group id and rule count.

**Acceptance Scenarios**:

1. **Given** no firewall group exists, **When** the operator declares one with a name and
   three inbound rules, **Then** the group is created upstream with exactly those three rules
   and the resource becomes Synced + Ready.
2. **Given** a declared group, **When** the operator re-applies the identical definition,
   **Then** no upstream change is made (idempotent) and the resource stays Ready.
3. **Given** a declared group, **When** a rule is changed out-of-band in the dashboard,
   **Then** the system detects the drift and converges the upstream rules back to the
   declared set.

---

### User Story 2 - Attach a firewall to a load balancer (Priority: P2)

An operator attaches a firewall group to one or more services — load balancers in v1,
identified by an opaque service reference (`{id, type}`) — so the group's rules actually filter
that service's traffic: allowed traffic reaches it, everything else is blocked. Detaching
removes the filtering for that service.

**Why this priority**: Rules only take effect once attached; this turns a declared group into
enforced network policy (e.g. locking down a Kubernetes load balancer's public IP to known
source CIDRs). It depends on US1 (a group must exist) but is independently testable.

**Independent Test**: With a ready group, the operator attaches a load balancer by its service
reference. The load balancer appears under the group's connected services; a connection to an
allowed port succeeds and a connection to a non-allowed port is refused. Removing the
attachment restores the prior behavior.

**Acceptance Scenarios**:

1. **Given** a ready group and a load-balancer service reference, **When** the operator attaches
   it, **Then** the service is listed among the group's connected services and the group's rules
   govern its traffic.
2. **Given** a service attached to a group, **When** the operator removes the attachment,
   **Then** the service is detached and the group no longer governs its traffic.
3. **Given** a service that is already attached to a different rule group (1:1 exclusivity),
   **When** the operator attaches it here, **Then** the resource reports a clear conflict
   condition and does not silently steal the service from the other group.

---

### User Story 3 - Outbound rules alongside inbound (Priority: P2)

An operator declares **outbound** rules in addition to inbound, controlling which destinations
the attached services may reach.

**Why this priority**: Egress control is a common requirement (e.g. restrict outbound to a
known subnet) and is a small additive increment over US1, but inbound is the more common
first need, so it ranks just below the MVP.

**Independent Test**: Declare a group with both inbound and outbound rules; both sets are
present upstream and enforced once attached (allowed egress destination reachable, others
blocked).

**Acceptance Scenarios**:

1. **Given** a group, **When** the operator adds an outbound rule, **Then** the rule is created
   upstream in the outbound direction and the inbound rules are unaffected.
2. **Given** a group with inbound and outbound rules, **When** observed, **Then** each rule's
   direction is reported correctly.

---

### User Story 4 - Day-2 lifecycle: edit, detach, delete (Priority: P3)

An operator changes the rule set (add / modify / remove a rule), updates the comment, detaches
a service, and eventually deletes the group — all without unexpected disruption.

**Why this priority**: Lifecycle correctness matters for production use but is exercised after
the create/attach paths work; it can be validated last.

**Independent Test**: Change a rule's port → the upstream rule updates in place without the
group being recreated (its identity and other attachments are preserved). Delete the group →
its rules are removed, all services are detached, and nothing is left orphaned.

**Acceptance Scenarios**:

1. **Given** a group with attachments, **When** a single rule's port is changed, **Then** the
   upstream rule set converges to the new definition while the group identity and existing
   service attachments are unchanged.
2. **Given** a group attached to services, **When** the operator deletes it, **Then** the
   services are detached and the group plus its rules are removed; a subsequent observation
   shows it gone.
3. **Given** a group that no longer exists upstream, **When** the operator deletes the
   resource, **Then** deletion succeeds (already-gone is treated as success).

---

### Edge Cases

- **All-addresses rule**: a rule whose source/destination is "all addresses" (`0.0.0.0/0`) is
  valid and must round-trip correctly.
- **Port range vs single port**: both a single port and a contiguous port range must be
  expressible and validated (start ≤ end, within the valid port space).
- **Protocol without ports**: for protocols that do not use ports (e.g. ICMP), a port value is
  not applicable and must be omitted/ignored rather than rejected inconsistently.
- **Duplicate rules**: two rules identical in direction + address + protocol + port are
  redundant; the system treats this as an invalid/duplicate configuration rather than silently
  creating both.
- **Empty rule set**: a group with no rules is an allow-list that blocks everything for its
  attached services — permitted, but the effect (no traffic) should be obvious to the operator.
- **Delete while attached**: deleting a group that is still attached to services must detach
  first, then remove — never leave a dangling attachment.
- **Attachment to a missing/not-ready service**: gated and retried, not a permanent failure.
- **Rule-count / group-count limits**: upstream may cap the number of rules per group (and
  groups per account); exceeding a cap surfaces a clear, terminal validation error.
- **Out-of-band edits**: rules or attachments changed in the dashboard converge back to the
  declared state on the next reconcile.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Operators MUST be able to declare a firewall rule group with a name and an
  optional human-readable comment.
- **FR-002**: A group MUST behave as an allow-list — traffic not matched by any rule is blocked
  ("remaining traffic blocked").
- **FR-003**: Operators MUST be able to declare **inbound** rules, each specifying a source
  address (a CIDR or "all addresses"), a protocol, and a single port or a contiguous port
  range, plus an optional per-rule comment.
- **FR-004**: Operators MUST be able to declare **outbound** rules with the same shape as
  inbound rules (destination address, protocol, port/range, comment).
- **FR-005**: The system MUST provision the group and all of its rules upstream and report the
  resource as Synced + Ready only when the upstream rule set matches the declared set.
- **FR-006**: The system MUST mirror, in observable status, the upstream group identity, the
  effective rule count, and the list of attached services.
- **FR-007**: Operators MUST be able to attach the group to one or more services, identified by
  an opaque service reference (`{id, type}`; v1 supports the `loadbalancer` type), so the
  group's rules govern those services' traffic. The firewall group is the single writer of its
  attachment set.
- **FR-008**: Operators MUST be able to detach a previously attached service.
- **FR-009**: The system MUST honor the upstream 1:1 exclusivity — a service belongs to at most
  one rule group. Attaching a service that is already bound to a different group MUST surface a
  clear conflict condition and MUST NOT silently move the service.
- **FR-010a**: A referenced service that does not (yet) exist upstream, or a transient attach
  failure, MUST yield a clear not-ready reason and be retried (not a permanent failure); a
  service id that is invalid/unknown MUST surface a terminal error.
- **FR-010**: Operators MUST be able to change the rule set in place (add, modify, remove
  rules) without the group identity being recreated and without disturbing existing
  attachments.
- **FR-011**: The system MUST detect and converge drift — if rules or attachments are changed
  out-of-band, the next reconcile restores the declared state.
- **FR-012**: The system MUST validate rule inputs — well-formed address/CIDR, port within the
  valid range, start ≤ end for ranges, and port omitted for portless protocols — rejecting
  invalid input with an actionable message.
- **FR-013**: The system MUST treat duplicate rules (identical direction + address + protocol +
  port) as an invalid configuration rather than creating redundant upstream rules.
- **FR-014**: On deletion, the system MUST detach all services and remove the group and its
  rules; an already-removed group MUST be treated as a successful deletion.
- **FR-015**: The system MUST surface transient upstream conditions (rate limiting, temporary
  errors) as retryable without flapping the resource's readiness, and terminal errors
  (validation, quota) as a clear failed state.

### Key Entities *(include if feature involves data)*

- **Firewall rule group**: the unit an operator declares. Attributes: name, optional comment,
  policy behavior (allow-list / default-deny), the set of rules it contains, and the set of
  services it is attached to. Identified upstream by a stable id mirrored into status.
- **Rule**: one entry in a group. Attributes: direction (inbound / outbound), source-or-
  destination address (CIDR or "all"), protocol, port or port range, optional comment. Belongs
  to exactly one group.
- **Service attachment**: a link from a group to a Timeweb service, identified by an opaque
  reference (`{id, type}`; `loadbalancer` in v1), that the group's rules apply to. Exclusive —
  a given service is attached to at most one group. Mirrored in status as the set of services a
  group currently governs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can declare a firewall group with multiple inbound rules and have it
  become active (created upstream, reported ready) within 60 seconds of applying it.
- **SC-002**: The set of rules enforced upstream exactly matches the declared set — no extra
  permitted ports and no missing rules — verified by inspecting the group after reconciliation.
- **SC-003**: After attaching a group to a load balancer, traffic to an allowed port reaches the
  load balancer and traffic to a non-allowed port is blocked, with no manual dashboard steps.
- **SC-004**: A day-2 change to a single rule takes effect without recreating the group or
  dropping existing service attachments (no connectivity flap for unrelated rules).
- **SC-005**: Deleting a group detaches every service and removes all of its rules, leaving no
  orphaned group, rule, or attachment upstream.
- **SC-006**: Attaching a service that is already bound to another group is reported as a clear
  conflict (never a silent move); a transiently-unavailable service attaches automatically once
  it becomes available, without permanent failure.

## Assumptions

- **Firewall-centric attachment model**: the group declares which services it is attached to
  (matching the dashboard, where a group lists its connected services), consistent with this
  provider's existing owning-resource idiom (e.g. the Router declares its network attachments).
  The firewall group is the single writer of the attachment; services do not separately
  reference the firewall.
- **Opaque service references**: attachments are expressed as `{id, type}` Timeweb service
  identifiers, not typed cross-MR references — the v1 targets (load balancers) are not modeled as
  managed resources in this provider, so there is no MR to reference. Typed convenience refs may
  be added later for kinds the provider does manage.
- **Service scope (v1) = load balancers**: the attachable target is the `loadbalancer` service
  type (the environment runs Kubernetes load balancers, not cloud servers). The full attachable
  catalog (whether servers or other types are also eligible) is probe-verified during planning;
  additional types are additive later.
- **Exclusive attachment**: a service belongs to at most one rule group at a time (upstream 1:1
  constraint). Re-attaching a service bound elsewhere is a reported conflict, not a takeover.
- **Inline rules, single writer**: a group's rules and attachments are part of that group's
  declared desired state (one resource is the sole writer), mirroring the Router/attachment and
  S3User/grant patterns already in the provider — not separate per-rule resources.
- **Allow-list policy**: groups are allow-lists with default-deny ("Разрешающий" / remaining
  traffic blocked). A deny-list policy type is out of scope for v1 unless the upstream API
  requires modeling it explicitly.
- **Protocols** are limited to those the Timeweb firewall supports (at minimum TCP/UDP, and
  ICMP without ports); the exact protocol set and value spellings are to be probe-verified
  during planning.
- **Namespaced declarative resource**: the feature is exposed as a namespaced managed resource
  in the provider's network service group, consistent with Network / FloatingIP / Router. The
  exact kind name and API group are a design detail resolved in planning.
- **Upstream endpoints and quirks are probe-verified at plan time** (the provider's Phase-0
  research convention), since the firewall API is not part of the official published spec.
- **Credentials and access** reuse the existing provider configuration and account token; no new
  credential surface is introduced.

## Out of Scope (v1)

- Attaching to service types beyond `loadbalancer` (e.g. cloud servers, databases) until the
  attachable-service catalog is probe-confirmed — additive when added.
- Typed cross-MR attachment references (e.g. a `serverRef`); v1 uses opaque `{id, type}` only.
- A standalone non-inline `FirewallRule` managed resource (AWS `SecurityGroupRule`-style). Rules
  are inline (`Firewall.spec.rules[]`, single-writer) in v1; a separate per-rule kind is a
  deferred, additive future option for multi-team shared groups, gated behind a `Firewall`
  opt-out mode.
- Deny-list / explicitly-blocking rule semantics beyond the default-deny allow-list.
- Importing/adopting pre-existing dashboard-created groups beyond standard
  observe-and-reconcile (no special migration tooling).
- Predefined "service" templates (the dashboard's connectable service catalog) — only explicit
  address/protocol/port rules are modeled in v1.
