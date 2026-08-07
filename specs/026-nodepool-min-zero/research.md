# Research: Nodepool autoscaling scale-to-zero (minSize: 0)

**Feature**: 026-nodepool-min-zero | **Date**: 2026-08-07

## R-1: The `>= 2` floor is stale upstream

- **Decision**: relax the provider's floor to `minSize >= 0`.
- **Rationale**: live panel capture 2026-08-07 — PATCH
  `/k8s/clusters/1096397/groups/117127` with
  `{"is_autoscaling":true,"min_size":0,"max_size":3,...}` → 200, response and
  subsequent GET echo `min_size: 0`. The published swagger still declares
  `minimum: 2` on both `min_size` and `max_size` (the origin of the 015-era CEL
  rule); the 015/024 features inherited that floor without a live probe of 0
  because scale-to-zero wasn't in scope then. Wire evidence beats the published
  spec (established convention: the swagger is a hand-patched superset precisely
  because the published doc lags reality).
- **Alternatives considered**: keeping `>= 1` (autoscaler always holds one node)
  — rejected: the whole point is zero-cost idle pools, and 0 is proven.

## R-2: Zero-node readiness semantics

- **Decision**: `Available` at 0 nodes **iff** the pool is converged
  (`upToDate`), autoscaling is declared enabled, and declared `minSize == 0`.
  Everything else keeps the 024 T034 guard (`Creating` at 0/0).
- **Rationale**: the T034 guard exists because a manual pool observed at 0/0 was
  silently Available while genuinely broken. Scale-to-zero is the one state
  where 0 actual nodes IS the desired steady state — but only when the declared
  floor is 0 and the declaration has converged. Gating on `upToDate` gets the
  observed-flag/bounds match for free (isNodepoolUpToDate already compares
  them), so no additional observed-state plumbing into the condition helper.
  A drained pool whose declared floor is >0 stays non-Available (transitional:
  the upstream autoscaler will restore it — the provider must neither claim
  Available nor interfere).
- **Alternatives considered**: (a) Available whenever autoscaling is on and
  count is inside [min,max] — rejected: makes Available a bounds computation
  disconnected from actual node health, and changes behavior for existing >=1
  pools (FR-006 forbids). (b) new condition reason `ScaledToZero` with
  Ready=True — rejected: standard `Available` keeps GitOps health tooling
  working unmodified; the state is visible in the new status mirror instead.

## R-3: Autoscaling status mirror (024's deferred US3)

- **Decision**: add optional `status.atProvider.autoscaling
  {enabled, minSize, maxSize}` mirrored from the group GET.
- **Rationale**: 024 deferred exactly this because a patch release allows zero
  CRD change; 026 is a minor with a CRD change anyway. The spec's FR-005
  requires the zero state to be faithfully visible, and both kuttl and the live
  gate need something assertable for "bounds converged to 0" — `min_size: 0`
  vs `absent` is indistinguishable without a mirror. Mirror-only (zone-echo
  idiom): never an input to convergence decisions.
- **Alternatives considered**: asserting via provider logs or raw API curl in
  the gate — rejected: not declarative, not kuttl-assertable, and useless to
  operators day-to-day.

## R-4: maxSize floor

- **Decision**: relax `maxSize` floor to 1 (field `Minimum=1` already present;
  drop only the CEL `>= 2` clause). Probe `max_size: 1` at the live gate (P-2);
  if upstream rejects it, restore a `maxSize >= 2` CEL clause before release.
- **Rationale**: the `>= 2` on max came from the same stale swagger floor. A
  single-node burst pool (`{min:0, max:1}`) is a legitimate shape. Unproven by
  the capture (max was 3), hence the explicit gate + fallback rather than
  shipping blind — guard-the-trap philosophy.
- **Alternatives considered**: keeping `maxSize >= 2` — safe but leaves an
  arbitrary floor the moment min 0 lands; rejected in favor of probe-then-keep.

## R-5: No wire-shape changes needed

- **Decision**: no changes to `buildCreateNodeGroupBody` or the 024 bounds
  PATCH; pin 0-serialization with unit tests.
- **Rationale**: both paths already send `min_size`/`max_size` as `*int`;
  `json` `omitempty` on pointers checks nil, not pointee, so `&zero` reaches
  the wire as `0`. The generated client carries no validation (oapi-codegen
  ignores `minimum`), so the swagger hand-patch (D-4) is documentation only and
  needs no regen.
- **Alternatives considered**: none viable — there is nothing to change.

## R-6: Create-path symmetry — softened to a confirmation

- **Decision**: treat create-with-`min_size: 0` as documented-valid, confirmed
  by P-1 (fresh pool at the live gate) before release.
- **Rationale**: the PATCH is wire-proven AND the official scale-to-zero doc's
  walkthrough sets `min_size: 0` at group **creation** (panel) — fetched
  2026-08-07, pinned in spec.md "Upstream doc facts". If the API create
  nonetheless rejects 0: never silently rewrite the declaration; the create
  error surfaces classified with the upstream message, the wire fact gets
  recorded, and the plan pivots to "create at declared bounds with min>=1,
  converge to 0 via the existing bounds PATCH on the next reconcile" — an
  explicit, documented two-step, not a silent clamp.
- **Alternatives considered**: probing create before implementation — requires
  ordering a real paid node group off-cycle; the live gate orders one anyway,
  so the probe is folded into it.

## R-7: Upstream prerequisite & drain blockers — document-only (clarify Q1)

- **Decision**: the scale-to-zero prerequisite (≥2 permanently active nodes in
  OTHER pools — official doc) and the drain blockers (`safe-to-evict:"false"`,
  PDBs, controller-less pods) get NO runtime handling: no admission rule, no
  condition, no Warning event. Docs + release notes only (FR-007).
- **Rationale** (owner decision 2026-08-07): the failure mode is benign — the
  pool never drains but stays fully functional, and the provider's convergence
  and readiness semantics are unaffected (a non-drained pool is just an
  autoscaled pool at N>0 nodes; the D-2 carve-out fires only at actual 0). The
  provider cannot reliably compute "permanently active elsewhere": other pools
  may themselves be autoscaled, or be panel-managed outside any MR. The 021
  guard-the-trap philosophy targets states that break or mislead; this is
  plain platform semantics.
- **Alternatives considered**: best-effort Warning event (sum other groups'
  counts < 2) — rejected: guesses about "permanent", adds a cross-group read
  dependency to every autoscaled-pool Observe; hard guard/park — rejected:
  blocks a legal, functional (if not-yet-zero-cost) configuration.

## Open probes (resolved at the live gate; shape per clarify Q2 — pre-existing
staging cluster, `nodeSelector`-pinned Deployment, no fresh cluster)

| ID  | Question | Fallback |
|-----|----------|----------|
| P-1 | Does the API group **create** accept `min_size: 0` (panel-doc says yes)? | Two-step: create with declared bounds floored upstream, bounds-PATCH to 0 next reconcile (explicit, R-6) |
| P-2 | Does upstream accept `max_size: 1`? | Restore `maxSize >= 2` CEL clause pre-release (R-4) |
| P-3 | Does the idle pool drain to 0 within tolerance (~5 min idle window + ~2 min `DeletionCandidateOfClusterAutoscaler` taint-to-delete, per doc) with the prerequisite satisfied by the staging cluster's base pool? | If it never drains despite prerequisite + proper workload, that's an upstream gap: record wire fact, RU support ticket, docs warning — provider behavior ships regardless (correct at whatever count upstream settles on) |
