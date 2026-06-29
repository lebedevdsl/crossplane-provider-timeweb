# Implementation Plan: Firewall — declarative Timeweb Cloud firewall rule groups

**Branch**: `013-firewall-api` | **Date**: 2026-06-28 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/013-firewall-api/spec.md`

## Summary

Add a namespaced managed-resource kind **`Firewall`**
(`network.m.timeweb.crossplane.io/v1alpha1`) that manages a Timeweb Cloud **firewall rule
group** declaratively: the group identity (`name`, `description`, default-deny `policy`), its
inbound/outbound **rules** (`direction`/`protocol`/`port`/`cidr`), and its **service
attachments** (which load balancers / servers / DBs / apps the group governs). All three are
inline in one resource (single-writer), mirroring the existing **Router** kind. Attachments use
**opaque `{id, type}` service references** (clarified 2026-06-28) — v1 targets **load
balancers** (`resource_type=balancer`) since the environment runs Kubernetes LBs, not cloud
servers. The full upstream API is the documented `/api/v1/firewall/*` surface (already present in
`docs/openapi-timeweb.json`); the only published-spec gap is the `ResourceType` enum, which lists
only `server` but really accepts `server | dbaas | balancer | app` (live-verified 2026-06-28).
The controller is modeled on Router but **simpler**: opaque attachments mean no cross-MR
reference resolution, no label selector, no catalog resolver, and no `Watches`.

## Technical Context

**Language/Version**: Go (latest stable, tracked by `go.mod` per `project_go_tooling_policy`);
Crossplane v2 namespaced MR model (`.m.` groups).

**Primary Dependencies**: `crossplane-runtime/v2`, `sigs.k8s.io/controller-runtime`,
`internal/clients/timeweb` (the existing hand-written-wrapper client). **No new third-party
dependency** — the firewall is plain Timeweb REST, signed with the account Bearer token.

**Storage**: N/A (external state is Timeweb's API; MR status mirrors it). Stateless reconciler.

**Testing**: `go test` four-case pattern per Constitution §III (success / not-found / transient /
terminal) against a fake `timeweb` client; kuttl/k3d e2e optional and gated on an explicit
context. Group + rules lifecycle is self-contained e2e; **service-attachment e2e requires a
pre-existing balancer id** (no `LoadBalancer` kind exists in the provider) and is env-gated.

**Target Platform**: Linux (Crossplane provider pod, amd64, distroless/static:nonroot); k3d /
Timeweb for e2e.

**Project Type**: Crossplane provider (single Go module).

**Performance/Constraints**: Per-host conservative rate limiting via the shared `timeweb.Client`
limiter (Qrator — `project_timeweb_qrator_ddos_egress_block`). `Observe` issues 3 reads (GET
group + GET rules + GET resources); `Update` paces mutations like Router
(`maxFirewallMutationsPerReconcile`) so a large rule/attachment delta doesn't burst the API.

**Scale/Scope**: 1 new kind (`Firewall`); new hand-written client file
`internal/clients/timeweb/firewall.go`; new controller `internal/controller/network/firewall_*`;
register in `cmd/provider/main.go`; regenerate CRDs + DeepCopy. Optional one-line openapi enum
patch for fidelity (not required — the client is hand-written).

## Open clarifications (resolved)

- **Attachment direction / target model** (spec Clarifications 2026-06-28): firewall-centric,
  single-writer, **opaque `{id, type}` service references** (not typed cross-MR refs); v1 target
  = load balancers (`balancer`); upstream enforces **1:1 exclusivity** (a service is in at most
  one group).
- **Policy**: groups carry a `policy` of `DROP` (default-deny allow-list — the dashboard's
  "Разрешающий") or `ACCEPT`. The create endpoint sets it via a query param; the group PATCH body
  omits it → **policy is create-only (immutable)**. Default `DROP`.
- **Client strategy**: hand-write the firewall endpoints in `internal/clients/timeweb/firewall.go`
  (the `storages_users_v2.go` / `doV2` pattern), **not** via oapi-codegen regen — this sidesteps
  the stale `ResourceType` enum (published spec lists only `server`) and the `-include-tags`
  churn. The controller just sends `resource_type=balancer` as a string.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.0.0.*

- **§I CRD Contract Stability — PASS.** `Firewall` is a new `v1alpha1` CRD (additive). DeepCopy +
  CRD YAML regenerated and committed in the same PR (`make generate`). No change to existing
  kinds.
- **§II Idempotent, Side-Effect-Aware Reconciliation — PASS.** `Observe` is read-only (GET group +
  rules + resources). `Create` is idempotent via external-name = group UUID plus a by-name
  adoption guard (Router idiom). Rules and attachments converge by **set diff** (full reconcile,
  no incremental drift). `Delete` tolerates already-gone (404 → success). Errors classified into
  the existing `timeweb.Classify` taxonomy; exclusivity conflict surfaces a terminal condition.
  No cross-MR work on the delete path.
- **§III Controller Test Discipline — PASS.** Four-case unit tests for Observe/Create/Update/
  Delete plus rule-set/attachment-set diff tests, using the fake `timeweb` client; no live HTTP.
- **Provider Constraints — PASS.** No new credential surface: the account token (already from
  `ProviderConfig.spec.credentials`) is reused; nothing logged in spec/status.
- **Observability — PASS.** Standard `Synced`/`Ready` conditions + structured logger; reuse
  `shared.RecordConditionChange` and the existing reason vocabulary (add `ServiceConflict` for the
  exclusivity case).

No violations → Complexity Tracking intentionally empty.

## Project Structure

### Documentation (this feature)

```text
specs/013-firewall-api/
├── plan.md              # This file
├── research.md          # Phase 0 — R-1..R-8 (API surface, enum gap, exclusivity, client choice)
├── data-model.md        # Phase 1 — Firewall spec/status, rule + attachment shapes, diff rules
├── quickstart.md        # Phase 1 — operator walkthrough + troubleshooting matrix
├── contracts/           # Phase 1
│   ├── firewall-v1alpha1.md          # CRD contract + conditions table
│   └── timeweb-firewall-endpoints.md # probe-verified endpoint inventory, bodies, quirks
└── tasks.md             # Phase 2 (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
apis/network/v1alpha1/
├── firewall_types.go        # + FirewallParameters/Observation/Spec/Status/Firewall/FirewallList
├── groupversion_info.go     # + FirewallKind/GVK; SchemeBuilder.Register(&Firewall{}, &FirewallList{})
└── zz_generated.deepcopy.go # regenerated

internal/clients/timeweb/
└── firewall.go              # hand-written: group CRUD, rule CRUD, resource link/unlink, reverse-lookup

internal/controller/network/
├── controller.go            # + SetupFirewall(mgr, log, pollInterval) (no Watches, no resolver cache)
├── firewall_connector.go    # Connect: ResolveToken, build timeweb client (no ref resolution)
├── firewall_external.go     # Observe/Create/Update/Delete + rule/attachment set diff
└── firewall_external_test.go# four-case unit tests + diff tests

cmd/provider/main.go          # + networkctrl.SetupFirewall(...)
docs/openapi-timeweb.json     # (optional) extend ResourceType enum to server|dbaas|balancer|app
package/crds/...firewalls.yaml# regenerated CRD
examples/firewall.yaml        # operator example
```

**Structure Decision**: Place `Firewall` in the existing **network** API group + controller
package (alongside Network / FloatingIP / Router), matching the dashboard's "Управление сетями →
Firewall" placement. Mirror the Router controller's Observe-as-sole-authority + paced one-pass
Update, but drop Router's ref/selector/catalog/Watches machinery — firewall attachments are
opaque `{id, type}` literals, so there is nothing to resolve or watch. Hand-write the client
endpoints rather than regenerate (the `doV2` pattern), keeping the dependency-free footprint.

## Complexity Tracking

No constitution violations — section intentionally empty.
