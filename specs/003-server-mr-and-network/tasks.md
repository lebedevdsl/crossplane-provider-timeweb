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

- [X] T025 [P] [US2] `internal/controller/network/connector.go` + `controller.go` written. The connector serves `Network` for v0.3 (the FloatingIP reconciler joins the same package in Phase 6). Neither network-group kind needs the catalog resolver (no preset/OS sizing), so the connector is resolver-free — simpler than the compute connector. `SetupNetwork(mgr, log, pollInterval)` registers the reconciler with the standard v2 ModernManaged options + `managed.WithManagementPolicies()`.

### External methods (single file `internal/controller/network/network_external.go`)

- [X] T026 [P] [US2] `(*networkExternal).Observe` implemented in `network_external.go`. GET `/api/v2/vpcs/{id}` (the string VPC ID is stored verbatim as the external-name — no `EncodeID` round-trip like the int-ID kinds). 404 → `ResourceExists:false`; else unmarshal `{vpc: Vpc}` envelope, populate `atProvider.{upstreamID, assignedCIDR}`, call `Available()`. `isNetworkUpToDate` compares ALL fields (mutable + immutable) so an immutable-field edit routes through Update for an explicit rejection rather than being silently ignored.
- [X] T027 [P] [US2] `(*networkExternal).Create` implemented. POST `/api/v2/vpcs` (v2 path per R-6) via `buildCreateVPCBody` (`name`, `subnet_v4`, `location`, optional `description` + `availability_zone`). `meta.SetExternalName` from the returned `vpc.id` string; populate status; `Creating()`.
- [X] T028 [P] [US2] `(*networkExternal).Update` implemented. Re-fetches via GET, runs the R-6 immutable guard via `shared.FirstImmutableDiff` over `{name, subnetCIDR, location, availabilityZone}` → `shared.RejectImmutableChange` on the first drift. The CRD carries NO `oldSelf==self` CEL (T012 decision — immutability enforced controller-side, matching the Server precedent). Only `description` PATCHes (`/api/v2/vpcs/{id}`); skips the upstream call when unchanged.
- [X] T029 [P] [US2] `(*networkExternal).Delete` implemented. DELETE `/api/v1/vpcs/{id}` (v1 path per R-6 — handled by the generated `DeleteVPC` method's route). 404 idempotent; empty external-name → no-op.

### Server controller integration

- [X] T030 [US2] `internal/controller/compute/refs.go::resolveNetworkRef` already gates Server.Create on Network readiness — an empty `status.atProvider.upstreamID` yields `ErrTargetNotReady` (FR-011), and the wrapped message names the Network dependency. NOTE: the connector surfaces resolveRefs failures via crossplane-runtime's standard Connect-error path, which renders as `Synced=False, reason=ReconcileError` (the runtime does not expose a per-error custom reason on Connect, so `reason=Reconciling` from the spec wording is not separately emitted — the message still names the unready dependency). Documented as a minor deviation; revisit if a custom reason is required.
- [X] T031 [US2] FR-012 location-mismatch pre-flight added in `resolveNetworkRef`: it now also returns the referenced Network's `spec.forProvider.location`, and `resolveRefs` compares it to the Server's location, returning the new typed `ErrNetworkLocationMismatch` on a mismatch. INTERPRETATION: on the `networkRef`/`networkSelector` path the Network MR already carries its location, so no upstream VPC GET is needed (cleaner + keeps the check unit-testable with a fake client per T033). The `networkID` import path (no MR to read) defers its location check to US3/T038 via an upstream `GetVPC` in the Server external — noted there.

### Tests

- [X] T032 [P] [US2] `internal/controller/network/network_external_test.go` written — Constitution §III 4-case (Success / NotFound / Transient / Terminal) per `Observe`/`Create`/`Update`/`Delete`. Plus: `Observe_ExternalNameEmpty_ReturnsNotExists`, `Observe_DescriptionDrift_NotUpToDate`, `Create_NetworkError`, `Create_TerminalError_OverlappingCIDR`, `Update_DescriptionOnly_PATCHes`, `Update_NoChange_SkipsUpstream`, `Update_ImmutableFieldChange_Name`/`_SubnetCIDR` (asserts `Synced=False, reason=ImmutableFieldChange` + no upstream PATCH), `Delete_EmptyExternalName_NoOp`. The `Create_LocationEnumValidation_RejectedByCRD` case from the original task text is omitted — CRD enum enforcement is an admission-time concern that needs an envtest API server, not the unit-level fake; it's exercised by the e2e bundles instead. Uses `timeweb.FakeClient` (counterfeiter) keyed by `*Returns`.
- [X] T033 [P] [US2] `internal/controller/compute/refs_test.go` extended: the blocked-on-not-ready case already existed (`NetworkRef_NotReady_EmptyUpstreamID`); added `NetworkRef_LocationMismatch` (asserts `ErrNetworkLocationMismatch`) and `NetworkRef_LocationMatch_Resolves` (same-location resolves to the VPC ID).

### E2E

- [X] T034 [P] [US2] `test/e2e/kuttl/tests/08-network-lifecycle/` created (01-create + 01-assert + 02-patch + 02-assert). Tiny CIDR `10.30.0.0/24`, location `ru-1` (the API code; the dashboard's `msk-1` label maps to `ru-1` per `project_timeweb_location_codes_api_vs_dashboard`). Bundle 01 creates a Network and asserts `[Synced=True, Ready=True]` (assert timeout 120s). Bundle 02 PATCHes `description` and asserts no condition flap. Wrapper `test/e2e/scripts/kuttl.sh` extended to export `$TWE_NETWORK_NAME` (timestamped) + add it to the envsubst allow-list; orphan inventory already lists `networks`/`floatingips` (T024).
- [X] T035 [US2] `test/e2e/kuttl/tests/10-server-with-network/` created (01-create + 01-assert). Creates an SSHKey + a Network (`10.31.0.0/24`, `ru-1`) + 2 Servers each with `networkRef` to it; asserts all three reach `[Synced=True, Ready=True]` (assert timeout 720s for two ~10-min VM provisions). kuttl YAML can't assert CIDR membership of `privateIP`, so that check moves to the live canary (T059) — noted in the bundle header. Cleanup ordering (Servers before Network) is handled by kuttl's reverse-order teardown of the objects it created.

**Checkpoint**: At end of Phase 4, US2 is independently functional. Servers can be wired into private networks via crossplane-style refs.

---

## Phase 5: User Story 3 — Reference an externally-managed VPC by ID (Priority: P3)

**Goal**: Operator sets `Server.forProvider.networkID: <vpc-id>` directly, bypassing `networkRef`. Server attaches to the dashboard-managed VPC without Crossplane managing the VPC. Spec US3, FR-005.

**Independent Test**: With an existing dashboard-created VPC ID, apply a Server with `forProvider.networkID: <id>` and no `networkRef`. Server reaches `[Ready=True, Synced=True]`; `status.atProvider.privateIP` falls inside the VPC's CIDR. Deleting the Server leaves the VPC untouched.

- [X] T036 [US3] Trio precedence `ID > Ref > Selector` confirmed in `refs.go::resolveRefs` (the `NetworkID == nil && NetworkRef != nil` guard means a set `NetworkID` skips the `Network` MR lookup; selector errors only when `NetworkID` is still unset). Added `populateResolvedRefs` in `server_external.go::Create` which records `status.atProvider.{resolvedNetworkID, resolvedProjectID, resolvedSSHKeyIDs}` from the resolved spec — for the import path `resolvedNetworkID` is the operator-supplied VPC id verbatim. (These resolved-* status fields were declared in US1 but not previously populated; wired here.)
- [X] T037 [P] [US3] `refs_test.go` extended with `NetworkID_BypassesRefLookup` — fake client has NO `Network` MR; a Server with a bare `networkID` resolves with no error and leaves the id unchanged.
- [X] T038 [US3] Location-mismatch check fires on the `networkID` path via `(*serverExternal).checkNetworkLocationByID` — called at the top of `Create` when `NetworkRef==nil && NetworkID!=nil` (the operator-set-ID signature). GETs the VPC, compares `location`, returns `ErrNetworkLocationMismatch` on mismatch and `ErrTargetNotFound` on 404 (wrong imported id). `server_external_test.go` covers `NetworkIDImport_LocationMatch`/`_LocationMismatch`/`_VPCNotFound` (mismatch asserts no `CreateServer` call). The ref path keeps its no-API check from T031.
- [X] T039 [US3] `test/e2e/kuttl/tests/10b-server-with-network-id/` created (01-create + 01-assert). The wrapper (`kuttl.sh` section 3b) pre-creates an out-of-band VPC via `curl POST /api/v2/vpcs`, exports `$TWE_IMPORT_VPC_ID`, and deallocates it at exit (`DELETE /api/v1/vpcs/{id}` in the trap, after kuttl tears the Server down). Bundle applies a Server with `forProvider.networkID` + no `networkRef` and asserts `[Synced=True, Ready=True]` plus `status.atProvider.resolvedNetworkID == $TWE_IMPORT_VPC_ID`. Skipped (dir removed from the tmp copy) when `TIMEWEB_E2E_SKIP_IMPORT=1`. `$TWE_IMPORT_VPC_ID` added to the envsubst allow-list. (privateIP-in-CIDR assertion is deferred to the live canary T059 — not expressible in kuttl YAML.)

**Checkpoint**: At end of Phase 5, US3 import path is exercised.

---

## Phase 6: User Story 4 — Pin a floating IPv4 to a server (Priority: P2)

> **Regenerated 2026-06-01** for the reversed **Server-consumes-IP** model
> (spec.md "FloatingIP reference reversal"). `FloatingIP` is pure
> allocation (no `serverRef`); the **Server** controller owns bind/unbind
> via `Server.forProvider.floatingIPRefs`. The original FloatingIP-owns-
> binding tasks are superseded by the list below. Authoritative shapes:
> `data-model.md §1.1/§1.3` + `contracts/floatingip-v1alpha1.md` (both
> regenerated). The committed types already match (`floatingip_types.go`
> is allocation-only; `server_types.go` has the `floatingIPRefs/Selector/IDs`
> trio + the CEL mutual-exclusion rule).

**Goal**: Operator declares an allocation-only `FloatingIP`, then a `Server`
with `forProvider.floatingIPRefs: [{name: <fip>}]`. The Server controller,
once the VM is `Ready=True`, calls `POST /floating-ips/{id}/bind` with
`resource_type: "server"`. Re-pointing `floatingIPRefs` triggers unbind+bind;
clearing it triggers unbind; Server delete unbinds all first. Spec US4,
FR-016/017 (post-reversal wording), SC-005/007.

**Independent Test**: Apply a `FloatingIP` (`location` + `isDDoSGuard` only) →
`[Ready=True, Synced=True]`, `status.atProvider.ip` populated, unbound. Apply
a `Server` with `floatingIPRefs: [{name: <fip>}]`. Once Ready, the Server's
`status.atProvider.boundFloatingIPs` lists the IP's upstream ID and the IP's
upstream `bound_to.resource_id` == server id. Clear `floatingIPRefs` → Server
unbinds; the FloatingIP stays allocated+unbound. Delete the FloatingIP →
deallocates.

### Status type fix (precedes the bind logic)

- [X] T040 [US4] `Server.status.atProvider.BoundFloatingIPs` changed `[]int64`→`[]string` in `server_types.go`. `make generate` regenerated `apis/compute/v1alpha1/zz_generated.deepcopy.go` + `package/crds/compute.m.timeweb.crossplane.io_servers.yaml` (string-array). `boundFloatingIPs` is built from per-IP confirmation GETs (the `Vds` server GET has no `floating_ip_ids` field), not the server observation.

### FloatingIP controller — allocation only (file `internal/controller/network/floatingip_external.go`)

- [X] T041 [P] [US4] `(*floatingIPExternal).Observe` implemented in `network/floatingip_external.go`. GET floating-ip (string ID = external-name); 404 → `ResourceExists:false`; unmarshal `{ip: FloatingIp}` envelope (note: the key is `ip`, not `floating_ip`); populate `ip` + `observedBoundTo.{resourceType, resourceID}` (resourceID via the `FloatingIp_ResourceId` union's `AsFloatingIpResourceId0`); `Available()`; `ResourceUpToDate` compares `comment` only.
- [X] T042 [P] [US4] `(*floatingIPExternal).Create` implemented. POST floating-ips with `is_ddos_guard` + a resolved `availability_zone` (`availabilityZoneFor`: spec value, else a per-location default map `{ru-1:spb-1, ru-2:msk-1, ru-3:spb-3, nl-1:ams-1, de-1:fra-1, kz-1:ala-1}`, else a loud error since the upstream body requires an AZ). Allocated **unbound**; external-name = `ip.id`; connection details published.
- [X] T043 [P] [US4] `(*floatingIPExternal).Update` + `Delete` implemented. Update: immutable guard on `availabilityZone`+`isDDoSGuard` via `shared.FirstImmutableDiff` → `RejectImmutableChange`; PATCH `comment` only (skip when unchanged). (`location` isn't on the upstream `FloatingIp` GET shape, so it's not diff-compared — it's structurally immutable via the create-time AZ derivation; documented.) Delete: DELETE floating-ips, 404 idempotent, no force-unbind. The `network` connector now type-switches `Network`/`FloatingIP` (via `resource.ModernManaged`); `network.SetupFloatingIP` added + wired in `main.go`.

### Server controller — owns bind/unbind (`internal/controller/compute/`)

- [X] T044 [US4] `refs.go::resolveRefs` now resolves `floatingIPRefs` → flat `fp.FloatingIPIDs` (`[]string` of FloatingIP upstream IDs) via `resolveFloatingIPRefs`, mirroring the `sshKeyRefs`→`sshKeyIDs` idiom (not-found → `ErrTargetNotFound`; empty upstreamID → `ErrTargetNotReady`; `floatingIPSelector` → not-implemented error). DEVIATION from spec scenario 4: a not-ready FloatingIP ref gates the Server's reconcile via `ErrTargetNotReady` (surfaced `Synced=False, reason=ReconcileError` — same Connect-error limitation as T030) rather than "Server created, binding deferred". Consistent with how network/project/sshkey refs already gate; the converged end-state is identical (Server exists + bound once the FIP is allocated). Documented as an accepted simplification.
- [X] T045 [US4] Bind/unbind convergence in new `internal/controller/compute/floatingip_bind.go`. `observeBoundFloatingIPs` (read-only, per-IP GET → `bound_to.resource_id == serverID`) is called from `Observe` (records `boundFloatingIPs`, folds `stringSetsEqual(bound, desired)` into `ResourceUpToDate`) and `Update`. `reconcileFloatingIPBindings` (in `Update`) unbinds bound-not-desired and binds desired-not-bound; binding is **deferred until the VM is "on"** (returns a retry error otherwise — matches "bind after Ready"). `bindFloatingIP` uses the `BindFloatingIp_ResourceId` union (`FromBindFloatingIpResourceId0`, `resource_type: server`); `unbindFloatingIP` tolerates 404 (idempotent). NOTE: binding runs in `Update`, not `Create` — `Create` leaves the server unbound (the VM isn't running yet), and the first post-Ready reconcile converges it.
- [X] T046 [US4] `Server.Delete` now unbinds every `status.atProvider.boundFloatingIPs` entry (idempotent via `unbindFloatingIP`) before `DeleteServer`. The FloatingIP MRs stay in the cluster.

### Tests

- [X] T047 [P] [US4] `internal/controller/network/floatingip_external_test.go` written — Constitution §III 4-case per `Observe`/`Create`/`Update`/`Delete`, plus `Observe_Success_Unbound`, `Observe_MirrorsBoundTo`, `Create_AllocatesUnbound` (asserts no Bind call), `Create_NoDefaultAZ_Errors`, `Update_CommentPATCH`, `Update_NoChange_SkipsUpstream`, `Update_ImmutableField_IsDDoSGuard`, `Delete_EmptyExternalName_NoOp`.
- [X] T048 [P] [US4] `refs_test.go` extended with `FloatingIPRefs_Resolved`/`_NotReady_EmptyUpstreamID`/`_NotFound` + `FloatingIPSelector_NotImplemented` (replacing the obsolete `FloatingIPTrio_RejectsUntilPhase6`). `server_external_test.go` gained `TestServerFloatingIPBinding`: `Create_DoesNotBind`, `Update_BindsDesired`, `Update_RepointFloatingIPRefs` (1 unbind + 1 bind, via `GetFloatingIpReturnsOnCall`), `Update_ClearFloatingIPRefs_UnbindOnly`, `Delete_UnbindsBoundFloatingIPsFirst`, `Observe_ConfirmsBoundSet`. Uses the `timeweb.FakeClient` bind/unbind/GetFloatingIp call counters.

### Connection secret

- [X] T049 [P] [US4] `floatingIPConnectionDetails` publishes `ip` + `upstreamID`, returned from both FloatingIP `Observe` and `Create`. The bundle's `writeConnectionSecretToRef` wires it through the runtime.

### E2E

- [X] T050 [US4] `test/e2e/kuttl/tests/11-floating-ip-bind/` created (Server-driven, 3 steps + asserts). `01-allocate` (FloatingIP `ru-1` + Server, no refs) → both `[Synced,Ready]`. `02-bind` re-applies the full Server with `floatingIPRefs:[{name:e2e-fip}]` (full-spec re-apply so kubectl's 3-way merge adds the field); asserts the FloatingIP's `status.atProvider.observedBoundTo.resourceType == server` (the dynamic `resourceID` and the Server's `boundFloatingIPs` upstream-ID value aren't asserted — kuttl can't match dynamic values; the live canary covers them). `03-unbind` re-applies the Server without `floatingIPRefs` (3-way merge removes it → controller unbinds); asserts both MRs stay `[Synced,Ready]` (kuttl can't assert the ABSENCE of `observedBoundTo`, so the unbind itself is a canary check). No new `$TWE_*` var needed — the FloatingIP has no upstream name field; the Server reuses `$TWE_SERVER_NAME`.

**Checkpoint**: At end of Phase 6, US4 is independently functional. The Server keeps a stable public IPv4 across recreates (DNS pinning, firewall allowlists), with bind/unbind owned solely by the Server controller.

---

## Phase 7: Polish & cross-cutting concerns

**Purpose**: Operator docs, lint, constitution audit, live e2e canary.

- [X] T051 [P] `docs/servers.md` written — operator guide: minimum Server, private-network attachment (`networkRef` + `networkID` import), FloatingIP pinning via the Server's `floatingIPRefs` (Server-consumes-IP, post-reversal), project assignment, the `PaymentRequired`/`no_paid` row, troubleshooting matrix, and a "what's NOT in v0.3" list (incl. **network disks** — answering the operator question raised this session). Location codes use the API values (`ru-1`…), not dashboard labels.
- [X] T052 [P] `README.md` updated — added `Server`/`Network`/`FloatingIP` rows to the Resources table, the network-group forward-compat commitment, a `docs/servers.md` pointer, and the new e2e bundles (08–11 + 10b) in the e2e section.
- [X] T053 [P] `package/crossplane.yaml` description now lists `Server`/`Network`/`FloatingIP` (incl. the Server-consumes-IP note) and points the readme annotation at `docs/servers.md`.
- [X] T054 [P] `make lint` (full module, project config) → **0 issues**. No `//nolint` needed on the new `managed.go` forwarders — the existing `.golangci.yml` exclusion already covers them.
- [X] T055 [P] §III audit (grep) confirms the four-case pattern (Success / NotFound / Transient / Terminal) across every `external` method in `internal/controller/{compute,network}`: compute Server `TestObserve`/`TestCreate`/`TestUpdate`/`TestDelete`; network `Network*` + `FloatingIP*` (8 NotFound / 5 Success / 8 Terminal / 7 Transient markers). No gaps. Bonus: `TestSetReadyCondition` + `TestObserve_NoPaid` cover the new `PaymentRequired` mapping.
- [X] T056 [P] `apis/compute/v1alpha1/doc.go` + `apis/network/v1alpha1/doc.go` corrected to the reversed model (Server owns the `floatingIPRefs` trio + bind/unbind; FloatingIP is pure allocation with no `serverRef`). `apis/v1alpha1/doc.go` unchanged (dual-PC description still accurate). Also fixed the stale FloatingIP section + location codes in `quickstart.md`.
- [X] T057 `CLAUDE.md` plan pointer already targets `specs/003-server-mr-and-network/plan.md` (verified, line 4) — no change needed.
- [~] T058 BLOCKED by the user's "do not run e2e more" directive (2026-06-01). The `quickstart.md` divergences findable statically were fixed anyway: the FloatingIP `serverRef` model → `floatingIPRefs`, and `location: msk-1` (dashboard label, rejected by the CRD enum) → `ru-1`. The fresh-k3d walkthrough itself was not re-run.
- [~] T059 BLOCKED by the same no-e2e directive. A partial live canary DID run earlier this session against `k3d-provider-timeweb-e2e` before being stopped: it verified Server create→`on`→Ready, FloatingIP allocation (real IPs), VPC create (v2) + delete (`/api/v1/vpcs/{id}`→204), and Server delete; it also surfaced two real issues now fixed (kuttl condition-order asserts → `[Ready, Synced]`; `no_paid`→`PaymentRequired`). The full green run is gated on a funded account (the test account hit `no_paid`).

**Checkpoint**: Release-ready. All 4 user stories independently functional + e2e green against live Timeweb.

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1 (Setup)**: T001–T002 — no dependencies; run as the first PR.
- **Phase 2 (Foundational)**: T003–T013 — depends on Phase 1. T003/T004 (groupversion files) ⊂ T005/T006/T007 (types) ⊂ T008/T009 (managed.go) ⊂ T011 (regen). T010 (resolver dimensions) parallel to all of the above. T012 (CEL) requires T011's regen baseline. T013 (scheme registration) requires T011.
- **Phase 3 (US1, MVP)**: T014–T024 — depends on Phase 2. Most controller work is in `internal/controller/compute/`. **MVP target.**
- **Phase 4 (US2)**: T025–T035 — depends on Phase 2. US2 controller work is in `internal/controller/network/`. Some US2 tasks touch `internal/controller/compute/` (T030/T031/T033) and so must follow Phase 3 OR coordinate. Recommended: ship US1 first, then US2 as a follow-up PR.
- **Phase 5 (US3)**: T036–T039 — depends on Phase 4 (specifically T031 location-mismatch check). Tiny phase; can land alongside US2's PR.
- **Phase 6 (US4)**: T040–T050 — depends on Phase 2 (FloatingIP types + scheme reg) + Phase 3 (US1's Server controller, which post-reversal owns bind/unbind — T044/T045/T046 edit `internal/controller/compute/`). T040 (the `BoundFloatingIPs []int64`→`[]string` type fix + regen) precedes T045. Does NOT require US2 or US3.
- **Phase 7 (Polish)**: T051–T059 — depends on whichever user stories you ship in the release.

### User-story dependencies

- US1 → US2: US2 touches `internal/controller/compute/refs.go` (the same file US1 introduces). US1 must merge first.
- US1 → US4: post-reversal, US4 bind/unbind work lives in the **Server** controller (`internal/controller/compute/` — refs.go + a new `floatingip_bind.go`), so US4 now depends directly on US1's compute controller; the FloatingIP allocation half lives in `internal/controller/network/`. Independent of US2's Network controller.
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
