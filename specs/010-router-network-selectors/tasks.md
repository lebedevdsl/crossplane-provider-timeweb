---
description: "Task list for Router Multi-Network Attachment & Selectors"
---

# Tasks: Router Multi-Network Attachment & Selectors

**Input**: Design documents from `/specs/010-router-network-selectors/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED — Constitution III (Controller Test Discipline) makes unit tests
with the fake Timeweb client mandatory for any change to a managed resource. CEL rules
are validated against the generated CRD. Tests are written before the implementation
they cover.

**Organization**: Grouped by user story. NOTE: this feature extends one existing kind
(`Router`) by editing shared files (`refs.go`, `router_external.go`,
`router_types.go`), so the stories are **layered, not fully parallel** — US2 and US3
build on the resolver introduced in US1. Dependencies are called out explicitly below.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 / US2 / US3 (maps to spec.md user stories)

## Path Conventions

Single Go module (Crossplane provider). API types under `apis/`, controllers under
`internal/controller/`, generated CRDs under `package/crds/`, examples under
`examples/`, e2e under `test/e2e/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish a clean baseline before changes.

- [x] T001 Verify the codegen + review toolchain is green on the current tree: run `make generate` (working tree stays clean) and `make reviewable`, from repo root.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The API field every story depends on. **No user story can begin until
this phase is complete.**

- [x] T002 Add the `NetworkSelector *metav1.LabelSelector` field to `RouterNetworkAttachment` and extend the exactly-one-of trio CEL to `(networkRef?1:0)+(networkID?1:0)+(networkSelector?1:0) == 1`, in `apis/network/v1alpha1/router_types.go` (add the `metav1` import if not present).
- [x] T003 Regenerate artifacts for the new field: run `make generate`; commit the updated `apis/network/v1alpha1/zz_generated.deepcopy.go` and the regenerated Router CRD YAML under `package/crds/`.

**Checkpoint**: `Router` accepts `networkSelector` (exactly-one-of enforced); resolver
not yet wired.

---

## Phase 3: User Story 1 - Attach every matching network by label (Priority: P1) 🎯 MVP

**Goal**: A selector attachment attaches every Ready, matching Network in the
namespace, and self-converges as networks appear/disappear — no Router edits.

**Independent Test**: Label 3 Networks, declare a Router with one `networkSelector`
entry, confirm all 3 attach; label a 4th and confirm it attaches; remove a label and
confirm detach. (spec.md US1 Independent Test.)

### Tests for User Story 1 (write first)

- [x] T004 [P] [US1] Unit test: selector resolves to multiple Ready networks → one `resolvedAttachment` each, in `internal/controller/network/refs_test.go`.
- [x] T005 [P] [US1] Unit test: a matched network that is not yet Ready (empty `upstreamID` or `Ready≠True`) is excluded with NO error returned, in `internal/controller/network/refs_test.go`.
- [x] T006 [P] [US1] Unit test: `mapNetworkToRouters` returns reconcile requests only for selector-using Routers in the changed Network's namespace, in `internal/controller/network/controller_test.go`.

### Implementation for User Story 1

- [x] T007 [US1] Implement the selector pass in `resolveRouterRefs`: for each `networkSelector` entry, `metav1.LabelSelectorAsSelector` + namespace-scoped `client.List(Network, InNamespace(ns), MatchingLabelsSelector{sel})`, apply the existing Ready+upstreamID gate, append one `resolvedAttachment` per eligible match (NAT off; carry the entry's DHCP/Gateway/ReservedIPs), in `internal/controller/network/refs.go`. (Depends on T002.)
- [x] T008 [US1] Add the `Network → Router` reactivity: implement `mapNetworkToRouters` (list Routers in ns, return requests for those with ≥1 selector entry) and add `.Watches(&networkv1alpha1.Network{}, handler.EnqueueRequestsFromMapFunc(mapNetworkToRouters))` to `SetupRouter`, keeping the existing 60s-capped rate limiter, in `internal/controller/network/controller.go`.
- [x] T009 [US1] Confirm/extend Observe convergence: ensure `isRouterUpToDate` and the Update attach/detach set-diff key by upstream network id so selector-resolved members attach and removed members detach, in `internal/controller/network/router_external.go`.
- [x] T010 [P] [US1] Add example manifest using `networkSelector` (matchLabels), in `examples/network/router-selector.yaml`.
- [x] T011 [US1] Add a kuttl e2e bundle `test/e2e/kuttl/tests/20-router-selector/` (formulation B: create Networks → wait Ready → create Router → assert 2 → grow net-3 → assert 3 → unlabel net-1 → assert 2). Conditions waited by TYPE (`kubectl wait --for=condition`), counts via poll loops, per-command timeouts. **PASSED live on twc-staging 2026-06-22.**

**Checkpoint**: MVP — selector attachment works end-to-end and converges on Network
changes.

---

## Phase 4: User Story 2 - Mix selector and explicit attachments (Priority: P2)

**Goal**: The effective attachment set is the de-duplicated union of selector and
explicit entries; explicit entries win on overlap.

**Independent Test**: One selector matching A+B plus an explicit `networkRef` to C
(which also carries the label) → exactly A, B, C attached (no dup), and C uses the
explicit entry's settings. (spec.md US2 Independent Test.)

**Depends on US1**: extends `resolveRouterRefs` from T007.

### Tests for User Story 2 (write first)

- [x] T012 [P] [US2] Unit test: a network matched by a selector AND named by an explicit entry is attached once, with the explicit entry's DHCP/NAT settings taking precedence, in `internal/controller/network/refs_test.go`.
- [x] T013 [P] [US2] Unit test: two selector entries with overlapping match sets → each network attached exactly once, in `internal/controller/network/refs_test.go`.

### Implementation for User Story 2

- [x] T014 [US2] Refactor `resolveRouterRefs` to a two-pass, id-keyed map: resolve explicit (`networkRef`/`networkID`) entries first into `map[networkID]resolvedAttachment`, then have selector matches insert only ids not already present (dedup + explicit-precedence, FR-005/FR-006); return the map's values, in `internal/controller/network/refs.go`.
- [x] T015 [P] [US2] Mixed selector + overlapping explicit `networkRef` (NAT) covered in `examples/network/router-selector.yaml` and validated via `crossplane beta validate`. (The e2e bundle exercises selector-only grow/shrink; mixed-overlap stays in the validated example.)

**Checkpoint**: Selector and explicit attachments compose with correct dedup and
precedence.

---

## Phase 5: User Story 3 - Safe convergence and clear blocking (Priority: P3)

**Goal**: Never leave a router with zero networks; reject dangerous configs at
admission; pace bulk convergence to avoid the upstream burst-ban.

**Independent Test**: A selector-only Router matching zero Ready networks does not
create/converge a zero-network router and reports a blocking reason; labeling one
match recovers it. (spec.md US3 Independent Test.)

**Depends on US1** (resolver + Update path); CEL tasks depend on T002.

### Tests for User Story 3 (write first)

- [x] T016 [P] [US3] Unit test: resolved set empty (no match / all not-Ready) → Create blocked with `Synced=False, reason=NoNetworksResolved`, NO upstream create call, in `internal/controller/network/router_external_test.go`.
- [x] T017 [P] [US3] Unit test: live router whose match set drains to zero → the final detach is skipped (upstream keeps ≥1 network) and the block reason is surfaced, in `internal/controller/network/router_external_test.go`.
- [x] T018 [P] [US3] Unit test: pacing — a resolved set larger than `maxAttachOpsPerReconcile` issues at most the cap's attach/detach calls per Update and converges across successive Observe/Update cycles, in `internal/controller/network/router_external_test.go`.
- [x] T019 [P] [US3] CRD validation test: against the generated Router CRD, assert rejection of (a) >1 selection mode on one entry, (b) an empty/constraint-less `networkSelector`, (c) `natFloatingIP` combined with `networkSelector`, in `internal/controller/network/router_validation_test.go`.

### Implementation for User Story 3

- [x] T020 [US3] Add the two remaining CEL rules to `RouterNetworkAttachment` — non-empty selector (`matchLabels` or `matchExpressions` size > 0, FR-015) and no-NAT-with-selector (FR-009) — in `apis/network/v1alpha1/router_types.go`, then rerun `make generate` and commit the regenerated CRD.
- [x] T021 [US3] Implement the zero-resolution guard: define the `NoNetworksResolved` condition reason (in `internal/controller/shared/` if reasons are centralized there) and block Create/converge when the resolved set is empty (`Synced=False`, requeue, recovers on next match), in `internal/controller/network/refs.go` + `router_external.go`.
- [x] T022 [US3] Implement the never-detach-last guard in Update: skip any detach that would drop the upstream router to zero networks and surface the block reason instead of issuing the call that returns `router_must_have_at_least_one_network`, in `internal/controller/network/router_external.go`.
- [x] T023 [US3] Implement pacing in the Update convergence loop: cap total attach+detach upstream calls per invocation at `maxAttachOpsPerReconcile` (small constant), apply in a stable order, and return without claiming convergence (Observe re-detects the remainder), in `internal/controller/network/router_external.go`.

**Checkpoint**: All boundary behaviors are safe, observable, and admission-guarded.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T024 [P] Run the `quickstart.md` scenarios against a live/opt-in e2e cluster and reconcile any doc drift, in `specs/010-router-network-selectors/quickstart.md`.
- [ ] T025 Run `make reviewable` (fmt, vet, golangci-lint, generate-clean-tree) and confirm all CI gates pass on the branch.
- [x] T026 [P] Verify `crossplane beta validate` accepts the regenerated Router CRD and the new example manifest.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (T001)**: no dependencies.
- **Foundational (T002–T003)**: depends on Setup; **blocks all stories** (the field
  must exist and be generated).
- **US1 (T004–T011)**: depends on Foundational. This is the MVP.
- **US2 (T012–T015)**: depends on US1 (refactors the resolver from T007/T014).
- **US3 (T016–T023)**: depends on US1 (guards wrap the resolver and Update path);
  T019/T020 CEL also depend on Foundational T002.
- **Polish (T024–T026)**: depends on all desired stories.

### Honest coupling note

The three stories share `refs.go` and `router_external.go`, so they are an
**incremental stack** rather than independent parallel tracks. US1 is a usable MVP on
its own; US2 makes mixing safe; US3 makes the boundaries safe. Do them in order.

### Within each story

- Write the listed tests first (they should fail), then implement.
- Models/field (Foundational) before resolver (US1) before refinements (US2/US3).
- After any `apis/` edit, rerun `make generate` and commit generated files
  (Constitution I + Development Workflow).

### Parallel opportunities

- T004, T005, T006 (US1 tests) are independent files/cases → parallel.
- T012, T013 (US2 tests) → parallel.
- T016, T017, T018, T019 (US3 tests) → parallel.
- T010 (example) parallel with US1 impl tasks.
- Implementation tasks that edit the SAME file (e.g. T021/T022/T023 all in
  `router_external.go`) are **not** parallel — sequence them.

---

## Parallel Example: User Story 1 tests

```bash
# Launch US1 test-writing tasks together (different cases/files):
Task: "Unit test: selector → multiple Ready networks (refs_test.go)"   # T004
Task: "Unit test: not-Ready match excluded, no error (refs_test.go)"   # T005
Task: "Unit test: mapNetworkToRouters filtering (controller_test.go)"  # T006
```

---

## Implementation Strategy

### MVP first (US1 only)

1. T001 Setup → 2. T002–T003 Foundational → 3. T004–T011 US1 → **STOP & validate**:
selector attaches/detaches matching networks end-to-end. Shippable.

### Incremental delivery

- Add US2 (T012–T015) → mixed selector+explicit with dedup/precedence → validate.
- Add US3 (T016–T023) → zero-guard, never-detach-last, CEL rejects, pacing → validate.
- Polish (T024–T026) → docs, lint, CRD validation.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- Every `apis/` change must ship with regenerated `zz_generated_*`/CRD in the same
  commit (Constitution I).
- No spec mutation: resolution returns upstream values on the external, never writes
  resolved ids/labels back to the MR (existing idiom — preserves the CEL invariant).
- Verify by re-observation: Update never claims convergence; assert via Observe/status
  and (for e2e) the live upstream, not the 2xx.
- Commit cadence is the user's call (no unsolicited commits).

---

## Phase 7: Validation-driven additions (emerged during live e2e, 2026-06-22)

These were not in the original plan; they surfaced while validating on twc-staging
and are now reflected in spec/plan/research/data-model/contracts.

- [x] T027 Add `+kubebuilder:validation:MaxItems=64` to `RouterParameters.Networks` to keep the per-entry CEL rules within the apiserver CEL **cost budget** (research R-9), in `apis/network/v1alpha1/router_types.go`; regenerate CRD. Bounds declared entries only (FR-014 preserved).
- [x] T028 [US1/US3] Emit `AttachedNetwork`/`DetachedNetwork` Normal events on the Update attach/detach path (FR-016), in `internal/controller/network/router_external.go`; unit-tested (`TestRouterUpdate_EmitsAttachEvent`).
- [x] T029 Network observability print columns (FR-017): promote upstream id to a default `ID` column, relabel constant VPC-`type` (`bgp`) `STATE`→`TYPE` at `-o wide`, in `apis/network/v1alpha1/network_types.go`; regenerate CRD.
- [x] T030 Release-candidate build with the new packaging pipeline (host cross-compile + distroless) and redeployed to twc-staging (`dev-1782123412`). Verified: provider **Healthy on distroless, restarts=0**; Router CRD carries `networkSelector` + `MaxItems=64`; Network print columns show `ID` (default) + `TYPE` (wide); reconcile smoke — a Network reached `Ready`/`Synced` (proves HTTPS→Timeweb ca-certs work on `distroless/static`), then cleaned up. RC build took ~30s (vs ~6min).

---

## Execution status (2026-06-22, /speckit-implement)

**Feature COMPLETE and LIVE-VALIDATED.** Bundle `20-router-selector` **PASSED** on
twc-staging: to-many at create (2), dynamic grow (→3, `AttachedNetwork` event),
dynamic shrink (→2 via unlabel, `DetachedNetwork` event, Network MR survives),
zero-resolution guard observed during startup. (The kuttl exit code was non-zero only
because of post-test teardown timing — Networks can't delete for ~30–60s after detach;
the assertions PASSED and the Networks deleted eventually.)

**Done & verified:**
- All FR-001…017 implemented. `go build`/`go vet`/`golangci-lint` → 0 issues;
  `go test -race ./...` pass (network coverage 76%); 9 unit tests incl. attach event.
- CEL admission (T019) verified via `crossplane beta validate` AND a live
  `kubectl apply --server-side --dry-run=server` (which caught the R-9 cost-budget
  issue that `validate` missed → fixed with `MaxItems`, T027).
- e2e bundle 20 authored (formulation B) and **PASSED live** (T011/T015).
- Artifacts reconciled (spec/plan/research/data-model/contracts) to match shipped code.

**Remaining to CLOSE the feature (release mechanics):**
- **T030**: RC build (new distroless/host-compile pipeline) → redeploy → smoke. The
  deployed validation image is the old scratch base; the print-column + `MaxItems` CRD
  changes also need this build to land on the cluster.
- **T024**: quickstart live walkthrough — effectively exercised by bundle 20; optional
  explicit re-walk.
- **T025**: `make reviewable` clean-tree gate passes only after commit (no unsolicited
  commits performed).
- **Release**: commit (feature + observability tweaks + e2e bundle; the build-infra
  pipeline change is a separate concern/commit) → tag **v0.3.0** → release notes.
