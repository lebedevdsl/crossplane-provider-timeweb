<!--
Sync Impact Report
==================
Version change: (uninitialized template) → 1.0.0
Bump rationale: Initial ratification — all placeholders filled with concrete
content for the first time. Semantic versioning begins at 1.0.0 per project
convention for an adopted (non-draft) governing document.

Modified principles:
  - [PRINCIPLE_1_NAME] → I. CRD Contract Stability (NON-NEGOTIABLE)
  - [PRINCIPLE_2_NAME] → II. Idempotent, Side-Effect-Aware Reconciliation
  - [PRINCIPLE_3_NAME] → III. Controller Test Discipline
Principles removed: [PRINCIPLE_4_NAME], [PRINCIPLE_5_NAME]
  (intentional — 3-principle constitution requested)

Added sections:
  - Provider Constraints (replaces [SECTION_2_NAME])
  - Development Workflow (replaces [SECTION_3_NAME])

Removed sections: none

Templates requiring updates:
  - ✅ .specify/templates/plan-template.md — Constitution Check is
        principle-agnostic ("[Gates determined based on constitution file]");
        no edit needed.
  - ✅ .specify/templates/spec-template.md — no constitution references;
        no edit needed.
  - ✅ .specify/templates/tasks-template.md — no constitution references;
        no edit needed.
  - ✅ .specify/templates/checklist-template.md — generic scaffold;
        no edit needed.
  - ✅ CLAUDE.md — pointer-only file; no edit needed.

Follow-up TODOs: none
-->

# Crossplane Provider Timeweb Constitution

## Core Principles

### I. CRD Contract Stability (NON-NEGOTIABLE)

Every Managed Resource API is published as a versioned Kubernetes CRD. Once a version
reaches `v1beta1` or beyond, its schema MUST evolve only via additive, backward-compatible
changes. Breaking field changes REQUIRE a new API version (e.g. `v1beta1` → `v1`) with a
documented deprecation cycle and, where field semantics shift, a conversion strategy.
Generated artifacts — `zz_generated_*.go`, `DeepCopy` methods, `managed.Managed`
implementations, and CRD YAML manifests — MUST be regenerated and committed in the same
pull request as any change under `apis/`, so the public contract and reconciler logic
never drift.

**Rationale**: Users of a Crossplane provider declare cloud state in YAML and reconcile
it through GitOps. Silent schema drift corrupts that workflow and can stick clusters in
unrecoverable states.

### II. Idempotent, Side-Effect-Aware Reconciliation

The `external` client's `Observe`, `Create`, `Update`, and `Delete` methods MUST be safe
to invoke repeatedly. `Observe` MUST be read-only and report `ResourceExists` /
`ResourceUpToDate` without mutating upstream Timeweb state. `Create`, `Update`, and
`Delete` MUST tolerate eventual consistency: re-invocation MUST NOT create duplicates,
MUST NOT double-charge, and MUST converge on the declared spec. The external-name
annotation is the sole authority for upstream resource identity. Errors returned by the
Timeweb API MUST be explicitly classified as transient (trigger a requeue) or terminal
(surface `Synced=False` with a reason on the CR); errors MUST NEVER be silently
swallowed.

**Rationale**: Crossplane invokes the reconciler on every Kubernetes event and on a
periodic poll. Non-idempotent calls produce orphaned cloud resources, duplicate charges,
and CRs stuck in inconsistent conditions — failure modes that are expensive to detect
and to clean up.

### III. Controller Test Discipline

Every `external` client implementation MUST ship with unit tests covering, at minimum:
the success path of each method, the "resource not found" path, and at least one
transient and one terminal error path. Tests MUST use a fake Timeweb API client (no live
HTTP). Pull requests that add or modify a managed resource MUST NOT merge unless these
tests pass in CI. Integration tests against a real Timeweb account are encouraged but
OPTIONAL; the unit-test gate is mandatory.

**Rationale**: Controller bugs manifest as silent infrastructure drift, frequently
discovered after hours. Catching them in CI costs minutes; catching them in production
costs incidents.

## Provider Constraints

- **Credentials**: Timeweb API tokens MUST be sourced exclusively from
  `ProviderConfig.spec.credentials` referencing a Kubernetes `Secret`. Tokens MUST NEVER
  appear in CR specs, status fields, log lines, Kubernetes events, or error messages.
- **Runtime compatibility**: Each release MUST declare the supported `crossplane-runtime`
  version range in `go.mod` and the supported Kubernetes server versions in the README.
  Bumping either is a release-note item.
- **Observability**: Controllers MUST emit structured logs through the runtime-provided
  logger and surface user-facing state via the standard `Synced` and `Ready` conditions.
  Ad-hoc `fmt.Println`/`log.Printf` calls are prohibited in reconciliation paths.

## Development Workflow

- **Code generation**: After any change under `apis/`, contributors MUST run the
  project's code-generation target (e.g. `make generate`) and commit the regenerated
  `zz_generated_*` files in the same PR. CI MUST verify the working tree is clean after
  regeneration.
- **Review gates**: PRs that add or modify a managed resource MUST be approved by at
  least one maintainer familiar with that Timeweb resource. PRs that touch shared
  reconciler scaffolding MUST be approved by a project maintainer.
- **Versioning**: Provider releases follow Semantic Versioning (MAJOR.MINOR.PATCH). CRD
  API version transitions (`v1alpha1` → `v1beta1` → `v1`) are independent of provider
  semver but MUST be highlighted in release notes alongside any migration steps.

## Governance

This constitution supersedes ad-hoc conventions for managed-resource design,
reconciliation behavior, and test coverage. Amendments require a pull request that
updates this document, increments the version per the policy below, and updates the
`Last Amended` date. The PR description MUST justify the bump type.

- **Versioning policy**:
  - **MAJOR**: Removal or backward-incompatible redefinition of a principle or
    governance rule.
  - **MINOR**: A new principle or section, or materially expanded normative guidance
    within an existing principle.
  - **PATCH**: Clarifications, wording fixes, typos, and non-semantic refinements.
- **Compliance review**: Every PR description MUST identify the principles its changes
  touch. Reviewers MUST verify alignment with this constitution and reject (or require
  justification for) complexity that cannot be defended under these principles.
- **Runtime guidance**: `CLAUDE.md` at the repo root remains the pointer to the current
  feature plan; this constitution governs the rules that any plan MUST respect.

**Version**: 1.0.0 | **Ratified**: 2026-05-18 | **Last Amended**: 2026-05-18
