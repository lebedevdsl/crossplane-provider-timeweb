# Feature Specification: Nodepool Taints

**Feature Branch**: `015-nodepool-taints`

**Created**: 2026-07-10

**Status**: Draft

**Input**: User description: "prepare extension of nodepool object, taints"

## Clarifications

### Session 2026-07-10

- Q: If the live probe found no upstream taint support, should v1 fall back
  to dataplane tainting via the cluster kubeconfig? → A: Moot — operator
  live-probed node-group create with `taints` and upstream accepted and
  echoed them (group 117093, `biba=boba:NoSchedule`); management-plane path
  confirmed, no fallback needed.
- Q: Should taint drift be detected, and are taints day-2 mutable? → A: Yes
  to both, at the management plane: the node-group GET returns `taints`, so
  the provider diffs declared vs upstream-reported taints on every
  observation, reverts out-of-band upstream edits (single-writer), and
  reconciles operator edits in place — no group replacement, no create-time
  immutability. Direct node-object edits (`kubectl taint` on a node) that
  don't alter the group's API-reported taints remain out of scope.
- Q: Is the day-2 update surface real, and what shape? → A: Probe-verified —
  `PATCH https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups/117093` →
  200. The panel sends a full-state body (`name`, `labels`, `taints`,
  `public_ip_enabled`, autoscaler/autohealing flags) — set-replace semantics,
  not deltas — and the response echoes the updated `labels` and the `taints`
  carried in the body.
- Q: Scope of mutability? → A: Node **labels are in scope too**: they get the
  same day-2 mutability and drift reconciliation as taints, lifting their
  previous create-only contract (label shape/declaration unchanged).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Provision a dedicated worker group with taints (Priority: P1)

A platform operator declares a worker group (nodepool) that is reserved for a
specific class of workload — e.g. an ingress pool, a batch pool, or a
database pool. They add one or more Kubernetes taints to the nodepool
declaration so that, from the moment each node joins the cluster, no workload
without a matching toleration can be scheduled onto it. The operator never
has to run an imperative post-provisioning step (`kubectl taint ...`) and
never has a window where an untainted node accepts stray pods.

**Why this priority**: This is the core value of the feature. Without
declarative taints, dedicated pools require manual tainting after every
provision, and the taint is applied *after* the node is already schedulable —
stray workloads can land on the node first and defeat the isolation. This is
the only way to make dedicated pools reliable.

**Independent Test**: Declare a nodepool with a taint, wait for it to become
Ready, then (a) verify every node of the group carries the taint, and
(b) deploy a pod without a toleration and confirm it is never scheduled onto
the group's nodes, while a pod with the toleration is.

**Acceptance Scenarios**:

1. **Given** a Ready parent cluster, **When** the operator creates a nodepool
   declaring taints (each with a key, an optional value, and an effect),
   **Then** the group is provisioned and every node joins the cluster already
   carrying exactly the declared taints.
2. **Given** a nodepool declared with taint `dedicated=ingress:NoSchedule`,
   **When** a pod without a matching toleration is created, **Then** the pod
   is never scheduled onto any node of that group.
3. **Given** a nodepool declared with taints, **When** the operator inspects
   the nodepool resource, **Then** the declared taints are visible on the
   resource without needing cluster credentials.

---

### User Story 2 - Taints survive node lifecycle events (Priority: P2)

An operator scales a tainted nodepool up (manually or via the autoscaler), or
autohealing replaces a failed node. Every node added to the group after the
initial provisioning carries the same declared taints — the isolation
guarantee holds for the life of the group, not just at creation.

**Why this priority**: A taint that only applies to the initial nodes is a
silent correctness hole: the first scale-up event produces an untainted node
and breaks the dedicated-pool guarantee without any visible error. The
feature is not trustworthy without this.

**Independent Test**: Create a tainted nodepool with N nodes, increase the
node count, and verify the newly added node(s) carry the same taints as the
original ones.

**Acceptance Scenarios**:

1. **Given** a Ready tainted nodepool, **When** the operator raises the node
   count, **Then** each newly added node joins carrying the declared taints.
2. **Given** a tainted nodepool with autoscaling enabled, **When** the
   autoscaler adds a node, **Then** the new node carries the declared taints.

---

### User Story 3 - Day-2 taint and label updates with drift correction (Priority: P2)

An operator changes the scheduling metadata of a live pool — adds a taint to
start draining general workloads off it, removes one to open it up, fixes a
taint value, or edits the pool's node labels. The edit converges to the
worker group in place: no group replacement, no manual API calls.
Conversely, if someone edits the group's taints or labels out-of-band (panel
or direct API call), the provider notices on its next observation and
restores the declared sets.

**Why this priority**: Taint and label policy evolves over a pool's
lifetime; forcing group replacement for a metadata edit would destroy and
recreate every node. Drift reversion is the same single-writer guarantee
every other kind in this provider gives.

**Independent Test**: On a Ready tainted nodepool, edit the declared taints
and labels and verify the group's upstream-reported sets converge to the new
declaration without the group being recreated; then edit the group's taints
or labels out-of-band and verify the provider restores the declared sets.

**Acceptance Scenarios**:

1. **Given** a Ready nodepool with taint set T1, **When** the operator edits
   the declaration to taint set T2, **Then** the upstream group reports
   exactly T2 without the group being replaced, and the nodepool reports
   in-sync only once it does.
2. **Given** a Ready nodepool with label set L1, **When** the operator edits
   the declaration to label set L2, **Then** the upstream group reports
   exactly L2 without the group being replaced.
3. **Given** a Ready tainted nodepool, **When** the group's taints or labels
   are changed out-of-band via panel or API, **Then** the provider reverts
   the group to the declared sets on a subsequent reconcile.

---

### User Story 4 - Invalid taint declarations are rejected upfront (Priority: P3)

An operator makes a mistake in a taint declaration — a misspelled effect, a
malformed key, or a duplicate entry. The declaration is rejected at
submission time with a message naming the offending entry, before any cloud
resource is created or billed.

**Why this priority**: Admission-time rejection is a usability and
cost-safety layer on top of US1/US2. Without it, a typo surfaces only as an
upstream provisioning failure minutes later (or worse, is silently dropped),
but the core feature still functions for correct input.

**Independent Test**: Submit nodepool manifests with an unknown effect, an
invalid key, and a duplicated key+effect pair; verify each is rejected at
apply time with a specific error, and that no upstream group was created.

**Acceptance Scenarios**:

1. **Given** a nodepool manifest with taint effect `NoScheduleTypo`, **When**
   the operator applies it, **Then** the apply is rejected immediately with a
   message listing the allowed effects.
2. **Given** a manifest declaring the same key+effect pair twice, **When**
   applied, **Then** it is rejected as a duplicate.
3. **Given** a rejected manifest, **When** the operator checks the cloud
   account, **Then** no worker group was created.

---

### Edge Cases

- **Upstream accepts but does not honor taints on nodes**: the create surface
  is confirmed (see Assumptions), but if nodes were ever to come up untainted
  despite an accepted-and-echoed request (silent no-op — a known behavior
  pattern of this platform), the nodepool must NOT report Ready-and-in-sync
  as if the taints were applied; node-level application is verified in the
  acceptance run.
- **Taint/label changes on an existing group**: the operator edits taints or
  labels on an already-provisioned nodepool; the change is reconciled to the
  group in place (update surface probe-verified — see Assumptions). Whether
  the platform propagates a group-level change to *already-running* nodes
  (vs only nodes joining afterwards) is upstream behavior that MUST be
  verified live during planning.
- **Out-of-band taint/label edit upstream**: someone changes the group's
  taints or labels via the panel or API directly; the provider detects the
  drift on observation and reverts the group to the declared sets.
- **Clearing all taints or labels**: the operator removes the last taint or
  label from the declaration; the group must converge to an empty set (the
  update carries the full replacement list, so an empty list must be honored
  — verify live that it clears rather than being ignored).
- **Same key, different effects**: Kubernetes permits the same key with
  different effects (e.g. `NoSchedule` + `NoExecute`); the declaration must
  accept this while rejecting exact key+effect duplicates.
- **Value-less taints**: a taint with a key and effect but no value is valid
  in Kubernetes and must be accepted.
- **Existing nodepools**: resources created before this feature have no
  taints field; they must continue to reconcile without spurious updates or
  recreation (no default taints). Pools with declared labels gain drift
  correction for them — the intended consequence of lifting the create-only
  contract, not a regression.
- **Taints + autoscaling from minimum**: the platform's autoscaler minimum is
  2 nodes, so scale-from-zero taint-awareness is not a concern in v1.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The nodepool declaration MUST accept an optional list of
  Kubernetes node taints, each consisting of a key (required), a value
  (optional), and an effect (required).
- **FR-002**: The effect MUST be restricted to the three Kubernetes taint
  effects — `NoSchedule`, `PreferNoSchedule`, `NoExecute` — and any other
  value MUST be rejected at admission.
- **FR-003**: Taint keys and values MUST be validated at admission against
  Kubernetes label-syntax conventions (key: optional DNS-subdomain prefix +
  name segment; value: label-value syntax), so malformed entries never reach
  the provisioning platform.
- **FR-004**: Declaring the same key+effect pair more than once MUST be
  rejected at admission; the same key with different effects MUST be
  accepted.
- **FR-005**: Declared taints MUST be applied to the worker group at
  provisioning time such that every node joins the cluster already carrying
  them (no post-join tainting window).
- **FR-006**: Every node added to the group after initial provisioning —
  manual scale-up, autoscaler scale-up, or autohealing replacement — MUST
  carry the declared taints.
- **FR-007**: Taints AND node labels MUST be mutable day-2: edits to the
  declared taints or labels (add, remove, modify) on an existing nodepool
  MUST be reconciled to the worker group in place — no group replacement, no
  node recreation initiated by the provider. This lifts the labels field's
  previous create-only contract; the label declaration shape is unchanged.
- **FR-008**: The taints field MUST be optional with no default; existing
  nodepool declarations MUST remain valid with no schema migration and no
  spurious updates or recreation. The only behavior change for pre-existing
  resources is the intended one: declared labels are now drift-corrected
  (FR-013) instead of write-once.
- **FR-009**: The declared taints MUST be visible on the nodepool resource
  itself, so an operator can audit a group's taint policy without cluster
  credentials.
- **FR-010**: If the provisioning platform cannot honor taints for a request
  it otherwise accepts, the nodepool MUST NOT be reported as fully in sync;
  the discrepancy must be surfaced to the operator rather than silently
  ignored.
- **FR-011**: The taints list MUST be bounded to a sensible maximum number of
  entries (small — taints per pool are few in practice) so declarations
  remain validatable within platform admission limits.
- **FR-012**: Operator-facing documentation and examples MUST cover declaring
  a tainted dedicated pool, including the matching workload toleration.
- **FR-013**: The provider MUST detect drift between the declared taints and
  labels and the group's upstream-reported taints and labels on every
  observation, and MUST revert out-of-band upstream changes to the declared
  state (the nodepool declaration is the single writer of the group's taint
  and label sets).
- **FR-014**: The nodepool MUST NOT report in-sync after a taint or label
  edit until the upstream group reports the declared sets.

### Key Entities

- **Worker group (nodepool)**: existing entity — one homogeneous group of
  worker nodes belonging to a parent managed cluster. Gains an optional
  **taints** attribute; its existing **labels** attribute keeps its shape but
  becomes day-2 reconciled instead of create-only.
- **Taint**: one scheduling-repulsion rule applied to every node of the
  group: `key` (required, Kubernetes label-key syntax), `value` (optional,
  label-value syntax), `effect` (one of `NoSchedule`, `PreferNoSchedule`,
  `NoExecute`). Identity within a group is the key+effect pair.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can provision a dedicated (tainted) worker pool
  with a single declarative manifest and zero imperative post-provisioning
  steps; 100% of the group's nodes carry the declared taints from the moment
  they are schedulable.
- **SC-002**: 100% of nodes added by scale-up, autoscaling, or autohealing
  carry the declared taints with no operator action.
- **SC-003**: 100% of malformed taint declarations (bad effect, bad key
  syntax, duplicate key+effect) are rejected at submission time with a
  message identifying the problem, and zero cloud resources are created for
  rejected declarations.
- **SC-004**: Zero behavior change for existing nodepool declarations:
  resources created before the feature reconcile with no new drift, updates,
  or recreation.
- **SC-005**: A pod without a matching toleration is never observed running
  on a node of a tainted group (verified over the group's full lifecycle in
  the acceptance run: provision → scale → heal).
- **SC-006**: A taint or label edit on a live nodepool converges to the
  upstream group without group replacement or node recreation, and an
  out-of-band change to the group's taints or labels is reverted to the
  declared sets within one reconcile cycle.

## Assumptions

- **Upstream capability VERIFIED (live probe, 2026-07-10)**: node-group
  create accepts `taints: [{key, value, effect}]` alongside `labels`, and the
  created group echoes the taints back in the create response (observed live:
  `POST https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups` → 201,
  group 117093, taint `biba=boba:NoSchedule` accepted and persisted verbatim
  in the group object). The field is absent from the published API docs, so
  it joins the hand-patched documented-superset (same treatment as
  `public_ip_enabled`). Two caveats for planning: (1) the probe went through
  the panel's internal endpoint (`timeweb.cloud/.../groups`), not the public
  `api.timeweb.cloud/.../node-groups` surface the provider calls — the same
  field must be re-confirmed there with a bearer token before wiring; (2) a
  create-response echo is not node-level convergence — acceptance still
  verifies the taint on the actual cluster nodes (FR-010, SC-001). The day-2
  *update* surface for taints was not probed — see the update-path assumption
  below.
- **Day-2 update path VERIFIED (live probe, 2026-07-10)**: `PATCH
  https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups/117093` → 200.
  The panel sends the group's full state (`name`, `labels`, `taints`,
  `public_ip_enabled`, autoscaler/autohealing flags) — **set-replace
  semantics**, not deltas — and the response echoes the updated `labels` and
  the `taints` carried in the body (a label was added live; the taint set
  rode along unchanged and was accepted). Remaining to verify during
  planning: (1) the same PATCH on the public `api.timeweb.cloud`
  node-groups surface (path/verb may differ from the panel's), (2) a
  *changed* taint set via PATCH (only labels were changed in the probe),
  (3) whether a group-level taint/label change propagates to
  already-running nodes or only to nodes that join afterwards, and
  (4) that an empty list clears the set rather than being ignored. If
  updates turn out to reach only future nodes, that limitation is surfaced
  to the operator (not silently reported as converged) and recorded as an
  upstream quirk per project convention.
- **Standard Kubernetes taint semantics apply**: the managed clusters run
  conformant Kubernetes, so the three standard effects and the standard
  taint/toleration matching rules are assumed; the feature adds no custom
  scheduling semantics.
- **Scope is taints plus label mutability**: the labels declaration shape is
  unchanged, but its create-only contract is lifted — labels get the same
  day-2 reconcile-and-revert treatment as taints (Clarifications
  2026-07-10). Sizing, autoscaling, and networking on the nodepool are
  unchanged. Cluster-autoscaler taint-awareness for scale-from-zero is out of
  scope (platform minimum is 2 nodes when autoscaling is enabled).
- **Verification uses live acceptance runs**: per project convention,
  "provisioned" claims are verified by re-observation of the live platform
  and the resulting cluster nodes, not by accepted API calls alone.
