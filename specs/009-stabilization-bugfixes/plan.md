# Implementation Plan: Stabilization & Bugfixes (live-e2e hardening round)

**Branch**: `009-stabilization-bugfixes` (to be created off `008-packaging` once
008 is committed) | **Date**: 2026-06-21 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/009-stabilization-bugfixes/spec.md`

## Summary

A stabilization round consolidating the findings surfaced while running the live
e2e suite for 008 on the Timeweb staging cluster. Scope: **observability**
(populate/relabel nodepool + server status/columns), **e2e reliability**
(context-flake retry, region parameterization, opt-in parallelism), **custom
sizing** (verify the k8s-worker path, prefer non-promo standard configurators,
clear errors when none are orderable), **auto-network traceability** (record the
auto-created VPC id on the owner — no delete, no sweep), and **release hygiene**
(debug off, clean semver, validate the private-cluster path). Two new features
(server SSH-key runtime mgmt; dataplane delete-guard annotations) are explicitly
OUT of scope. Technical approach: additive `status.atProvider` fields + printcolumn
edits (regenerate CRDs), a small resolver classification change, a bash-harness
retry + env parameterization, and a release checklist — all backed by fake-client
unit tests per Constitution III.

## Technical Context

**Language/Version**: Go (tracks latest stable, per `project_go_tooling_policy`); module floats deps.

**Primary Dependencies**: crossplane-runtime v2 (namespaced MRs), controller-runtime, controller-gen (CRD/deepcopy generation), oapi-codegen-generated Timeweb client (`internal/clients/timeweb/generated`, hand-patched superset), kuttl (e2e), golangci-lint via `go run`.

**Storage**: N/A (stateless controllers; state is the Timeweb API + CR status).

**Testing**: `go test` with the fake Timeweb client (`timeweb.FakeClient`); kuttl bundles for live e2e against `twc-staging`.

**Target Platform**: Linux container (multi-arch), runs as a Crossplane provider Pod; reconciles the Timeweb Cloud API.

**Project Type**: Single project — Crossplane infrastructure provider (Go).

**Performance Goals**: No regression. Client request rate stays ≤ 2 r/s globally (Qrator-safe, from 008); reconcile throughput unchanged.

**Constraints**: CRD changes MUST be additive (Constitution I); `Observe` MUST stay read-only (II); every controller change MUST ship fake-client unit tests (III); after any `apis/` edit, regenerate + commit `zz_generated_*` + `package/crds` in the same change (Dev Workflow). No new external dependencies (use stdlib/community tools).

**Scale/Scope**: 10 reconciled MR kinds; ~5 controllers touched (kubernetes, compute, shared/resolver); the bash e2e harness; no new kinds.

## Constitution Check

*GATE: Must pass before Phase 0. Re-checked after Phase 1.*

| Principle | Assessment |
|---|---|
| **I. CRD Contract Stability (additive only)** | ✅ PASS. All schema touches are **additive**: new `status.atProvider` fields (nodepool `clusterID` already exists — only its *population* changes; node public address; server resolved AZ; owner's auto-network id) and printcolumn edits. The `PUBLIC-IP`→`PUBLIC` nodepool column is a **printcolumn rename**, not a spec-field rename — no API field changes, no breakage. Kinds are `v1alpha1` (pre-`v1beta1`), so even non-additive would be permitted, but we stay additive anyway. CRDs + `zz_generated_*` regenerated and committed in the same change. |
| **II. Idempotent, Side-Effect-Aware Reconciliation** | ✅ PASS. New status writes happen in the existing `Observe`/`Create` paths and are read-only w.r.t. upstream (mirroring observed values). The auto-network id is recorded from data already returned by Observe — no extra mutating call. FR-009 (clear "no orderable configurator" error) and the misleading-error fixes strengthen the transient/terminal classification (errors never silently swallowed). FR-011 explicitly does NOT delete (no new side effect). |
| **III. Controller Test Discipline** | ✅ PASS. Each controller change ships fake-client unit tests (success + transient + terminal where relevant), following the established `TestServerCustomSizing` gpu-regression pattern. No live HTTP in unit tests. |
| **Provider Constraints** (credentials, runtime compat, structured logs) | ✅ PASS. No credential-handling change; runtime version unchanged; new observability uses the existing structured logger + status, not new log sinks. |
| **Dev Workflow** (regenerate after `apis/`) | ✅ PASS — `make generate-crds` after every `apis/` edit; generated files committed together. |

**No violations → Complexity Tracking is empty.**

## Project Structure

### Documentation (this feature)

```text
specs/009-stabilization-bugfixes/
├── plan.md              # This file
├── research.md          # Phase 0 — [VERIFY] resolution + decisions
├── data-model.md        # Phase 1 — status/column additions per kind
├── quickstart.md        # Phase 1 — operator + maintainer walkthrough
├── contracts/           # Phase 1 — column/status, resolver, harness contracts
└── tasks.md             # Phase 2 — /speckit-tasks (not created here)
```

### Source Code (repository root) — files this feature touches

```text
apis/
├── kubernetes/v1alpha1/kubernetesclusternodepool_types.go   # PUBLIC-IP→PUBLIC; CLUSTER populated; node public addr
├── kubernetes/v1alpha1/kubernetescluster_types.go           # auto-network id in status (US4)
└── compute/v1alpha1/server_types.go                         # resolved availabilityZone in status (US1)

internal/controller/
├── kubernetes/nodepool_external.go        # populate clusterID + node public addr in Observe
├── kubernetes/cluster_external.go         # record auto-created network id in status
├── compute/server_external.go             # mirror resolved AZ; (k8s-worker gpu fix only if VERIFY confirms)
└── shared/resolver/
    ├── select_configurator.go             # prefer non-promo standard family (FR-010); clear no-orderable error (FR-009)
    └── dimensions.go                       # surface configurator family/tag for classification

internal/clients/timeweb/generated/        # only if a hand-patch is needed (e.g. node public-addr field)

test/e2e/
├── scripts/kuttl.sh                        # context-check retry (FR-005); TWE_LOCATION/TWE_AZ (FR-006); opt-in parallel (FR-007)
├── kuttl/kuttl-test.yaml                   # drop outdated parallel:1 rationale; opt-in parallelism
├── kuttl/tests/**                          # region via ${TWE_LOCATION}/${TWE_AZ} (FR-006)
└── presets.local.env                       # seed TWE_LOCATION=ru-3 / TWE_AZ=msk-1

deploy/deploymentruntimeconfig.yaml         # ensure --debug OFF for release (US5)
docs/                                       # parallel-e2e + quota note; release runbook
```

**Structure Decision**: Single-project Crossplane provider — no new modules. Changes are surgical edits to existing `apis/`, `internal/controller/`, and `test/e2e/`, mirroring the layout used by features 003–008.

## Phase 0: Outline & Research

Two spec markers are `[VERIFY]` (live-reobservation), and three clarified
decisions need a concrete approach. See `research.md`. Summary of what Phase 0
resolves:

- **R-1 [VERIFY] k8s-worker custom create gpu** — does `/api/v1/k8s/clusters/{id}/groups` require an explicit `gpu` field like `/servers` did? Resolve by a targeted live probe (bundle 17 re-run after the context-flake fix). Masters need no gpu (settled).
- **R-2 [VERIFY] node public address** — do public-by-default k8s worker nodes carry a public address in the upstream `NodeOut.network` field we don't parse? GET a live node; decide whether to surface it (FR-003) or record that nodes are private-only.
- **R-3 Configurator family classification** — how to distinguish "non-promo standard family" from "promo/legacy" from the catalog `tags` (e.g. `msk_nvme` standard vs `discount35`/`ssd_2022`/`spb_gpu` promo) without price math (FR-009/FR-010).
- **R-4 Context-flake retry** — root cause + retry/backoff shape for the `kuttl.sh` context-existence precheck (FR-005).
- **R-5 Region parameterization** — `TWE_LOCATION`/`TWE_AZ` threading through bundles + presets seed; how `azLocation` maps interact (FR-006).
- **R-6 Opt-in parallelism** — mechanism (separate `KUTTL_TEST` jobs vs kuttl `parallel:N`) + quota guidance (FR-007).
- **R-7 Auto-network id capture** — where Observe/Create can read the auto-created VPC id to record on the owner (FR-011).

**Output**: `research.md` (all decisions resolved or marked as a live-probe task with a defined acceptance signal).

## Phase 1: Design & Contracts

1. **`data-model.md`** — per-kind additive status fields + printcolumn changes
   (nodepool: `clusterID` population + `PUBLIC`/`PUBLIC-NODES` column + node
   public address; server: resolved `availabilityZone`; cluster: auto-network
   id), with the "additive-only" validation rule and the column-naming
   convention (`READY · SYNCED · <domain> · [STATE] · ID · AGE`).

2. **`contracts/`** —
   - `observability.md`: the exact column set + status JSONPaths per kind (the
     contract the CRDs must satisfy).
   - `resolver-selection.md`: configurator selection ordering (standard-family
     preference; no-orderable error shape).
   - `e2e-harness.md`: context-retry behavior, `TWE_LOCATION`/`TWE_AZ` contract,
     parallel-run contract + quota note.

3. **Agent context update** — point the `<!-- SPECKIT START -->`…`END` block in
   `CLAUDE.md` at this plan.

**Output**: `data-model.md`, `contracts/*`, `quickstart.md`, updated `CLAUDE.md`.

## Complexity Tracking

No constitution violations — section intentionally empty.
