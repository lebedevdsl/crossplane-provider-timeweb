# Tasks: Declarative cluster↔router integration (routerRef)

**Prerequisites**: plan.md (D-1..D-6), spec.md (US1–US3, FR-001..009)

## Phase 1: Foundation

- [ ] T001 Baseline green on `022-cluster-router-integration`
- [ ] T002 [P] `docs/openapi-timeweb.json`: add nullable `virtual_router_id` to the updateCluster body (record/convention)
- [ ] T003 `kubernetescluster_types.go`: `RouterRef`/`RouterID` (+at-most-one CEL), status `RouterIntegrated *bool` + `IntegratedRouterID *string`; `make generate`

## Phase 2: US1 — integration convergence (P1) 🎯

- [ ] T004 [US1] `refs.go`: `resolveClusterRouterRef` (Router MR → external-name uuid, Ready-gated) + connector wiring (skip on deletion)
- [ ] T005 [US1] `cluster_external.go`: `integrateClusterRouter(ctx, id, *string)` via `UpdateClusterWithBody` raw JSON (uuid | null); observe-side `integratedWith` from one `GetRouters` read (only when declared or recorded); drift rows per D-5 incl. NAT-wait condition; status record + mirror
- [ ] T006 [US1] Unit matrix: integrate-on-create, drift re-integrate, move, detach-on-removal (+record cleared), no-detach-on-transient-ref, refless-zero-reads, wait-on-NAT-less, idempotent-over-existing
- [ ] T007 [US1] Docs: kubernetes.md private-cluster section rewritten around `routerRef`; conditions.md; example manifest

## Phase 3: US2 — nodepool classification (P2)

- [ ] T008 [P] [US2] `nodepool_external.go`: classify the `router_required_…`/`router_must_have_…` family → routerRef remedy (no recreate language); tests incl. unrelated-400 passthrough

## Phase 4: Validation + release

- [ ] T009 Full gate (build/test/lint/vuln/generate-idempotent/examples)
- [ ] T010 Dev-tag → inyan-staging; log scan
- [ ] T011 Live gate: network → router+NAT → cluster+routerRef → private nodepool Ready; panel-detach repair; move; detach; desync re-verify (finalize D-6 text on evidence)
- [ ] T012 Release v0.11.0: notes, merge, tag, CI publish, staging → v0.11.0, GitLab update, preface trim
