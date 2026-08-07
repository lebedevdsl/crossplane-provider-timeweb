# Implementation Plan: Nodepool autoscaling scale-to-zero (minSize: 0)

**Branch**: `026-nodepool-min-zero` | **Date**: 2026-08-07 (rev 2, post-clarify) | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/026-nodepool-min-zero/spec.md`
(2 clarifications recorded 2026-08-07 + upstream doc facts pinned)

## Summary

Target **v0.13.0** (minor — CRD change), NON-BREAKING (pure relaxation +
additive status). Wire capture 2026-08-07 (panel PATCH on cluster 1096397 /
group 117127) proves upstream accepts `min_size: 0` with autoscaling on, and the
official scale-to-zero doc (`docs/k8s/kubernetes-autoscaling/
autoscaling-to-zero-nodes`, fetched same day) documents setting it at group
creation — the published swagger `minimum: 2` floor and the provider's matching
CEL rule are stale. Three deltas:

1. **Admission relax**: `autoscaling.minSize` floor 2 (effective) → 0; `maxSize`
   floor 2 → 1; `maxSize >= minSize` kept (R-1, R-4).
2. **Zero-state readiness**: a converged, autoscaling-enabled pool with declared
   `minSize: 0` and 0 upstream nodes reports **Available** — carve-out from the
   024 T034 "never Available at 0 nodes" guard, which stays for every other path
   (R-2).
3. **Autoscaling status mirror**: `status.atProvider.autoscaling
   {enabled,minSize,maxSize}` from the group GET — delivers 024's deferred US3
   (patch-release constraint gone) and makes bounds convergence assertable in
   kuttl/live gates (R-3).

**Clarified boundaries (2026-08-07)**: upstream's scale-to-zero prerequisite
(≥2 permanently active nodes in other pools) and the drain blockers are
**document-only** — no admission rule, no condition, no Warning event (R-7); the
live drain validation attaches to the pre-existing staging cluster and drives
drain/scale-up with a `nodeSelector`-pinned Deployment (no fresh cluster).

No Create/Update logic change: both paths already send bounds as `*int`, and a
pointer-to-0 serializes (unit-pinned, R-5). Swagger hand-patch is doc-only
(`minimum` keywords don't affect codegen).

## Technical Context

**Language/Version**: Go (latest stable, per repo tooling policy)

**Primary Dependencies**: crossplane-runtime v2, controller-runtime, oapi-codegen
generated Timeweb client (`internal/clients/timeweb`)

**Storage**: N/A (state lives in the CR + upstream API)

**Testing**: `go test` with fake Timeweb client (Constitution III), kuttl bundle 25
extension, live gate on `inyan-staging`

**Target Platform**: Kubernetes (Crossplane provider), amd64 image

**Project Type**: Crossplane provider (single Go module)

**Performance Goals**: N/A — no new API calls; reconcile shape unchanged

**Constraints**: 2 r/s shared limiter (018) untouched; owned-fields PATCH
discipline (015); Observe-sole-authority (024); document-don't-guard for
upstream autoscaler semantics (clarify Q1)

**Scale/Scope**: 1 kind touched (`KubernetesClusterNodepool`), ~4 source files +
regen + docs + e2e

## Constitution Check

*GATE: evaluated pre-Phase-0; re-evaluated post-Phase-1 — PASS both.*

- **I. CRD Contract Stability**: v1alpha1 (pre-v1beta1 rules don't bind, but the
  change is additive/relaxing anyway: no valid manifest becomes invalid; new
  status field is optional). CRD YAML + DeepCopy regenerated and committed in the
  same PR. PASS.
- **II. Idempotent, Side-Effect-Aware Reconciliation**: no new write paths;
  Observe stays read-only (readiness + mirror are computed from the existing
  GETs); error classification untouched; not-found handling untouched. Bounds
  convergence (including 0) reuses the 024 drift/PATCH machinery — already
  idempotent. The document-only decision (Q1) adds zero reconcile branches.
  PASS.
- **III. Controller Test Discipline**: unit matrix added for the readiness
  carve-out, up-to-date comparison with 0 bounds, and 0-serialization of
  create/patch bodies — all against the fake client. PASS.

## Design decisions

- **D-1 admission** (`kubernetesclusternodepool_types.go`): `MinSize` field
  `Minimum=1` → `Minimum=0`; `MaxSize` field `Minimum=1` stays (floor 1). The
  kind-level CEL rule drops its `>= 2` clauses to:
  `!has(autoscaling) || !enabled || maxSize >= minSize` (message updated).
  Field-level minimums carry the floors, so the rule stays trivially cheap (no
  CEL cost-budget exposure). Negative min is caught by `Minimum=0`;
  `{min:0, max:0}` is caught by `MaxSize Minimum=1` — spec US3 scenarios all
  covered.
- **D-2 readiness** (`nodepool_external.go`): compute
  `zeroOK := upToDate && declaredOn && spec.Autoscaling.MinSize == 0` in Observe
  and pass it to `setNodepoolReadyCondition`. In the T034 guard
  (`declared == 0 && len(nodes) == 0`): `zeroOK` → `Available` (event on
  transition, as today), else `Creating` unchanged. `upToDate` already implies
  the observed flag+bounds match the declaration, so no extra observed-state
  plumbing. Manual pools, mid-provision states, and drained-but-drifting pools
  keep today's behavior. A prerequisite-violating pool (only capacity in the
  cluster) never reaches 0 nodes, so it never hits the carve-out — consistent
  with Q1's document-only decision by construction.
- **D-3 status mirror**: new optional
  `status.atProvider.autoscaling {enabled bool, minSize *int, maxSize *int}`
  populated in `populateNodepoolStatus` from the GET's
  `is_autoscaling`/`min_size`/`max_size` (nullable when off — mirror the nulls
  as omitted). Zone-echo style: mirror-only, never an input to convergence.
- **D-4 swagger hand-patch** (`docs/openapi-timeweb.json`): `min_size`
  `minimum: 2` → `0` on both node-group schemas (create ~L42303, update
  ~L52269) + a description note "(published floor is stale — live probe
  2026-08-07 accepts min_size:0; official scale-to-zero doc concurs; feature
  026)". `max_size` minimum → 1 with the same note, pending P-2. No regen
  required (validation keywords don't reach the generated client);
  re-apply-on-regen note added to the hand-patch inventory.
- **D-5 no wire changes**: `buildCreateNodeGroupBody` and the 024 bounds PATCH
  already pass `*int` pointers; `&zero` serializes as `0` (omitempty checks
  nil, not value). Pinned by unit tests rather than code.
- **D-6 docs** (FR-007, expanded by Q1): `docs/kubernetes.md` autoscaling
  section — drop "≥ 2", document scale-to-zero with: the **prerequisite**
  (≥2 permanently active nodes in other pools, else the pool never drains —
  including the "only pool in the cluster" corollary), the **scale-up trigger**
  (pods must target the group via `nodeSelector`/`nodeAffinity` on group id or
  labels), the **drain blockers** (`safe-to-evict: "false"`, PDBs,
  controller-less pods, unsatisfiable rescheduling), readiness semantics
  (Ready=True at 0 nodes iff converged+enabled+minSize 0), and drain timing
  (~5 min idle window + ~2 min taint-to-delete). Release notes carry the
  prerequisite. `examples/kubernetesclusternodepool.yaml` comment updated. No
  runtime enforcement anywhere.
- **D-7 preface seed**: `specs/_next-nodepool-autoscaler-settings.preface.md` —
  the panel's `autoscaler_settings` tuning block (thresholds/durations/
  `zero_or_max_node_scaling`) + the `autoscaler_settings.enabled: false` vs
  `is_autoscaling: true` echo quirk, per the capture-upstream-quirks convention.

## Project Structure

### Documentation (this feature)

```text
specs/026-nodepool-min-zero/
├── plan.md              # This file (rev 2)
├── spec.md              # Feature spec (wire facts + clarifications + doc facts)
├── research.md          # Phase 0 — R-1..R-7 decisions, P-1..P-3 probes
├── data-model.md        # Phase 1 — CRD deltas + readiness state table
├── quickstart.md        # Phase 1 — operator walkthrough incl. prerequisite
├── contracts/
│   ├── nodepool-minzero-v1alpha1.md    # CRD contract (validation + readiness)
│   └── timeweb-nodegroup-minzero.md    # wire contract + doc facts + probes
└── checklists/requirements.md
```

### Source Code (repository root)

```text
apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go   # D-1 relax, D-3 mirror type
apis/kubernetes/v1alpha1/zz_generated.deepcopy.go             # regen
package/crds/kubernetes.m.timeweb.crossplane.io_kubernetesclusternodepools.yaml  # regen
internal/controller/kubernetes/nodepool_external.go           # D-2 readiness, D-3 populate
internal/controller/kubernetes/nodepool_external_test.go      # unit matrix
docs/openapi-timeweb.json                                     # D-4 (doc-only)
docs/kubernetes.md                                            # D-6
examples/kubernetesclusternodepool.yaml                       # D-6
test/e2e/kuttl/tests/25-nodepool-autoscaling/                 # scale-to-zero steps
specs/_next-nodepool-autoscaler-settings.preface.md           # D-7
```

**Structure Decision**: existing provider layout; no new packages, no new files
outside tests/docs/spec artifacts.

## Validation

- **Unit** (Constitution III): readiness matrix — {zeroOK, drained, drifting,
  manual-0, mid-provision, failed-node} → condition; `isNodepoolUpToDate` with
  declared/observed 0 bounds (match + drift both directions); create body and
  bounds-PATCH body serialize `min_size: 0` (D-5 pin).
- **kuttl bundle 25 extension**: admission accept `{0,3}` / reject `{2,1}`,
  `{0,0}`, `{-1,3}` (server dry-run, fail-closed script step per the 021
  idiom); day-2 patch to `minSize: 0` asserted via the new status mirror
  (`kubectl wait --for=condition` idiom, never positional).
- **Live gate** (`inyan-staging`, e2e context pinned; shape per clarify Q2 —
  NO fresh cluster):
  1. Pre-check: the pre-existing Ready cluster's base pool has ≥2 active nodes
     (the prerequisite); record group ids.
  2. **P-1** fresh `minSize: 0` pool attached by flat `clusterID` — create-path
     confirmation (doc already shows creation-time 0 via panel).
  3. Day-2 edit 2→0 on a second declared pool — bounds PATCH convergence,
     asserted via the new status mirror.
  4. Drain: deploy a `nodeSelector`-pinned Deployment (proper controller — not
     a bare pod, else drain-blocked) on the min-0 pool, scale it to 0 → pool
     drains (tolerate ~5 min idle window + ~2 min taint-to-delete + node
     deprovision) → **Ready=True at 0 nodes** holds over a ≥15 min soak with
     zero corrective writes (provider logs scanned per the
     read-logs-in-validation convention).
  5. Scale-up-from-zero smoke: scale the Deployment back to 1 → a node
     provisions, pool returns to Ready=True at 1 node, provider issued no count
     writes throughout.
  6. **P-2** `maxSize: 1` probe (create or PATCH). Fallbacks per research.md.
- **Release**: v0.13.0, example-first notes carrying the prerequisite;
  production `ci` pool manifest aligned to `minSize: 0` post-release (ops note
  in quickstart — the running provider drift-repairs the panel's 0 back to the
  declared 2 until then).

## Complexity Tracking

No constitution violations — table not needed.
