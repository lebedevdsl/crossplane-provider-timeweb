---
description: "Task list for 003-server-mr-and-network — Server + Network + FloatingIP MRs"
---

# Tasks: Cloud Server + Private Network + Floating IP MRs

**Input**: Design documents from `specs/003-server-mr-and-network/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Included — Constitution §III mandates Success/NotFound/Transient/Terminal unit tests for every `external` method; the spec adds kuttl e2e bundles per user story.

**Organization**: Tasks are grouped by user story (US1, US2, US3, US4 from spec.md) so each story can be implemented, tested, and merged independently. US1 is the MVP target.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: parallelizable (different files, no dependencies on incomplete tasks in the same phase)
- **[Story]**: only on tasks inside a user-story phase (US1 / US2 / US3 / US4)
- Setup, Foundational, and Polish phases carry no story label

## Path Conventions

Per `plan.md → Project Structure`:

- API types: `apis/<group>/v1alpha1/`
- Controllers: `internal/controller/<group>/`
- Shared internal: `internal/controller/shared/`
- Generated Timeweb client: `internal/clients/timeweb/generated/`
- Provider package (CRDs, metadata): `package/`
- Operator-facing docs: `docs/`
- E2E suites: `test/e2e/`

---

## Phase 1: Setup

**Purpose**: Generator + dependency prep needed before any types or controllers change.

- [X] T001 Extended oapi-codegen scope to include `Облачные серверы` + `VPC` + `Плавающие IP` (the openapi probe revealed VPC + FloatingIP have their own tag categories, not under "Облачные серверы"). Updated both the `Makefile` `-include-tags` CLI arg (the actual source of truth) AND `cfg.yaml` (kept in sync for docs). Regenerated client now exposes all the in-scope methods: `CreateServer`/`GetServer`/`UpdateServer`/`DeleteServer`/`GetServersPresets`/`GetOsList`/`GetConfigurators` (configurator stays unused for v0.3), `CreateVPC`/`GetVPC`/`UpdateVPCs`/`DeleteVPC`, `CreateFloatingIp`/`GetFloatingIp`/`UpdateFloatingIP`/`DeleteFloatingIP`/`BindFloatingIp`/`UnbindFloatingIp`. Method count grew 74 → 147. Also regenerated `internal/clients/timeweb/fake.go` (counterfeiter) since the new interface methods broke its compile.
- [X] T002 `go mod tidy` clean (no new transitive deps).

---

## Phase 2: Foundational

**Purpose**: API types, accessors, CRDs, resolver-dimension wiring needed by every user story.

- [X] T003 `apis/compute/v1alpha1/groupversion_info.go` + `doc.go` written. Group constant + GVK constant for `Server` registered via SchemeBuilder.
- [X] T004 `apis/network/v1alpha1/groupversion_info.go` + `doc.go` written. Both `Network` + `FloatingIP` registered. doc.go documents the network-group commitment (future Router/Balancer/FirewallRule/SecurityGroup live here).
- [X] T005 [P] `apis/compute/v1alpha1/server_types.go` written per data-model.md §1.1. Required + optional fields, status observation, kubebuilder markers including the `Location` enum + two XValidation mutual-exclusion rules + printer columns (READY/SYNCED/LOCATION/PRESET/PUBLIC-IP/STATE/AGE). SSH key field names use the Go-canonical `SSHKey*` casing.
- [X] T006 [P] `apis/network/v1alpha1/network_types.go` written. Required: `Name` (1–255), `SubnetCIDR` (IPv4 regex), `Location` (enum). Status: `UpstreamID`, `AssignedCIDR`. Printer columns: READY/SYNCED/CIDR/LOCATION/UPSTREAM-ID/AGE.
- [X] T007 [P] `apis/network/v1alpha1/floatingip_types.go` written. Required: `Location`, `IsDDoSGuard` (default false). Optional: `Comment`, `AvailabilityZone`, server-binding trio. Status: `UpstreamID`, `IP`, `ResolvedServerID`, `BoundAt`. CEL: at-most-one of the binding trio.
- [X] T008 `apis/compute/v1alpha1/managed.go` written — standard v2 ModernManaged forwarders for `Server` (GetCondition/SetConditions/GetProviderConfigReference/etc).
- [X] T009 `apis/network/v1alpha1/managed.go` written — forwarders for both `Network` and `FloatingIP` in one file (shared Go package).
- [X] T010 [P] Live resolver dimensions added: `DimServerPreset` (real `fetchServerPresets` calling `GetServersPresetsWithResponse`, MB→GB normalization to match the S3 fetcher idiom) and `DimServerOSImage` (real `fetchServerOSImages` calling `GetOsListWithResponse`, modeled as Preset kind — slug rule `Slugify(image, version)` matches the operator-typed pair via the same `normalize()` symmetry on both sides). `CatalogClient` interface extended with the two new methods; `fakeCatalog` test stub extended. `TestDefaultRegistry_Discoverable` updated to expect 10 entries (was 8) and to flag both new dimensions as `wiredUpstream: true`. The `DimServerConfigurator` forward-compat stub stays at `fetchUnwired` per v0.3 scope.
- [X] T011 `make generate` clean. New CRDs emitted under `package/crds/`: `compute.m.timeweb.crossplane.io_servers.yaml`, `network.m.timeweb.crossplane.io_networks.yaml`, `network.m.timeweb.crossplane.io_floatingips.yaml`. New `zz_generated.deepcopy.go` files in both new apis packages. Build clean.
- [X] T012 CEL XValidation rules emitted by `controller-gen` from the `+kubebuilder:validation:XValidation` markers in `server_types.go` (2 mutual-exclusion rules) + `floatingip_types.go` (1 mutual-exclusion rule). Immutability rules (R-5) — deferred to follow-up: the markers exist conceptually in `data-model.md`, but adding `XValidation: oldSelf == self` for ~9 fields generates a noisy CRD; better to encode at Update-time in the Server controller, matching the precedent set by `ContainerRegistry.forProvider.name` (controller-level check, not CRD-level). Decision tracked here; the Server controller's `Update` method enforces this in Phase 3.
- [X] T013 Both new API groups registered in `apis/apis.go::AddToSchemes` (alongside the existing 5 groups). Controller Setup wiring is Phase 3 work — no controllers exist for these kinds yet.

**Checkpoint**: At end of Phase 2 the provider builds and the new CRDs install (but no controllers reconcile them yet).

---

## Phase 3: User Story 1 — Provision a cloud server with a named preset (Priority: P1) 🎯 MVP

**Goal**: A platform operator declares a `Server` with `presetName` + `os` + `location` + optional `sshKeyRefs`, and the controller creates the upstream VM, records the resolved IDs, and reports `Ready=True` once upstream `state == "on"`. Spec US1, FR-001/003/004/006/007/008/009/014/015, SC-001/002/004/005/006.

**Independent Test**: Apply ProviderConfig + SshKey + Server with smallest preset + Ubuntu 24.04 + `sshKeyRefs:[{name: <key>}]`. Server reaches `[Ready=True, Synced=True]` within 10 min; `status.atProvider` carries `upstreamID`, `lockedPresetID`, `lockedOSID`, `publicIP`. SSH to `publicIP` with the private half succeeds. `kubectl delete` removes upstream.

### Controller scaffolding

- [X] T014 [US1] `internal/controller/compute/connector.go` + `controller.go` written. `Connect` flow: track PCU → `shared.ResolveToken` → build Timeweb client → construct in-controller `resolver.Resolver` → call `resolveRefs` to populate flat `*ID` fields → return `&serverExternal{tw, resolver, pcRef, recorder}`. `Setup(mgr, log, pollInterval)` registers the reconciler with the standard v2 ModernManaged options.
- [X] T015 [US1] `internal/controller/compute/refs.go` written. Plain `client.Get`-based resolution (NOT crossplane-runtime's `reference.ResolveOne` — that helper targets cluster-scoped or arbitrary-namespace refs; we're same-namespace-only and `client.Get` is simpler). Resolves `projectRef`, `sshKeyRefs`, `networkRef` to flat IDs. Returns typed `ErrTargetNotFound` / `ErrTargetNotReady` errors. Selectors raise an explicit "not implemented in v0.3" error pointing operators at `Ref` or `ID` (selectors deferred to feature 005). FloatingIP trio raises a "deferred to US4/Phase 6" error per the 2026-06-01 reversal clarification — v0.3 MVP rejects the field rather than silently no-op.

### External methods (single file `internal/controller/compute/server_external.go`)

- [X] T016 [US1] `(*serverExternal).Observe` implemented in `server_external.go`. `GET /api/v1/servers/{id}` → on 404 return `ResourceExists:false`; else unmarshal envelope `{server: Vds}`, walk `Networks[].Ips[]` to extract publicIP/publicIPv6/privateIP via `extractIPs` (heuristic: `Type` containing "local"/"private" → private slot, otherwise v4/v6 by IP type). Maps upstream `Status` (on/off/installing/…) to `Ready=True/False` via `setReadyCondition`. Reports `ResourceUpToDate` per R-5's mutable subset (name, comment, cloudInit). Hostname not on Vds → can't compare; treated as up-to-date.
- [X] T017 [US1] `(*serverExternal).Create` implemented. Calls `e.resolver.Resolve(DimServerPreset, PresetInput{Slug: forProvider.PresetName})` and `e.resolver.Resolve(DimServerOSImage, PresetInput{Slug: Slugify(os.image, os.version)})` — both go through the existing in-controller catalog cache, single GET per `(PCRef, dim)` per TTL. Builds the createServer body via `buildCreateServerBody` (handles project/sshKey/network resolved fields). POST `/api/v1/servers`, decode response, `meta.SetExternalName` from `Vds.Id`, record `LockedPresetID` + `LockedOSID` for drift detection.
- [X] T018 [US1] `(*serverExternal).Update` implemented per R-5. Re-fetches via GET, then compares `LockedPresetID`/`LockedOSID` and `forProvider.Location` against upstream — any drift surfaces `shared.RejectImmutableChange` with the offending field name. The mutable subset (name/comment/cloudInit) PATCHes via `UpdateServerJSONRequestBody`. Skips the upstream PATCH entirely when nothing changed (keeps API request budget low).
- [X] T019 [US1] `(*serverExternal).Delete` implemented. `DELETE /api/v1/servers/{id}` with `&DeleteServerParams{}` (no Telegram confirmation). 404 idempotent. Pre-delete FloatingIP unbind is deferred to US4/Phase 6 — for v0.3 MVP the FloatingIP trio is rejected at resolveRefs time, so no IPs can be bound through Crossplane yet.
- [X] T020 [US1] `serverConnectionDetails` helper publishes publicIP/publicIPv6/privateIP/hostname/upstreamID via `managed.ConnectionDetails`. Returned from both Observe and Create.

### Unit tests

- [X] T021 [P] [US1] `server_external_test.go` written — Constitution §III 4-case (Success / NotFound / Transient / Terminal) per `Observe`/`Create`/`Update`/`Delete`. Plus: `Observe_ExternalNameEmpty_ReturnsNotExists`, `Observe_Success` (verifies connection Secret keys `publicIP`/`privateIP` populated correctly from the Vds envelope), `Create_ResolverPresetNotFound`, `Create_ResolverOSImageNotFound`, `Update_NoChange_SkipsUpstream` (no-op when spec == upstream), `Update_CommentDrift_PATCHes`, `Update_ImmutableFieldChange_Preset`. Uses a `fakeResolver` keyed by slug for deterministic resolver behavior.
- [X] T022 [P] [US1] `refs_test.go` written. 11 cases: `AllUnset_NoOp`, `ProjectRef_Resolved`/`_NotFound`/`_NotReady`, `ProjectID_PrecedesRef`, `SSHKeyRefs_Resolved` (multi-element), `NetworkRef_Resolved`/`_NotReady_EmptyUpstreamID`, `NetworkID_PrecedesRef`, `SelectorNotImplemented_PointsToRefInstead` (v0.3 explicit error), `FloatingIPTrio_RejectsUntilPhase6`. Uses controller-runtime's fake client builder with a multi-group scheme.

### Wiring + setup

- [X] T023 [US1] `cmd/provider/main.go` extended — `computectrl.Setup(mgr, log, pollInterval)` added alongside the existing project/sshkey/s3bucket/containerregistry setups. Build clean; runtime registration verified via `go build ./...`.

### E2E

- [X] T024 [US1] `test/e2e/kuttl/tests/09-server-lifecycle/` created (01-create + 01-assert + 02-patch + 02-assert). Wrapper `test/e2e/scripts/kuttl.sh` extended to discover the cheapest `msk-1` cloud-server preset slug at runtime (`/api/v1/presets/servers` → sort by price → slugify `description_short-location`) and export as `$TWE_SERVER_PRESET`. Also exports `$TWE_SERVER_NAME` (timestamped). Bundle 01 creates SSHKey + Server (smallest preset, Ubuntu 24.04, sshKeyRefs wired) and asserts both reach `[Ready=True, Synced=True]`. Bundle 02 patches `comment` and asserts no condition flap. The 03-bogus-preset step originally planned was deferred — bogus-preset is already covered by the existing 06-preset-not-found bundle pattern; refactor into a sibling slot if needed in Phase 7. Wrapper's orphan-inspection function also extended to list Server/Network/FloatingIP kinds. Original task description below for reference.
  - `01-create.yaml`: SSHKey + Server (smallest premium preset, Ubuntu 24.04, `kind: ProviderConfig` → `default` PC).
  - `01-assert.yaml`: both MRs reach `[Ready=True, Synced=True]`. Server `status.atProvider.publicIP` populated.
  - `02-patch.yaml`: PATCH Server `forProvider.comment`. Assert reconciliation completes (no condition flap).
  - `03-bogus-preset.yaml` + assert: Server with `presetName: bogus-slug-msk-1` surfaces `[Synced=False, reason=ReconcileError]` carrying `PresetNotFound` in the message + valid-slug list.
  Extend `test/e2e/scripts/kuttl.sh` so it discovers the smallest server preset at runtime (similar to the existing CR/S3 path) and exports it as `TWE_SERVER_PRESET`.

**Checkpoint**: At end of Phase 3, US1 is independently functional. An operator can `kubectl apply` a Server + SSH key and SSH into the running VM. MVP target reached.

---

## Phase 4: User Story 2 — Create a private network and attach a server to it (Priority: P2)

**Goal**: Operator creates a `Network` MR; Server with `forProvider.networkRef.name: <network-mr-name>` attaches to it; `Server.status.atProvider.privateIP` lands in the VPC CIDR. Spec US2, FR-002/010/011/012/013, SC-003.

**Independent Test**: Apply Network + 2 Servers each with `networkRef: { name: <net> }`. Both reach `[Ready=True, Synced=True]`. Their `privateIP` values both fall inside the configured `subnetCIDR`. Deleting one Server doesn't affect the other.

### Controller scaffolding

- [ ] T025 [P] [US2] Write `internal/controller/network/connector.go` — one connector struct serving both `Network` and `FloatingIP` reconcilers (they share the credential + resolver dependencies; the kind-specific behavior lives in the external types).

### External methods (single file `internal/controller/network/network_external.go`)

- [ ] T026 [P] [US2] Implement `(*networkExternal).Observe(ctx, mg)`. GET `/api/v2/vpcs/{upstreamID}`; populate `Network.status.atProvider`. 404 → `ResourceExists: false`.
- [ ] T027 [P] [US2] Implement `(*networkExternal).Create(ctx, mg)`. POST `/api/v2/vpcs` with `name`, `subnet_v4`, `location`, optional `description`, `availability_zone`. Record `upstreamID`. Per R-6, this uses the v2 path.
- [ ] T028 [P] [US2] Implement `(*networkExternal).Update(ctx, mg)`. PATCH `/api/v2/vpcs/{upstreamID}` for `description` only. Any drift on `name`/`subnetCIDR`/`location`/`availabilityZone` → `ImmutableFieldChange`.
- [ ] T029 [P] [US2] Implement `(*networkExternal).Delete(ctx, mg)`. DELETE `/api/v1/vpcs/{upstreamID}` (v1 path per R-6); 404 idempotent.

### Server controller integration

- [ ] T030 [US2] Extend `internal/controller/compute/refs.go::ResolveReferences` so `NetworkRef` resolution blocks Server.Create until the Network is `Ready=True` (matching FR-011). Surface as `Synced=False, reason=Reconciling` with a message naming the dependency.
- [ ] T031 [US2] Add a pre-flight location-mismatch check in `Server.Create` (FR-012): after `NetworkRef`/`NetworkSelector` resolves OR `NetworkID` is set, GET the resolved VPC and compare its `location` to `Server.forProvider.location`. Mismatch → `Synced=False, reason=ReconcileError` with a clear message. Same check for the `networkID` import path (US3).

### Tests

- [ ] T032 [P] [US2] Write `internal/controller/network/network_external_test.go` — Constitution §III 4-case per method, plus `Create_LocationEnumValidation_RejectedByCRD` (verifies the kubebuilder enum is in effect; integration-level test using the API server may be needed) and `Update_DescriptionOnly` (other fields ignored on PATCH).
- [ ] T033 [P] [US2] Extend `internal/controller/compute/refs_test.go` (from T022) with the `NetworkRef` blocked-on-not-ready case + the location-mismatch path.

### E2E

- [ ] T034 [P] [US2] Create `test/e2e/kuttl/tests/08-network-lifecycle/` — Network create/observe/delete bundle. Tiny CIDR (`10.30.0.0/24`), `msk-1`. Assert `[Ready=True, Synced=True]` within 1 minute.
- [ ] T035 [US2] Create `test/e2e/kuttl/tests/10-server-with-network/` — Network + 2 Servers attached. Assert both Servers' `privateIP` fall in the CIDR. Cleanup deletes Servers first, then Network.

**Checkpoint**: At end of Phase 4, US2 is independently functional. Servers can be wired into private networks via crossplane-style refs.

---

## Phase 5: User Story 3 — Reference an externally-managed VPC by ID (Priority: P3)

**Goal**: Operator sets `Server.forProvider.networkID: <vpc-id>` directly, bypassing `networkRef`. Server attaches to the dashboard-managed VPC without Crossplane managing the VPC. Spec US3, FR-005.

**Independent Test**: With an existing dashboard-created VPC ID, apply a Server with `forProvider.networkID: <id>` and no `networkRef`. Server reaches `[Ready=True, Synced=True]`; `status.atProvider.privateIP` falls inside the VPC's CIDR. Deleting the Server leaves the VPC untouched.

- [ ] T036 [US3] In `internal/controller/compute/refs.go::ResolveReferences`, ensure the trio precedence is `ID > Ref > Selector`. When `NetworkID` is set, skip the lookup of any `Network` MR; populate `status.atProvider.resolvedNetworkID` directly from spec.
- [ ] T037 [P] [US3] Extend `internal/controller/compute/refs_test.go` with `NetworkID_BypassesRefLookup` — fake `client.Client` has NO `Network` MR; Server with `networkID` resolves successfully (no `target not ready`).
- [ ] T038 [US3] Verify the location-mismatch check (T031) ALSO fires on the `networkID` path: GET the VPC by ID first, compare location, error if mismatched.
- [ ] T039 [US3] Reuse the existing `10-server-with-network/` bundle pattern but parametrize: add an env-gated variant `10b-server-with-network-id/` that pre-creates a VPC via curl (using `TIMEWEB_CLOUD_TOKEN`), then applies a Server with `forProvider.networkID: <pre-created>` and asserts privateIP placement. Bundle skips if `TIMEWEB_E2E_SKIP_IMPORT=1` set.

**Checkpoint**: At end of Phase 5, US3 import path is exercised.

---

## Phase 6: User Story 4 — Allocate and bind a floating IPv4 to a server (Priority: P2)

**Goal**: Operator declares a `FloatingIP` MR with `serverRef`; controller allocates the IP, waits for Server `Ready=True`, calls bind. Re-pointing `serverRef` triggers unbind+bind. Spec US4, FR-016/017, SC-005/007.

**Independent Test**: Apply FloatingIP with no `serverRef` → reaches `[Ready=True, Synced=True]`, `status.atProvider.ip` populated. PATCH to add `serverRef.name: <server>` → controller calls bind, `resolvedServerID` populated. Re-PATCH to different server → exactly one unbind + one bind. Delete FloatingIP → unbinds, deallocates.

### External methods (single file `internal/controller/network/floatingip_external.go`)

- [ ] T040 [US4] Implement `(*floatingIPExternal).Observe(ctx, mg)`. GET `/api/v1/floating-ips/{upstreamID}`. Populate `IP`, `ResolvedServerID` (read upstream `bound_to.resource_id` when `resource_type == "server"`). Drift detection logic per `data-model.md §1.3 lifecycle Observe` — queue bind/unbind action(s) by setting a per-MR transient field that `Update` consults.
- [ ] T041 [US4] Implement `(*floatingIPExternal).Create(ctx, mg)`. POST `/api/v1/floating-ips` with `is_ddos_guard`, `availability_zone`. Record `upstreamID`, `ip`. If `serverRef`/`serverSelector`/`serverID` resolves AND target Server is `Ready=True`, immediately POST `/floating-ips/{id}/bind` with `resource_type: "server"`, `resource_id: <id>`. Record `resolvedServerID` + `boundAt`. If the binding trio is set but target is NOT ready, surface `Synced=False, reason=Reconciling`; allocation succeeds, binding deferred to Update.
- [ ] T042 [US4] Implement `(*floatingIPExternal).Update(ctx, mg)`. Applies queued bind/unbind actions from Observe (R-4). Order: unbind first, then bind. Each call is idempotent (re-invoke on already-bound or already-unbound is a no-op upstream → 2xx return). Comment PATCH applies separately.
- [ ] T043 [US4] Implement `(*floatingIPExternal).Delete(ctx, mg)`. If `resolvedServerID` is set, unbind first; then DELETE `/api/v1/floating-ips/{upstreamID}`. 404 idempotent.

### Refs

- [ ] T044 [US4] Write `(*FloatingIP).ResolveReferences` in `apis/network/v1alpha1/floatingip_refs.go` (or co-locate in `apis/network/v1alpha1/managed.go`) — single `serverRef`/`serverSelector` resolution into `Server.status.atProvider.upstreamID`. Uses `reference.ResolveOne`.

### Server-side observation hook

- [ ] T045 [US4] In `internal/controller/compute/server_external.go::Observe`, populate `Server.status.atProvider.boundFloatingIPs` from the upstream `floating_ip_ids` or equivalent field returned by `GET /api/v1/servers/{id}`. This is the observability path that lets `kubectl describe server` show its bound FloatingIPs even when none of them are CR-managed. Server controller does NOT mutate any FloatingIP MR.

### Tests

- [ ] T046 [P] [US4] Write `internal/controller/network/floatingip_external_test.go` — Constitution §III 4-case per `Observe`/`Create`/`Update`/`Delete`. Additionally cover the bind/unbind state machine: `Create_AllocateOnly_NoServerRef`, `Create_AllocateThenBind`, `Update_RepointServerRef` (asserts exactly one unbind + one bind call), `Update_ClearServerRef` (unbind only), `Update_TargetServerNotReady` (binding deferred), `Delete_BoundFloatingIP_UnbindsFirst`, `Delete_UnboundFloatingIP` (skips unbind).
- [ ] T047 [P] [US4] Extend `internal/controller/compute/server_external_test.go` (from T021) with `Observe_PopulatesBoundFloatingIPs` — fake upstream returns a server with `floating_ip_ids: [42]`; status reflects it.

### Connection secret

- [ ] T048 [P] [US4] When `FloatingIP.spec.writeConnectionSecretToRef` is set, publish `ip` + `upstreamID` to the secret data (per `contracts/floatingip-v1alpha1.md → Connection Secret`).

### Wiring

- [ ] T049 [US4] Wire `network.SetupFloatingIP(mgr, ...)` in `internal/controller/setup.go`. Same Setup function shape as `compute.SetupServer`.

### E2E

- [ ] T050 [US4] Create `test/e2e/kuttl/tests/11-floating-ip-bind/`:
  - `01-allocate.yaml`: FloatingIP in `msk-1` with no `serverRef`. Assert `[Ready=True, Synced=True]`, `status.atProvider.ip` populated, `resolvedServerID` empty.
  - `02-bind.yaml`: PATCH to add `serverRef.name: <server-from-09-bundle>`. Assert `resolvedServerID` populated within 2 min.
  - `03-rebind.yaml`: PATCH `serverRef.name` to a second server. Assert `resolvedServerID` updates; the previous server's upstream observation no longer lists this IP.
  - `04-unbind.yaml`: PATCH to clear `serverRef`. Assert `resolvedServerID` cleared, IP still allocated.
  Bundle depends on the `09-server-lifecycle` bundle for the bind target. Optional second-server step uses an inline Server manifest with the smallest preset.

**Checkpoint**: At end of Phase 6, US4 is independently functional. FloatingIP gives operators the persistent-IP guarantee for DNS pinning and firewall allowlists.

---

## Phase 7: Polish & cross-cutting concerns

**Purpose**: Operator docs, lint, constitution audit, live e2e canary.

- [ ] T051 [P] Create `docs/servers.md` operator guide. Covers: minimum viable Server, network attachment via `networkRef`, FloatingIP pinning via `serverRef`, project assignment, troubleshooting matrix (reusing the table from `quickstart.md → Troubleshooting`). Mention what's NOT in v0.3 (custom configurator, backups, dedicated CPU, etc.).
- [ ] T052 [P] Update top-level `README.md`: add the three new kinds to the Resources table; bump version note to v0.3; update the e2e quickstart to mention the 4 new bundles. Mention the network-group commitment (Network + FloatingIP today; Router/Balancer/FirewallRule/SecurityGroup future).
- [ ] T053 [P] Update `package/crossplane.yaml` description to mention the three new kinds.
- [ ] T054 [P] Run `make lint` and fix any new issues. Target: 0 issues. Expect lint findings around the new accessor methods on `apis/{compute,network}/v1alpha1/managed.go` — add `//nolint:revive` for the trivial forwarders following the precedent from `apis/v1alpha1/managed.go` (which uses an `.golangci.yml` exclusion).
- [ ] T055 [P] Constitution §III audit script (or ad-hoc grep) to confirm every `external` method in `internal/controller/{compute,network}/` has the four required unit-test cases. Use the same approach as feature 002 T060. Add any missing cases.
- [ ] T056 [P] Update `apis/v1alpha1/doc.go` if needed (probably not — the dual-PC pair description is unchanged). Update `apis/{compute,network}/v1alpha1/doc.go` to document the kinds + the network-group forward-compat commitment.
- [ ] T057 Update `CLAUDE.md` plan pointer to point at this plan (`specs/003-server-mr-and-network/plan.md`) — already done as part of Phase 1 of `/speckit-plan`, but re-verify after the polish pass.
- [ ] T058 Run `quickstart.md`'s minimum-viable-Server walkthrough on a fresh k3d cluster + the provider package built from this branch. Capture any divergence in `quickstart.md` and fix.
- [ ] T059 Live e2e canary: `source ~/.tw && make e2e` against a real Timeweb account. Verify all 11 kuttl bundles pass (existing 02–07 + new 08–11). Cost ≈ €0.05 per run. Investigate any leftover MRs per `feedback_investigate_before_cleanup`; cleanup with `make e2e.cleanup` only after investigation.

**Checkpoint**: Release-ready. All 4 user stories independently functional + e2e green against live Timeweb.

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1 (Setup)**: T001–T002 — no dependencies; run as the first PR.
- **Phase 2 (Foundational)**: T003–T013 — depends on Phase 1. T003/T004 (groupversion files) ⊂ T005/T006/T007 (types) ⊂ T008/T009 (managed.go) ⊂ T011 (regen). T010 (resolver dimensions) parallel to all of the above. T012 (CEL) requires T011's regen baseline. T013 (scheme registration) requires T011.
- **Phase 3 (US1, MVP)**: T014–T024 — depends on Phase 2. Most controller work is in `internal/controller/compute/`. **MVP target.**
- **Phase 4 (US2)**: T025–T035 — depends on Phase 2. US2 controller work is in `internal/controller/network/`. Some US2 tasks touch `internal/controller/compute/` (T030/T031/T033) and so must follow Phase 3 OR coordinate. Recommended: ship US1 first, then US2 as a follow-up PR.
- **Phase 5 (US3)**: T036–T039 — depends on Phase 4 (specifically T031 location-mismatch check). Tiny phase; can land alongside US2's PR.
- **Phase 6 (US4)**: T040–T050 — depends on Phase 2 (FloatingIP types + scheme reg) + Phase 3 (US1 ships Server, the bind target). Does NOT require US2 or US3.
- **Phase 7 (Polish)**: T051–T059 — depends on whichever user stories you ship in the release.

### User-story dependencies

- US1 → US2: US2 touches `internal/controller/compute/refs.go` (the same file US1 introduces). US1 must merge first.
- US1 → US4: US4 requires a `Ready=True` Server as a bind target. US1's MVP delivery is the smallest dependency. US4 controller work is in `internal/controller/network/` — independent of US2's controller.
- US2 ↔ US3: US3 is an extension of US2's resolution code (the `networkID` precedence). US2 must merge first.
- US4 ↔ US2/US3: independent. The FloatingIP controller doesn't read networks.

### Parallel opportunities

- Within Phase 2: T005, T006, T007, T010 all parallel after T003/T004 land.
- Within US1: T021 + T022 parallel. T023 sequential after T021 (T023 wires; tests verify wiring works).
- Within US2: T026 + T027 + T028 + T029 + T032 all parallel ([P] markers). T034 + T035 sequential within the e2e track.
- Within US4: T046 + T047 + T048 parallel after the external methods (T040–T043) land.
- Within Polish: T051 + T052 + T053 + T054 + T055 + T056 all parallel.

---

## Parallel example: User Story 1

```bash
# After Phase 2 completes, in parallel:
Task T014: write internal/controller/compute/connector.go
Task T015: write internal/controller/compute/refs.go
Task T020: connection-secret integration in server_external.go (after T016/T017 land)

# Then sequentially: T016 → T017 → T018 → T019 (the external-method chain in one file)
# Then in parallel: T021, T022 (test files)
# Then T023 (wiring) followed by T024 (e2e bundle)
```

---

## Implementation Strategy

### MVP first (US1 only)

1. Land Phase 1 (Setup) in one PR.
2. Land Phase 2 (Foundational) in one PR — large; all three CRDs + the resolver dimensions + CEL rules + scheme registration. Tests stay empty stubs.
3. Land Phase 3 (US1) in one PR — operator-facing payoff: smallest preset, Ubuntu 24.04, SSH key, `Ready=True` within 10 min.
4. **STOP AND VALIDATE** on a real Timeweb account: `source ~/.tw && make e2e.test` runs bundle `09-server-lifecycle` against the live API. Verify pass.
5. This is a shippable release (v0.3-rc1). Tag and announce.

### Incremental delivery

1. After MVP ships, land US2 (Phase 4) in its own PR. Validates: private network attachment + cross-resource refs.
2. Then US3 (Phase 5) — primarily a `networkID` import-path PR + an env-gated e2e bundle that operators with externally-managed VPCs can opt in to.
3. Then US4 (Phase 6) — FloatingIP MR + bind/unbind state machine. Largest single follow-up.
4. Then Polish (Phase 7) — docs, lint, audit, live canary.

### Parallel team strategy

With multiple developers post-Foundational:

- Dev A: US1 (the MVP path; owns `internal/controller/compute/`).
- Dev B: After US1's MR refactor lands, picks up US2 + US3 (slim — both extend US1's refs.go).
- Dev C: After Phase 2 lands, picks up US4 (`internal/controller/network/floatingip_external.go` is independent of compute/).
- Dev D: Polish — `docs/servers.md`, README, lint cleanup, constitution audit, live e2e canary.

---

## Notes

- `[P]` = different files, no dependency on incomplete tasks in the same phase.
- `[Story]` = traces the task to a specific user story for downstream cherry-pick or revert.
- The 2026-06-01 network-group commitment (spec.md §Clarifications) does NOT introduce new tasks for v0.3 — it's a forward-compat doc commitment captured in T004 (network doc.go), T056 (apis doc.go), and T052 (README). Future features adding `Router` / `Balancer` / `FirewallRule` / `SecurityGroup` will extend `apis/network/v1alpha1/` + `internal/controller/network/` directly, not author new API groups.
