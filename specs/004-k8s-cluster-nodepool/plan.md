# Implementation Plan: Managed Kubernetes (Cluster + Nodepool + Addon MRs)

**Branch**: `004-k8s-cluster-nodepool` | **Date**: 2026-06-06 | **Spec**: [./spec.md](./spec.md)

**Input**: Feature specification from `/specs/004-k8s-cluster-nodepool/spec.md`

## Summary

Three new Crossplane v2 managed-resource kinds modeling the Timeweb dashboard's "Create Kubernetes cluster" flow, simplified to the fixed-preset path:

1. **`KubernetesCluster`** (`kubernetes.m.timeweb.crossplane.io/v1alpha1`) — the managed control plane. Sized via master `presetName` (resolved by the in-controller catalog resolver), `k8sVersion` (exact match against `/api/v1/k8s/k8s-versions`), `networkDriver` (CRD enum), `availabilityZone` (CRD enum). Optional `masterNodesCount` (default 1, HA via 3), `networkRef`/`networkID` (VPC attach — reuses the feature-003 `Network` kind), `projectRef`/`projectID`. Publishes a `kubeconfig` connection Secret. Supports in-place version upgrade.
2. **`KubernetesClusterNodepool`** (same group) — one upstream worker group, referencing its parent cluster via `clusterRef`/`clusterID`. Sized via worker `presetName`; `nodeCount` is mutable (scaling via relative node add/remove deltas). Optional `labels` (`map[string]string`), `autoscaling{enabled,minSize,maxSize}`, `autohealing`.
3. **`KubernetesClusterAddon`** (same group) — one installed cluster addon, referencing its parent cluster. Carries `type` + `version` (+ optional `yamlConfig`), validated against the cluster's available-addons catalog.

The feature reuses every primitive from features 001/002/003:
- Shared `ProviderConfigSpec` + dual-PC pair (no per-feature PC plumbing).
- In-controller resolver — **promotes the forward-compat K8s dimensions** already registered in feature 002 (`DimKubernetesMasterPreset`, `DimKubernetesWorkerPreset`, `DimKubernetesVersion`) from their `fetchUnwired` stubs to real fetchers. `DimKubernetesNetworkDriver` + `DimAvailabilityZone` stay stubbed — both are validated by CRD enums instead (admission-time, stable value sets).
- `shared.ResolveToken` for connector credentials; the same-namespace `client.Get` ref-resolution idiom established in feature 003 (`internal/controller/compute/refs.go`).
- The feature-003 `Network` kind (VPC attach) + feature-001 `Project` kind (project assignment) are reused unchanged as reference targets.
- Existing condition vocabulary (`ReasonReconciling`, `ReasonImmutableFieldChange`, `ReasonPaymentRequired`, the resolver `…NotFound` message style).

**Explicitly deferred** (per spec.md Clarifications + Assumptions): the custom-configurator sizing path (`configuration{cpu,ram,disk}` on Cluster + Nodepool), `is_ingress`/`is_k8s_dashboard` toggles, `oidc_provider`, `maintenance_slot`, `cluster_network_cidr` overrides.

The **API-group commitment**: `kubernetes.m.timeweb.crossplane.io` is locked as the canonical group for all managed-Kubernetes kinds (mirroring the network-group commitment from feature 003). Future kinds (cluster OIDC config, maintenance policy) extend the same group + same Go package.

## Technical Context

**Language/Version**: Go (latest stable tracked by `go.mod`; same as features 001/002/003).

**Primary Dependencies** *(unchanged from feature 003 — listed as a constitution-check gate)*:
- `github.com/crossplane/crossplane-runtime/v2` — namespaced MR runtime, `xpv2.ManagedResourceSpec`, `resource.ModernManaged`, `managed.WithManagementPolicies()`.
- `github.com/crossplane/crossplane/apis/v2/core/v2` — `xpv2` type set.
- `k8s.io/{api,apimachinery,client-go}`; `sigs.k8s.io/controller-runtime`; `controller-tools`.
- `github.com/oapi-codegen/oapi-codegen/v2` — Timeweb client generation. The `Makefile` `-include-tags` allowlist + `cfg.yaml` expand to include **`Kubernetes`** (covers `/api/v1/k8s/*` + `/api/v1/presets/k8s`). Regenerating exposes `CreateCluster`/`GetCluster`/`UpdateCluster`/`DeleteCluster`/`GetClusterKubeconfig`/`UpdateClusterVersion`, `CreateNodeGroup`/`GetNodeGroup`/`DeleteNodeGroup`/`IncreaseNodeGroupNodes`/`ReduceNodeGroupNodes`, `GetClusterAddons`/`CreateClusterAddon`/`DeleteClusterAddon`/`GetClusterAddonsConfigs`, `GetK8SPresets`/`GetK8SVersions`/`GetK8SNetworkDrivers`. The counterfeiter fake (`internal/clients/timeweb/fake.go`) is regenerated since the interface grows.
- `golang.org/x/sync` (singleflight) — already direct (resolver cache).
- `golangci-lint` + `kubectl-kuttl` via `hack/tools.go`.

**Storage**: None at the provider layer. Catalog cache is process-local per controller instance (features 001/002).

**Testing**:
- Unit: `go test` with the counterfeiter Timeweb fake (extended for cluster/nodegroup/addon). Constitution §III four-case rule (Success / NotFound / Transient / Terminal) per external method, plus the scaling, upgrade, and bind-style logical methods.
- E2E: extend `test/e2e/kuttl/tests/` with `12-k8s-cluster-lifecycle/`, `13-k8s-nodepool-scaling/`, `14-k8s-cluster-with-network/`, `15-k8s-addon/`. Single required input remains `TIMEWEB_CLOUD_TOKEN`. The wrapper (`test/e2e/scripts/kuttl.sh`) discovers the smallest master + worker `/presets/k8s` slugs and a valid `k8sVersion` at runtime. Cluster e2e is the slowest canary (SC-001: ≤20 min cluster, ≤15 min pool → assert timeouts).

**Target Platform**: Linux containers (provider runtime); Kubernetes 1.27+ control planes (CRD CEL features required for the mutual-exclusion validation).

**Project Type**: Crossplane v2 provider — single Go module (same shape as features 001/002/003).

**Performance Goals**:
- SC-006 — at most one upstream catalog GET per `(PCRef, dimension)` per TTL window for the K8s preset + version dimensions. Mechanism: existing resolver cache (singleflight + TTL).
- Default reconcile poll interval: 60s (controller flag, kept at default).
- SC-001 — `apply → Ready=True` ≤ 20 min (cluster), ≤ 15 min (nodepool after cluster ready).

**Constraints**:
- Constitution §I — CRDs evolve additively post-`v1beta1`. Currently `v1alpha1`, freely revisable.
- Constitution §II — `Observe` read-only; writes idempotent; errors classified transient/terminal. **Highest-risk surfaces this feature adds**: (a) nodepool scaling via *relative* `count` deltas (`POST/DELETE /groups/{id}/nodes {count}`) — each `Update` re-observes `observedNodeCount` and issues `delta = desired − observed` so re-invocation converges and never double-adds; (b) version upgrade (`PATCH /versions/update`) — only fired when `observed != desired` and the target is a valid catalog upgrade, otherwise a no-op.
- Constitution §III — every `external` method ships with the four-case pattern; scaling/upgrade get dedicated cases.
- xpkg lint allow-list — `package/` holds CRDs/MRDs/webhook configs only (`project_xpkg_allowed_kinds`).

**Scale/Scope**: 3 new MR kinds, 1 new API group (1 new Go `apis` package + 1 new `internal/controller` package), 3 promoted resolver dimensions, 4 new e2e bundles. ~2 new Go packages following the established per-group pattern.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Verdict | Notes |
|-----------|---------|-------|
| I. CRD Contract Stability | ✓ PASS | All three CRDs are `v1alpha1` (freely revisable). CEL mutual-exclusion rules on `{networkRef,networkSelector,networkID}` / `{projectRef,projectSelector,projectID}` / `{clusterRef,clusterSelector,clusterID}` are additive. `make generate` regenerates DeepCopy + CRD YAML in the same change set (§I + Development Workflow). |
| II. Idempotent Reconciliation | ✓ PASS | `Observe` is read-only (cluster GET, nodegroup GET, kubeconfig GET, addon-list GET). Cluster `Create` is a single POST; `Delete` 404-idempotent. The two novel mutating surfaces are guarded: scaling computes a fresh delta from the observed count each reconcile (no double-add); version upgrade fires only on a real, valid version diff. Nodepool `Observe` needs the parent cluster ID → persisted in `status.atProvider.clusterID` so re-observation never depends on a live ref lookup. |
| III. Controller Test Discipline | ✓ PASS | Four-case (Success/NotFound/Transient/Terminal) per external method on all three kinds, using the counterfeiter fake. Scaling (up/down/no-op), upgrade (valid/invalid-target/no-op), and addon install/remove get explicit cases. |
| Provider Constraints | ✓ PASS | Credentials from the shared `ProviderConfigSpec` only. The fetched **kubeconfig contains a cluster credential** → published solely via the connection Secret (`writeConnectionSecretToRef`), never logged or placed in status/spec. Structured logging; standard `Synced`+`Ready` conditions. |
| Development Workflow | ✓ PASS | `make generate` after every `apis/` change; CI verifies a clean tree post-regen. Generated client tag-allowlist bump (`Kubernetes`) committed with the regenerated client + fake. |
| Complexity tracking | ✓ EMPTY | No principle violations to justify. |

**Re-check after Phase 1**: still PASS. The novel mechanics (relative-delta scaling, in-place version upgrade, kubeconfig-as-connection-secret) all fit the standard Observe→Create/Update/Delete pattern with single-owner side effects. No bespoke runtime machinery; no cross-controller mutation split.

## Project Structure

### Documentation (this feature)

```text
specs/004-k8s-cluster-nodepool/
├── plan.md                                          # This file
├── spec.md                                          # Feature spec (post /speckit-clarify)
├── research.md                                      # Phase 0 — decisions + alternatives
├── data-model.md                                    # Phase 1 — entities + lifecycle + dimensions
├── quickstart.md                                    # Phase 1 — operator walkthrough
├── contracts/
│   ├── kubernetescluster-v1alpha1.md               # Cluster kind contract
│   ├── kubernetesclusternodepool-v1alpha1.md       # Nodepool kind contract
│   ├── kubernetesclusteraddon-v1alpha1.md          # Addon kind contract
│   └── timeweb-k8s-endpoints.md                     # Upstream /api/v1/k8s/* endpoints touched
├── tasks.md                                         # Generated by /speckit-tasks
└── checklists/
    └── requirements.md                             # Spec quality checklist
```

### Source Code (repository root)

```text
apis/
├── v1alpha1/                               # PC + ClusterPC + PCU + ClusterPCU (unchanged)
├── compute/v1alpha1/                       # Existing (feat 003 — Server)
├── network/v1alpha1/                       # Existing (feat 003 — Network referenced by KubernetesCluster.networkRef)
├── kubernetes/v1alpha1/                    # NEW — managed-Kubernetes kinds
│   ├── kubernetescluster_types.go
│   ├── kubernetesclusternodepool_types.go
│   ├── kubernetesclusteraddon_types.go
│   ├── managed.go                          # ModernManaged forwarders for all three kinds
│   ├── doc.go
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
├── project/v1alpha1/                       # Existing — referenced by KubernetesCluster.projectRef
└── (other existing groups)

internal/
├── clients/timeweb/                        # API client wrapper + counterfeiter fake (extended)
│   └── generated/                          # oapi-codegen output; tag-allowlist adds "Kubernetes"
├── controller/
│   ├── kubernetes/                         # NEW — all three K8s controllers in one package
│   │   ├── connector.go                    # builds resolver + client; SetupCluster/Nodepool/Addon
│   │   ├── refs.go                         # same-namespace client.Get resolution {network,project,cluster}
│   │   ├── cluster_external.go             # + cluster_external_test.go
│   │   ├── cluster_upgrade.go              # version-upgrade convergence (+ test)
│   │   ├── nodepool_external.go            # + nodepool_external_test.go (incl. scaling)
│   │   └── addon_external.go               # + addon_external_test.go
│   ├── compute/  network/  project/  sshkey/  containerregistry/  s3bucket/   # Existing
│   └── shared/
│       └── resolver/
│           └── dimensions.go               # MODIFIED — promote 3 K8s dimensions stub→real
│
package/                                    # xpkg input — CRDs + metadata
├── crds/
│   ├── kubernetes.m.timeweb.crossplane.io_kubernetesclusters.yaml          # NEW
│   ├── kubernetes.m.timeweb.crossplane.io_kubernetesclusternodepools.yaml  # NEW
│   ├── kubernetes.m.timeweb.crossplane.io_kubernetesclusteraddons.yaml     # NEW
│   └── (existing CRDs)
└── crossplane.yaml                         # MODIFIED — description lists the new kinds

test/e2e/                                   # k3d-based e2e harness
├── kuttl/tests/
│   ├── 08-network-lifecycle/ … 11-floating-ip-bind/   # Existing (feat 003)
│   ├── 12-k8s-cluster-lifecycle/           # NEW — create/observe/kubeconfig/delete
│   ├── 13-k8s-nodepool-scaling/            # NEW — create pool, scale up, scale down
│   ├── 14-k8s-cluster-with-network/        # NEW — cluster attached to a Network via networkRef
│   └── 15-k8s-addon/                       # NEW — install + remove an addon
├── scripts/kuttl.sh                        # MODIFIED — discovers smallest master/worker preset + a k8s version
└── README.md                               # MODIFIED — new MRs + bundles documented

docs/
├── openapi-timeweb.json                    # Vendored upstream schema
├── servers.md                              # Existing (feat 003)
└── kubernetes.md                           # NEW — operator guide for the three K8s kinds
```

**Structure Decision**: One new API group `kubernetes.m.timeweb.crossplane.io` under the established `<svc>.m.timeweb.crossplane.io` convention (`project_crossplane_v2_conventions`). All three kinds live in a single `apis/kubernetes/v1alpha1` Go package and a single `internal/controller/kubernetes` controller package — the same one-package-per-group pattern feature 003 used for `network` (which hosts both `Network` and `FloatingIP`). Future managed-Kubernetes kinds extend this group + package rather than fragmenting. No cluster-scoped resources here, so the `apis/cluster/` split (deferred since feature 002) stays deferred.

## Complexity Tracking

> Empty — Constitution Check passed at both gates without violations. The three areas of new behavior (relative-delta nodepool scaling, in-place cluster version upgrade, kubeconfig published as a connection Secret) are each handled by the standard Observe→Create/Update/Delete pattern with single-owner, idempotent side effects — directly supported by crossplane-runtime v2. No bespoke runtime mechanics introduced.
