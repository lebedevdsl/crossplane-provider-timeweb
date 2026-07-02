# Tasks: Firewall — declarative Timeweb Cloud firewall rule groups

**Input**: Design documents from `/specs/013-firewall-api/`

**Prerequisites**: plan.md, spec.md (US1–US4, FR-001..015 +FR-010a), research.md (R-1..R-8),
data-model.md, contracts/ (firewall-v1alpha1, timeweb-firewall-endpoints), quickstart.md

**Tests**: Unit tests are **mandatory** — Constitution §III requires the four-case pattern
(success / not-found / transient / terminal) for every `external` client, with the `timeweb` fake
(no live HTTP). Included per story below.

**Organization**: Tasks grouped by user story (US1–US4) for independent implementation/testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4 (maps to spec.md); Setup/Foundational/Polish carry no story label

## Path Conventions

Crossplane provider, single Go module. Real paths: `apis/network/v1alpha1/`,
`internal/clients/timeweb/`, `internal/controller/network/`, `cmd/provider/`, `docs/`,
`package/crds/`, `examples/`, `test/e2e/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Optional documentation fidelity. No new dependencies — the firewall is plain Timeweb
REST over the existing `timeweb.Client`.

- [ ] T001 [P] (optional, fidelity only) Extend `docs/openapi-timeweb.json` `ResourceType` enum to `server;dbaas;balancer;app` (published spec lists only `server`) per `project_openapi_handpatched_superset`. Not required for the build — the client is hand-written (R-8).

**Checkpoint**: baseline clean (`go build ./...`).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: API types, hand-written client surface, and controller scaffolding that ALL stories need.

**⚠️ CRITICAL**: No user-story work begins until this phase is complete.

- [X] T002 Define Firewall API types (`FirewallParameters`, `FirewallRule`, `ServiceAttachment`, `FirewallObservation`, `FirewallRuleStatus`, `FirewallSpec`, `FirewallStatus`, `Firewall`, `FirewallList`) with kubebuilder markers (enums `ingress;egress` / `tcp;udp;icmp` / `DROP;ACCEPT` / `server;dbaas;balancer;app`; defaults `policy=DROP`, `serviceType=balancer`; `MaxItems=128`; status print columns READY/SYNCED/POLICY/RULES/ATTACHED/ID/AGE) in `apis/network/v1alpha1/firewall_types.go`
- [X] T003 Register `FirewallKind`/`FirewallGroupVersionKind` + `SchemeBuilder.Register(&Firewall{}, &FirewallList{})` in `apis/network/v1alpha1/groupversion_info.go`
- [X] T004 Run `make generate` → `zz_generated.deepcopy.go` + `package/crds/network.m.timeweb.crossplane.io_firewalls.yaml`
- [X] T005 [P] Hand-write `internal/clients/timeweb/firewall.go` (the `doV2` pattern, returns `*http.Response` for `Classify`/`DecodeBody`): group — `CreateFirewallGroup(name,desc,policy)`, `GetFirewallGroup`, `ListFirewallGroups`, `PatchFirewallGroup`, `DeleteFirewallGroup`; rule — `ListFirewallRules`, `CreateFirewallRule`, `DeleteFirewallRule` (and optional `PatchFirewallRule`); resource — `ListFirewallResources`, `LinkFirewallResource(id,type)`, `UnlinkFirewallResource(id,type)`; reverse-lookup — `GetServiceFirewallGroups(type,id)`
- [X] T006 [P] Add `ReasonServiceConflict` reason constant (exclusivity) in `internal/controller/shared/conditions.go`
- [X] T007 Scaffold `internal/controller/network/controller.go` `SetupFirewall(mgr, log, pollInterval)`: `managed.NewReconciler` + `WithManagementPolicies()` + `ratelimiter.NewController()` — **no `Watches`, no resolver cache**
- [X] T008 Implement `internal/controller/network/firewall_connector.go` `Connect`: `shared.ResolveToken`, build `timeweb` client, return the `external` (no reference resolution — attachments are opaque literals)
- [X] T009 Register `networkctrl.SetupFirewall(mgr, log, pollInterval)` in `cmd/provider/main.go`

**Checkpoint**: types registered, client + reason available, controller wired — stories can begin.

---

## Phase 3: User Story 1 - Declare a firewall rule group (Priority: P1) 🎯 MVP

**Goal**: One `Firewall` → a group with inbound rules → Synced + Ready, with full single-group
lifecycle (create / converge / delete), no attachments yet.

**Independent Test**: Apply a `Firewall` with `policy: DROP` and several inbound TCP rules; it goes
Synced + Ready with `RULES=N`; editing a rule converges upstream without recreating the group;
deleting it removes the group and its rules.

### Tests for User Story 1 ⚠️

- [X] T010 [P] [US1] Four-case unit tests for `Observe` + `Create` + `Delete` (success / not-found / transient / terminal) with the `timeweb` fake in `internal/controller/network/firewall_external_test.go`
- [X] T011 [P] [US1] Unit tests for the rule canonical set-diff (order-insensitive equality, `toAdd`/`toRemove`, duplicate-tuple detection) in `internal/controller/network/firewall_external_test.go`

### Implementation for User Story 1

- [X] T012 [US1] Implement rule canonicalization + set-diff helpers (canonical `{direction,protocol,normalizedPort,cidr}` tuple; duplicate detection; `toAdd`/`toRemove` vs observed rule ids) in `internal/controller/network/firewall_external.go`
- [X] T013 [US1] Implement `Observe` (GET group + GET rules; populate `status.atProvider` incl. `policy`/`ruleCount`/`rules`; `ResourceUpToDate` = name+description+policy match AND rule-set equal; group 404 → not-exists) in `internal/controller/network/firewall_external.go`
- [X] T014 [US1] Implement `Create` (POST `/firewall/groups?policy=` `{name,description}` → external-name = `group.id`; POST each rule; by-name adoption guard) in `internal/controller/network/firewall_external.go`
- [X] T015 [US1] Implement `Update` rule + identity reconcile (PATCH name/description if drifted; POST missing rules, DELETE extra rules by `rule_id`; paced `maxFirewallMutationsPerReconcile`; return without claiming convergence) in `internal/controller/network/firewall_external.go`
- [X] T016 [US1] Implement `Delete` (DELETE `/firewall/groups/{id}`; 404 → success; set `Deleting`) in `internal/controller/network/firewall_external.go`
- [X] T017 [US1] Map conditions/errors (`Creating`/`Available`; duplicate rule tuple → terminal `InvalidConfiguration` FR-013; `APIError`; transient requeue) via `shared.RecordConditionChange` in `internal/controller/network/firewall_external.go`
- [X] T018 [P] [US1] Add example manifest `examples/firewall.yaml` (group + inbound rules, `policy: DROP`)

**Checkpoint**: MVP — a declarative rule group with inbound rules is fully functional and
independently testable (create / drift-converge / delete).

---

## Phase 4: User Story 2 - Attach a firewall to a load balancer (Priority: P2)

**Goal**: `attachedServices[]` (opaque `{serviceID, serviceType}`) attach/detach the group to load
balancers, honoring 1:1 exclusivity.

**Independent Test**: Apply a `Firewall` with one `balancer` attachment; it goes Synced + Ready with
`ATTACHED=1`; a connection to an allowed port reaches the LB and a non-allowed port is blocked;
removing the attachment detaches it; attaching a service already bound elsewhere reports
`ServiceConflict`.

### Tests for User Story 2 ⚠️

- [X] T019 [P] [US2] Unit tests for the attachment set-diff (`{id,type}`), attach/detach, and the already-bound-elsewhere → `ServiceConflict` path, with the `timeweb` fake in `internal/controller/network/firewall_external_test.go`

### Implementation for User Story 2

- [X] T020 [US2] Implement attachment canonicalization + set-diff (`{serviceID, serviceType}`; `toAttach`/`toDetach`) in `internal/controller/network/firewall_external.go`
- [X] T021 [US2] Extend `Create` to attach each declared service (POST `/firewall/groups/{id}/resources/{serviceID}?resource_type=<type>`) in `internal/controller/network/firewall_external.go`
- [X] T022 [US2] Extend `Update` to attach missing / detach extra services (paced) and classify already-bound-elsewhere as terminal `ServiceConflict` (FR-009), in `internal/controller/network/firewall_external.go`
- [X] T023 [US2] Extend `Observe` to GET `/resources`, populate `status.atProvider.attachedServices`, and include the attachment set in `ResourceUpToDate` in `internal/controller/network/firewall_external.go`
- [X] T024 [US2] Ensure `Delete` removes attachments (rely on group-delete cascade; else detach-then-delete — verify per research R-7) in `internal/controller/network/firewall_external.go`
- [X] T025 [P] [US2] Extend `examples/firewall.yaml` with an `attachedServices` `balancer` entry

**Checkpoint**: US1 + US2 — the group governs a real load balancer; exclusivity is enforced.

---

## Phase 5: User Story 3 - Outbound rules alongside inbound (Priority: P2)

**Goal**: `egress` rules are handled identically to `ingress` (direction is part of the canonical
key).

**Independent Test**: Apply a `Firewall` with both inbound and outbound rules; both round-trip
upstream, each with the correct `direction`, and the resource is Synced + Ready.

### Tests for User Story 3 ⚠️

- [X] T026 [P] [US3] Unit tests: `egress` rule round-trip and a mixed `ingress`+`egress` set-diff (direction discriminates otherwise-identical tuples) in `internal/controller/network/firewall_external_test.go`

### Implementation for User Story 3

- [X] T027 [US3] Confirm rule render/diff is direction-agnostic (`egress` handled like `ingress`; no special-casing) and add an `egress` rule to `examples/firewall.yaml`

**Checkpoint**: US1–US3 — inbound + outbound rules both enforced.

---

## Phase 6: User Story 4 - Day-2 lifecycle hardening (Priority: P3)

**Goal**: `policy` immutability, identity-preserving edits, and robust adoption — all without
disruption.

**Independent Test**: Change a rule's port → the group identity and attachments are unchanged;
attempt to change `policy` → terminal `ImmutableFieldChange`; a Create that errored-yet-created
re-adopts the same group by name rather than duplicating.

### Tests for User Story 4 ⚠️

- [X] T028 [P] [US4] Unit tests: `policy`-immutable rejection; in-place rule/port change preserves group identity + attachment set; error-yet-created adoption (no duplicate group) in `internal/controller/network/firewall_external_test.go`

### Implementation for User Story 4

- [X] T029 [US4] Enforce `policy` immutability in `Update` via `shared.FirstImmutableDiff` + `shared.RejectImmutableChange` (`ImmutableFieldChange`) in `internal/controller/network/firewall_external.go`
- [X] T030 [US4] Harden the Create-path adoption guard (error-yet-created → re-list by name, adopt the existing group, set external-name; never duplicate) per `project_adoption_reattaches_failed_orphan` in `internal/controller/network/firewall_external.go`

**Checkpoint**: all four stories independently functional; full lifecycle correct.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T031 [P] Run `golangci-lint`/`gosec`/`govulncheck` via `go run` (no host install) and `crossplane beta validate` on the regenerated `Firewall` CRD
- [X] T032 Verify `make generate` leaves a clean tree (Constitution §I/CI gate — deepcopy + CRD YAML committed with the `apis/` change)
- [X] T033 [P] Add an opt-in kuttl/k3d e2e bundle under `test/e2e/kuttl/tests/`: group + rules **self-contained** (assert `Synced=True` and `Ready=True` via `wait --for=condition`; pin explicit context — `feedback_kuttl_wait_for_condition`); the attachment step gated on a `TWE_FW_BALANCER_ID` env (no `LoadBalancer` kind exists, so it rides a pre-existing balancer)
- [X] T034 [P] Document `Firewall` in the `README.md` resources table + a short usage note (link `examples/firewall.yaml`)
- [X] T035 Live re-observation pass for the probe-at-impl items (research R-2/R-3/R-4/R-7): port-range delimiter, `DELETE group` cascade, exclusivity error code, `balancer` resource-id rendering — record findings (`feedback_capture_upstream_quirks` / `feedback_verify_by_reobservation`)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)**: no deps; T001 optional and standalone.
- **Foundational (P2)**: T002→T003→T004 ordered; T005/T006 parallel; T007→T008 after types+client; T009 after T007. **Blocks all stories.**
- **US1 (P3)**: depends on Foundational. **MVP.**
- **US2 (P4)**: depends on US1's Observe/Create/Update/Delete (extends each with the resource dimension).
- **US3 (P5)**: depends on US1's rule diff (adds egress coverage); independent of US2.
- **US4 (P6)**: depends on US1's Update/Create (adds immutability + adoption).
- **Polish (P7)**: after the desired stories.

### Within Each User Story

- Tests first (must fail), then helpers/diff, then Observe/Create/Update/Delete, then conditions.

### Parallel Opportunities

- Foundational: T005 ∥ T006 (then T007/T008); types (T002) ∥ client (T005) once T002 lands.
- US3 is largely independent of US2 and can proceed in parallel after US1.
- All `[P]` test tasks within a story run together.

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → **STOP & VALIDATE** a single rule
group (apply, confirm Synced+Ready, edit a rule converges, delete cleans up) → demo.

### Incremental Delivery

US1 (MVP) → US2 (LB attachment + exclusivity) → US3 (outbound) → US4 (day-2 hardening) → Polish.
Each story is an independently testable increment.

---

## Notes

- The `external` client imports only `internal/clients/timeweb` — no AWS, no new deps.
- Rules and attachments are **unordered sets**; Observe is the sole convergence authority and
  `Update` is paced (`maxFirewallMutationsPerReconcile`), mirroring Router.
- Opaque attachments ⇒ no cross-MR refs ⇒ no `Watches`, and the finalizer can never wedge on a
  missing dependency (`project_ref_gate_must_not_block_delete` — here it's structurally free).
- `policy` is create-only; enforce immutability in `Update` (not CEL).
- Regenerate deepcopy + CRD YAML in the same PR as `apis/` changes (Constitution §I).
- Commit after each task or logical group; the user batches commits manually.
