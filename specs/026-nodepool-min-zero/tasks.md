# Tasks: Nodepool autoscaling scale-to-zero (minSize: 0)

**Input**: Design documents from `specs/026-nodepool-min-zero/`

**Prerequisites**: plan.md (rev 2), spec.md (clarified 2026-08-07), research.md
(R-1..R-7), data-model.md, contracts/

**Tests**: unit tests are MANDATORY (Constitution III); kuttl/live-gate tasks
implement the plan's Validation section.

**Organization**: US1 = declare & converge `minSize: 0`; US2 = Ready=True at
zero nodes; US3 = guardrails still reject nonsense.

## Phase 1: Setup

No setup tasks — existing provider layout, no new packages or tooling.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the CRD schema change every story reads.

- [ ] T001 Relax admission + add mirror type in
      `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go`:
      `NodepoolAutoscaling.MinSize` marker `Minimum=1`→`Minimum=0` (comment: floor
      relaxed per 2026-08-07 wire capture + official scale-to-zero doc);
      `MaxSize` keeps `Minimum=1`; kind-level CEL (≈L261) →
      `!has(...autoscaling) || !...enabled || ...maxSize >= ...minSize`, message
      `when autoscaling is enabled: maxSize must be >= minSize`; new
      `NodepoolAutoscalingStatus {Enabled bool; MinSize *int; MaxSize *int}`
      (mirror-only doc comment) + optional
      `KubernetesClusterNodepoolObservation.Autoscaling *NodepoolAutoscalingStatus`.
- [ ] T002 Regenerate DeepCopy + CRD YAML (`make generate` or repo equivalent);
      verify `apis/kubernetes/v1alpha1/zz_generated.deepcopy.go` and
      `package/crds/kubernetes.m.timeweb.crossplane.io_kubernetesclusternodepools.yaml`
      reflect Minimum=0, new CEL message, and the status mirror. Tree clean
      after regen (Constitution I).
- [ ] T003 [P] Hand-patch `docs/openapi-timeweb.json` (doc-only, D-4):
      `min_size.minimum` 2→0 and `max_size.minimum` 2→1 on the node-group
      create (~L42303) and update (~L52269) schemas, each description gaining
      "(published floor is stale — live probe 2026-08-07 accepts min_size:0;
      official scale-to-zero doc concurs; feature 026)". No client regen.

**Checkpoint**: CRD accepts `{enabled:true, minSize:0, maxSize:3}`; schema
carries `status.atProvider.autoscaling`.

---

## Phase 3: User Story 1 — Declare a scale-to-zero pool (P1) 🎯 MVP

**Goal**: `minSize: 0` passes admission, reaches the wire on create and day-2
PATCH, drift-repairs, and is visible in the new status mirror.

**Independent test**: apply a `minSize: 0` manifest → admission accepts;
fake-client unit tests show `min_size: 0` in create/PATCH bodies and
`status.atProvider.autoscaling.minSize: 0` after Observe.

- [ ] T004 [US1] Populate the mirror in `populateNodepoolStatus`
      (`internal/controller/kubernetes/nodepool_external.go`): map
      `nodeGroupBody.IsAutoscaling/MinSize/MaxSize` →
      `cr.Status.AtProvider.Autoscaling` (nil bounds mirrored as omitted;
      always set the block once observed so a day-2 disable clears stale
      bounds).
- [ ] T005 [US1] Unit tests in
      `internal/controller/kubernetes/nodepool_external_test.go`:
      (a) `buildCreateNodeGroupBody` with `{enabled,0,3}` emits
      `"min_size":0` (D-5 pin — marshal and assert the byte presence);
      (b) Update bounds PATCH body carries `min_size:0` when converging 2→0;
      (c) `isNodepoolUpToDate` matrix with declared `{0,3}`: observed
      `{true,0,3}` → true; observed `{true,2,3}` → false (bounds drift);
      observed `{false,nil,nil}` → false (flag drift);
      (d) Observe populates the status mirror from the GET (incl. `minSize:0`
      and the off/nulled case).

**Checkpoint**: US1 fully unit-verified; wire correctness deferred to the live
gate (P-1).

---

## Phase 4: User Story 2 — A pool at zero nodes reports healthy (P2)

**Goal**: converged autoscaled pool with declared `minSize: 0` at 0 upstream
nodes is Ready=True; every other 0-node path keeps today's behavior.

**Independent test**: fake-client Observe with group
`{is_autoscaling:true,min_size:0,max_size:3,node_count:0}`, empty node list,
spec `{enabled,0,3}` → `Available`; same with spec `{enabled,2,3}` or
autoscaling absent → NOT Available.

- [ ] T006 [US2] Readiness carve-out in
      `internal/controller/kubernetes/nodepool_external.go`: compute
      `zeroOK := upToDate && fp.Autoscaling != nil && fp.Autoscaling.Enabled &&
      fp.Autoscaling.MinSize == 0` in Observe; extend
      `setNodepoolReadyCondition(cr, upToDate, zeroOK, declared, nodes,
      recorder)`; inside the T034 guard (`declared == 0 && len(nodes) == 0`):
      `zeroOK` → `xpv2.Available()` (RecordConditionChange as for the normal
      Available path), else `Creating` unchanged. Update the function doc
      comment (0 nodes is the desired steady state iff converged + enabled +
      declared floor 0).
- [ ] T007 [US2] Unit readiness matrix in
      `internal/controller/kubernetes/nodepool_external_test.go`:
      zeroOK at 0/0 → Ready=True `Available`; manual pool at 0/0 → `Creating`;
      autoscaled `minSize:2` drained to 0/0 (upToDate) → `Creating`;
      `minSize:0` declared but bounds drifting (`!upToDate`) at 0/0 →
      `Reconciling`; failed node present → `UpstreamFailed` (precedence over
      zeroOK); scale-up in progress (declared 1, 0 active listed) →
      `Reconciling`.

**Checkpoint**: US2 unit-verified; live Ready=True-at-zero soak deferred to the
gate (P-3).

---

## Phase 5: User Story 3 — Validation still rejects nonsense (P3)

**Goal**: invalid bounds fail admission with rule-naming messages; previously
valid manifests stay valid.

**Independent test**: server-side dry-run applies against a cluster with the
regenerated CRD.

- [ ] T008 [US3] Extend `test/e2e/kuttl/tests/25-nodepool-autoscaling/` with a
      fail-closed admission script step (021 idiom, explicit kubectl context):
      server dry-run REJECTS `{enabled,2,1}` (maxSize>=minSize message),
      `{enabled,0,0}` (maxSize minimum), `{enabled,-1,3}` (minSize minimum);
      ACCEPTS `{enabled,0,3}` and the pre-existing `{enabled,2,3}` shape
      (SC-004 non-breaking pin).
- [ ] T009 [US3] Extend bundle 25 with a day-2 scale-to-zero step: patch the
      bundle's pool to `autoscaling: {enabled:true, minSize:0, maxSize:3}`,
      assert convergence via the NEW mirror
      (`status.atProvider.autoscaling.minSize == 0`) using
      `kubectl wait`-by-condition/jsonpath — never positional condition
      asserts; then restore the original bounds for the existing
      disable/re-enable steps to stay valid.

**Checkpoint**: admission matrix pinned in e2e; bundle 25 still passes
end-to-end.

---

## Phase 6: Polish, Live Gate & Release

- [ ] T010 [P] Update `docs/kubernetes.md` autoscaling section (D-6): drop
      "`minSize`/`maxSize` ≥ 2"; document scale-to-zero — prerequisite (≥2
      permanently active nodes in other pools, else the pool never drains;
      only-pool corollary), scale-up trigger (nodeSelector/nodeAffinity
      targeting required), drain blockers (safe-to-evict:"false", PDBs,
      controller-less pods), readiness semantics (Ready=True at 0 iff
      converged + enabled + minSize 0), drain timing (~5 min idle + ~2 min
      taint-to-delete), doc link. Update the autoscaling example comment in
      `examples/kubernetesclusternodepool.yaml` to show `minSize: 0`.
- [ ] T011 [P] Seed `specs/_next-nodepool-autoscaler-settings.preface.md`
      (D-7): panel `autoscaler_settings` tuning block modeling +
      `autoscaler_settings.enabled: false` vs `is_autoscaling: true` echo
      quirk (wire capture 2026-08-07).
- [ ] T012 Full local validation: `go test ./...`, lint via `go run` per repo
      policy, `crossplane beta validate` / examples server dry-run against the
      regenerated CRD; tree clean after regen.
- [ ] T013 Live gate on `inyan-staging` (plan Validation, clarify Q2 shape —
      NO fresh cluster; dev-tag `make xpkg.push VERSION=dev-$(date +%s)` +
      e2e deploy, provider image strings-checked, kubectl context pinned):
      (1) pre-check base pool ≥2 active nodes; (2) P-1 fresh `minSize: 0` pool
      by flat `clusterID` → Synced/Ready, upstream GET echoes 0, mirror shows
      0; (3) day-2 2→0 on a second pool → bounds PATCH converges, mirror
      updates; (4) drain: nodeSelector-pinned Deployment on the min-0 pool,
      scale to 0 → pool reaches 0 nodes (tolerate ~5+2 min + deprovision) →
      Ready=True holds ≥15 min, provider logs show zero count writes + no
      error spam (log scan is a first-class step); (5) scale-up smoke:
      Deployment to 1 → node provisions, Ready=True at 1; (6) P-2
      `maxSize: 1` probe (PATCH); on failure restore `maxSize >= 2` CEL
      clause before release (R-4 fallback); (7) cleanup e2e resources
      (investigate-before-cleanup rule applies to anything unexpected).
- [ ] T014 Release v0.13.0: batch commits (owner-authorized "roll to release"
      2026-08-07), release notes example-first with the prerequisite warning +
      `docs/kubernetes.md` link, tag `v0.13.0`, push tag (CI-on-tag publishes
      the xpkg); post-release ops note: align production `ci` pool manifest to
      `minSize: 0` (until then the provider drift-repairs the panel's 0 back
      to the declared bounds).

---

## Dependencies & Execution Order

```text
T001 → T002 → {T004, T006, T008, T009}     # schema before controller/e2e
T003 [P] anytime
T004 → T005; T004 → T009                    # mirror before its tests/asserts
T006 → T007
T010, T011 [P] anytime after plan
{T002..T011} → T012 → T013 → T014           # validate → gate → release
```

US1 (T004–T005) and US2 (T006–T007) touch the same two files — sequential, US1
first. US3's T008 is parallel to US1/US2 once T002 lands. MVP = Phase 2 + US1.

## Implementation Strategy

Single PR/branch, stories landed in priority order; the live gate (T013) is
the release gate — probes P-1/P-2/P-3 resolve there, with R-4/R-6 fallbacks
applied before T014 if any fail.
