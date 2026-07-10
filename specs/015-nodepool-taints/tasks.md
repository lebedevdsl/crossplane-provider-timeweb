# Tasks: Nodepool Taints (+ label mutability)

**Input**: Design documents from `/specs/015-nodepool-taints/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Unit tests are MANDATORY (constitution III) and included per story.
The kuttl bundle is authored as a durable artifact; the live gate runs the
lighter custom-manifest walk (plan.md "Validation strategy").

**Organization**: grouped by user story; US1 create-path is the MVP slice.

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup

*(No setup tasks — existing provider module, toolchain, and harness carry
forward unchanged.)*

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: wire the upstream surface + CRD schema every story builds on.

- [X] T001 Hand-patch `docs/openapi-timeweb.json`: add `Taint` schema
      (`{key, value, effect}`, required key+effect), add `taints` array to
      `NodeGroupIn`, add `PATCH /api/v1/k8s/clusters/{cluster_id}/groups/{group_id}`
      operation (operationId `UpdateClusterNodeGroup`, body
      `{name?, labels?: []SetLabels, taints?: []Taint}` → 200
      `NodeGroupResponse`) per contracts/timeweb-nodegroup-patch.md
- [X] T002 Regenerate the client (`make generate-client`) and verify
      `UpdateClusterNodeGroup` + `Taint` exist in
      `internal/clients/timeweb/generated/zz_generated_client.go` (build passes)
- [X] T003 Add `NodepoolTaint` type + `Taints []NodepoolTaint` field
      (MaxItems=12, key/value patterns + lengths, effect enum, type-level CEL
      unique-(key,effect) rule) and update the `Labels` comment (day-2 mutable)
      in `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go` per
      research.md R-6
- [X] T004 Regenerate DeepCopy + CRD YAML (`make generate-crds`) and commit
      artifacts under `apis/kubernetes/v1alpha1/zz_generated.deepcopy.go` +
      `package/crds/`

**Checkpoint**: schema + client surface ready — story work can begin.

---

## Phase 3: User Story 1 — Provision a dedicated worker group with taints (Priority: P1) 🎯 MVP

**Goal**: nodes join the cluster already carrying the declared taints.

**Independent Test**: create a nodepool manifest with a taint; upstream group
echoes it; node objects carry it from join (quickstart §1).

- [X] T005 [US1] Marshal `spec.forProvider.taints` (spec order, nil value →
      `""`) into the create body in `buildCreateNodeGroupBody`,
      `internal/controller/kubernetes/nodepool_external.go`
- [X] T006 [US1] Unit tests: create body includes taints (with/without
      value; no-taints omits field) in
      `internal/controller/kubernetes/nodepool_external_test.go`

**Checkpoint**: create-with-taints functional — MVP.

---

## Phase 4: User Story 2 — Taints survive node lifecycle events (Priority: P2)

**Goal**: scale-up/autoscale/autoheal nodes carry the group's sets (upstream
guarantee — verify, don't implement).

- [X] T007 [P] [US2] Author the scale-up taint-persistence step in the kuttl
      bundle: `test/e2e/kuttl/tests/22-nodepool-taints/` (nodeCount bump +
      assert group still reports declared taints; condition-TYPE waits)

**Checkpoint**: lifecycle persistence asserted by the durable e2e artifact.

---

## Phase 5: User Story 3 — Day-2 taint and label updates with drift correction (Priority: P2)

**Goal**: edits converge in place via owned-fields PATCH; out-of-band edits
reverted; empty set clears.

- [X] T008 [US3] Extend the Observe decode struct (`nodeGroupBody`) with
      `Labels`/`Taints` and add order-insensitive set-diff helpers
      (`metadataUpToDate`: taints identity (key,effect), equality incl.
      ""-coalesced value; labels map⇄array fold) wired into
      `isNodepoolUpToDate` in `internal/controller/kubernetes/nodepool_external.go`
      (research.md R-5)
- [X] T009 [US3] Add the metadata-convergence leg to `Update` — one
      `UpdateClusterNodeGroup` PATCH carrying ONLY `name`+`labels`+`taints`
      (full-set replace), placed BEFORE the autoscaling early-return, errors
      through `timeweb.Classify` — in
      `internal/controller/kubernetes/nodepool_external.go` (R-4, R-7)
- [X] T010 [US3] Unit tests in
      `internal/controller/kubernetes/nodepool_external_test.go`: up-to-date
      despite order/representation differences; drift ⇒ PATCH issued with
      owned fields only; empty declared sets ⇒ PATCH with `[]` (clear);
      autoscaling-enabled pool still converges metadata (and skips count);
      PATCH transient error ⇒ classified requeue; PATCH terminal error ⇒
      classified surface; not-found path

**Checkpoint**: full day-2 mutability + single-writer reversion functional.

---

## Phase 6: User Story 4 — Invalid taint declarations are rejected upfront (Priority: P3)

**Goal**: bad effect / bad key / duplicate (key,effect) rejected at admission.

- [X] T011 [P] [US4] Add `examples/kubernetes/nodepool-taints.yaml` (valid
      dedicated-pool example) and verify `make validate-examples` passes
- [X] T012 [US4] Verify admission rejections against a live apiserver
      (server-side dry-run on the e2e control plane): unknown effect,
      malformed key, duplicate key+effect, >12 items — messages match
      contracts/nodepool-taints-v1alpha1.md (CEL cost-budget check from
      feature 007 applies here)

**Checkpoint**: all four stories complete.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T013 [P] Complete kuttl bundle `test/e2e/kuttl/tests/22-nodepool-taints/`
      (create-with-taints assert → day-2 edit step → clear step → delete),
      registered in the harness like bundles 12/13 (README table row)
- [X] T014 [P] Refresh `docs/kubernetes.md`: taints section + labels
      mutability note (quickstart.md §3/§4 content)
- [X] T015 Run `make reviewable` (generate + lint + test; clean tree)
- [X] T016 Live validation gate per plan.md: `make e2e.up` + `make e2e.deploy`,
      apply the custom minimal-nodepool manifest (flat `clusterID` to the
      pre-existing Ready cluster) and walk create-with-taints → node
      propagation (read-only kubeconfig check) → day-2 edit (public-host
      PATCH proof) → empty-set clear → out-of-band revert → delete;
      re-observe upstream at each step
- [X] T017 Release prep: version bump to v0.6.0, release notes (taints +
      labels-mutability contract change highlighted per constitution),
      package build + publish, GitHub release

---

## Dependencies & Execution Order

- **Phase 2** blocks everything; run T001→T002 and T003→T004 (T001/T003 can
  start together — different files).
- **US1 (T005-T006)**: after Phase 2. MVP checkpoint.
- **US2 (T007)**: independent of US1 code (bundle artifact); needs T003/T004
  schema for manifests.
- **US3 (T008-T010)**: after Phase 2; same file as US1 tasks — sequence
  after T005 to avoid conflicts.
- **US4 (T011-T012)**: after T004 (CRD YAML); T012 needs the e2e control
  plane (can fold into the T016 session).
- **Polish**: T013/T014 parallel any time after their inputs; T015 before
  T016; T016 before T017.

## Parallel Opportunities

- T001 ∥ T003 (openapi vs types file)
- T007 ∥ US3 tasks (bundle files vs controller files)
- T011 ∥ T013 ∥ T014 (examples / bundle / docs)

## Implementation Strategy

Sequential single-implementer flow: Phase 2 → US1 → US3 (same file,
natural continuation) → US2/US4 artifacts → polish → live gate (T016
validates US1+US3 end-to-end and US4 dry-runs in one session) → release.
