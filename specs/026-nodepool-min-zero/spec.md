# Feature Specification: Nodepool autoscaling scale-to-zero (minSize: 0)

**Feature Branch**: `026-nodepool-min-zero`

**Created**: 2026-08-07

**Status**: Draft

**Input**: User description: "allow min nodes to be 0 on kuberenetesclusternodepool" + a live
panel wire capture (2026-08-07) proving upstream accepts it.

## Wire facts (authoritative, captured live 2026-08-07)

Panel PATCH `https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups/117127` on the
production `ci` worker group:

- Request body: `{"name":"ci", ..., "is_autoscaling":true, "is_autohealing":false,
  "min_size":0, "max_size":3, "autoscaler_settings":{...}, "public_ip_enabled":false,
  "virtual_router_id":"b099..."}` → **200 OK**.
- Response echoes `"min_size": 0, "max_size": 3, "is_autoscaling": true` synchronously
  (`node_count` still 2 at capture time — the autoscaler drains later on its own schedule).
- The panel also sends an `autoscaler_settings` tuning block
  (`scale_down_utilization_threshold`, `scale_down_unneeded_duration`,
  `zero_or_max_node_scaling`, ...) the provider has never modeled — **out of scope** here
  (see Assumptions).
- Quirk noted for the record: the response's `autoscaler_settings.enabled` reads `false`
  while `is_autoscaling` is `true`; `is_autoscaling` is the field the provider already keys
  on (feature 024) and the pool demonstrably autoscales — ignore `autoscaler_settings.enabled`.

This invalidates the ">= 2 when autoscaling is enabled" floor the provider enforces today
(admission rule from feature 015, kept in 024): upstream accepts a zero minimum, enabling
scale-to-zero pools.

## Clarifications

### Session 2026-08-07

- Q: How should the provider handle upstream's "≥2 permanently active nodes
  elsewhere in the cluster" prerequisite for scale-to-zero? → A: Document-only —
  no runtime check. The failure mode is benign (the pool never drains, stays
  functional); the provider cannot reliably compute "permanently active
  elsewhere" (other pools may be autoscaled or panel-managed outside any MR);
  the autoscaler owns the count either way. Prerequisite + drain-blockers go
  into docs/quickstart + release notes.
- Q: What shape should the live drain validation take, given the prerequisite? →
  A: Attach the `minSize: 0` pool by flat `clusterID` to the pre-existing Ready
  cluster on `inyan-staging` (015 idiom, no cluster provisioning) — its base
  pool satisfies the ≥2-permanently-active-nodes prerequisite (verified at gate
  time). Exercise drain via a `nodeSelector`-pinned Deployment: scale workload
  to 0 → pool drains to 0 nodes → Ready=True soak; then scale the workload up
  for a scale-up-from-zero smoke. No fresh cluster in the gate.

### Upstream doc facts (fetched 2026-08-07)

`https://timeweb.cloud/docs/k8s/kubernetes-autoscaling/autoscaling-to-zero-nodes`:

- Scale-to-zero requires **at least two permanently active nodes elsewhere in
  the cluster** (any other node group(s)) to host system components. Corollary:
  a `minSize: 0` pool cannot be the cluster's only/first capacity — such a pool
  simply never drains to zero.
- The panel's own walkthrough sets `min_size: 0` **at group creation** —
  create-path support is documented, not just the captured PATCH (softens open
  probe P-1 to a confirmation).
- Scale-up from zero triggers on `Pending` pods that target the group via
  `nodeSelector`/`nodeAffinity` (group id or the group's custom labels).
- Drain-to-zero is blocked by: pods annotated
  `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"`, PodDisruptionBudget
  restrictions, unsatisfiable rescheduling constraints, and controller-less
  pods.
- Drained nodes are tainted `DeletionCandidateOfClusterAutoscaler=...` and
  deleted ~2 minutes later.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Declare a scale-to-zero pool (Priority: P1)

An operator runs a dedicated CI/batch worker pool that is idle most of the day. They declare
`autoscaling: {enabled: true, minSize: 0, maxSize: 3}` on the `KubernetesClusterNodepool` so
the pool holds zero nodes (zero cost) when idle and grows only while jobs are pending.

**Why this priority**: This is the feature. Today admission rejects `minSize` below 2, so the
operator pays for at least two permanently-running workers per isolated pool.

**Independent Test**: Apply a nodepool manifest with `minSize: 0` — it must pass admission,
converge upstream (upstream reflects `min_size: 0`), and the resource must reach
Synced=True/Ready=True.

**Acceptance Scenarios**:

1. **Given** a new nodepool manifest with `autoscaling: {enabled: true, minSize: 0,
   maxSize: 3}`, **When** applied, **Then** admission accepts it and the created upstream
   group carries `min_size: 0`.
2. **Given** an existing autoscaled pool with `minSize: 2` (e.g. today's production
   mitigation), **When** the operator edits the manifest to `minSize: 0`, **Then** the
   change converges upstream day-2 without recreating the pool.
3. **Given** a converged `minSize: 0` pool, **When** someone edits the bounds out-of-band in
   the panel, **Then** the declared bounds are restored (existing single-writer drift
   repair, unchanged).

---

### User Story 2 - A pool at zero nodes reports healthy (Priority: P2)

The autoscaler drains the idle pool to zero nodes. The operator's tooling (GitOps health
checks, dashboards) must see the nodepool resource as converged and healthy — zero nodes is
the desired steady state, not an error or a pending provision.

**Why this priority**: Without this, US1 is unusable in practice: the resource would sit
Ready=False forever while idle, tripping every health gate the operator has. (Today's
readiness logic explicitly treats a 0-node group as still-creating.)

**Independent Test**: Observe a converged `minSize: 0` pool after the autoscaler has removed
all nodes — the resource must report Synced=True and Ready=True and stay stable.

**Acceptance Scenarios**:

1. **Given** a converged autoscaling-enabled pool whose upstream node count has reached 0,
   **When** reconciled, **Then** the resource reports Ready=True and the provider issues no
   scale-up or corrective writes.
2. **Given** the same pool, **When** the autoscaler later grows it back (pending pods),
   **Then** readiness follows the existing per-node rules (Ready=False while nodes
   provision, Ready=True when all active) with no provider interference in the count.

---

### User Story 3 - Validation still rejects nonsense (Priority: P3)

An operator typos the bounds. Admission must still reject impossible combinations with a
clear message rather than shipping them upstream.

**Why this priority**: Guardrail retention — relaxing the floor must not silently drop the
other bounds checks.

**Independent Test**: Server-side dry-run applies of invalid manifests are rejected with
messages naming the violated rule.

**Acceptance Scenarios**:

1. **Given** `autoscaling: {enabled: true, minSize: 2, maxSize: 1}`, **When** applied,
   **Then** admission rejects it (max must be >= min).
2. **Given** `autoscaling: {enabled: true, minSize: 0, maxSize: 0}`, **When** applied,
   **Then** admission rejects it (a pool that can never have a node is not orderable).
3. **Given** a negative `minSize`, **When** applied, **Then** admission rejects it.

---

### Edge Cases

- **Zero-node readiness vs. the existing 0-node guard**: the current readiness rule
  deliberately refuses Available when declared and actual counts are both 0 (a feature-024
  fix for a manual-scaling path). The scale-to-zero state must be distinguished from that
  path: 0 nodes is Available **only** for an autoscaling-enabled, converged pool.
- **Create vs. update**: the wire capture proves `min_size: 0` on PATCH; the create path is
  assumed symmetric and MUST be verified at the live gate (see Assumptions).
- **`nodeCount` interplay**: `nodeCount` (min 1) is ignored while autoscaling is on —
  unchanged. A `minSize: 0` pool still declares `nodeCount >= 1`; disabling autoscaling
  later converges the pool back to that count via the existing 024 off-path.
- **Autoscaling disabled**: bounds are not sent and not validated against this rule —
  unchanged; manual pools cannot scale to zero (`nodeCount` floor stays 1).
- **Scale-up-from-zero latency**: how fast the upstream autoscaler provisions the first node
  for pending pods is upstream behavior, not provider scope; the provider only guarantees it
  does not interfere.
- **Cluster prerequisite not met** (fewer than 2 permanently active nodes in other pools —
  e.g. a `minSize: 0` pool as the cluster's only capacity): the pool never drains to zero but
  remains fully functional. Per clarification 2026-08-07 this is documented, NOT guarded —
  no admission rule, no condition, no Warning event; the provider's convergence and readiness
  semantics are unaffected (a non-drained pool is just an autoscaled pool at N>0 nodes).
- **Drain blockers** (safe-to-evict:"false" pods, PDBs, controller-less pods): same
  document-only treatment — upstream autoscaler semantics, listed in the operator docs.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Admission MUST accept `autoscaling.minSize: 0` when autoscaling is enabled.
- **FR-002**: Admission MUST continue to reject: negative `minSize`, `maxSize < 1`, and
  `maxSize < minSize` (messages naming the violated rule).
- **FR-003**: A declared `minSize: 0` MUST converge upstream on both create and day-2
  update, and MUST be repaired on out-of-band drift — identical single-writer semantics to
  every other bounds value (feature 024), with 0 no longer a special case.
- **FR-004**: A converged, autoscaling-enabled pool whose upstream node count is 0 MUST
  report Ready=True, and the provider MUST NOT issue corrective count writes for it. The
  existing not-Available-at-0-nodes guard MUST remain in force for pools where 0 nodes is
  not the autoscaler's decision (manual pools, mid-provision states).
- **FR-005**: The status mirror MUST faithfully reflect the zero state (observed node count
  0, autoscaling bounds including `minSize: 0`, an empty node list).
- **FR-006**: Existing pools with `minSize >= 1` MUST see no behavior change (relaxation
  only — NON-BREAKING; no manifest that is valid today becomes invalid).
- **FR-007**: Operator documentation MUST be updated: the current "minSize/maxSize >= 2"
  guidance is stale; document scale-to-zero and its readiness semantics, the upstream
  prerequisite (≥2 permanently active nodes in other pools — else the pool never drains),
  the scale-up trigger (pods must target the group via nodeSelector/nodeAffinity), and the
  drain blockers (safe-to-evict:"false", PDBs, controller-less pods). Release notes MUST
  carry the prerequisite. No runtime enforcement of any of these (clarified 2026-08-07).

### Key Entities

- **KubernetesClusterNodepool.autoscaling**: `{enabled, minSize, maxSize}` — the only
  surface that changes: `minSize` floor drops from 2 (effective) to 0. `maxSize` floor
  becomes explicitly 1.
- **Upstream worker group**: `min_size`/`max_size`/`is_autoscaling` on
  `/k8s/clusters/{cid}/groups/{gid}` — already modeled; now proven to accept
  `min_size: 0`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can declare a `minSize: 0` autoscaled pool and reach
  Synced=True/Ready=True with the zero minimum reflected upstream — on a fresh pool and as
  a day-2 edit of an existing pool.
- **SC-002**: A pool the autoscaler has drained to zero nodes holds Ready=True with zero
  provider-issued corrective writes over a soak of at least 15 minutes (no oscillation, no
  count fight).
- **SC-003**: All invalid bound combinations (negative min, max 0, max < min) are rejected
  at admission with messages that name the violated rule.
- **SC-004**: Every existing kuttl/e2e assertion for autoscaled pools with `minSize >= 2`
  still passes unchanged (non-breaking relaxation).

## Assumptions

- **Create-path symmetry**: the PATCH is wire-proven, and the official scale-to-zero doc's
  walkthrough sets `min_size: 0` at group **creation** (panel) — so create-path support is
  documented, pending API confirmation at the live gate on `inyan-staging`; if the API create
  nonetheless rejects 0, the gap gets recorded as a wire fact and the plan adjusted
  (day-2-only would still deliver US1 scenario 2). Note the prerequisite is orthogonal:
  creating a `minSize: 0` pool as the cluster's first capacity is expected to be API-valid —
  it just never drains (documented edge case, not guarded).
- **Live-gate shape (clarified 2026-08-07)**: pre-existing Ready staging cluster, pool
  attached by flat `clusterID`, drain exercised via a `nodeSelector`-pinned Deployment; the
  cluster's base pool provides the ≥2 permanently active nodes. e2e assertions on the drain
  must tolerate the upstream drain timing (idle window ~5 min + ~2 min taint-to-delete).
- **`minSize: 1` is implied valid**: upstream accepting 0 makes the old ">= 2" floor
  obsolete entirely; 1 is not separately probed but sits inside the proven range and is
  covered by the same live gate.
- **`autoscaler_settings` tuning is out of scope**: the panel's scale-down
  thresholds/durations block is a separate modeling decision — seed as a `_next` preface if
  wanted; the provider continues to omit the block (upstream defaults apply).
- **`virtual_router_id` in the panel PATCH is not provider business** (feature 021/022
  territory; the provider's owned-fields-only PATCH discipline from 015 means it never
  touches it).
- **Scale-from-zero scheduling behavior** (how pending pods trigger the first node) is
  upstream cluster-autoscaler responsibility; the provider only stays out of the way.
- The `ci` pool from the wire capture (cluster 1096397, group 117127) turned out at the live
  gate (2026-08-07) to be the **inyan-staging** cluster's pool and **panel-managed** (no MR)
  — nothing declares bounds for it, so its `min_size: 0` stands with no drift-fight. If ever
  brought under management, declare `minSize: 0` to match.
