# Feature Specification: Nodepool autoscaling day-2 convergence

**Feature Branch**: `024-fix-autoscaling-day2`

**Created**: 2026-07-25

**Status**: Draft

**Input**: User description: "fix autoscaling on/off" from `specs/_next-nodepool-autoscaling-day2.preface.md` — live gap 2026-07-25 on the production workers pool: flipping autoscaling on→off (+ lowering nodeCount) never applies the flag and starts a count fight (provider reduces, the still-enabled upstream autoscaler restores to min, repeat).

**Source**: preface (code-verified gap) + owner mitigation currently live (declared autoscaling-on, min=2 — stable because the Update early-return stops all count writes).

## Probe results (live 2026-07-25/26, staging — supersedes P-1/P-2/P-4 as open items)

- **P-1 RESOLVED**: the day-2 op is the undocumented-superset group PATCH.
  `PATCH /k8s/clusters/{cid}/groups/{gid}` with `{"is_autoscaling": false}`
  → 200, flag off + min/max nulled, echoed synchronously and by re-GET.
  Re-enable `{"is_autoscaling": true, "min_size": 2, "max_size": 3}` → 200,
  echoed. The GET payload carries `is_autoscaling`/`min_size`/`max_size`
  (+ an `autoscaler_settings` block) despite the published `NodeGroupOut`
  schema omitting them — observation surface confirmed.
- **P-2 RESOLVED (guess disproven)**: the toggle applied while the pool's
  node was still `installing` — no state restriction observed; no
  wait-classification needed.
- **P-4 CLOSED for this release (inconclusive-leaning-no)**: reduce of a
  1-node group returned 204 but `node_count` flapped 0→1 and the installing
  node survived. CRD `Minimum=1` stands; re-probe on an active node only if
  parking-at-zero is ever wanted (separate minor).

## The gap (code-verified, four holes)

Autoscaling is CREATE-ONLY in the provider today:

1. The three autoscaling settings are sent only in the create body
   (`nodepool_external.go:566–571`).
2. The observation struct does not decode them — the provider cannot see the
   upstream flag state at all.
3. Up-to-date logic: declared-on ⇒ unconditionally converged (day-2 off→on
   and min/max edits are undetectable); declared-off ⇒ compares only node
   count (flag drift invisible).
4. The day-2 PATCH structurally carries only name/labels/taints — no
   autoscaling verb exists anywhere.

Consequence: declared off + lower nodeCount ⇒ only the count delta fires,
against a pool whose autoscaler is still on ⇒ the fight.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Turning autoscaling off on a live pool works (Priority: P1)

The operator flips a live pool from autoscaling to a fixed count:

```yaml
    # was: autoscaling: {enabled: true, minSize: 2, maxSize: 5}
    nodeCount: 1        # autoscaling block removed (or enabled: false)
```

The provider first disables autoscaling upstream, verifies by re-observation,
and only then converges the node count. No fight: count deltas are NEVER
issued while the OBSERVED (not declared) flag is on.

**Why this priority**: the live incident — "workers at 1" is currently
unreachable; the fight burns reconciles and API budget and blocks a paid-node
saving.

**Independent Test**: fake-client Update with observed flag on and declared
off → the disable op is issued and NO count delta in the same pass; next
reconcile (observed off) → the delta fires.

**Acceptance Scenarios**:

1. **Given** a live pool with upstream autoscaling on and a spec declaring it
   off with a lower nodeCount, **When** reconciled, **Then** the disable op is
   sent first and no count write happens in that pass.
2. **Given** the disable has been observed applied, **When** reconciled,
   **Then** the count converges to the declared value via the normal deltas.
3. **Given** upstream autoscaling on (regardless of what the spec says),
   **When** any reconcile runs, **Then** no count delta is ever issued
   (observed-flag guard — ends the fight even under external/stale state).

---

### User Story 2 - Turning autoscaling on / retuning min-max day-2 works (Priority: P2)

The operator enables autoscaling on a fixed-count pool, or edits min/max on
an autoscaled one. Both converge day-2: the flag+bounds are applied upstream
and verified by re-observation; bounds drift (panel edits) is repaired.

**Acceptance Scenarios**:

1. **Given** a fixed-count pool and a spec gaining
   `autoscaling: {enabled: true, minSize, maxSize}`, **When** reconciled,
   **Then** the enable op with the bounds is applied and observed.
2. **Given** an autoscaled pool with declared min/max differing from
   observed, **When** reconciled, **Then** the bounds are re-applied
   (single-writer drift repair).

---

### User Story 3 - The autoscaling state is visible on the CR (Priority: P3)

`status.atProvider` mirrors the observed flag and bounds so drift, the fight,
and convergence are all diagnosable from `kubectl` without the panel.

**Acceptance Scenarios**:

1. **Given** any pool, **When** observed, **Then** status mirrors the
   upstream `autoscaling` state (enabled/min/max).

---

### Edge Cases

- Observed-flag guard beats declared state everywhere: even a spec declaring
  a count while upstream is autoscaled (any cause) produces no count writes —
  drift is reported, the disable is the only mutation offered.
- Off→on when the current count is outside [min,max]: probe decides whether
  the upstream adjusts or rejects (P-3); until then the enable is sent as
  declared and upstream errors are classified normally.
- The toggle verb is UNDOCUMENTED (the documented PATCH lacks the fields);
  P-1 (panel devtools capture) is a hard precondition for planning the write
  path. Possible state restrictions (e.g. while the group is Provisioning)
  are UNKNOWN — recorded as P-2, explicitly an unverified guess; no
  wait-classification is designed unless the probe shows a restriction.
- The 015 rule stands: the day-2 PATCH must carry ONLY owned fields — if the
  toggle rides the same PATCH, the write must include exactly the autoscaling
  trio and never resend name/labels/taints in the same call unless proven
  safe.
- min/max CEL (≥2, min≤max) stays as shipped in 015; `nodeCount` semantics
  with autoscaling on remain "ignored while autoscaled" (documented).
- **P-4 (owner-raised): scale-to-zero — schema half ANSWERED 2026-07-25**:
  the documented create floor is `node_count: minimum 1` (identical in the
  repo spec and the fresh official spec) — a pool cannot be BORN empty; our
  CRD mirrors it (`nodeCount` Minimum=1). The reduce op
  (`DELETE …/groups/{id}/nodes`, `count ≥ 1` = nodes-to-remove) documents NO
  floor on the RESULTING size — whether reducing a 1-node group to 0 is
  accepted is server-side and undocumented. Remaining live probe: reduce a
  1-node throwaway group by 1. If it works and parking-at-zero is wanted, the
  CRD Minimum drops to 0 (schema change ⇒ minor release) with defined Ready
  semantics for an intentionally empty pool; if rejected, Minimum=1 stands
  and the question closes.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The provider MUST observe the pool's autoscaling state (flag +
  bounds) and mirror it in status.
- **FR-002**: Up-to-date evaluation MUST detect drift in both flag directions
  and in the bounds.
- **FR-003**: The update path MUST converge autoscaling BEFORE any count
  logic, with convergence verified by re-observation (2xx ≠ converged).
- **FR-004**: Count deltas MUST be gated on the OBSERVED flag being off —
  never on the declared state alone.
- **FR-005**: The write path follows the probe-verified upstream op (P-1);
  writes carry only the fields the probe proves owned; upstream rejections
  are classified per the standard rules. Any probe-discovered state
  restriction gets an explicit classification only if proven (P-2).
- **FR-006**: Unit tests per Constitution III: disable-then-count ordering,
  observed-flag count gate, enable-with-bounds, bounds drift repair,
  status mirror, declared-on no-longer-blind (off→on detected).
- **FR-007**: NON-BREAKING; no spec-surface change expected (status mirror is
  additive). Release per repo convention once verified on staging (live gate:
  on→off→count-1 on a real pool — the incident shape — and off→on).

### Key Entities

- **KubernetesClusterNodepool**: unchanged spec surface; status gains the
  autoscaling mirror; Update gains the flag convergence + observed-flag gate.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The incident sequence (live autoscaled pool → declare off +
  nodeCount 1) converges hands-free: flag off upstream, then exactly one
  count reduction, zero oscillation over a ≥15-min soak.
- **SC-002**: Off→on and min/max edits converge day-2 and repair panel-side
  drift within one poll cycle.
- **SC-003**: With upstream autoscaling on, zero count-mutation calls are
  issued regardless of spec (verified by fake call counting and the live
  soak).
- **SC-004**: The production workers pool can drop the mitigation (min=2
  autoscaling) and reach the originally intended fixed count with no manual
  steps.

## Assumptions

- P-1 (panel capture of the toggle op) lands before `/speckit-plan`; the
  expected shape is the undocumented-superset group PATCH carrying
  `is_autoscaling`/`min_size`/`max_size` (the 015 discovery pattern), but the
  plan is written against what the capture actually shows.
- P-2 state-restriction is an unverified guess — designed-in only if proven.
- The current production mitigation (autoscaling-on, min=2) stays until this
  ships; SC-004 is the acceptance that it can be lifted.
- Target release: patch-or-minor per the release convention (no spec-surface
  change expected ⇒ patch v0.11.2 unless the plan surfaces a schema need).
