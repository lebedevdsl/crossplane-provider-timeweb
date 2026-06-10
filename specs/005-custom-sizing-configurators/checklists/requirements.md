# Specification Quality Checklist: Custom Sizing (Configurators) + Group Tidy-up + Tech Debt

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-06-07
**Feature**: [spec.md](../spec.md)

## Content Quality

- [X] No implementation details (languages, frameworks, APIs)
- [X] Focused on user value and business needs
- [X] Written for non-technical stakeholders
- [X] All mandatory sections completed

## Requirement Completeness

- [X] No [NEEDS CLARIFICATION] markers remain
- [X] Requirements are testable and unambiguous
- [X] Success criteria are measurable
- [X] Success criteria are technology-agnostic (no implementation details)
- [X] All acceptance scenarios are defined
- [X] Edge cases are identified
- [X] Scope is clearly bounded
- [X] Dependencies and assumptions identified

## Feature Readiness

- [X] All functional requirements have clear acceptance criteria
- [X] User scenarios cover primary flows
- [X] Feature meets measurable outcomes defined in Success Criteria
- [X] No implementation details leak into specification

## Notes

- Crossplane-provider spec: intentionally references upstream Timeweb API shapes
  (configurators, `configuration` blocks) + established v2/resolver conventions,
  consistent with specs 001–004.
- **All 16 items pass.** The three clarifications are resolved (2026-06-07):
  (1) ContainerRegistry = **hard move** to `kubernetes.m.timeweb.crossplane.io`
  (mirrors the dashboard's Kubernetes→"Реестры контейнеров" tab; breaking,
  acceptable pre-1.0); (2) presets = **additive** (kept first-class);
  (3) tech-debt scope = Server CEL bug + e2e harness fixes + condition-reason
  alignment (auto-VPC orphan handling deferred).
- `ramGB` (not `ramMB`) is the operator-facing RAM unit per the 2026-06-07
  decision — controller normalizes ×1024 to the upstream MB units.
- The Server `resources` field set + configurator selection algorithm follow the
  feature-003 deferral clarification verbatim; the resolver `Configurator`
  primitive already exists (feature 002).
