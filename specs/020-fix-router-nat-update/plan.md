# Implementation Plan: Router NAT-on-update bind fix

> **Descope 2026-07-25**: v0.9.2 ships Part 1 only (summary item 1, D-1..D-5).
> Part 2 (summary item 2, D-6/D-7, the kubernetes touch points) was reverted —
> premises invalidated by handoff Parts 3+ (`virtual_router_id`, post-attach
> snapshot desync); respecced in `specs/_next-router-features.preface.md`.

**Branch**: `020-fix-router-nat-update` | **Date**: 2026-07-24 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/020-fix-router-nat-update/spec.md`

## Summary

Two verified bugs from the timeweb-infra#135/#132 production rollout, one patch
(v0.9.2, NON-BREAKING):

1. **Router NAT-on-update** — the update path calls `UpdateRouterNat` with an
   address the router does not own (create attaches declared NAT IPs via the
   create body `ips[]`; update has no attach step) → endless 404
   `ip_not_found`. Fix: precondition the NAT write on observed ownership
   (`router.Ips`); when unowned, resolve the FloatingIP and **bind it to the
   router** via `POST /floating-ips/{uuid}/bind {resource_type: "router",
   resource_id: <router-uuid>}` (undocumented enum value, live-verified
   2026-07-24), then let re-observation drive the NAT enable. Never steal a
   binding held by another resource — degraded condition instead; one blocked
   attachment must not abort the rest of the update pass. NAT disable never
   unbinds.
2. **Cluster↔router linkage frozen at cluster-create** — a cluster created on a
   router-attached NAT-less network never joins `router.parent_services`;
   private nodepools then fail `router_required_for_worker_groups_without_public_ip`
   forever, recreate-only. Fix: create-precondition on KubernetesCluster
   (block + requeue until the network's router wiring has NAT; router-less
   networks allowed + Warning event), friendly nodepool-side classification of
   the upstream error code, additive `status.atProvider.routerLinked` mirror.

## Technical Context

**Language/Version**: Go (latest stable per project policy; go.mod floats)

**Primary Dependencies**: crossplane-runtime v2, controller-runtime, generated
Timeweb client (`internal/clients/timeweb/generated`) — no new dependencies

**Storage**: N/A (stateless controllers; state lives in CR status + upstream)

**Testing**: `go test` with fake Timeweb client (Constitution III); kuttl
admission bundles unchanged; live e2e gate on `inyan-staging` (never
`inyan-infra`)

**Target Platform**: linux/amd64 container (distroless), Crossplane v2 provider

**Project Type**: Crossplane provider (established repo layout)

**Performance Goals**: no hot-loop reconciles; all new upstream reads/writes go
through the process-global shared rate limiter (018); paced mutations respect
`maxRouterMutationsPerReconcile`

**Constraints**: NON-BREAKING — no spec-surface CRD changes (status-only
addition on KubernetesCluster); no client regeneration this patch

**Scale/Scope**: 2 controllers touched (network/router, kubernetes/cluster+
nodepool), 1 status field, ~6 design decisions D-1..D-7

## Constitution Check

*Constitution v1.1.0.*

- **I. CRD Contract Stability** — PASS. No spec fields change. One additive
  status field (`KubernetesCluster.status.atProvider.routerLinked`); CRD YAML +
  DeepCopy regenerated in the same PR (`make generate`).
- **II. Idempotent, Side-Effect-Aware Reconciliation** — PASS with care:
  - Observe stays read-only (routerLinked is computed from GETs; the Part 2
    precondition runs in Create, where reads are legitimate).
  - The bind step is idempotent by precondition: already-owned → no bind;
    bound-to-this-router-but-not-yet-in-router.Ips races settle via
    re-observation. Re-invocation cannot double-bind (bind of an already
    router-bound IP is skipped by the ownership check).
  - Never-steal: a binding held elsewhere is NEVER broken (degraded condition;
    converge-on-freed). Errors are classified, never swallowed; the blocked
    state is surfaced via condition + Event, not silence.
  - 019's canonical-not-found rule untouched: `ip_not_found` classification is
    unchanged — the fix eliminates the doomed write rather than reinterpreting
    the 404.
- **III. Controller Test Discipline** — PASS. Unit tests (fake client) for:
  bind-then-converge, owned→NAT-direct, bound-elsewhere→condition+continue,
  blocked-does-not-wedge-other-attachments, disable-no-unbind (Part 1);
  create-blocked / create-proceeds-after-NAT / router-less-warning,
  routerLinked mirror, nodepool error classification (Part 2).
- **Provider Constraints** — tokens untouched; structured logging only;
  conditions via the shared vocabulary.

Re-check after design: PASS (no violations introduced by D-1..D-7; no
Complexity Tracking entries needed).

## Design decisions

- **D-1 — ownership source**: the NAT precondition reads the router GET the
  Update pass already holds (`router.Ips[].Ip`). No extra call. The same set
  drives the Observe-side drift row (unchanged).
- **D-2 — FIP identity + never-steal check**: one `GET /api/v1/floating-ips`
  list, match by declared address → `{id (uuid), resource_type/resource_id}`.
  Free (`resource_type` nil) → bind. Bound to this router → treat as owned
  (skip; observation catches up). Bound elsewhere → degraded condition, skip
  attachment, continue loop. Unresolvable (no FIP with that address) →
  degraded condition likewise. Covers both declaration forms (ref and raw
  `ip`) uniformly — FR-011 — without touching Connect-time ref resolution.
- **D-3 — client surface**: call the existing generated `BindFloatingIp` with a
  locally-defined typed constant `router` (new const in the hand-written
  `internal/clients/timeweb` package; zz_generated is NOT edited). Also
  hand-patch `docs/openapi-timeweb.json` bind enum with `router` so the next
  regen keeps the value (hand-patched-superset convention); no regen this
  patch. `resource_id` uses the string arm of the generated union (router
  UUID).
- **D-4 — sequencing**: the bind is a paced mutation (`ops++`). After a
  successful bind the pass does NOT call `UpdateRouterNat` for that attachment
  — the next reconcile's Observe sees ownership and the existing NAT branch
  fires (Observe-as-sole-authority; 2xx ≠ converged). Slightly slower (one
  extra reconcile), structurally safer.
- **D-5 — blocked-NAT surfacing without wedging**: the bind-blocked outcome is
  recorded as a typed condition (shared vocabulary, `Synced=False`-style
  upstream-failure reason + Event naming network, IP, holder, remedy) and the
  per-attachment loop **continues**; the pass returns nil error so requeue
  follows poll cadence, not error backoff storm. Real API errors on issued
  calls still return classified as today.
- **D-6 — Part 2 wiring check + routerLinked**: resolve the cluster's network
  → `GET /api/v1/routers` list → find the router whose
  `GET /routers/{id}/networks` contains the network id. Outcomes at Create:
  no router → allowed + Warning event; router entry with empty `nat_ip` →
  refuse + condition + requeue (transient-style, converges when NAT appears);
  `nat_ip` set → proceed. Cluster Observe reuses the same read to populate
  `routerLinked` = router found AND its `parent_services` contains the cluster
  external-name with type `k8s`. Reads are rate-limited by the shared
  transport; router count per account is small.
- **D-7 — nodepool classification**: match `*timeweb.APIError` with
  `Code == "router_required_for_worker_groups_without_public_ip"` in the
  nodepool create/update error path → condition message naming the frozen
  linkage (check router `parentServices`; cluster recreation is the only
  remedy) instead of the raw 400. Only this code — unrelated 400s unaffected.

## Project Structure

### Documentation (this feature)

```text
specs/020-fix-router-nat-update/
├── plan.md              # This file
├── spec.md              # Feature spec (clarified 2026-07-24, Parts 1+2)
├── research.md          # Phase 0 — probe consolidation R-1..R-6
├── data-model.md        # Phase 1 — status additions, condition vocabulary
├── quickstart.md        # Phase 1 — operator walkthrough
├── contracts/
│   ├── router-nat-bind.md            # Part 1 behavior contract
│   └── cluster-linkage-guard.md      # Part 2 behavior contract
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/controller/network/router_external.go        # NAT branch: ownership precondition, bind helper, blocked-condition, loop-continue
internal/controller/network/router_external_test.go   # Part 1 unit coverage
internal/clients/timeweb/floating_ips.go              # (or errors.go-adjacent) router bind const + thin helper if needed
docs/openapi-timeweb.json                             # bind enum += "router" (hand-patch, no regen)
apis/kubernetes/v1alpha1/kubernetescluster_types.go   # status.atProvider.routerLinked (additive)
internal/controller/kubernetes/cluster_external.go    # Create precondition + Observe routerLinked
internal/controller/kubernetes/cluster_external_test.go
internal/controller/kubernetes/nodepool_external.go   # error-code classification
internal/controller/kubernetes/nodepool_external_test.go
package/crds/ + zz_generated deepcopy                 # make generate output (same PR)
docs/conditions.md                                    # new condition reasons documented
specs/019.../                                         # untouched
```

**Structure Decision**: existing repo layout; no new packages except possibly a
small hand-written FIP helper file in `internal/clients/timeweb` following the
`firewall.go` hand-written pattern.

## Validation plan

1. `go test ./...`, lint via `go run` golangci-lint, `go build ./...` — unit
   gate (Constitution III).
2. `make generate` clean tree; kuttl admission bundles unchanged and passing.
3. Dev-tag build + push (`VERSION=dev-<epoch>`), deploy to `inyan-staging`
   (explicit context pinned; annotation-bump re-pull), broad provider-log error
   scan.
4. Live e2e (staging, self-contained): fresh Network + FloatingIP + Router
   (no NAT) → update Router adding NAT (the #135 shape) → assert bind + NAT
   converge, Synced & Ready both True. Part 2 negative: cluster on the
   router-attached NAT-less network → blocked condition; enable NAT → cluster
   creates; private nodepool converges (canonical ordering). NAT-disable
   leaves IP bound.
5. Release: notes (example-first style), semver v0.9.2, tag after notes commit;
   xpkg + image publish per 008 pipeline.
