# Implementation Plan: Custom Sizing (Configurators) + Group Tidy-up + Tech Debt

**Branch**: `005-custom-sizing-configurators` | **Date**: 2026-06-08 | **Spec**: [./spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-custom-sizing-configurators/spec.md`

## Summary

Three bundled workstreams, all heavily reusing primitives that already exist in the tree:

1. **Custom configurator sizing** (headline) — add a `forProvider.resources` block (`cpu`, `ramGB`, `diskGB`, + optional axes) as a CEL-`exactly-one-of` alternative to `presetName` on `Server` (US1), then `KubernetesCluster` + `KubernetesClusterNodepool` (US2). The operator types the resources they want; the in-controller resolver's **`Configurator` dimension** (`SelectConfigurator`, already implemented in feature 002) resolves them to an upstream `configurator_id`. This removes the "ambiguous preset slug" pain that the feature-004 live e2e exposed.
2. **ContainerRegistry → kubernetes group** (US3) — hard move of `ContainerRegistry` + `ContainerRegistryRepository` from `containerregistry.m.timeweb.crossplane.io` into `kubernetes.m.timeweb.crossplane.io`, mirroring the Timeweb dashboard (registries are a tab inside the Kubernetes section). Breaking apiVersion change, acceptable pre-1.0 / no external consumers.
3. **Tech-debt pass** (US4) — (a) fix the `Server` controller's `resolveRefs` **spec-mutation / at-most-one CEL-reject-on-persist** latent bug (same fix already shipped on `KubernetesCluster` in feature 004); (b) e2e harness fixes (`make e2e.down` not deleting the k3d cluster/registry, kuttl multi-`--test` scoping, condition-order assert fragility); (c) align Connect-error condition reason (`Reconciling` for unready-dependency gating, vs the generic `ReconcileError`).

Reused as-is: the resolver `Configurator` primitive (`ConfiguratorInput/Entry`, `CapacityBound`, `SelectConfigurator`), the `GetConfigurators*` generated methods, `shared.ResolveToken`, the catalog cache, the v2 ModernManaged scaffolding. **Presets stay first-class** (additive).

## Technical Context

**Language/Version**: Go (latest stable tracked by `go.mod`; same as features 001–004).

**Primary Dependencies** *(unchanged — constitution-check gate)*: crossplane-runtime/v2, crossplane apis/v2, k8s.io/{api,apimachinery,client-go}, controller-runtime, controller-tools, oapi-codegen/v2 (the `Облачные серверы` tag already pulls `/api/v1/configurator/servers` → `GetConfigurators*`; no new tag needed), golangci-lint + kubectl-kuttl via `hack/tools.go`.

**Storage**: None at the provider layer. Catalog cache is process-local (features 001/002).

**Testing**:
- Unit: `go test` + counterfeiter fake. Constitution §III four-case per new external path. The `SelectConfigurator` algorithm + fetchers already have resolver-package tests (feature 002); this feature adds the controller-side resources-path tests (Create, NoConfiguratorAvailable, sizing-switch) and the Server-CEL-fix test.
- E2E: new bundles `16-server-custom-sizing`, `17-k8s-custom-sizing`; **update** `05-containerregistry` to the new group apiVersion; the Server-CEL fix is covered by the existing `10-server-with-network` (which would have hit the bug). The wrapper discovers configurator-satisfiable sizing at runtime, keeping cost minimal.

**Target Platform**: Linux containers; Kubernetes 1.27+ (CRD CEL for the `exactly-one-of` rules).

**Project Type**: Crossplane v2 provider — single Go module.

**Performance Goals**:
- ≤1 catalog GET per `(PCRef, dimension)` per TTL for the `Configurator` dimension (existing resolver cache).
- Custom-sized provisioning reaches Ready within the same window as the preset path.

**Constraints**:
- Constitution §I — the ContainerRegistry group move is **breaking** but permitted: all kinds are `v1alpha1` (freely revisable) and there are no external consumers ([[user_project_owner]]). All other changes (`resources` blocks, `lockedConfiguratorID`, CEL XOR) are additive. `make generate` regenerates DeepCopy + CRD YAML in the same change set.
- Constitution §II — `Observe` stays read-only; configurator resolution is cache-backed; `lockedConfiguratorID` drives drift + sizing-switch detection. The Server-CEL fix *improves* §II compliance (no spec mutation on resolve).
- Constitution §III — four-case tests per new path.
- xpkg lint allow-list — `package/` holds CRDs/MRDs/webhook configs only.

**Scale/Scope**: No new API groups; no new resolver dimension *kinds*. Touches 3 existing kinds (Server, KubernetesCluster, KubernetesClusterNodepool) with a `resources` block, relocates 2 kinds (ContainerRegistry, ContainerRegistryRepository), promotes 1 dimension (`DimServerConfigurator`) stub→real, and fixes 3 tech-debt items.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Verdict | Notes |
|-----------|---------|-------|
| I. CRD Contract Stability | ✓ PASS (with one justified breaking move) | `resources`/`lockedConfiguratorID`/CEL-XOR are additive. The ContainerRegistry **group rename is breaking** — justified by `v1alpha1` (pre-`v1beta1`, freely revisable per §I) + no external consumers; called out in Complexity Tracking. Regenerate + commit artifacts together. |
| II. Idempotent Reconciliation | ✓ PASS | Configurator resolution is a cache-backed read inside Create; re-invocation is stable. `lockedConfiguratorID` prevents silent re-sizing. The Server `resolveRefs` fix removes a spec-mutation that could trip CEL on persist — net §II improvement. |
| III. Controller Test Discipline | ✓ PASS | Four-case for the resources-path Create on all three kinds; plus NoConfiguratorAvailable, sizing-switch, and the Server-CEL regression test. `SelectConfigurator`/fetcher tests already exist. |
| Provider Constraints | ✓ PASS | No credential surface change. Structured logging; standard conditions (`NoConfiguratorAvailable`, `SizingSwitchRequiresRecreate`, `Reconciling` already in the shared vocabulary). |
| Development Workflow | ✓ PASS | `make generate` after `apis/` changes (large here: relocated CRDs + new fields); CI tree-clean gate. |
| Complexity tracking | ⚠ ONE justified breaking change | The ContainerRegistry group rename — see Complexity Tracking. |

**Re-check after Phase 1**: still PASS. The only non-additive change is the deliberate, justified group move; everything else extends existing, tested machinery.

## Project Structure

### Documentation (this feature)

```text
specs/005-custom-sizing-configurators/
├── plan.md          # This file
├── spec.md          # Feature spec (clarified)
├── research.md      # Phase 0
├── data-model.md    # Phase 1
├── quickstart.md    # Phase 1
├── contracts/
│   ├── server-resources-v1alpha1.md            # Server.resources contract
│   ├── kubernetes-resources-v1alpha1.md        # Cluster/Nodepool.resources contract
│   ├── containerregistry-group-move.md         # the relocation contract
│   └── timeweb-configurator-endpoints.md       # /api/v1/configurator/servers inventory
├── tasks.md         # /speckit-tasks
└── checklists/requirements.md
```

### Source Code (repository root) — key changes

```text
apis/
├── compute/v1alpha1/server_types.go            # MODIFIED — + ServerResources block, + lockedConfiguratorID; CEL presetName XOR resources
├── kubernetes/v1alpha1/
│   ├── kubernetescluster_types.go              # MODIFIED — + resources (cpu/ramGB/diskGB), + lockedConfiguratorID, CEL XOR
│   ├── kubernetesclusternodepool_types.go      # MODIFIED — + resources (+gpu), + lockedConfiguratorID, CEL XOR
│   ├── containerregistry_types.go              # NEW (moved from apis/containerregistry) — group becomes kubernetes.m.…
│   ├── containerregistryrepository_types.go    # NEW (moved)
│   ├── groupversion_info.go                     # MODIFIED — register the two relocated kinds
│   ├── managed.go                               # MODIFIED — forwarders for the two relocated kinds
│   └── zz_generated.deepcopy.go                 # regenerated
├── containerregistry/                           # DELETED (group removed)
└── apis.go                                      # MODIFIED — drop containerregistry AddToScheme

internal/controller/
├── shared/resolver/dimensions.go               # MODIFIED — promote DimServerConfigurator stub→real (fetchServerConfigurators over GetConfigurators); reused for K8s
├── compute/
│   ├── server_external.go                       # MODIFIED — resources→configurator resolve, lockedConfiguratorID, sizing-switch; build body with configurator_id
│   └── refs.go                                  # MODIFIED — resolveRefs no longer mutates spec (carry resolved ids on the external) — tech-debt fix
├── kubernetes/
│   ├── cluster_external.go                       # MODIFIED — resources→configuration block
│   └── nodepool_external.go                      # MODIFIED — resources→configuration block
├── containerregistry/                            # repointed to kubernetesv1alpha1 types (controller pkg stays; consolidation optional)
└── shared/conditions.go                          # reasons already present: NoConfiguratorAvailable, SizingSwitchRequiresRecreate, Reconciling

package/crds/                                     # containerregistry CRDs renamed to *.kubernetes.m.timeweb.crossplane.io_*.yaml; server/cluster/nodepool CRDs regenerated
cmd/provider/main.go                              # MODIFIED — CR setup repointed to the kubernetes-group GVKs
test/e2e/kuttl/tests/                             # 05-containerregistry updated; 16-server-custom-sizing + 17-k8s-custom-sizing added
docs/                                             # servers.md + kubernetes.md updated (custom sizing); CR docs moved under the kubernetes guide
```

**Structure Decision**: The relocated ContainerRegistry **types** move into the existing `apis/kubernetes/v1alpha1` package (so the group string matches the package's `groupversion_info.go`). The **controllers** stay in `internal/controller/containerregistry/` repointed to the relocated types (lowest churn; a controller package name need not equal the API group) — with consolidation into `internal/controller/kubernetes/` noted as an optional follow-up. No new API groups; the `resources` work extends existing kinds in place.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|--------------------------------------|
| ContainerRegistry API-group rename (breaking, non-additive) | The dashboard co-locates registries under Kubernetes ("Реестры контейнеров" tab); the CRD group should mirror what panel users see (operator request). | An additive alias (serving both groups) was offered and rejected by the operator — it doubles CRD surface + maintenance for a pre-1.0 provider with no external consumers, where a clean rename is cheap and correct. |
