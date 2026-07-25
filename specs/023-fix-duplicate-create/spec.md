# Feature Specification: Duplicate-create defenses (identity stomp + lost result)

**Feature Branch**: `023-fix-duplicate-create`

**Created**: 2026-07-25

**Status**: Draft (root cause CONFIRMED — see Incident)

**Input**: User description: "fix duplicate create after retry bug" — production incident 2026-07-25: three identical `workers-xl` node groups (119639/119641/119643) on cluster 1101703 from ONE declared KubernetesClusterNodepool.

## Incident — confirmed root cause (owner verdict, 2026-07-25)

**GitOps-side root cause, provider behaved per contract.** The ArgoCD
Application carried `crossplane.io/external-name` pinned in git (ids
119631/119633, commit 652823e) — stale after the cluster was recreated. The
loop: Argo selfHeal forces the old id → Observe decodes a VALID id whose
group 404s (canonical envelope — genuinely absent) → `ResourceExists: false`
→ Create mints a new group and writes the new external-name → Argo stomps it
back to the stale pin → repeat. Group timestamps :42/:43/:46 = sync cadence;
the `external-create-failed: 17:41:46` marker was the initial
`router_required` 400 before router integration landed. Git managed an
annotation only the provider may own.

Two provider-side findings survive the confirmation (defense-in-depth —
the root is ours, but the provider defends cheaply):

1. **Identity-stomp defense (would have cut the loop at the second sync)**:
   when the external identity points at a missing resource while the
   resource's own status remembers a DIFFERENT live identity, blind
   re-creation is wrong — that contradiction is surfaced, not "fixed" by
   minting a duplicate.
2. **Lost-result adoption gap**: the nodepool create path has no
   error-yet-created / ambiguous-create adoption guard — the same guard the
   cluster and router controllers gained in feature 006. Constitution II:
   "re-invocation MUST NOT create duplicates".

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A stomped/stale external identity never mints duplicates (Priority: P1)

A GitOps tool (or a human) reverts/pins the external-name to an id whose
resource no longer exists, while the MR's status still remembers the group
the provider actually created (a different id). Instead of treating the 404
as "create a new one", the provider recognizes the contradiction and surfaces
a typed condition naming both ids and the two remedies: restore the
external-name to the remembered id (and stop managing the annotation in
git), or clear the status memory to deliberately force a net-new create.
Nothing is created while the contradiction stands.

**Why this priority**: this is the confirmed incident loop — each sync minted
a 3-node billable group. The defense turns an unbounded duplication loop into
one clear condition.

**Independent Test**: fake-client Observe with external-name=A (404 upstream),
status remembering id B → no create, condition names A and B; external-name
restored to B → converges normally.

**Acceptance Scenarios**:

1. **Given** external-name pointing at a 404-ing id and status remembering a
   different group id, **When** reconciled, **Then** no create is issued and
   the condition names both ids and both remedies.
2. **Given** external-name and the status memory pointing at the SAME id that
   404s (genuine out-of-band deletion), **When** reconciled, **Then** the
   normal recreate path runs unchanged — deletion recovery must not regress.
3. **Given** the blocked contradiction, **When** the external-name is
   restored to the remembered id, **Then** the resource converges with no
   further intervention.
4. **Given** a fresh MR (no status memory), **When** reconciled, **Then**
   behavior is unchanged.

---

### User Story 2 - A lost create result never duplicates a nodepool (Priority: P1)

One declared nodepool; the create attempt's result is lost (client-side
timeout while upstream completes, provider pod replaced mid-create,
error-yet-created response). On retry the provider first checks the parent
cluster's groups for one matching its full declared identity: exactly one ⇒
adopt; zero ⇒ create; several ⇒ typed condition demanding explicit adoption
(never guess — group names are NOT unique, the incident proved it). Clean
first creates are unchanged (no extra reads).

**Why this priority**: the audited gap behind 006's cluster/router incidents,
still open on the nodepool; any Qrator slow-path or restart can trigger it
independently of GitOps hygiene.

**Acceptance Scenarios**:

1. **Given** the runtime's ambiguous-create markers and one upstream group
   matching the full declared identity, **When** reconciled, **Then** the
   group is adopted, no create issued.
2. **Given** the markers and no matching group, **Then** exactly one create.
3. **Given** the markers and several matching groups, **Then** a condition
   lists the candidate ids and the operator actions; nothing created or
   deleted.
4. **Given** no markers (clean create), **Then** today's behavior,
   zero additional API calls.

---

### User Story 3 - Every kind audited: guard or justification (Priority: P2)

Each managed kind ships either the applicable guards (identity-stomp defense
where status memory exists; ambiguous-create adoption where a listable
identity exists) or a recorded justification why duplication cannot occur.
The audit lands in the feature artifacts; kinds with live risk and a listable
surface get the guard now.

**Acceptance Scenarios**:

1. **Given** the audit table, **Then** 100% of kinds carry guard/justification;
   every guard row has unit coverage.

---

### User Story 4 - Operators know the rules and the exits (Priority: P3)

Documentation gains: (a) a GitOps-hygiene section — `crossplane.io/external-name`
is provider-owned; never render it from git; ArgoCD users add the
`ignoreDifferences` stanza (example provided) so selfHeal cannot stomp it;
(b) a runbook for the runtime's "cannot determine creation result" wedge
(verify upstream, adopt-or-clear, post-fix duplication safety); (c) the two
new condition reasons in the conditions reference.

**Acceptance Scenarios**:

1. **Given** the docs, **Then** the external-name ownership rule, the ArgoCD
   `ignoreDifferences` example, the wedge runbook, and both conditions are
   findable and accurate.

---

### Edge Cases

- The stomp-defense discriminator is the RECORDED upstream identity in
  status: same-id 404 ⇒ legitimate recreate; different-id 404 ⇒ contradiction
  condition. If the nodepool observation does not currently record its own
  group id, recording it is part of this feature (status-only addition —
  version implications assessed at plan time against the repo's
  patch="no CRD change" convention).
- Status memory can be stale in the OTHER direction (status remembers an id
  that was legitimately deleted out-of-band while external-name was already
  cleared for net-new): clearing status is the documented deliberate escape
  hatch, and the condition text names it.
- The adoption guard's identity match uses the full declaration (parent
  cluster + name + sizing where readable); multi-match ALWAYS refuses.
- Adopted groups flow through the existing per-node readiness gates — a
  broken adopted group shows its true state (no blind-adopt-of-failed
  regression, the recorded 014 concern).
- Both guards are per-kind local checks with zero cost on healthy paths:
  stomp defense fires only on the 404+mismatch contradiction; adoption guard
  only under ambiguity markers.
- Remediating the incident's orphan groups (119639/119641) and fixing the
  Application manifest are operations tasks, already with the prod-side
  agent/owner; the provider never auto-deletes what it did not record.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When the external identity resolves to a canonical not-found
  AND the resource's status records a DIFFERENT upstream identity, the
  provider MUST NOT create; it MUST surface a typed condition naming both
  identities and the remedies (restore external-name / clear status memory
  deliberately). Same-identity not-found keeps today's recreate behavior.
- **FR-002**: The nodepool MUST record its upstream group identity in status
  (the discriminator for FR-001), if not already recorded.
- **FR-003**: Under the runtime's ambiguous-create markers, the nodepool
  create path MUST list-and-match before POSTing: one full-identity match ⇒
  adopt; zero ⇒ create; several ⇒ typed refuse-to-guess condition naming the
  candidates. Clean creates unchanged (zero extra reads).
- **FR-004**: All managed kinds MUST be audited for both defenses; each kind
  ends the feature with exactly one of: guards shipped (the incident kind at
  minimum), a written can't-duplicate justification, or an explicitly
  SCHEDULED follow-up with rationale (patch scope stays bounded; the audit
  table is the artifact).
- **FR-005**: Docs MUST gain the GitOps external-name ownership rule with an
  ArgoCD `ignoreDifferences` example, the create-wedge runbook, and the new
  condition reasons.
- **FR-006**: Unit tests per Constitution III for every guard path
  (stomp: mismatch-blocks / same-id-recreates / restore-converges;
  adoption: adopt-one / create-on-zero / refuse-on-many / clean-unchanged).
- **FR-007**: NON-BREAKING. Version per repo convention resolved at plan
  time: pure behavior+conditions ⇒ patch; if FR-002 requires a new status
  field ⇒ the established additive-fields precedent applies (release-
  maintainer consult recorded in plan).

### Key Entities

- **KubernetesClusterNodepool**: both guards; status upstream-identity record
  (FR-002) if absent today.
- **Ambiguity markers**: the runtime's create-incomplete/create-failed
  annotations — sole trigger of the adoption guard.
- **Contradiction condition**: the stomp-defense signal (external identity vs
  status memory).
- **Audit table**: per-kind defenses/justification record.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Replaying the confirmed incident (stale pinned external-name +
  selfHeal stomp) against the fixed provider produces ZERO new groups and one
  clear condition within one sync cycle — verified by unit simulation and a
  live staging replay (pin a stale id, watch the condition, restore, watch
  convergence).
- **SC-002**: Replaying the lost-result shape (result dropped N times) yields
  exactly one group for one declaration.
- **SC-003**: Genuine out-of-band group deletion still recreates within one
  poll cycle (no recovery regression).
- **SC-004**: Healthy-path API call counts unchanged (fake call counting).
- **SC-005**: Audit covers 100% of kinds; all gates green at release.

## Assumptions

- The prod Application manifest fix (drop the pinned annotation) and orphan
  cleanup are handled operationally — this feature is the provider defense
  plus documentation so the class of mistake is caught, named, and bounded.
- The parent cluster's group list is a sufficient adoption surface (id, name,
  sizing per group).
- Version target: v0.11.1 if plan confirms status already records the group
  id (pure patch); otherwise the additive-status precedent decides.
