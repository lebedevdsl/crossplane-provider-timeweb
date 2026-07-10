# Implementation Plan: Nodepool Taints (+ label mutability)

**Branch**: `015-nodepool-taints` | **Date**: 2026-07-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/015-nodepool-taints/spec.md`

## Summary

Add declarative Kubernetes node **taints** to `KubernetesClusterNodepool`
(`kubernetes.m.timeweb.crossplane.io/v1alpha1`) and make both taints and the
existing node **labels** day-2 mutable with single-writer drift correction.
Upstream surface (live-verified 2026-07-10): node-group **create** accepts
`taints: [{key, value, effect}]` alongside `labels`; node-group **GET**
returns both arrays; the same `/groups/{group_id}` path accepts an
undocumented **PATCH** carrying `labels`/`taints` (panel-verified; the
public-host PATCH is exercised through the provider during validation). The
controller extends the existing Observe/Update pair: Observe set-diffs
declared vs upstream-reported taints+labels; Update issues one PATCH with
only the owned fields (`name`, `labels`, `taints`) when they drift — before
the autoscaling early-return, so metadata converges on autoscaled pools too.
CRD gains a bounded, admission-validated `taints` list (effect enum, k8s
label-syntax patterns, duplicate key+effect CEL guard); `labels` keeps its
shape and loses its create-only contract.

## Technical Context

**Language/Version**: Go (latest stable, per `go.mod` policy — currently the
toolchain pinned there)

**Primary Dependencies**: crossplane-runtime v2, controller-runtime,
oapi-codegen-generated Timeweb client (`internal/clients/timeweb/generated`,
regenerated from the hand-patched `docs/openapi-timeweb.json`)

**Storage**: N/A (state lives in the CR + upstream Timeweb API)

**Testing**: Go unit tests with fake Timeweb HTTP client (constitution III);
kuttl e2e bundle (authored; live validation via custom manifest against a
pre-existing cluster — see Validation strategy)

**Target Platform**: Linux container (distroless), amd64; runs in any
Crossplane v2 control plane

**Project Type**: Kubernetes controller (Crossplane provider)

**Performance Goals**: no new API traffic in steady state (taints/labels ride
the existing GET in Observe); at most one extra PATCH per reconcile while
converging

**Constraints**: Timeweb API behind Qrator — no bursts, reuse existing
single-GET Observe shape; PATCH body carries ONLY owned fields so the
undocumented endpoint can't clobber autoscaler/sizing state

**Scale/Scope**: one kind touched; ≤12 taints per pool (MaxItems, CEL cost
budget); no new kinds, no new controllers, no ref/selector machinery

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-checked post-design — PASS.*

- **I. CRD Contract Stability**: additive only — new optional `taints` field
  on a v1alpha1 kind; `labels` schema unchanged (its create-only *comment*
  is documentation, not schema; alpha allows the contract shift and it is a
  release-note item). DeepCopy + CRD YAML regenerated and committed in the
  same PR (`make generate`). PASS.
- **II. Idempotent, Side-Effect-Aware Reconciliation**: Observe stays
  read-only (taints/labels parsed from the existing GET); Update recomputes
  the diff from fresh upstream state every reconcile and PATCHes the full
  declared set (set-replace — naturally idempotent, no deltas to double-
  apply); external-name authority unchanged; Timeweb errors classified via
  the existing `timeweb.Classify` path. PASS.
- **III. Controller Test Discipline**: unit tests extend
  `nodepool_external_test.go` with fake-client coverage: up-to-date
  detection (order-insensitive), PATCH-on-drift success, PATCH transient +
  terminal error classification, empty-set clear, autoscaling-enabled
  metadata convergence, not-found. PASS.
- **Provider Constraints**: no credentials in specs/logs; structured logging
  untouched; conditions stay `Synced`/`Ready` + existing shared reasons.
  PASS.

## Project Structure

### Documentation (this feature)

```text
specs/015-nodepool-taints/
├── plan.md              # This file
├── research.md          # Phase 0 (R-1..R-8, live-probe evidence)
├── data-model.md        # Phase 1 (Taint type, diff semantics)
├── quickstart.md        # Phase 1 (operator walkthrough)
├── contracts/
│   ├── nodepool-taints-v1alpha1.md      # CRD delta contract
│   └── timeweb-nodegroup-patch.md       # endpoint inventory + probe log
└── tasks.md             # Phase 2 (/speckit-tasks — NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
apis/kubernetes/v1alpha1/
└── kubernetesclusternodepool_types.go   # + NodepoolTaint type, taints field,
                                         #   validation markers; labels comment
                                         #   updated (mutable)

docs/
└── openapi-timeweb.json                 # hand-patch: NodeGroupIn.taints,
                                         #   Taint schema, PATCH op on
                                         #   /k8s/clusters/{id}/groups/{gid}

internal/clients/timeweb/generated/
└── zz_generated_client.go               # regenerated (UpdateClusterNodeGroup,
                                         #   Taint type)

internal/controller/kubernetes/
├── nodepool_external.go                 # observe struct + set-diff +
                                         #   metadata PATCH in Update
└── nodepool_external_test.go            # new unit coverage

package/crds/ + examples/                # regenerated CRD YAML + example MR
docs/kubernetes.md                       # taints/labels section refresh
test/e2e/kuttl/tests/22-nodepool-taints/ # kuttl bundle (authored)
```

**Structure Decision**: existing single-module provider layout; no new
packages. The only generated-code change is the client regen; all logic
lands in the existing nodepool controller file pair.

## Validation strategy (implementation-phase gate)

The kuttl bundle `22-nodepool-taints` is authored as the durable e2e
artifact. The live gate for this feature runs lighter: `make e2e.up` +
`make e2e.deploy` (local k3d control plane, side-loaded provider build),
then a **custom manifest** — a minimal 1-node pool carrying taints+labels
attached by flat `clusterID` to a pre-existing Ready cluster — exercising,
in order: create-with-taints, node-level propagation (read-only kubeconfig
check), day-2 taint/label edit (public-host PATCH verification), empty-set
clear, out-of-band drift reversion, delete. This validates the full FR
surface without provisioning a cluster; the production `inyan-infra`
control plane and its `cloud-infra` MRs are not touched.

## Complexity Tracking

No constitution violations — table intentionally empty.
