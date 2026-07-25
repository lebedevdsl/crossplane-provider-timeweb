# Tasks: Router peering — static routes, multi-router networks, NAT release

**Prerequisites**: plan.md (D-1..D-6), spec.md (US1–US5, FR-001..012)

## Phase 1: Setup + hygiene

- [ ] T001 Baseline green (`go build ./... && go test ./internal/...`) on branch `021-router-peering-routes`
- [ ] T002 [P] Bump `golang.org/x/text` ≥ v0.39.0 (`go get` + tidy); `make vuln` passes (FR-010)
- [ ] T003 [P] [US5] Pinning unit test: stale k8sVersion error names valid versions (resolver `DimensionValueNotFoundError` path through `validateVersion`) in `internal/controller/kubernetes/cluster_external_test.go`

## Phase 2: US1 — static routes (P1) 🎯

- [ ] T004 [US1] `apis/network/v1alpha1/router_types.go`: `StaticRoutes []RouterStaticRoute` ({subnet CIDR-pattern, nexthop?, via?{routerRef, networkRef}}; CEL exactly-one-of, MaxItems=32, bounded unique-subnet) + status `StaticRoutes []RouterStaticRouteStatus{id, subnet, nexthop}`
- [ ] T005 [US1] `internal/controller/network/refs.go`: resolve `via` pairs at Connect → `resolvedRoutes []resolvedRoute{Subnet, Nexthop}` (neighbor Router Ready + observed gateway in the referenced network; ErrTargetNotReady gate; skip on deletion)
- [ ] T006 [US1] `internal/controller/network/router_external.go`: Observe — `GetStaticRoutes` (`{static_routes}` envelope), status mirror, subnet-keyed diff row in `isRouterUpToDate`; Update — delete-undeclared/changed + create-missing via `DeleteStaticRoute`/`PostStaticRoute` under `ops` pacing
- [ ] T007 [US1] Unit tests: create/delete/replace-changed-nexthop, drift-repair after out-of-band delete, pacing cap, via-not-ready gate, `/available` never called
- [ ] T008 [US1] `make generate` (CRDs + DeepCopy); `docs/routers.md` staticRoutes section; example manifest

## Phase 3: US2 — multi-router hardening (P1)

- [ ] T009 [P] [US2] Unit test pinning scoping: shared network attached to our router + declared ⇒ zero attach/detach ops; detach of shared net issues DELETE only on own router id
- [ ] T010 [P] [US2] Inspection note in research.md: Network controller has no reserved/busy-address drift logic (leaked reservations inert) — verify and record

## Phase 4: US3 — NAT declarative release (P2)

- [ ] T011 [US3] `router_external.go`: release step in NAT-disable and NAT-change branches — skip if address declared on another attachment; `GetDnatRules` guard (present ⇒ Normal retained-event); else `natIPBindability` → `UnbindFloatingIp` when bound to this router; failures ⇒ Warning event only; `ops++`
- [ ] T012 [US3] Unit tests: release on disable; release of old address on change; retained when declared elsewhere / DNAT match; panel-attached idle address untouched; unbind error ⇒ event + nil
- [ ] T013 [US3] `docs/routers.md` + `docs/conditions.md`: release semantics replace the leave-bound note

## Phase 5: US4 — clusterNetworkCIDR (P2)

- [ ] T014 [US4] `kubernetescluster_types.go`: `ClusterNetworkCIDR *{PodsNetwork, ServicesNetwork}` (CIDR patterns, CEL transition-immutable) + `buildCreateClusterBody` wiring; `make generate`
- [ ] T015 [US4] Unit tests: body carries declared CIDRs; absent ⇒ omitted; docs/kubernetes.md + example

## Phase 6: Validation + release

- [ ] T016 Full gate: build, tests, lint, `make vuln`, generate idempotent, validate-examples
- [ ] T017 Dev-tag xpkg → ghcr; deploy `inyan-staging`; provider-log error scan
- [ ] T018 Related kuttl bundles on staging: `18-router-lifecycle`, `20-router-selector`
- [ ] T019 Live scripted e2e: two routers + common transit net; static routes both forms; panel-delete drift repair; survivor deletion; NAT release end-to-end (freed address rebindable)
- [ ] T020 Live: cluster create with declared CIDRs → verify upstream → delete
- [ ] T021 Release v0.10.0: notes, merge to main, tag, CI publish (vuln gate green), staging → v0.10.0, update #135/#132 references
