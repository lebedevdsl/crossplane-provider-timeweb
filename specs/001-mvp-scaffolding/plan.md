# Implementation Plan: MVP Scaffolding & Resource Coverage for the Timeweb Crossplane Provider

**Branch**: `001-mvp-scaffolding` | **Date**: 2026-05-18 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/001-mvp-scaffolding/spec.md`

## Summary

Build a Crossplane v2 provider for Timeweb Cloud as a fully native controller binary —
no Upjet, no Terraform-provider lineage. v0.1 ships seven CRDs: `ProviderConfig`
(cluster-scoped) plus six namespaced kinds — `Project`, `SshKey`, `S3Bucket`,
`ContainerRegistry`, `ContainerRegistryRepository`, and the observe-only
`ContainerRegistryPreset`. All reconcilers, API types, tests, examples, and CI/release
plumbing are owned by this repository.

The project uses **standard Go and Kubernetes/Crossplane ecosystem tools** — `go test`,
`golangci-lint`, `controller-gen`, `oapi-codegen`, `counterfeiter`, `kuttl`, `cosign` —
configured rather than reinvented. The "no external dependency" constraint applies to
**Terraform/Upjet code-generation lineage only**, not to community build-time tooling.
Constitution III's four-case unit-test rule is enforced by a documented test-naming
convention plus PR review, not by a custom in-tree linter.

Three architectural decisions, all anchored in the spec's Clarifications section,
distinguish this plan from the original `PLAN.md` draft:

1. **Native-only code lineage.** No build-time or runtime dependency on Upjet,
   `terraform-provider-timeweb-cloud`, or any out-of-tree code-generation pipeline
   that introduces a Terraform-provider source.
2. **Vertical-slice MVP.** Seven CRDs cover credentials, lifecycle CRUD with a typed
   connection Secret, an immutable-bodied resource, a project-scoped grouping
   resource, a parent-child managed-resource pair, and an observe-only catalog data
   source — together they prove every reconciler pattern the project will reuse in
   post-MVP work.
3. **Operator-needs-driven post-MVP.** The thirteen TF-set resources deferred from
   MVP are *candidates*, not commitments. There is no enumerated v1.0 resource list.

## Technical Context

**Language/Version**: Latest stable Go (currently 1.26). Tracked per Clarifications
2026-05-18 — when Go publishes a new release, `go.mod` is bumped via an explicit PR.
CI uses `actions/setup-go` with `go-version: 'stable'` so the workflow always builds
against the same toolchain as `go.mod` declares.

**Primary Dependencies**:
- `github.com/crossplane/crossplane-runtime` — managed-reconciler, ProviderConfig
  scaffolding, condition helpers, external-name plumbing, `ProviderConfigUsage`
  finalizer.
- `sigs.k8s.io/controller-runtime` — manager, reconcile loop, client, leader
  election, Prometheus metrics, health/ready probes.
- `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` — Kubernetes API surfaces.
- `github.com/oapi-codegen/oapi-codegen/v2` — build-time generator emitting the
  vendored Timeweb HTTP client under `internal/clients/timeweb/generated/` from
  `docs/openapi-timeweb.json`. Runtime imports only the generated package; the
  generator binary is a build tool, not linked into the provider image.
- `sigs.k8s.io/controller-tools` (`controller-gen`) — DeepCopy + CRD YAML emission
  from kubebuilder-annotated Go types. Build-time only.
- `golang.org/x/time/rate` — token-bucket rate limiter shared across the HTTP
  client.

**Build/CI tooling (standard ecosystem)**:
- `golangci-lint` (v2) — the community-standard aggregator linter (govet, staticcheck,
  errcheck, revive, gofmt/goimports, misspell, unconvert, …). Configured by
  `hack/.golangci.yml`. **Invoked via `go run` from `hack/tools.go`** so it always
  compiles against the project's own Go toolchain — no host-installed binary
  required (per Clarifications 2026-05-18). Sole static-analysis gate.
- `go test -race -cover` — the test merge gate. No custom test runner.
- `go tool cover` — produces coverage profiles for human inspection.
- `github.com/maxbrunsfeld/counterfeiter/v6` — generates fakes against the
  oapi-codegen-produced Timeweb client interface.
- `github.com/kudobuilder/kuttl` — Kubernetes-ecosystem-standard e2e harness.
- `cosign` — keyless signing of the OCI image and the Crossplane xpkg.

**Explicitly NOT used**: `crossplane/upjet`, `terraform-provider-timeweb-cloud`, any
Terraform binary or Terraform plugin SDK, any hand-rolled in-tree linter or test
runner (per Clarifications 2026-05-18).

**Storage**: N/A. Provider state lives in Kubernetes etcd as Custom Resources and
connection Secrets; no external datastore.

**Testing**:
- **Unit** (`go test ./...`): mandatory per Constitution III. Each controller's
  `external` client method is exercised via Go-standard table-driven sub-tests with
  four fixed names — `t.Run("Success", …)`, `t.Run("NotFound", …)`,
  `t.Run("TransientError", …)`, `t.Run("TerminalError", …)` — using a counterfeiter
  fake of the generated Timeweb client interface (FR-007). The merge gate is plain
  `go test`; the four-case naming discipline is verified by PR review against the
  documented convention.
- **End-to-end (kuttl, `test/e2e/`)**: kuttl bundles install the provider into a
  kind cluster wired to an in-memory `httptest`-based fake Timeweb server. Validates
  condition transitions, finalizer ownership, connection-Secret publishing, and
  immutable-field rejection — without spending money on Timeweb.
- **Live smoke** (`test/live/`): a small Go harness round-trips the three SC-002
  resources (Project, SshKey, S3Bucket) against a real Timeweb staging account using
  `TIMEWEB_CLOUD_TOKEN` from the environment. Run manually before each release;
  never in CI.

**Target Platform**:
- Provider OCI image: multi-arch (`linux/amd64`, `linux/arm64`) published to
  `ghcr.io/lebedevdsl/provider-timeweb`, cosign keyless signature on every tagged image.
- Runtime: Crossplane v2 control plane on Kubernetes 1.28+. Crossplane v1 is
  unsupported (spec Assumption).

**Project Type**: Crossplane v2 native provider distributed as an OCI package
(`xpkg`). Architecturally a long-running Kubernetes controller binary.

**Performance Goals**:
- Reconcile poll interval: 1 minute (Crossplane default). Operator-overridable via
  the provider's `--poll-interval` flag at install time.
- Time from CR `kubectl apply` to first reconciliation start: under 30 seconds.
- Reconciliations MUST NOT exceed Timeweb's documented 20 requests/sec/endpoint rate
  limit. The HTTP client embeds a `rate.Limiter` keyed by host; HTTP 429 responses
  trigger exponential backoff (1s → 32s, capped) and do NOT flip `Synced=False`
  (FR-014).

**Constraints**:
- Credentials: Timeweb token sourced exclusively from a Kubernetes `Secret` referenced
  by `ProviderConfig.spec.credentials` (FR-003, Constitution Provider Constraints).
  Token value never logged, never surfaced in events.
- Scope: All managed resources namespaced (FR-016). ProviderConfig cluster-scoped.
- Localization: All CRD descriptions English, hand-authored (FR-018).
- Code generation: Generated files committed alongside `apis/` changes; CI rejects
  PRs with a dirty tree after `make generate` (FR-008, Constitution I). The only two
  generators are `oapi-codegen` and `controller-gen`.
- Immutable fields: reject-and-surface, never auto-recreate (FR-017).
- Tooling: configure standard ecosystem tools, do not invent replacements
  (Clarifications 2026-05-18).

**Scale/Scope**:
- 7 published CRDs in v0.1 (1 cluster-scoped + 6 namespaced).
- Expected per-cluster fleet: ~10–50 CRs across one or two environments.
- Repository size estimate: ~3 kLOC hand-written Go (controllers, types,
  ProviderConfig glue, error/rate-limit helpers), ~20 kLOC `oapi-codegen` output,
  ~3 kLOC test code.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

The constitution at `.specify/memory/constitution.md` (v1.0.0) defines three core
principles.

### I. CRD Contract Stability (NON-NEGOTIABLE)

**Status**: PASS.
- All seven CRDs start at `v1alpha1`. Graduation to `v1beta1` per Constitution I
  only after one minor release of production use.
- `make generate` runs `oapi-codegen` (when `docs/openapi-timeweb.json` changed) and
  `controller-gen` (always); CI runs `make generate` and `git diff --exit-code` to
  enforce FR-008.
- Native-only ownership of CRD shape: bumping `openapi.json` updates the **client**,
  not the **CRD**.

### II. Idempotent, Side-Effect-Aware Reconciliation

**Status**: PASS.
- Every controller implements `managed.ExternalClient` directly. Per-method
  idempotency documented and unit-tested via the four-named-sub-test convention.
- External-name is the sole upstream identity (FR-013). Numeric IDs stringified;
  parent-child `ContainerRegistryRepository` uses composite encoding `parent/child`
  (research.md §R-2).
- HTTP errors flow through a single classifier (research.md §R-3): 408/409/425/429/5xx
  → transient, 4xx (other) → terminal.
- The observe-only `ContainerRegistryPreset` reconciler runs on a timer (research.md
  §R-6); it is the only controller reaching the API on a non-CR trigger.

### III. Controller Test Discipline

**Status**: PASS.
- Every hand-written `external` client implementation ships with unit tests
  exercising four named sub-tests (`Success` / `NotFound` / `TransientError` /
  `TerminalError`) per method against a counterfeiter fake (FR-007).
- The merge gate is plain `go test -race -cover` and `golangci-lint`; the four-name
  discipline is enforced by PR review against the convention documented in FR-007
  and applied uniformly across every controller package. No custom linter (per
  Clarifications 2026-05-18).
- Live-account integration tests under `test/live/` are encouraged but explicitly
  OPTIONAL per Constitution III; SC-002 names the three resources smoke-tested
  before each release.

**No violations. Complexity Tracking remains empty. Proceeding to Phase 0.**

## Project Structure

### Documentation (this feature)

```text
specs/001-mvp-scaffolding/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output: per-kind CRD contracts
│   ├── providerconfig-v1alpha1.md
│   ├── project-v1alpha1.md
│   ├── sshkey-v1alpha1.md
│   ├── s3bucket-v1alpha1.md
│   ├── containerregistry-v1alpha1.md
│   ├── containerregistryrepository-v1alpha1.md
│   └── containerregistrypreset-v1alpha1.md
├── checklists/
│   └── requirements.md  # from /speckit-specify
└── tasks.md             # produced later by /speckit-tasks
```

### Source Code (repository root)

```text
crossplane-provider-timeweb/
├── apis/                                 # CRD Go types (hand-written, kubebuilder-annotated)
│   ├── v1alpha1/                         # ProviderConfig + ProviderConfigUsage (cluster-scoped)
│   ├── project/v1alpha1/                 # Project MR types + zz_generated_deepcopy.go
│   ├── sshkey/v1alpha1/                  # SshKey MR types
│   ├── objectstorage/v1alpha1/           # S3Bucket MR types
│   ├── containerregistry/v1alpha1/       # ContainerRegistry, Repository, Preset
│   └── zz_register.go                    # generated kind registry (controller-gen)
├── cmd/
│   └── provider/main.go                  # entry point: manager setup, controller wiring,
│                                         # leader election, metrics, healthz, signal handling
├── examples/                             # FR-011: one example per published kind
│   ├── providerconfig.yaml
│   ├── project.yaml
│   ├── sshkey.yaml
│   ├── s3bucket.yaml
│   ├── containerregistry.yaml
│   ├── containerregistryrepository.yaml
│   └── README.md
├── internal/
│   ├── clients/
│   │   └── timeweb/
│   │       ├── generated/                # oapi-codegen output (do not edit)
│   │       ├── client.go                 # thin wrapper: bearer round-tripper, rate
│   │       │                             # limiter, structured logging, secret-aware
│   │       │                             # error masking
│   │       ├── errors.go                 # transient/terminal classifier (R-3)
│   │       ├── fake.go                   # counterfeiter-generated fake of the
│   │       │                             # client interface
│   │       └── doc.go
│   ├── controller/
│   │   ├── providerconfig/               # ProviderConfig + Usage tracker
│   │   ├── project/                      # external client + reconciler wiring
│   │   ├── sshkey/
│   │   ├── s3bucket/
│   │   ├── containerregistry/            # registry, repository, preset (timer-based)
│   │   └── shared/                       # external-name encode/decode helpers,
│   │                                     # immutable-field reject helper, condition
│   │                                     # builders (used by every controller)
│   ├── features/                         # alpha/beta feature-gate flags (if needed)
│   └── version/                          # version + build metadata
├── package/                              # Crossplane package metadata
│   ├── crds/                             # generated CRD YAML (one file per kind)
│   ├── policies/                         # ValidatingAdmissionPolicy manifests
│   │   └── preset-readonly.yaml          # rejects edits to ContainerRegistryPreset.spec
│   └── crossplane.yaml                   # meta.pkg.crossplane.io/v1 Provider manifest
├── docs/
│   ├── openapi-timeweb.json              # vendored API spec (moved from repo root)
│   ├── data-sources-design.md            # FR-010 deliverable (User Story 3)
│   ├── resources/                        # per-resource operator guides
│   │   ├── providerconfig.md
│   │   ├── project.md
│   │   ├── sshkey.md
│   │   ├── s3bucket.md
│   │   ├── containerregistry.md
│   │   ├── containerregistryrepository.md
│   │   └── containerregistrypreset.md
│   └── archive/PLAN.md                   # original Upjet-leaning draft, preserved
│                                         # with a "superseded" header (R-10)
├── test/
│   ├── e2e/kuttl/                        # kuttl bundles using a fake Timeweb server
│   │   ├── 00-providerconfig/
│   │   ├── 10-project-crud/
│   │   ├── 20-sshkey-crud/
│   │   ├── 30-s3bucket-crud/
│   │   ├── 40-containerregistry-crud/
│   │   └── 50-cr-repository-and-preset/
│   └── live/                             # manual SC-002 smoke against staging Timeweb
│       ├── main.go
│       └── README.md
├── hack/
│   ├── boilerplate.go.txt                # license header for generated files
│   ├── tools.go                          # pin build-tool versions in go.mod
│   │                                     # (oapi-codegen, controller-gen,
│   │                                     # counterfeiter, kuttl, golangci-lint)
│   ├── .golangci.yml                     # golangci-lint configuration
│   └── prepare.sh                        # one-time bootstrap (clone-and-run-this)
├── .github/workflows/
│   ├── ci.yaml                           # FR-012: golangci-lint + generate-clean + go test -race -cover
│   └── release.yaml                      # tag → multi-arch OCI image + cosign keyless
├── Makefile                              # native-only targets: generate, build, test,
│                                         # cover, lint, reviewable, image, xpkg.build, release
├── go.mod
├── go.sum
├── README.md
├── CLAUDE.md                             # pointer to current plan + companion docs
└── PROVIDER.md                           # operator install/usage guide
```

**Structure Decision**: Follow the Crossplane native-provider layout
(`crossplane/provider-template` shape) with these deliberate deviations:

1. No `config/` directory (that's an Upjet artifact). Per-resource configuration —
   external-name encoders, immutable-field declarations, late-init exclusions —
   lives inside each controller package and the kubebuilder annotations on each
   `apis/` type.
2. `internal/clients/timeweb/` separates `generated/` (oapi-codegen output) from a
   thin hand-written wrapper that owns auth, rate limiting, structured logging, and
   error classification. Keeps the generated layer regeneration-safe.
3. Test discipline relies on standard Go ecosystem tools — `go test` and
   `golangci-lint`. Constitution III's four-case rule is enforced by the documented
   sub-test naming convention (FR-007) plus PR review, not by a custom linter.
4. `docs/archive/PLAN.md` preserves the original Upjet-leaning draft with a
   superseded header so the history is recoverable without misleading new
   contributors (R-10).

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

No constitutional violations. This section is intentionally empty.
