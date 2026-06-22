# Implementation Plan: Router Multi-Network Attachment & Selectors

**Branch**: `010-router-network-selectors` | **Date**: 2026-06-22 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/010-router-network-selectors/spec.md`

## Summary

Add a third network-selection mode — a label **selector** — to each `Router`
network attachment, alongside the existing `networkRef` / `networkID`. A selector
attachment expands **to-many**: it attaches every `Ready` `Network` in the router's
namespace whose labels match, and the attached set self-converges as networks are
created, (un)labeled, or deleted. The effective attachment set is the de-duplicated
union of all entries, with explicit (ref/id) entries winning on overlap. NAT stays
explicit (rejected on selector entries); the upstream "≥1 network" invariant is
pre-empted with a runtime zero-resolution guard; and large match sets converge
incrementally with paced upstream calls to avoid the Qrator burst-ban.

Technical approach: extend `RouterNetworkAttachment` with an optional
`*metav1.LabelSelector` field guarded by CEL (exactly-one-of trio; non-empty
selector; no NAT-with-selector); expand selectors inside `resolveRouterRefs` against
a namespace-scoped `client.List` gated on `Ready`+upstreamID (the existing readiness
gate); dedup/precedence in resolution; add a zero-resolution block; cap mutations
per reconcile in the Update convergence loop for pacing; and add a `Network → Router`
mapping `Watches` so new/changed networks re-enqueue matching routers promptly.
Status needs **no schema change** — `status.atProvider.networks` already mirrors the
upstream GET, so selector-resolved networks appear automatically (FR-011/SC-007).

### Implementation deltas (post-plan, from live e2e on twc-staging 2026-06-22)

Three additions emerged during validation, now reflected across the artifacts:
- **`networks` `MaxItems=64`** — required to keep the per-entry CEL rules within the
  apiserver CEL **cost budget** (research R-9). Bounds declared entries only; the
  resolved set stays unbounded (FR-014). `crossplane beta validate` misses cost
  violations — verify with `kubectl apply --server-side --force-conflicts
  --dry-run=server` against a real apiserver.
- **Attach/detach events** (`AttachedNetwork`/`DetachedNetwork`, FR-016) on the Update
  path — observability for the no-spec-edit set changes.
- **`Network` print columns** (FR-017): promote the upstream id to a default column;
  relabel the constant VPC-`type` (`bgp`) column to `TYPE` at `-o wide`.

The e2e validation used **formulation B**: create the networks → wait Ready → then
create the router, so the selector resolves to the full set at create (deterministic,
no readiness race). Bundle: `test/e2e/kuttl/tests/20-router-selector/` (PASS).

## Technical Context

**Language/Version**: Go (latest stable per `go.mod`; floats — see go-tooling policy)

**Primary Dependencies**: `crossplane-runtime/v2` (managed reconciler, conditions),
`crossplane/apis/v2/core/v2` (xpv2 references), `sigs.k8s.io/controller-runtime`
(builder `Watches`, `controller.Options` rate limiter, `handler.MapFunc`),
`k8s.io/apimachinery` (`metav1.LabelSelector`, `labels` conversion), existing
in-package Timeweb client + `resolver` cache.

**Storage**: N/A — desired state in the Router CRD, real state in the Timeweb API.

**Testing**: `go test` with the fake Timeweb client (constitution III); CEL admission
rules verified via the generated CRD / envtest-style validation tests; kuttl/k3d e2e
bundle for the selector scenarios (live e2e is opt-in / Timeweb-internal per feat 008).

**Target Platform**: Linux controller running in-cluster (Crossplane provider pod).

**Project Type**: Single Go module — Crossplane provider. Source under `apis/` and
`internal/controller/`.

**Performance Goals**: A single newly-matching Ready network attaches within one
reconcile interval (SC-002). Large match sets converge incrementally without bursting
upstream (FR-014). No latency target tighter than the poll interval is promised.

**Constraints**: Namespace-scoped resolution only (FR-010); no spec mutation (resolve
to upstream values carried on the external, never written back — preserves the
exactly-one-of CEL invariant); upstream requires ≥1 attached network at all times
(probe-verified `400 router_must_have_at_least_one_network`); upstream sits behind
Qrator and bans call bursts (paced mutations, conservative retries).

**Scale/Scope**: Match set is unbounded by contract (FR-014) but realistically tens
of networks per router; resolution lists Networks in one namespace per reconcile.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. CRD Contract Stability** — PASS. The change is **additive** to `Router`
  (`v1alpha1`, pre-beta): one new optional field on the attachment struct plus
  tightened CEL. No existing field changes meaning; existing ref/id manifests are
  untouched (FR-012). `zz_generated_*`, DeepCopy, and CRD YAML MUST be regenerated
  and committed in the same PR (`make generate`).
- **II. Idempotent, Side-Effect-Aware Reconciliation** — PASS. `Observe` stays
  read-only; selector expansion is a pure function of cluster state recomputed each
  reconcile. Paced, incremental attach/detach is idempotent (set-diff converges;
  re-invocation never duplicates). The zero-resolution guard prevents an invalid
  upstream call rather than emitting one. Errors classified transient (waiting on a
  not-yet-Ready match) vs terminal (e.g. impossible config) via the existing
  `ErrTargetNotReady` / classification idiom.
- **III. Controller Test Discipline** — PASS (planned). New unit tests with the fake
  client cover: selector → many networks (success); zero-match → block; matched-but-
  not-Ready exclusion; dedup + explicit-wins precedence; pacing (bounded ops per
  reconcile); never-detach-last-network. CEL rules (trio, non-empty selector, no
  NAT-with-selector) covered by validation tests against the generated CRD.
- **Provider Constraints** — PASS. No new credentials or token surfaces; user-facing
  state via standard `Synced`/`Ready` conditions; structured logging only.

No violations → **Complexity Tracking is empty**.

## Project Structure

### Documentation (this feature)

```text
specs/010-router-network-selectors/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── router-selector-v1alpha1.md   # CRD field + CEL + conditions contract
│   └── selector-resolution.md        # resolution / pacing / convergence contract
├── checklists/
│   └── requirements.md  # from /speckit-specify
└── tasks.md             # /speckit-tasks output (NOT created here)
```

### Source Code (repository root)

```text
apis/network/v1alpha1/
├── router_types.go          # +NetworkSelector field on RouterNetworkAttachment; CEL updates
└── zz_generated.deepcopy.go # regenerated (DeepCopy for new field)

internal/controller/network/
├── refs.go                  # resolveRouterRefs: selector expansion, dedup, precedence, zero-guard
├── router_external.go       # Update: paced attach/detach; never-detach-last guard
├── controller.go            # SetupRouter: add Network→Router mapping Watches
├── refs_test.go             # NEW/updated: resolution unit tests (fake client)
└── router_external_test.go  # updated: pacing + zero-guard convergence tests

package/crds/                # regenerated Router CRD YAML (CEL rules)

test/e2e/                    # kuttl bundle for selector attach/detach scenarios
examples/network/            # example Router using networkSelector
```

**Structure Decision**: Single-module Crossplane provider. The feature stays entirely
within the existing `network` API group and controller package — it extends the
already-shipped `Router` kind (feature 006) rather than adding a kind. No new package,
no group move.

## Complexity Tracking

> No constitution violations — section intentionally empty.
