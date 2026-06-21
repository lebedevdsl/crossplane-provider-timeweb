---
description: "Task list for 009-stabilization-bugfixes — observability + e2e reliability + custom-sizing + auto-net traceability + release hygiene. Additive only."
---

# Tasks: Stabilization & Bugfixes (live-e2e hardening round)

**Input**: `specs/009-stabilization-bugfixes/` (plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md).

**Scope**: Polishing existing resources — additive `status` fields + printcolumn edits + resolver classification + e2e harness robustness + release hygiene. No new kinds. Two `[VERIFY]` tasks gate on a live probe (bundle 17 + node GET, running now).

**Constitution gates**: every `apis/` edit → `make generate-crds` + commit generated files (Dev Workflow); every controller change → fake-client unit tests (III); `Observe` stays read-only (II); all schema changes additive (I).

## Format: `[ID] [P?] [Story?] Description`

## Implementation status — 2026-06-21 (as-built)

**DONE (built, unit-tested, deployed on dev-1782016513, partially live-validated):**
- US1: T006 `PUBLIC` rename ✅live · T007/T008 clusterID-on-Observe ✅+test · T009 doc-only (R-2) · T010/T011/T012 server AZ ✅live+test
- US2: T003 context retry · T013 location fixes · T014 region params (`TWE_LOCATION`/`TWE_AZ`, k8s bundles) · T015 parallel comment
- US3: T016/T017 no-op (R-1: worker needs no gpu) · T018 standard-family · T019 clear no-orderable error · T020 resolver test
- US4: T021/T022/T023 cluster `autoCreatedNetworkID` ✅live+test
- US5: T024 `--debug` off ✅live
- Polish: T028 rebuild+deploy ✅ · T031 full `go test ./...` green ✅ · T033 memory (R-1/R-2) ✅

**Both `[VERIFY]` RESOLVED live**: R-1 (k8s worker no gpu), R-2 (no per-node public IP).

**Live-validated this session**: server AZ (`spb-3`), cluster autonet id, `PUBLIC` column rename. `CLUSTER`-column populate is unit-test-proven; live confirm pending the smoke cluster reaching Ready.

**REMAINING (need user/billable decisions)**: T025 cut release semver · T026 validate bundle 19 (private cluster, billable) · T027 quirks support note · T029 full billable re-verify on the 009 build · T030 post-run VPC sweep · T032 docs update.

---

## Phase 1: Setup

- [X] T001 Spec artifacts generated (spec/plan/research/data-model/contracts/quickstart). Source findings: `specs/_next-008-followups.md`.
- [ ] T002 Confirm clean baseline: `go build ./... && go test ./... && make generate-crds` produce no diff before starting (on the 008 working tree).

## Phase 2: Foundational (blocking prerequisites)

- [X] T003 **Context-flake retry** (FR-005): `test/e2e/scripts/kuttl.sh` context-existence check retries 5× w/ 2s backoff before aborting (explicit-context safety unchanged). *(done — unblocks the live probe re-runs)*
- [X] T004 **[VERIFY] R-1 — k8s-worker gpu**: RESOLVED live (bundle 17, twc-staging) — worker nodegroup created without gpu (`Synced=True`); the k8s endpoint does NOT enforce gpu. **No worker fix** (T016 no-op).
- [X] T005 **[VERIFY] R-2 — node public-addr**: RESOLVED — `NodeOut` exposes only `node_ip` (private) + `network` (bandwidth integer); **no per-node public IPv4 field**. T009 → doc-only.

## Phase 3: User Story 1 — Accurate at-a-glance resource state (P1)

**Goal**: every reconciled kind shows correct, non-empty, correctly-labeled columns + status.
**Independent test**: apply cluster+nodepool+server → `CLUSTER` populated, `PUBLIC` labeled as a flag, server AZ in status, node IPs complete.

- [ ] T006 [US1] `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go` — rename printcolumn `PUBLIC-IP` → `PUBLIC` (JSONPath unchanged, `.spec.forProvider.publicIP`). Run `make generate-crds`.
- [ ] T007 [US1] `internal/controller/kubernetes/nodepool_external.go` — populate `status.atProvider.ClusterID` from the resolved parent on **every Observe** (in/after `populateNodepoolStatus`), not only Create (`:231`). (FR-001)
- [ ] T008 [P] [US1] `internal/controller/kubernetes/nodepool_external_test.go` — fake-client unit test: after an Observe of an existing nodepool, `ClusterID` is populated (regression for the empty-CLUSTER bug).
- [X] T009 [US1] **RESOLVED (R-2 = no public-IP field)** → doc-only: `NodeOut` has no per-node public IPv4 (only `node_ip` + bandwidth int). No status field added; `docs/` (T032) will note public reachability is the nodepool `publicIP` flag, not a per-node address. (FR-003)
- [ ] T010 [US1] `apis/compute/v1alpha1/server_types.go` — add `status.atProvider.availabilityZone *string` (additive). Run `make generate-crds`.
- [ ] T011 [US1] `internal/controller/compute/server_external.go` — mirror the resolved/observed AZ into `status.atProvider.availabilityZone` in Observe; SHOULD emit a condition/event when `spec.availabilityZone` ≠ observed (preset override). (FR-004)
- [ ] T012 [P] [US1] `internal/controller/compute/server_external_test.go` — unit test: observed AZ mirrored; override signal when spec≠observed.

## Phase 4: User Story 2 — Trustworthy live end-to-end validation (P1)

**Goal**: suite runs to completion; no flake-aborts; every bundle targets a fulfillable region; opt-in parallel.
**Independent test**: two full runs, zero flake-aborts, zero region-mismatch failures.

- [X] T013 [US2] Region location fixes already applied to bundles `13`, `14`, `16`, `17` (ru-1→ru-3/msk-1; added required `location`). *(done this session)*
- [ ] T014 [US2] **Region parameterization** (FR-006): add `TWE_LOCATION=ru-3` / `TWE_AZ=msk-1` to `test/e2e/presets.local.env`; replace hardcoded `location:`/`availabilityZone:` in ALL `test/e2e/kuttl/tests/**/*.yaml` with `${TWE_LOCATION}`/`${TWE_AZ}` (co-located resources share `${TWE_AZ}`).
- [ ] T015 [US2] **Opt-in parallelism** (FR-007): update the obsolete `parallel: 1` rationale comment in `test/e2e/kuttl/kuttl-test.yaml`; document the separate-`KUTTL_TEST`-jobs pattern + the account-quota ceiling in `test/e2e/README.md`.

## Phase 5: User Story 3 — Custom-sizing works and degrades gracefully (P2)

**Goal**: custom server + k8s-worker provision in an orderable region; clear error when none orderable; prefer standard family.
**Independent test**: custom server + worker reach Ready (ru-3); a promo-only region yields a named error.

- [X] T016 [US3] **NO-OP (R-1 resolved)**: the k8s worker create does NOT require `gpu` (live-confirmed) — no change to the worker body.
- [X] T017 [P] [US3] N/A — T016 is a no-op, no gpu test needed for the worker.
- [ ] T018 [US3] **Standard-family preference** (FR-010): `internal/controller/shared/resolver/select_configurator.go` (+ tag exposure in `dimensions.go`) — rank non-promo standard-family configurators ahead of promo/legacy (tag-prefix allow/deny list, no price math) before the existing tightest-fit tiebreak.
- [ ] T019 [US3] **Clear no-orderable error** (FR-009): when only promo/legacy entries satisfy the size, return an error naming the real cause ("no orderable configurator for <location>/<size>: only promo/legacy …"), not a phantom-preset error.
- [ ] T020 [P] [US3] `internal/controller/shared/resolver/select_configurator_test.go` — unit tests: standard preferred over promo when both fit; only-promo → clear terminal error; tightest-fit unchanged within a partition.

## Phase 6: User Story 4 — Auto-created networks are traceable (P2)

**Goal**: the auto-VPC id is recorded on the owner (no delete, no sweep).
**Independent test**: create a network-less cluster → its `status.atProvider.autoCreatedNetworkID` is set.

- [ ] T021 [US4] `apis/kubernetes/v1alpha1/kubernetescluster_types.go` — add `status.atProvider.autoCreatedNetworkID *string` (additive). Run `make generate-crds`.
- [ ] T022 [US4] `internal/controller/kubernetes/cluster_external.go` — record the auto-created VPC id from the observed cluster/node network (source confirmed in R-7) into status on Observe; **read-only** — no delete, no sweep. (FR-011)
- [ ] T023 [P] [US4] `internal/controller/kubernetes/cluster_external_test.go` — unit test: a network-less cluster's observed auto-VPC id is mirrored to status; provider issues no delete call.

## Phase 7: User Story 5 — Release readiness (P3)

**Goal**: debug-free, clean-semver release validated incl. the private path.

- [ ] T024 [US5] `deploy/deploymentruntimeconfig.yaml` — confirm `--debug` is absent (already reverted in-file); re-apply the clean config to twc-staging before release. (FR-012)
- [ ] T025 [US5] Cut the release from the validated tree with a **clean semver** tag (e.g. `v0.1.0`), not a `dev-<ts>` iteration tag; update `deploy/provider.yaml`. (FR-013)
- [ ] T026 [US5] [VERIFY-LIVE] Run `TIMEWEB_E2E_PRIVATE=1 KUTTL_TEST=19-private-cluster` once on the release build → Ready (Router+NAT, `publicIP:false`). (FR-014)
- [ ] T027 [US5] Capture the upstream quirks (misleading "preset 0"; non-orderable promo configurators in catalog; Qrator egress ban) into a Timeweb support note / `_next` doc, per the quirk-capture practice. (FR-015)

## Phase 8: Polish & cross-cutting

- [ ] T028 Rebuild a `dev-<ts>` image with all 009 controller/apis changes; redeploy to twc-staging (bump re-pull annotation).
- [ ] T029 [VERIFY-LIVE] Full suite re-verify on the 009 build (09/11/12/13/14/16/17/18 + the [VERIFY] resolutions), parameterized region — zero flake-aborts, zero region-mismatch, all real pass.
- [ ] T030 Live-API orphan check after the run (VPCs incl. the auto-net trio + any leftovers); confirm no NEW orphans beyond expected auto-VPCs.
- [ ] T031 [P] `make lint` + `go test ./...` green; `make generate` clean (no uncommitted generated diff); `make validate-examples` passes.
- [ ] T032 [P] Update `docs/` (kubernetes.md / README) for the `PUBLIC` column, server AZ status, auto-net traceability, parameterized e2e region, opt-in parallel.
- [ ] T033 [P] Sync memory + `specs/009-stabilization-bugfixes/` with as-built notes (R-1/R-2 outcomes, standard-family classification, region params).

---

## Dependencies & execution order

- **Phase 1–2** first. **T003 (context-flake) done** → unblocks **T004/T005 probes** (running).
- **US1 (P1)** + **US2 (P1)** are the MVP — independent, parallelizable across files.
- **US3** T016/T017 gate on **T004 (R-1)**; T018–T020 are independent.
- **US1** T009 gates on **T005 (R-2)**.
- **US4** independent.
- **US5** + **Polish** last (need all code changes → one rebuild T028 → re-verify T029).

## Parallel opportunities

- US1: T006 ∥ T010 (different `apis/` files); T008 ∥ T012 (tests).
- US3: T018/T019 (resolver) ∥ US4 T021/T022 (cluster) — different packages.
- Polish: T031 ∥ T032 ∥ T033.

## Implementation strategy (MVP-first)

1. **MVP = US1 + US2** (observability + reliable e2e) — the highest-value, lowest-risk polish; independently shippable.
2. Resolve the two **[VERIFY]** probes (T004/T005) early (live run already in flight) so US1-T009 and US3-T016 are decided from facts.
3. US3 (custom-sizing robustness) + US4 (auto-net traceability) next.
4. US5 + Polish: one rebuild → full live re-verify → release gate.
5. `[VERIFY-LIVE]` gates truth (re-observation), per the project's verify-by-reobservation rule.
