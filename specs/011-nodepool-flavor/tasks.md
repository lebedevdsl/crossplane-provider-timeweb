---
description: "Task list for feature 011-nodepool-flavor"
---

# Tasks: Nodepool Worker Configurator Flavor

**Input**: Design documents from `/specs/011-nodepool-flavor/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: INCLUDED — Constitution Principle III mandates unit tests for any managed-resource
change (success + not-found + transient + terminal paths), fake client, no live HTTP.

**Organization**: by user story (US1 P1 → US2 P2 → US3 P3), after shared foundational work.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no incomplete dependency)
- Paths are repo-relative.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Confirm the catalog/tag assumptions the design rests on; no project scaffolding needed (existing module).

- [X] T001 Confirm worker-family tags in the live `/api/v1/configurator/k8s` catalog match research R-1 (`k8s_configurator_general` = id 59 ru-3, `k8s_configurator_dedicated_cpu` = id 69 ru-3); record any drift in `specs/011-nodepool-flavor/research.md`.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The API field + generated artifacts every story depends on. No behavior is wired yet.

- [X] T002 Add `Flavor string` to the nodepool worker `Resources` struct in `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go` with markers `+kubebuilder:validation:Enum=standard;dedicated-cpu`, `+kubebuilder:default=standard`, `+optional` (do NOT add the field to the cluster master `Resources`).
- [X] T003 Run `make generate` and commit the regenerated CRD YAML under `package/crds/` and `apis/kubernetes/v1alpha1/zz_generated.deepcopy.go` (Constitution I — same PR).
- [X] T004 Verify the generated nodepool CRD shows `flavor` as an enum (`standard`/`dedicated-cpu`) with default `standard` and that the master cluster CRD has no `flavor` field (`grep` the generated YAML).

**Checkpoint**: API surface exists and is enum-validated; no selection behavior yet.

---

## Phase 3: User Story 1 — Small worker pool provisions on the general family (Priority: P1) 🎯 MVP

**Goal**: Default (`standard`) worker pools resolve to `k8s_configurator_general`, so small ratios (e.g. 2cpu/2GB) provision instead of being rejected by the dedicated family's hidden floor.

**Independent test**: nodepool `resources:{cpu:2,ramGB:2,diskGB:40}` with no `flavor` resolves to the general configurator and reaches Ready.

### Implementation

- [X] T005 [US1] Add `RequireTags []string` to `ConfiguratorInput` in `internal/controller/shared/resolver/resolver.go` (doc: entry eligible only if `entry.Tags ⊇ RequireTags`; empty = unconstrained).
- [X] T006 [US1] Add the tag-filter step to `SelectConfigurator` in `internal/controller/shared/resolver/select_configurator.go` — applied AFTER the capability (sizing) filter and BEFORE the standard/promo partition and tightest-fit sort; on empty survivor set return `NoConfiguratorAvailableError` citing the required tag(s).
- [X] T007 [US1] Map flavor→tag in `resolveK8sConfigurator` (`internal/controller/kubernetes/configurator.go`): accept the flavor, set `RequireTags=["k8s_configurator_general"]` for `standard`/unset, `["k8s_configurator_dedicated_cpu"]` for `dedicated-cpu`; apply only for `DimKubernetesWorkerConfigurator` (master passes none).
- [X] T008 [US1] Wire `cr.Spec.ForProvider.Resources.Flavor` (default empty→standard) into the `resolveK8sConfigurator` call in `internal/controller/kubernetes/nodepool_external.go`.

### Tests

- [X] T009 [P] [US1] `internal/controller/shared/resolver/select_configurator_test.go`: given a catalog where tightest-fit alone would pick the dedicated entry (the 59/69 case), `RequireTags=[general]` selects the general entry; and `RequireTags=[]` reproduces the pre-change selection (regression guard).
- [X] T010 [P] [US1] `internal/controller/kubernetes/configurator_test.go` (or nodepool test): flavor `standard` and unset both map to the general tag for the worker dim; master resolution passes no `RequireTags`.

**Checkpoint**: MVP — default pools resolve to general; the 2cpu/2GB regression is fixed.

---

## Phase 4: User Story 2 — Opt into a dedicated-CPU pool (Priority: P2)

**Goal**: `flavor: dedicated-cpu` resolves to `k8s_configurator_dedicated_cpu`, never crossing to general.

**Independent test**: nodepool `flavor: dedicated-cpu`, `resources:{cpu:2,ramGB:8,diskGB:40}` resolves to the dedicated configurator and reaches Ready.

### Tests & verification

- [X] T011 [P] [US2] `select_configurator_test.go` / `configurator_test.go`: `flavor: dedicated-cpu` maps to the dedicated tag and selects a dedicated entry even when a general entry is a tighter numeric fit (no cross-family pick).
- [X] T012 [US2] Add a dedicated-cpu example to `specs/011-nodepool-flavor/quickstart.md` validation block (already drafted — confirm it matches the shipped enum/defaults).

**Checkpoint**: both families selectable; selection never crosses families.

---

## Phase 5: User Story 3 — Clear error when sizing invalid for the chosen flavor (Priority: P3)

**Goal**: When no in-family configurator fits the sizing, surface a clear error naming the flavor/constraint; never substitute another family.

**Independent test**: `flavor: dedicated-cpu` with sizing below the family floor yields `Synced=False` with a message naming the flavor + constraint, and no general configurator is used.

### Implementation & tests

- [X] T013 [US3] Ensure the `NoConfiguratorAvailableError` from the tag/capability path surfaces the required tag (flavor) and the `ClosestRejected` in-family reason in `internal/controller/shared/resolver/select_configurator.go` / `errors.go`; confirm the kubernetes controller maps it to `Synced=False` with a descriptive reason (no silent fallback).
- [X] T014 [P] [US3] `select_configurator_test.go`: tag matches but sizing unsatisfiable → `NoConfiguratorAvailableError` with the in-family `ClosestRejected`; assert no entry from another tag is returned.

**Checkpoint**: error path is diagnosable; FR-005 satisfied.

---

## Phase 6: Polish & Cross-Cutting

- [X] T015 [P] Add a `flavor: standard` (default) and a `flavor: dedicated-cpu` example manifest under `examples/kubernetes/` (or the existing examples dir) per quickstart.
- [X] T016 [P] Draft `docs/release-notes/v0.3.2.md` (terse): nodepool `flavor` selector (standard/dedicated-cpu), default standard, fixes auto-pick of dedicated-cpu.
- [X] T017 Run `make reviewable` (lint + tests + clean-tree-after-generate) and confirm green.
- [ ] T018 (Optional) Live-validate on `twc-staging`: a `standard` 2cpu/2GB pool reaches Ready, and a `dedicated-cpu` pool reaches Ready; re-observe the locked configurator id per family.

---

## Dependencies & Execution Order

- **Setup (T001)** → **Foundational (T002–T004)** → user stories.
- **US1 (T005–T010)** is the MVP and unblocks US2/US3 (they reuse the same resolver path).
- **US2 (T011–T012)** and **US3 (T013–T014)** depend on US1's resolver/mapping changes; they are mostly verification + the error refinement.
- **Polish (T015–T018)** after the stories.

## Parallel Opportunities

- T009 and T010 (US1 tests) — different files, run in parallel after T005–T008.
- T011 (US2) and T014 (US3) tests — parallel once their code paths exist.
- T015 and T016 (examples, release notes) — parallel.

## Implementation Strategy

- **MVP = Phase 1 + 2 + US1**: ships the regression fix (default `standard`/general) — the highest-value slice.
- US2 and US3 are thin increments (the resolver already handles both via the same tag path); they add verification + the error refinement.
- Tests are mandatory (Constitution III), authored alongside each story's implementation.
