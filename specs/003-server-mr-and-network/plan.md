# Implementation Plan: Cloud Server + Private Network + Floating IP MRs

**Branch**: `003-server-mr-and-network` | **Date**: 2026-06-01 | **Spec**: [./spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-server-mr-and-network/spec.md`

## Summary

Three new Crossplane v2 managed-resource kinds that together model the "Create Server" flow in the Timeweb dashboard, simplified to the most common path:

1. **`Server`** (`compute.m.timeweb.crossplane.io/v1alpha1`) — a cloud VM. Sized via `presetName` (resolved by the in-controller catalog resolver shipped in feature 002), OS via `os.image + os.version` (resolved against `/api/v1/os/servers`), region via `location` enum, attached to optional `Network` (private VPC) + zero-or-more `SshKey` MRs via crossplane-style refs.
2. **`Network`** (`network.m.timeweb.crossplane.io/v1alpha1`) — a Timeweb VPC with `subnetCIDR` + `location`. Independently usable; referenced from `Server.forProvider.networkRef`.
3. **`FloatingIP`** (`network.m.timeweb.crossplane.io/v1alpha1`) — a Timeweb floating IPv4. Owns its upstream allocation AND bind/unbind to a `Server` via `forProvider.serverRef`. Keeps cross-MR mutation single-owner.

The feature reuses every primitive from feature 002:
- Shared `ProviderConfigSpec` + dual-PC pair (no per-feature PC plumbing)
- In-controller resolver (adds `ServerPreset` Preset dimension + `ServerOSImage` Enum dimension; `ServerConfigurator` stays at its forward-compat stub)
- `shared.ResolveToken` for connector credentials
- Existing condition vocabulary (`ReasonReconciling`, `ReasonImmutableFieldChange`, `ReasonPresetNotFound`, etc.)

The custom-configurator sizing path, dedicated CPU sub-tiers, backups, network drives, software_id, avatar, DDoS guard, reboot/clone/boot-mode actions, and additional `FloatingIP` bind targets (balancer / database / network) are **explicitly deferred** to follow-up features per the clarifications recorded in `spec.md`.

The 2026-06-01 **network-group commitment** (spec.md §Clarifications) locks `network.m.timeweb.crossplane.io` as the canonical API group for all future network-class kinds — `Network` + `FloatingIP` ship in v0.3; `Router`, `Balancer` (dashboard image #3), `FirewallRule` / `SecurityGroup` extend the same group in follow-up features.

## Technical Context

**Language/Version**: Go (latest stable tracked by `go.mod`; same as features 001/002).

**Primary Dependencies** *(unchanged from feature 002 — listed explicitly as a gate for the constitution check)*:
- `github.com/crossplane/crossplane-runtime/v2` v2.3.1 — namespaced MR runtime, `xpv2.ManagedResourceSpec`, `resource.ModernTracker`, the cross-resource reference resolver (`reference.ResolutionRequest`/`ResolveOne`/`ResolveMultiple`).
- `github.com/crossplane/crossplane/apis/v2/core/v2` v2.0.0-… — `xpv2` type set.
- `k8s.io/{api,apimachinery,client-go}` v0.35.x; `sigs.k8s.io/controller-runtime` v0.23.x; `controller-tools` v0.20.x.
- `github.com/oapi-codegen/oapi-codegen/v2` — Timeweb client generation. The current `cfg.yaml` allowlist (`Проекты`, `SSH-ключи`, `S3-хранилище`, `Реестр контейнеров`) expands to include `Облачные серверы` (cloud servers — covers VPC v2 + floating IPs in the upstream tag tree per the openapi probe). Generated client surface grows; binary size stays well below any concerning threshold.
- `golang.org/x/sync` (singleflight) — already direct.
- `golangci-lint v2.x` + `kubectl-kuttl` via `hack/tools.go`.

**Storage**: None at the provider layer. Catalog cache is process-local per controller instance (same as features 001/002).

**Testing**:
- Unit: `go test` with the fake Timeweb client (extended to cover Server / VPC / FloatingIP fakes — see contracts/). Constitution §III's four-case rule (Success / NotFound / Transient / Terminal) applies per external method.
- E2E: extend `test/e2e/kuttl/tests/` with bundles `08-network-lifecycle/`, `09-server-lifecycle/`, `10-server-with-network/`, `11-floating-ip-bind/`. Single required input remains `TIMEWEB_CLOUD_TOKEN`. Smallest premium preset (`2 × 3.3 ГГц / 2 ГБ / 40 ГБ` ≈ 800 ₽/мес) is the e2e canary per the spec checklist.

**Target Platform**: Linux containers (provider runtime); Kubernetes 1.27+ control planes (CRD CEL features required for the per-MR mutual-exclusion validation).

**Project Type**: Crossplane v2 provider — single Go module (same shape as features 001/002).

**Performance Goals**:
- SC-006 inheritance — at most one upstream catalog GET per `(PCRef, dimension)` per TTL window for `ServerPreset` + `ServerOSImage`. Mechanism: existing resolver cache.
- Default reconcile poll interval: 60s (controller flag, kept at default).
- Server provisioning: SC-001 caps `apply → Ready=True` at 10 minutes for the smallest preset.

**Constraints**:
- Constitution §I — CRDs evolve additively post-`v1beta1`. Currently `v1alpha1`, freely revisable.
- Constitution §II — `Observe` read-only, writes idempotent, errors classified transient/terminal. Particular care for FloatingIP bind/unbind: a re-invoked bind on an already-bound IP must succeed without changing state; an unbind on an already-unbound IP must succeed.
- Constitution §III — every `external` method ships with the four-case test pattern.
- xpkg lint allow-list — `package/` holds CRDs/MRDs/webhook configs only.

**Scale/Scope**: 3 new MR kinds, 2 new resolver dimensions, 1 new sub-controller per MR. Total ~6 new Go packages (3× `apis/<svc>/v1alpha1` patterns + 3× `internal/controller/<svc>`). The `network` Go package is sized to accept future kinds (`Router`, `Balancer`, `FirewallRule`, `SecurityGroup`) without restructuring — see Structure Decision below.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Verdict | Notes |
|-----------|---------|-------|
| I. CRD Contract Stability | ✓ PASS | All three new CRDs are `v1alpha1`. The `Server.forProvider.presetName + os` shape may evolve as Timeweb's catalog changes — but evolution within `v1alpha1` is freely permitted per the constitution. The CEL mutual-exclusion rules on `{networkRef, networkSelector, networkID}` / `{projectRef, projectSelector, projectID}` / `{serverRef, serverSelector, serverID}` are additive (a stricter rule cannot break an existing valid manifest). |
| II. Idempotent Reconciliation | ✓ PASS | `Server.Observe` is read-only; the resolver call inside it is cache-backed and stateless. `Server.Create` is single-call (the createServer endpoint takes everything in one POST). `FloatingIP` has the most idempotency-sensitive surface: bind/unbind are mutating endpoints, but we guard with `status.atProvider.resolvedServerID` so a re-invoked bind for the already-recorded server is a no-op. Same for unbind. The four-case test rule (§III) explicitly covers these paths. |
| III. Controller Test Discipline | ✓ PASS | Every external method on `Server` / `Network` / `FloatingIP` gets Success / NotFound / Transient / Terminal cases. The `FloatingIP` controller's bind/unbind paths each get their own four cases (treated as logical "methods" within Create/Update for testing purposes). |
| Provider Constraints | ✓ PASS | Credentials sourced exclusively from the shared `ProviderConfigSpec`. Tokens never enter `Server` / `Network` / `FloatingIP` spec, status, or logs. Structured logging through the runtime logger. Standard `Synced` + `Ready` conditions. |
| Development Workflow | ✓ PASS | `make generate` after every `apis/` change. Constitution §I's "regenerate + commit in the same PR" rule applies to the new DeepCopy + CRD YAML. CI verifies tree clean post-regen. |
| Complexity tracking | ✓ EMPTY | No principle violations; nothing to justify. |

**Re-check after Phase 1**: still PASS. The cross-MR coordination (FloatingIP binds itself to a Server) is the most novel piece, but it is structurally aligned with the existing one-MR-one-upstream-resource pattern — the bind/unbind side-effects are owned by the FloatingIP controller, not split across two controllers, which is the failure mode the constitution explicitly warns against.

## Project Structure

### Documentation (this feature)

```text
specs/003-server-mr-and-network/
├── plan.md                                 # This file
├── spec.md                                 # Feature spec (post /speckit-clarify)
├── research.md                             # Phase 0 — decisions + alternatives
├── data-model.md                           # Phase 1 — entities + lifecycle
├── quickstart.md                           # Phase 1 — operator-facing walkthrough
├── contracts/
│   ├── server-v1alpha1.md                  # Server kind contract
│   ├── network-v1alpha1.md                 # Network (VPC) kind contract
│   ├── floatingip-v1alpha1.md              # FloatingIP kind contract
│   └── timeweb-endpoints.md                # Subset of upstream endpoints touched by this feature
├── tasks.md                                # Generated by /speckit-tasks
└── checklists/
    └── requirements.md                     # Spec quality checklist
```

### Source Code (repository root)

```text
apis/
├── v1alpha1/                               # PC + ClusterPC + PCU + ClusterPCU (unchanged from feat 002)
├── compute/v1alpha1/                       # NEW — VM-class kinds (Server today; Disk/Backup/Snapshot later)
│   ├── server_types.go
│   ├── managed.go
│   ├── doc.go
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
├── network/v1alpha1/                       # NEW — network-class kinds (Network + FloatingIP today;
│   │                                       # Router/Balancer/FirewallRule/SecurityGroup later — same package)
│   ├── network_types.go
│   ├── floatingip_types.go
│   ├── managed.go
│   ├── doc.go
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
├── containerregistry/v1alpha1/             # Existing
├── objectstorage/v1alpha1/                 # Existing
├── project/v1alpha1/                       # Existing — referenced by Server.forProvider.projectRef
└── sshkey/v1alpha1/                        # Existing — referenced by Server.forProvider.sshKeyRefs

internal/
├── clients/timeweb/                        # API client wrapper + fake (extended)
│   └── generated/                          # oapi-codegen output; cfg.yaml tag-allowlist extended
├── controller/
│   ├── compute/                            # NEW — server controller (sibling kinds land here in future features)
│   │   ├── server_external.go
│   │   ├── server_external_test.go
│   │   ├── connector.go
│   │   └── refs.go                         # ResolveReferences for {project,network,sshKey,floatingIP}Refs
│   ├── network/                            # NEW — Network + FloatingIP controllers in one pkg
│   │   ├── network_external.go             # (future Router/Balancer/FirewallRule controllers join here)
│   │   ├── network_external_test.go
│   │   ├── floatingip_external.go
│   │   ├── floatingip_external_test.go
│   │   └── connector.go
│   ├── containerregistry/                  # Existing
│   ├── s3bucket/                           # Existing
│   ├── project/                            # Existing
│   ├── sshkey/                             # Existing
│   └── shared/                             # Existing — pcref + resolver primitive
│       └── resolver/
│           └── dimensions.go               # MODIFIED — adds ServerPreset (Preset) + ServerOSImage (Enum)

package/                                    # xpkg input — CRDs (3 new) + metadata
├── crds/
│   ├── compute.m.timeweb.crossplane.io_servers.yaml          # NEW
│   ├── network.m.timeweb.crossplane.io_networks.yaml         # NEW
│   ├── network.m.timeweb.crossplane.io_floatingips.yaml      # NEW
│   ├── timeweb.crossplane.io_providerconfigs.yaml            # Existing
│   ├── timeweb.crossplane.io_clusterproviderconfigs.yaml     # Existing
│   ├── (other existing CRDs)
└── crossplane.yaml                          # MODIFIED — description mentions new kinds

deploy/                                     # Operator-facing extras (unchanged)

test/e2e/                                   # k3d-based e2e harness
├── kuttl/tests/
│   ├── 02-project-import/                  # Existing
│   ├── 03-sshkey-lifecycle/                # Existing
│   ├── 04-s3bucket/                        # Existing
│   ├── 05-containerregistry/               # Existing
│   ├── 06-preset-not-found/                # Existing
│   ├── 07-multi-pc-isolation/              # Existing (feat 002)
│   ├── 07b-invalid-pc-kind/                # Existing (feat 002)
│   ├── 08-network-lifecycle/               # NEW — create/observe/delete VPC
│   ├── 09-server-lifecycle/                # NEW — smallest preset, Ubuntu 24.04, SSH key wired
│   ├── 10-server-with-network/             # NEW — VPC + Server attached via networkRef
│   └── 11-floating-ip-bind/                # NEW — FloatingIP alloc + bind + rebind + unbind
├── scripts/
│   ├── kuttl.sh                            # MODIFIED — discovers smallest server preset at runtime
│   └── cleanup.sh                          # Existing
└── README.md                               # MODIFIED — new MRs documented

docs/
├── openapi-timeweb.json                    # Vendored upstream schema
├── presets.md                              # Existing — Container Registry + S3 sizing
└── servers.md                              # NEW — operator-facing Server / Network / FloatingIP guide
```

**Structure Decision**: Two new API groups under the existing `<svc>.m.timeweb.crossplane.io` convention (per `project_crossplane_v2_conventions` memory):

- `compute.m.timeweb.crossplane.io` — VM-class kinds. v0.3 ships `Server`. Future features extend the same group + same Go package with `Disk`, `Backup`, `Snapshot`, etc.
- `network.m.timeweb.crossplane.io` — network-class kinds. v0.3 ships `Network` (VPC) + `FloatingIP`. **Forward-compat commitment** (spec.md §Clarifications "Session 2026-06-01 (network group commitment)" + new Assumption): future features extend the same group + same Go package with `Router`, `Balancer` (dashboard image #3 — "Создать балансировщик", own tariff, regional pricing), `FirewallRule`, `SecurityGroup`. Splitting these into separate groups (`loadbalancer.m.…`, `firewall.m.…`, etc.) is explicitly rejected — the Go-package overhead of a new API group (separate `groupversion_info.go`, separate `AddToScheme`, separate Crossplane registration) outweighs any conceptual-clarity gain when these kinds all share the dashboard's "Networking" section + the same set of cross-resource refs.

The `apis/cluster/` + `apis/namespaced/` package split deferred in feature 002 (D-1) stays deferred — these new kinds are all namespaced, no cluster-scoped resources in this feature, no reason to revisit.

## Complexity Tracking

> Empty — Constitution Check passed at both gates without violations. The two areas where this feature adds NEW behavior (cross-MR reference resolution for project/network/floatingIP refs; FloatingIP owning bind/unbind side-effects) are both directly supported by crossplane-runtime v2 — `reference.ResolveOne`/`ResolveMultiple` for the refs, and the standard Observe-then-Create-or-Update pattern for the bind. No bespoke runtime mechanics.
