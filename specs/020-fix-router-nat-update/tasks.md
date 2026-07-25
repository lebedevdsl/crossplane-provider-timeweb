# Tasks: Router NAT-on-update bind fix

> **Descope 2026-07-25**: v0.9.2 = Part 1 only. Phases 7–9 (US6–US8, T011–T019)
> and T025 were implemented then REVERTED (premises invalidated by handoff
> Parts 3+; see spec Clarifications 2026-07-25) — they move to
> `specs/_next-router-features.preface.md` (v0.10.0). T021's ticket draft
> updated to the consolidated findings.

**Input**: Design documents from `/specs/020-fix-router-nat-update/`
**Prerequisites**: plan.md (D-1..D-7), spec.md (US1–US8, FR-001..016), research.md (R-1..R-6), contracts/

**Tests**: Constitution III mandates fake-client unit tests for every touched external-client path — test tasks are included and NOT optional.

**Organization**: Part 1 (Router NAT bind) = US1–US5; Part 2 (cluster linkage guard) = US6–US8. Stories grouped by the code region they share.

## Phase 1: Setup

- [ ] T001 Verify clean baseline: `go build ./... && go test ./internal/... && git status` clean on branch `020-fix-router-nat-update`

## Phase 2: Foundational (client surface for Part 1)

- [ ] T002 Add typed constant `BindFloatingIpResourceTypeRouter = "router"` (as `twgen.BindFloatingIpResourceType`) plus a small `bindFloatingIPToRouter`-style helper doc-comment note in a hand-written client file `internal/clients/timeweb/floating_ips.go` (new; `firewall.go` pattern — do NOT touch zz_generated) (D-3, R-1)
- [ ] T003 [P] Hand-patch `docs/openapi-timeweb.json`: add `router` to the `bind-floating-ip` `resource_type` enum so future regens keep the value; record the patch in the file-local hand-patch convention (no regen this feature) (D-3)

## Phase 3: US1 — NAT added to an existing router converges automatically (P1) 🎯 MVP

**Goal**: update path binds the declared free FIP to the router, NAT converges next cycle; create path untouched.
**Independent test**: fake-client Update on a router whose `Ips` lacks the declared NAT address → Bind issued (router UUID string arm), NO NAT PATCH same pass; owned address → NAT PATCH only.

- [ ] T004 [US1] In `internal/controller/network/router_external.go` NAT-enable branch (~L448): add ownership precondition against observed `router.Ips` (pass the router GET result into the attachment loop); unowned → resolve FIP by address via one `GET /api/v1/floating-ips` list (decode `{ips:[FloatingIp]}` envelope), free → `BindFloatingIp(uuid, {resource_type: router, resource_id: router-UUID-string-arm})`, `ops++`, skip NAT this pass; owned → existing `UpdateRouterNat` unchanged (D-1, D-2, D-4; FR-001/002/003/011)
- [ ] T005 [US1] Unit tests in `internal/controller/network/router_external_test.go`: unowned+free → Bind called with correct body and no NAT PATCH; owned+drifted → NAT PATCH only, no FIP list read; bind transport/5xx error → classified error returned (contract test matrix rows 1, 2, 6)

## Phase 4: US2 — Bind failure surfaces a clear condition (P2)

**Goal**: bound-elsewhere / unresolvable NAT IP → typed degraded condition + Event, poll-cadence requeue, converge-on-freed; never steal.
**Independent test**: fake-client Update with FIP bound to a server → no Bind, condition set, nil error returned.

- [ ] T006 [US2] In the same NAT branch: bound-elsewhere / no-matching-FIP outcomes set a shared-vocabulary condition (upstream-failure reason; message per contracts/router-nat-bind.md naming network, IP, holder, remedy) + `RecordConditionChange` Event; NO bind/steal ever issued; outcome recorded without returning an error (FR-004/006; D-5)
- [ ] T007 [US2] Unit tests: bound-elsewhere → no Bind + condition + nil error; unresolvable address → same; freed-next-pass → Bind proceeds and condition path not re-entered (contract rows 3, 4 + FR-006)

## Phase 5: US3 — One blocked NAT doesn't wedge the router (P2)

**Goal**: blocked attachment skips; remaining attachments' attach/detach/DHCP/NAT still converge in the same pass.
**Independent test**: two attachments — A blocked (bound-elsewhere), B drifted DHCP → B's PATCH issued.

- [ ] T008 [US3] Restructure the per-attachment loop in `router_external.go` so the blocked-bind outcome `continue`s (blocked never consumes `ops` budget); issued-call errors keep returning classified as today (FR-005; D-5 loop-isolation)
- [ ] T009 [US3] Unit test: attachment A bound-elsewhere + attachment B DHCP drift in one pass → B PATCHed, A condition present; pacing budget not consumed by A (contract row 3 extension; edge case "pacing interplay")

## Phase 6: US4 — NAT disable leaves the IP bound (P3)

- [ ] T010 [P] [US4] Unit test in `router_external_test.go`: NAT removed from spec → `DeleteRouterNat` issued, NO `UnbindFloatingIp` call ever recorded by the fake (FR-007; contract row 5) — behavior is already unbind-free, the test pins it

## Phase 7: US6 — Cluster create-precondition (P1, Part 2 core)

**Goal**: cluster on router-attached NAT-less network never created; router-less allowed + Warning; auto-proceed once NAT lands.
**Independent test**: fake-client Create with wiring fixtures for all three rows of the contracts/cluster-linkage-guard.md table.

- [ ] T011 [US6] Add a shared wiring probe helper in `internal/controller/kubernetes/cluster_external.go` (or a small new file `internal/controller/kubernetes/router_wiring.go`): given a network id → `GET /api/v1/routers` + per-router `GET /routers/{uuid}/networks` → `{router *RouterOut, natIP string, err}`; read failures classified transient (contracts probe section)
- [ ] T012 [US6] Wire the precondition into `clusterExternal.Create` before the POST: router+NAT → proceed; router NAT-less → condition (message per contract) + Event + return nil-create (requeue, no POST); no router → proceed + Warning event (FR-012/013)
- [ ] T013 [US6] Unit tests in `internal/controller/kubernetes/cluster_external_test.go`: all three wiring rows + blocked-then-NAT-appears → POST issued on later pass; wiring read error → transient classified, no POST (contract test matrix rows 1–4)

## Phase 8: US7 — Nodepool failures name the real cause (P2)

- [ ] T014 [P] [US7] In `internal/controller/kubernetes/nodepool_external.go` create/update error paths: match `*timeweb.APIError` `Code == "router_required_for_worker_groups_without_public_ip"` → condition with the frozen-linkage message (per contract); all other codes unchanged (FR-014; D-7)
- [ ] T015 [P] [US7] Unit tests in `nodepool_external_test.go`: that code → frozen-linkage condition message; a different 400 code → existing behavior (contract rows 6–7)

## Phase 9: US8 — routerLinked observability (P3)

- [ ] T016 [US8] Add `RouterLinked *bool` to `KubernetesClusterObservation` in `apis/kubernetes/v1alpha1/kubernetescluster_types.go` (doc comment per data-model.md; status-only, additive)
- [ ] T017 [US8] Populate it in `clusterExternal.Observe` via the T011 wiring probe + owning router's `parent_services` (type `k8s`, id == external-name); nil when unobservable; probe errors must not fail Observe (FR-015)
- [ ] T018 [US8] `make generate` — regen CRD YAML (`package/crds/`) + DeepCopy in the same change set; verify clean tree (Constitution I)
- [ ] T019 [US8] Unit tests: linked / unlinked / router-less → true / false / false; wiring unreadable → nil + Observe still succeeds (contract row 5)

## Phase 10: US5 + Polish — docs, quirk capture, validation, release

- [ ] T020 [P] [US5] Document in `docs/` (router kind doc + `docs/conditions.md`): auto-bind behavior, bind-blocked condition reason + remedy, NAT-disable-leaves-bound, cluster precondition + Warning, nodepool frozen-linkage condition, `routerLinked` (FR-008 spec-Part1 / FR-009)
- [ ] T021 [P] [US5] Quirk capture per project convention: stale bind enum (docs say 4 values, API accepts `router`) + frozen cluster↔router linkage / no re-link API — support-ticket draft (RU) appended to the feature dir or existing quirks doc (FR-009)
- [ ] T022 Full gate: `go build ./...`, `go test ./...`, golangci-lint via `go run`, `make generate` idempotent, kuttl admission bundles unchanged
- [ ] T023 Dev-tag build + push (`VERSION=dev-$(date +%s)`), deploy to `inyan-staging` (explicit context; annotation bump), broad provider-log error scan
- [ ] T024 Live e2e Part 1 on staging (self-contained): Network + FloatingIP + Router (no NAT) → update adds NAT → assert bind + NAT converge (Synced AND Ready True), `ip_not_found` absent from logs; NAT disable → IP still bound upstream (SC-001/004)
- [ ] T025 Live e2e Part 2 on staging: cluster declared on the router-attached NAT-less network → blocked condition observed; enable NAT → cluster creates; private nodepool Ready; `routerLinked: true`; negative + canonical ordering (FR-016; SC-005/007)
- [ ] T026 Release v0.9.2: notes (example-first style; both fixes), version bump, notes committed before tag, tag + push + xpkg/image publish per 008 pipeline; close infra/timeweb-infra#135 with the release reference

## Dependencies & execution order

- T001 → T002/T003 → US1 (T004–T005) → US2 (T006–T007) → US3 (T008–T009); T010 anytime after T004.
- Part 2 independent of Part 1 code-wise: T011 → T012/T013 (US6) and T016→T017→T018→T019 (US8; reuses T011); T014/T015 (US7) fully parallel [P].
- Polish/validation (T020–T025) after all code; T026 last.
- Parallel opportunities: T003∥T002; T014/T015∥anything; T010∥T006+; T020/T021∥T022.

## Implementation strategy

MVP = Phase 3 (US1) — the #135 fix proper. Everything else hardens and
diagnoses. Part 1 and Part 2 touch disjoint controllers and can be built in
either order; single release gate at the end.
