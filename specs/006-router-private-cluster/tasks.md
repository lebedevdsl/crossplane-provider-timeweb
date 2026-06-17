---
description: "Task list for 006-router-private-cluster — RE-PLAN (2026-06-17) after the official Timeweb OpenAPI spec landed (NAT toggle ungated, resize resolved) + live e2e findings"
---

# Tasks: Router & Private Kubernetes Cluster Networking (re-plan)

**Input**: Design documents from `specs/006-router-private-cluster/` (regenerated
`plan.md`; `research.md`/`data-model.md`/`contracts/`/`quickstart.md` updated).

**Re-plan context**: The original 006 task set (old T001–T019, T022–T027) is
**already implemented** and shipped the Router kind, attachments, DHCP, the
resolver `DimRouterPreset`, the zone-affinity fixes, and bundle 18/19 scaffolding.
This regenerated set covers ONLY the work the spec-update + live findings created or
unblocked: the client regen, the NAT toggle (old T020, now implementable), the US3
mechanism change (router-NAT + default route — drop `public_ip_enabled`), the
router fixes F-5..F-9, and the still-open e2e canary (incl. the bundle-18 hang).

**Tests**: Included — Constitution §III four-case pattern (success / not-found /
transient / terminal) for every `external` method touched.

**Out of scope (feature 007)**: location/AZ unification + preset-slug simplification
(preface F-1..F-4 in `specs/_next-location-az-presets.preface.md`).

## Format: `[ID] [P?] [Story?] Description`

---

## Phase 1: Setup — regenerate the client from the official spec

**Purpose**: The official spec is merged (`docs/openapi-timeweb.json`, 207 paths;
Makefile `-include-tags` has `Роутеры`). Regenerate so all later phases code against
typed `NatIn`/`RouterEdit`/`parent_service` + the `{network_name}` param.

- [X] T001 `make generate-client` from the official `docs/openapi-timeweb.json` (router ops now under tag `Роутеры`); regenerate the counterfeiter fake (`go generate ./internal/clients/timeweb/...`). Expect the generated `public_ip_enabled` field on the node-group type to DISAPPEAR (intended — see T012). Record any new sed-patch needs for numerically-named response schemas in the Makefile.
- [X] T002 Reconcile the build after regen: update `internal/controller/network/router_external.go` call sites for the new generated shapes (`NatIn`, `RouterEdit`, the `{network_name}` network sub-path param). `go build ./...` clean (except the intentional nodepool break handled in T012).

**Checkpoint**: typed client exposes the NAT toggle + correct param names.

---

## Phase 2: Foundational — param identity + binding (blocks US2/US3)

- [X] T003 Confirm the network sub-resource param identity: probe whether `PATCH/DELETE /api/v1/routers/{router_id}/networks/{network_name}/nat` and the detach endpoint key on the network **name** or the `network-xxxx` **id** (the controller currently passes the id). Record the answer in `contracts/timeweb-router-endpoints.md` and align `getRouter`/detach/`convergeNAT` accordingly in `internal/controller/network/router_external.go`.
- [X] T004 [P] Re-evaluate the router→cluster binding: `RouterIn.parent_service{id,type}` exists at create. Decide create-time `parent_service` vs the derived-`parent_services` Observe mirror; document in `data-model.md` and adjust `buildCreateRouterBody` if adopting the explicit binding.

---

## Phase 3: User Story 1 — Router lifecycle fixes (Priority: P1)

**Goal**: Close the router-lifecycle correctness gaps found live. Spec US1, FR-001/002/006/009/010/011/012.

**Independent Test**: Router create→observe→delete with a NAT'd attachment; no_paid surfaces as PaymentRequired; deleting the router detaches its networks (they stay deletable); a settled router stops reconciling.

- [X] T005 [US1] F-6 — add the `starting` short-circuit to `Observe` in `internal/controller/network/router_external.go`: when `router.Status == "starting"` (still `Creating`), skip `isRouterUpToDate` and return `ResourceUpToDate: true` (mirror the existing Update guard) to kill the no-op reconcile/event loop. Test: Observe returns up-to-date while starting.
- [X] T006 [US1] F-7 — fix the no_paid mapping in `setRouterReadyCondition` (`router_external.go`): Timeweb reports router no_paid as `status:"error"` (no `no_paid` string), so the generic `error → UpstreamFailed "delete and recreate"` mis-describes a billing failure. Make the `error` message name the billing possibility (or cross-check a billing signal) and prefer `ReasonPaymentRequired` when it's a pay state. Tests: error-state mapping.
- [X] T007 [US1] F-8 — Router `Delete` issues a single `DeleteRouter` which CASCADES the network detach itself. CORRECTED 2026-06-17 (the detach-then-delete hypothesis was wrong + broke teardown: detaching the LAST network 400s, a router requires ≥1 network). Live-verified: plain `DELETE /routers/{id}` → 200, networks deletable immediately after (no `type:bgp` strand in the MR flow). Keeps the FR-012 parentServices guard. Test: one DeleteRouter, zero detaches; four-case Delete.
- [X] T008 [P] [US1] F-5 — `apis/network/v1alpha1/floatingip_types.go`: add `ResourceUUID *string` to `FloatingIPBindingObservation` (router bindings are UUID-keyed; the int64 `ResourceID` can't render them); decode `AsFloatingIpResourceId1()` in `populateFloatingIPStatus` (`floatingip_external.go`); make the `BOUND-TO` printcolumn a `string` rendering whichever id is set; add `router` to the `ResourceType` doc enum. `make generate`. Tests: router-binding populates `resourceUUID`.

**Checkpoint**: router lifecycle is correct on delete/billing/settle; FloatingIP shows router bindings.

---

## Phase 4: User Story 2 — NAT activation (Priority: P2) 🎯 the unblocked core

**Goal**: NAT actually activates (old T020, now implementable). Spec US2, FR-004/004a/005.

**Independent Test**: A Router attachment with `natFloatingIP` shows that address as `natIP` in status within one reconcile after create; removing it clears NAT; the reconcile loop stops once converged.

- [X] T009 [US2] Implement `convergeNAT` in `internal/controller/network/router_external.go` using the official ops: `PATCH /api/v1/routers/{router_id}/networks/{network_name}/nat` body `NatIn{nat_ip}` to ENABLE, `DELETE` to DISABLE. Drive it from `isRouterUpToDate`'s NAT row (declared `natFloatingIP` address vs observed `natIP`) in `Update`, replacing the create-only stub + the `NATConvergencePending` event. Short-circuit while `starting`.
- [X] T010 [P] [US2] `router_external_test.go` — §III four-case for `convergeNAT` + convergence table: enable-when-declared, disable-when-removed, no-op-when-converged (loop stops), starting-state-skip. (Live-validated 2026-06-17: manual dashboard enable converged the router and stopped the loop — the logic is proven; this codifies it.)
- [ ] T011 [US2] `test/e2e/kuttl/tests/18-router-lifecycle/` — add a NAT assert: after create, `status.atProvider` (or the attachment mirror) shows `natIP` == the egress FloatingIP address; flip/remove converges. (Depends on the bundle-18 hang fix, T015.)

**Checkpoint**: NAT egress works end-to-end without manual intervention; US3 unblocked.

---

## Phase 5: User Story 3 — Private cluster via router-NAT + default route (Priority: P2)

**Goal**: Zero-public-IP workers via network placement on the NAT'd router + default route — NOT a per-node flag. Spec US3, FR-007/008.

**Independent Test**: Network + Router(NAT) + cluster(networkRef) + nodepool on the routed network → all Ready; every `status.atProvider.nodes` entry has a local IP and no public address; router `parentServices`/binding names the cluster; egress works via NAT.

- [X] T012a [US3] KEEP the explicit `publicIP` flag (REVISED decision: Azure-AKS precedent + explicit-intent). `public_ip_enabled` re-added to `NodeGroupIn` in `docs/openapi-timeweb.json` as a spec hand-patch (live-but-unswaggered); regen sources the generated field from the spec; `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go` `publicIP *bool` + CEL kept; `nodepool_external.go` keeps the mapping. Build green. (Done 2026-06-17.)
- [ ] T012b [US3] Live re-probe: create a node group with `publicIP:false` and confirm nodes come up with NO public address (the field is in live payloads but never validated as honored on create). If ignored → fall back to network-placement privacy + document. Validated in bundle 19 (T014).
- [X] T013 [US3] Document + validate the egress mechanism: workers reach the internet via the router's NAT only when a default route via the attachment gateway exists (dashboard banner *"настройте маршрут по умолчанию через NAT"*). Capture in `docs/routers.md` / `docs/kubernetes.md` private-path section how the default route is established for Timeweb worker nodes (probe live).
- [ ] T014 [US3] `test/e2e/kuttl/tests/19-private-cluster/` — Network + FloatingIP + Router(NAT) + cluster(networkRef) + nodepool; assert all `[Ready,Synced]=True`, every node entry lacks a public address (SC-002), router binding names the cluster; teardown leaves no orphans. (Depends on T009 NAT + T015 hang fix; run scoped, never in the concurrent full suite — billing.)

**Checkpoint**: the private cluster is a documented, e2e-proven arrangement.

---

## Phase 6: e2e harness + live canary (T028) & polish

- [ ] T015 Investigate + fix the **bundle-18 hang**: the router-lifecycle bundle does not complete even when all 3 MRs are `Ready=True/Synced=True` and the router has settled (NOT the NAT loop). Inspect kuttl assert behavior vs the churning `resourceVersion` from the reconcile loop vs log buffering, using the new `test/e2e/.diagnostics/<ts>/` artifacts. Blocks T011/T014/T017.
- [X] T016 [P] F-9 — optional: raise the FloatingIP-create per-request timeout (30s→60s) in `internal/clients/timeweb/client.go` (or per-call) to cut retry churn on slow ru-3 IP allocation; keep the transient-retry. Test unaffected.
- [ ] T017 Live e2e canary (T028, scoped — NOT the concurrent full suite, to avoid month-in-advance `no_paid`): `18-router-lifecycle` (now with NAT assert), `19-private-cluster`, plus a `12-k8s-cluster-lifecycle` regression. Russian zones, cheapest tiers, immediate teardown; verify the live-API inventory (routers, VPCs, clusters, floating IPs) returns to baseline after teardown (the kuttl CR inventory is NOT sufficient — check the Timeweb API directly).
- [X] T018 [P] `make lint` (full module) → 0 issues; §III audit (grep) across every external method touched (router Observe/Update/Delete/convergeNAT, FloatingIP populate, nodepool create-body).
- [X] T019 [P] Sync `specs/006-router-private-cluster/` artifacts with as-built reality; update memory notes referencing the now-resolved captures and the `public_ip_enabled` removal; file the Timeweb support ticket for the stranded `e2e-plan-probe-net2` BGP network (F-8).

---

## Dependencies & Execution Order

- **Phase 1 (T001→T002)** blocks everything (typed client).
- **Phase 2 (T003, T004)** after T002; T003 blocks the NAT/detach work.
- **Phase 3 (US1)**: T005/T006/T007/T008 after T002; T008 parallel (different files).
- **Phase 4 (US2)**: T009 after T003; T010 after T009; T011 after T009 + T015.
- **Phase 5 (US3)**: T012 after T001 (reconciles the regen break); T013 anytime after Phase 3; T014 after T009 + T015.
- **Phase 6**: T015 early (unblocks all e2e); T017 last (after US1–US3 land); T016/T018/T019 parallel.

### Parallel opportunities
- T008 ∥ T005/T006/T007 (different files). T016 ∥ T018 ∥ T019. T004 ∥ Phase-3 tasks.

## Implementation Strategy

1. **Phase 1–2** (regen + param identity) — one PR; the gateway to everything.
2. **Phase 3 (US1 fixes)** — one PR; correctness gaps, independently shippable.
3. **Phase 4 (US2/NAT)** — the headline: NAT finally activates. **STOP AND VALIDATE** bundle 18 (after T015 unblocks it).
4. **Phase 5 (US3)** — private cluster on the proven NAT.
5. **Phase 6** — fix the e2e hang early (T015) so 4/5 can be validated; canary (T017) last.

## Notes
- `[P]` = different files, no dependency on incomplete tasks in the same phase.
- T015 (bundle-18 hang) is on the critical path for ALL e2e validation — schedule it early even though it sits in Phase 6.
- Defaults: nodes remain public-by-default (FR-008/SC-006) — US3 privacy comes from network placement on the NAT'd router, not a flag.
