---
description: "Task list for 007-maintenance-round — placement/location-AZ unification, preset-slug simplification, printcolumns, observability + correlated review backlog"
---

# Tasks: Maintenance Round — Placement, Preset & Printcolumn Cleanups

**Input**: Design documents from `specs/007-maintenance-round/` (plan.md, spec.md;
research.md/data-model.md/contracts/quickstart.md).

**Tests**: Included — Constitution §III four-case pattern (success / not-found /
transient / terminal) for every `external` method touched, plus the Go-review
test-gap items (floating-IP bind, adoption guard, AZ-echo, version-downgrade).

**Hard constraint**: BACKWARD COMPATIBILITY (FR-004) + no change to created
resources (FR-007). Every change is additive/aliasing; verified by SC-005 (zero
existing manifests break) against the existing e2e bundles.

## Format: `[ID] [P?] [Story?] Description`

---

## Phase 1: Setup

- [X] T001 Capture the baseline: `go build/vet/test ./...` + `make lint` all green, and snapshot the current CRD printcolumns + example manifests (the back-compat diff target for SC-005).

---

## Phase 2: Foundational — shared primitives (blocks US1/US2/US4)

- [X] T002 [P] `internal/controller/shared/azlocation.go` — replace the hardcoded 4-entry table with a `/api/v2/locations`-sourced, cached region↔zone lookup (all 8 regions); expose `AZToLocation`/`LocationZones`/`DefaultZoneForLocation`. (R-1; unblocks US1; fixes the `defaultAZByLocation` bug at the source.)
- [X] T003 [P] `internal/controller/shared/ptr.go` (new) — hoist `PtrEqString`/`StringPtr`/`DerefString`/`DerefBool`; delete the duplicate copies in s3bucket/containerregistry/project/compute/kubernetes/sshkey. (backlog P3-1)
- [X] T004 [P] `internal/controller/shared/conditions.go` — define the single shared condition-reason vocabulary set (Available, Creating, PaymentRequired, UpstreamFailed, ImmutableFieldChange, ParentNotReady, PresetNotFound, NoConfiguratorAvailable, DimensionValueNotFound, …). (R-5)
- [X] T005 `internal/controller/shared/map_resolver_error.go` (new) — `MapResolverErrorToCondition(mg, err)`; move the s3bucket/containerregistry implementations here + tests. (US4/FR-009a)

**Checkpoint**: live location lookup, shared reason set, resolver-error mapper, ptr helpers — all consumed by later phases.

---

## Phase 3: User Story 1 — Uniform placement + every region reachable (Priority: P1)

**Goal**: One `location` field everywhere + all 8 regions; FR-001/002. **Independent Test**: a Router and KubernetesCluster in `ru-2`/`nsk-1` provision; existing `availabilityZone`-only manifests still resolve.

- [X] T006 [US1] Switch `internal/controller/network/floatingip_external.go` off the inline `defaultAZByLocation` map onto the shared lookup (T002) — done as part of T002 (caller swapped to `shared.DefaultZoneForLocation`; floatingip tests updated for multi-AZ ru-1) — fixes the inverted `ru-2`/`ru-3` entries + adds `us-4`/`pl-1`. (backlog P1)
- [X] T007 [US1] `apis/*/v1alpha1/*_types.go` — uniform placement: every placed kind has required `location` + optional `availabilityZone`; Router/KubernetesCluster gain `location` with back-compat (derive from `availabilityZone` when only it is given — R-2), with `self == oldSelf` CEL. `make generate`.
- [X] T008 [US1] Controllers/resolver consume the shared lookup for all zone↔location derivation (replace remaining `azLocation` callers across kubernetes/network/compute).
- [X] T009 [P] [US1] Tests — region coverage (router/cluster in ru-2/pl-1/us-4), AZ↔location derivation, and back-compat for `availabilityZone`-only manifests.
- [ ] T010 [US1] `test/e2e/` — add an (env-gated) region-coverage assertion: a Router in a previously-unreachable region reaches `[Ready,Synced]=True`.

---

## Phase 4: User Story 2 — Simpler preset names + location-scoped errors (Priority: P2)

**Goal**: bare `<short>` + scoped not-found; FR-003/004/005. **Independent Test**: `presetName: ssd-15` + `location: ru-1` resolves == `ssd-15-ru-1`; a typo lists ru-1 presets only.

- [X] T011 [US2] `internal/controller/shared/resolver/resolver.go` + `resolve.go` — add `Location` to `PresetInput`; filter entries by location before slug matching (mirror the existing `Zone` filter).
- [X] T012 [US2] `resolver/slug.go` — match bare `<short>` after location-filtering, still accept `<short>-<location>` and the `-<id>` disambiguator (back-compat); replace `splitDisambiguator`'s hand-rolled accumulate with `strconv.ParseInt` (overflow guard). (US2 + backlog P2)
- [X] T013 [US2] `resolver/errors.go` — `PresetNotFoundError` lists the presets available for the operator's location, in simplified form.
- [X] T014 [US2] Pass `Location` to `PresetInput` at every call-site (server/router/cluster/nodepool/registry/s3); simplify `examples/` + `docs/` slugs to the bare form (long-form left working).
- [X] T015 [P] [US2] Tests — bare-slug == long-form; back-compat both forms + disambiguator; location-scoped not-found; overflow-suffix → clean not-found.
- [X] T016 [US2] `test/e2e/scripts/kuttl.sh` — simplify slug discovery (drop the `-$location` concat); assert a bare-form slug resolves.

---

## Phase 5: User Story 4 — kubectl-answerable health, all kinds (Priority: P1)

**Goal**: real conditions + transition Events + status mirrors + one vocabulary; FR-008/009/009a/011. **Independent Test**: each failure mode (payment/failure/dependency/unsupported-change) surfaces a distinct reason + one Event; terminal failures reported as such.

- [X] T017 [US4] Gate `Ready` on upstream state across all kinds (network/floatingip/server/containerregistry/s3bucket/addon/cluster/nodepool): no unconditional `Available()`; `failed`/`error` → terminal `UpstreamFailed`. `PaymentRequired` ONLY where a no-pay signal is confirmed — probe each billable kind (R-4); Server confirmed, Router best-effort; do not promise it elsewhere.
- [X] T018 [US4] Call `shared.MapResolverErrorToCondition` (T005) at ALL resolver call-sites — add it to the four that skip it (server/cluster/nodepool/router). (backlog P1)
- [X] T019 [US4] Status mirrors — `apis`: Network `state`; ContainerRegistry `state` + endpoint/hostname; Addon installed `version`. Populate in the controllers. `make generate`. (FR-011)
- [X] T020 [US4] Transition-only Events — a small helper that emits on condition *change* only; wire payment/failure/dependency-wait/deferred-bind/scaling transitions. (FR-009)
- [X] T021 [US4] Per-controller condition fixes (Go review): addon failure→`UpstreamFailed` + mid-install-vs-deleted guard (P2-7); repository 404→`ParentNotReady` (P2-5); cluster version path — typed condition + lateral-vs-downgrade classification + reject zero-parse (P1/P2-6); replace the `"ParentNotReady"` literal + gate dependent kinds on parent `Ready=True`; nodepool `xpv2.ReasonCreating`→shared reason.
- [X] T022 [P] [US4] Tests — §III four-case for every touched condition/event path; fill the Go-review test gaps (floating-IP bind/unbind/deferred, adoption guard 0/1/2, AZ-echo, version-downgrade).

---

## Phase 6: User Story 3 — Uniform, decluttered printcolumns (Priority: P3)

**Goal**: fixed order + single `ID` + wide-only diagnostics; FR-006. **Independent Test**: shared columns identical across kinds; FloatingIP shows one `BOUND-TO`; ids only in `-o wide`.

- [X] T023 [US3] `apis/*/v1alpha1` printcolumns — fixed order `LOCATION, ID, <≤2 extras>` between the Crossplane defaults and `AGE`; rename `UPSTREAM-ID`/`EXTERNAL-NAME`→`ID` (single, from the external-name annotation); `priority=1` for diagnostics; nodepool add `PUBLIC-IP`; repository add `AGE`. `make generate`.
- [X] T024 [US3] `internal/controller/network/floatingip_external.go` + `floatingip_types.go` — collapse `BOUND-TO`+`BOUND-UUID` into one string `BOUND-TO` populated from whichever id variant is set.
- [X] T025 [P] [US3] Document the column conventions; spot-check `kubectl get` uniformity across kinds.

---

## Phase 7: User Story 5 — Working onboarding (Priority: P1)

**Goal**: every example applies; getting-started + auth doc; FR-015/016. **Independent Test**: each `examples/` manifest applies unmodified; the doc walks token→Secret→ProviderConfig→first resource.

- [X] T026 [US5] Fix the broken examples — `examples/containerregistry.yaml`, `containerregistryrepository.yaml`, `s3bucket.yaml`: correct API group (`kubernetes.m.`), `initialSizeGB`, add `storageClass: hot`, drop the dead `cr-starter-5gb-1939` slug; match the docs.
- [X] T027 [P] [US5] Add missing examples — `server.yaml`, the three K8s kinds, standalone `network.yaml`/`floatingip.yaml`, and `providerconfig.yaml` + credential Secret (modern shape, NO `deletionPolicy`).
- [X] T028 [P] [US5] Add a getting-started + auth-setup doc (token → Secret → ProviderConfig → first resource); fix stale doc/comments — registry endpoint host (`cr.` vs `registry.`), `K8sVersion` example (`v1.31.x+k0s.0`), Server Update godoc `hostname`, post-005 resize-deferral comments. (FR-016)

---

## Phase 8: Backlog sweep — remaining correlated Go-review fixes (FR-017)

- [X] T029 [P] `network/router_external.go` — Router `Update` closes `resp.Body` before `Classify` (×6 sites): switch to `defer`/Classify-before-close + a `closeBody` helper; restores error detail + the 403-`networks_location_mismatch`-transient path. (backlog P1, [BOTH])
- [X] T030 [P] Immutability CEL gaps — add `self == oldSelf` to Addon `type`/`version`, SSHKey `name`/`body`, Network `name`/`subnetCIDR`, nodepool `name`. `make generate`; tests assert admission rejection. (backlog P1/P2)
- [X] T031 [P] `compute/server_external.go` + `floatingip_bind.go` — treat an unbindable-while-`!on` floating IP as still-creating (benign requeue), not a hard `Update` error every poll. (backlog P2-1)
- [X] T032 [P] `kubernetes/cluster_external.go` — adoption guard requires AZ (and resolved project) match, not name-only (P2-3); re-fetch kubeconfig only on the Ready transition / Secret-absent, not every Observe (P2-4).
- [X] T033 [P] `resolver/dimensions.go` + `cache.go` — `classifyUpstream` splits 4xx-permanent vs 5xx-transient (kill the dead default); don't memoize an empty-slice 200 (return transient); drop the `errIs` wrapper. (backlog P2/P3)
- [X] T034 [P] Small consistency fixes — nodepool `Available()` zero-declared/zero-node guard (P3-5); shared `isActiveState`/`isFailedState` (P3-3); drop the 2 unused resolver caches in `network/controller.go` (P3-2); fold the autoscaling `Minimum=2` into the enabled-gated CEL (P3-7); note the S3 `*float32` generated-client constraint (P3-6).

---

## Phase 9: e2e harness + polish

- [X] T035 Fix the kuttl condition-assert ordering — reordered `status.conditions` to Crossplane's emission order. Verified empirically against green bundles: ref-having kinds (resolve a reference in Connect → first reconcile fails → `[Synced, Ready]`); no-ref Create-path kinds → `[Ready, Synced]`; Observe-only → `[Synced, Ready]`. Flipped Router (18/01, 18/02) and Router+Cluster+Nodepool (19/01) to `[Synced, Ready]`; left historically-green bundles untouched; corrected the misleading 08-network comment. (the 006 bundle-18 "hang" root cause — kuttl v0.26 matches positionally.)
- [X] T036 `make lint` → 0 issues; §III grep audit across every touched `external` method; `go test ./...` green.
- [ ] T037 Live canary (scoped, cheapest tiers): region coverage (US1), bare-slug resolution (US2), real conditions/events (US4), and **SC-005** — re-run the existing e2e bundles unmodified to prove zero manifests break; orphan-sweep the live API after.
- [ ] T038 [P] Sync `specs/007-maintenance-round/` artifacts + memory notes with as-built reality.

---

## Dependencies & Execution Order

- **Phase 2** (T002–T005) blocks the user stories (shared lookup, vocabulary, mapper, ptr).
- **US1** (T006–T010) after T002. **US2** (T011–T016) after T002 (Location filter). **US4** (T017–T022) after T004/T005. **US3** (T023–T025) independent (after T024's display field). **US5** (T026–T028) independent.
- **Phase 8** backlog: T029–T034 mostly independent ([P]) — schedule alongside the matching story (T029 with US-router work, T030 with US1 CEL, etc.).
- **T035** (kuttl reorder) blocks any e2e that asserts conditions — do it before T010/T016/T037.
- **T037** (canary) last.

### Parallel opportunities
- Phase 2: T002 ∥ T003 ∥ T004. US-phase `[P]` test tasks. Phase 8 T029–T034 largely parallel (different files). US3/US5 ∥ US1/US2/US4.

## Implementation Strategy (MVP-first)

1. **Phase 2 + US1** (region coverage + the `defaultAZByLocation` correctness fix) — highest-value, unblocks the rest.
2. **US4** (observability + shared vocabulary) — absorbs the most Go-review findings; ship as its own PR.
3. **US2** (slugs), **US3** (columns), **US5** (onboarding) — independent, parallelizable PRs.
4. **Phase 8 backlog** — fold each small fix into the nearest story's PR; standalone ones (T029/T031/T032/T033) as a "correctness" PR.
5. **T035 kuttl fix early**, **T037 canary last** (SC-005 the gate).

## Notes
- Everything additive/back-compatible — long-form slugs + `availabilityZone`-only manifests keep working (FR-004); no upstream-behavior change (FR-007).
- `[P]` = different files, no dependency on incomplete same-phase tasks.
- Backlog confidence tags ([BOTH]/[Go]) from the spec's Maintenance Backlog carry into the task descriptions where relevant.
