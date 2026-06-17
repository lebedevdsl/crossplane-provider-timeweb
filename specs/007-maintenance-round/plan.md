# Implementation Plan: Maintenance Round — Placement, Preset & Printcolumn Cleanups

**Branch**: `007-maintenance-round` | **Date**: 2026-06-17 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/007-maintenance-round/spec.md`

## Summary

A cross-cutting maintenance round — no new kinds. It standardizes the provider's
**placement vocabulary** (uniform required `location` + optional `availabilityZone`,
sourced live from `/api/v2/locations` so all 8 regions work), **simplifies preset
names** (bare `<short>` accepted, location-filtered resolution, location-scoped
not-found errors), **rationalizes printcolumns** (fixed order, single `ID`,
`-o wide` diagnostics, FloatingIP bound-column collapse, nodepool `PUBLIC-IP`),
and closes a set of **observability + correctness gaps** found by two Go reviews
(Sonnet+Opus) and a devops review (gate `Ready` on real state, terminal
`UpstreamFailed`, a single shared reason vocabulary + `MapResolverErrorToCondition`,
transition-only Events, status mirrors, working examples/docs, and ~25 backlog
fixes). **Backward compatibility is a hard constraint** (FR-004): existing manifests
and the long-form slug must keep resolving; no change to which upstream resources are
created (FR-007).

## Technical Context

**Language/Version**: Go (latest stable per `project_go_tooling_policy`); Crossplane
v2 namespaced-MR model.

**Primary Dependencies**: crossplane-runtime v2, controller-runtime, the in-repo
`internal/clients/timeweb` HTTP client (the hand-patched-superset OpenAPI →
`oapi-codegen`), the in-controller catalog `resolver`, counterfeiter fakes.

**Storage**: N/A (external state is Timeweb's API; MR status mirrors it).

**Testing**: `go test` four-case pattern (§III) for every touched `external` method;
existing kuttl/k3d e2e bundles assert no regression. **Note:** the e2e condition
asserts must be reordered to Crossplane's emission order — kuttl v0.26 matches
`status.conditions` **positionally** (the 006 bundle-18 "hang" root cause); fix as
part of the e2e-touching work or the asserts silently never match.

**Target Platform**: Linux provider pod; k3d for e2e.

**Project Type**: Crossplane provider (single Go module).

**Performance/Constraints**: ≤1 catalog GET per (PCRef, dim) per TTL (shared cache);
**backward compatibility is mandatory** (FR-004); no behavioral change to created
resources (FR-007). `/api/v2/locations` is a new cached catalog read.

**Scale/Scope**: All MR kinds (apis/*/v1alpha1), the resolver, all controllers,
`internal/controller/shared`, docs/ + examples/. No new client surface.

### Open clarifications (NEEDS CLARIFICATION → Phase 0 research)

- **R-1 (US1/US2)**: confirm `/api/v2/locations` is the authoritative, stable source
  for region→zone and that it covers every product's placement; decide caching shape.
- **R-2 (US1)**: whether Router/KubernetesCluster move to required `location` +
  optional `availabilityZone` (their current required field is `availabilityZone`) —
  schema change + CEL; backward-compat path for existing `availabilityZone`-only
  manifests.
- **R-3 (US2)**: the bare-slug matching + back-compat rule (accept `<short>` AND
  `<short>-<location>` AND the `-<id>` disambiguator) and the location-scoped
  not-found list shape.
- **R-4 (US4)**: per billable kind, whether a no-pay signal exists in the payload
  (probe) — `PaymentRequired` is wired ONLY where found (Server confirmed; Router
  best-effort); the rest get `UpstreamFailed`/state-gating only.
- **R-5 (US4)**: the single shared reason vocabulary set + the `MapResolverErrorToCondition`
  contract, and the transition-only Event mechanism (emit on condition change).

## Constitution Check

*GATE: Must pass before Phase 0. Re-check after Phase 1.*

- **§III four-case tests** — every touched `external` method (conditions, resolver
  mapping, the backlog correctness fixes) keeps/gains success/not-found/transient/
  terminal coverage; the Go-review test-gap items (floating-IP bind, adoption guard,
  AZ-echo, version-downgrade) are filled. **PASS (planned).**
- **v2 conventions** — namespaced `.m.` groups, `managed.WithManagementPolicies()`
  unchanged. **PASS.**
- **No `deletionPolicy`** in examples/docs (the new examples must use modern shape;
  FR-015). **PASS.**
- **Error classification** — the round *tightens* it (4xx-permanent vs 5xx-transient,
  empty-catalog-as-transient, the shared reason vocabulary). **PASS / improved.**
- **Standard tooling** (no invented replacements). **PASS.**
- **Backward compatibility (FR-004)** — additive/aliasing only; no breaking change to
  applied manifests. Verified against existing e2e bundles. **PASS (gated by SC-005).**

No unjustified violations → Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/007-maintenance-round/
├── plan.md              # this file
├── spec.md              # US1–US5, FR-001..017, Maintenance Backlog (correlated)
├── research.md          # Phase 0 (R-1..R-5)
├── data-model.md        # CRD field/printcolumn/status changes, condition vocabulary
├── contracts/           # /api/v2/locations contract; not-found-error format; reason set
├── quickstart.md        # operator-facing before/after; onboarding/auth doc outline
└── tasks.md             # /speckit-tasks output
```

### Source Code (repository root) — areas touched

```text
apis/*/v1alpha1/
├── *_types.go            # uniform location/AZ fields + CEL (US1); printcolumn order +
│                         #   ID/BOUND-TO collapse + nodepool PUBLIC-IP (US3); status
│                         #   mirrors — Network state, registry state+endpoint, addon
│                         #   version (US4); immutability CEL gaps (backlog); projectID
│                         #   type unify + selectors + comment/description (FR-014)
└── zz_generated.deepcopy.go   # make generate

internal/controller/shared/
├── azlocation.go         # REPLACE the hardcoded 4-entry table with a /api/v2/locations
│                         #   -sourced lookup (US1; fixes defaultAZByLocation bug)
├── conditions.go         # the single shared reason vocabulary (US4/FR-009a)
├── ptr.go (new)          # hoisted ptrEqString/stringPtr/derefString/derefBool (backlog P3)
└── map_resolver_error.go (new)  # shared.MapResolverErrorToCondition (US4)

internal/controller/shared/resolver/
├── slug.go               # bare-slug match + back-compat + overflow guard (US2, backlog)
├── resolve.go            # Location filter in PresetInput (mirror Zone) (US2)
├── errors.go             # location-scoped not-found list (US2)
├── dimensions.go         # classifyUpstream 4xx-permanent; drop errIs (backlog)
└── cache.go              # don't memoize empty-slice 200 (backlog)

internal/controller/{compute,network,kubernetes,s3bucket,containerregistry,project,sshkey}/
                          # condition gating + transition-only Events + the resolver-error
                          #   mapping at all call-sites (US4); the per-controller backlog
                          #   correctness fixes (router body-close, server deferred-bind
                          #   churn, cluster adoption/kubeconfig, addon find/immutability,
                          #   repository 404, version classify, etc.)

docs/ + examples/         # fix broken examples, add missing ones + ProviderConfig +
                          #   getting-started/auth doc (US5/FR-015/016)
test/e2e/kuttl/           # reorder condition asserts to emission order (kuttl positional);
                          #   add region-coverage + simplified-slug assertions
```

**Structure Decision**: Existing single-module layout. The round centralizes shared
behavior (`shared/` gains the reason vocabulary, the resolver-error mapper, the ptr
helpers, and the live location lookup) and then sweeps each controller/API onto it —
maximizing consistency while keeping every change additive/back-compatible.

## Implementation Strategy (by user story, MVP-first)

1. **Foundational shared primitives** — the `/api/v2/locations` lookup, the shared
   reason vocabulary, `MapResolverErrorToCondition`, the `ptr` helpers. Unblocks US1/US2/US4.
2. **US1 (P1)** placement uniformity + region coverage — depends on the location lookup;
   includes the `defaultAZByLocation` inversion fix.
3. **US2 (P2)** preset-slug simplification + location-scoped errors.
4. **US4 (P1)** observability — conditions/events/status mirrors/shared vocabulary, swept
   across all kinds; absorbs the Go-review condition findings.
5. **US3 (P3)** printcolumns.
6. **US5 (P1)** examples + onboarding/auth docs.
7. **Backlog sweep** — the remaining correlated Go-review correctness/consistency fixes
   (FR-017), each small + four-case-tested.
8. **e2e** — reorder condition asserts (kuttl positional fix), add region/slug assertions,
   confirm SC-005 (zero manifests break) on a live canary.

## Complexity Tracking

*No constitution violations to justify.*

## Out of scope

- Destructive-delete guards (US5 original) → deferred `extra-annotations` feature
  (`specs/_next-extra-annotations.preface.md`).
- Merging `location`/`availabilityZone` (they are a real hierarchy).
- Any new resource kind or upstream behavior change.
