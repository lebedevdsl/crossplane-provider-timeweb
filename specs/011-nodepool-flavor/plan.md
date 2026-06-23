# Implementation Plan: Nodepool Worker Configurator Flavor

**Branch**: `011-nodepool-flavor` | **Date**: 2026-06-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/011-nodepool-flavor/spec.md`

## Summary

Add an optional `flavor` selector (`standard` | `dedicated-cpu`, default `standard`)
to `KubernetesClusterNodepool` custom worker sizing so the operator picks the worker
configurator **family** explicitly. Today the resolver auto-selects among all matching
non-master configurators by a tightest-fit sort, which silently lands on the
`dedicated-cpu` family (id 69) over `general` (id 59) because dedicated has a tighter
RAM ceiling — then the API rejects under-ratio sizings (e.g. 2cpu/4GB →
`invalid_configuration_ram: min ram 8192`). The fix constrains selection to the chosen
family's catalog tag **before** the fit sort, defaulting to `general` so small pools
"just work" like the cloud panel's default.

Technical approach: add `Flavor` to the nodepool's `Resources` API struct
(enum + kubebuilder default `standard`); add `RequireTags []string` to the resolver's
`ConfiguratorInput` and a tag-filter step in `SelectConfigurator` (between the capability
filter and the standard/promo partition); map `flavor → catalog tag` in
`resolveK8sConfigurator`. Master sizing is untouched (single family).

## Technical Context

**Language/Version**: Go (latest stable, tracked by `go.mod` — see project tooling policy)

**Primary Dependencies**: crossplane-runtime, sigs.k8s.io/controller-runtime,
oapi-codegen-generated Timeweb client (`internal/clients/timeweb/generated`)

**Storage**: N/A (stateless reconciler; sizing catalog fetched + cached in-memory by the resolver)

**Testing**: `go test` with the fake catalog client for resolver/`SelectConfigurator`
unit tests; controller-level tests with the fake Timeweb client; kuttl/k3d e2e bundle
optional

**Target Platform**: Linux container (amd64), distroless/static:nonroot

**Project Type**: Single Go module — Crossplane provider

**Performance Goals**: N/A — selection is in-memory over a small per-location catalog
(≈10 entries); no new upstream calls

**Constraints**: additive CRD change only (Principle I); no new Timeweb API calls in the
hot path (Qrator rate-limits — see project memory); any new CEL must be bounded
(CEL cost budget)

**Scale/Scope**: one new optional enum field + one new resolver filter axis; ~3 source
files + generated artifacts + tests

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. CRD Contract Stability (NON-NEGOTIABLE)** — PASS. `flavor` is a new **optional**
  field with a default; purely additive, backward-compatible (FR-007). `zz_generated_*`,
  DeepCopy, and CRD YAML MUST be regenerated and committed in the same PR (`make generate`).
- **II. Idempotent, Side-Effect-Aware Reconciliation** — PASS. `flavor` only influences
  Create-time configurator resolution. `Observe` stays read-only; the locked configurator
  on already-created pools is preserved (no re-resolution, no churn). No new side effects.
- **III. Controller Test Discipline** — PASS (with required additions). New unit tests:
  `SelectConfigurator` tag-filter (standard vs dedicated selection, cross-family exclusion),
  the `flavor → tag` mapping, default-to-standard, and the "no entry in family fits → clear
  error, no substitution" path. Fake catalog client; no live HTTP.

No violations → **Complexity Tracking not required**.

## Project Structure

### Documentation (this feature)

```text
specs/011-nodepool-flavor/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── nodepool-flavor-v1alpha1.md
│   └── configurator-flavor-selection.md
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
apis/kubernetes/v1alpha1/
└── kubernetesclusternodepool_types.go   # add Flavor to the worker Resources struct (enum + default)

internal/controller/shared/resolver/
├── resolver.go              # ConfiguratorInput: add RequireTags []string
└── select_configurator.go   # tag-filter step in SelectConfigurator (after capability, before standard/promo partition)

internal/controller/kubernetes/
├── configurator.go          # resolveK8sConfigurator: accept flavor, map flavor→tag, pass RequireTags
└── nodepool_external.go     # pass cr.Spec.ForProvider.Resources.Flavor into resolveK8sConfigurator

# regenerated in the same PR:
package/crds/…               # CRD YAML (make generate)
apis/kubernetes/v1alpha1/zz_generated.deepcopy.go
```

**Structure Decision**: Reuse the existing single-module provider layout. The change is
localized to the nodepool API type, the shared resolver's configurator selection, and the
kubernetes controller's configurator helper. No new packages or top-level directories.

## Complexity Tracking

> No constitution violations — section intentionally empty.
