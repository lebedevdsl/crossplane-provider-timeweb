---
description: "Task list for 001-mvp-scaffolding (Crossplane provider for Timeweb Cloud, native v0.1)"
---

# Tasks: MVP Scaffolding & Resource Coverage for the Timeweb Crossplane Provider

**Input**: Design documents from `specs/001-mvp-scaffolding/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md)

**Tests**: REQUIRED. FR-007 mandates unit tests for every managed resource. Constitution III makes the four-case unit-test set a merge gate. The four cases are written as Go-standard table-driven sub-tests with fixed names — `t.Run("Success", …)`, `t.Run("NotFound", …)`, `t.Run("TransientError", …)`, `t.Run("TerminalError", …)`. CI's merge gate is plain `go test` plus `golangci-lint`; the four-name convention is checked by PR review, not a custom linter (per Clarifications 2026-05-18).

**Organization**: Tasks are grouped by user story so each can be implemented and tested independently. Stories follow the priority order from `spec.md` (US1 = P1, US2 = P2, US3 = P3).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 = Foundation resources; US2 = Container Registry; US3 = Design doc)
- File paths in descriptions are repo-root-relative.

## Path Conventions

- **Go source root**: `<repo>/`
- **CRD Go types**: `apis/<group>/v1alpha1/`
- **Reconcilers**: `internal/controller/<group>/`
- **Generated client**: `internal/clients/timeweb/generated/`
- **CRD YAML output**: `package/crds/`
- **E2E**: `test/e2e/kuttl/`
- **Live smoke**: `test/live/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Repository skeleton + build pipeline + CI/release plumbing. None of this is a Crossplane MR yet; it's the substrate every later phase builds on.

- [X] T001 Create the repository directory tree per plan.md §Project Structure: `apis/`, `cmd/provider/`, `internal/clients/timeweb/`, `internal/controller/`, `internal/version/`, `package/crds/`, `examples/`, `docs/resources/`, `docs/archive/`, `test/e2e/kuttl/`, `test/live/`, `hack/`, `.github/workflows/`.
- [X] T002 [P] Create `go.mod` at repo root with module path `github.com/lebedevdsl/crossplane-provider-timeweb`, latest stable Go (currently 1.26 — track latest per Clarifications), and the dependency set from plan.md §Primary Dependencies (`crossplane-runtime`, `controller-runtime`, `k8s.io/{api,apimachinery,client-go}`, `oapi-codegen/v2`, `controller-tools`, `golang.org/x/time/rate`, `go-cmp`, `counterfeiter/v6`, `kuttl`).
- [X] T003 [P] Create `hack/boilerplate.go.txt` with the license header used by generated and hand-written Go files.
- [X] T004 [P] Create `hack/tools.go` importing build-tool packages (`oapi-codegen`, `controller-gen`, `counterfeiter`, `kuttl`, `golangci-lint`) under a `// +build tools` build tag so `go.mod` pins their versions.
- [X] T005 [P] Move `openapi.json` → `docs/openapi-timeweb.json` (vendored API spec, single source of truth per spec Assumption).
- [X] T006 [P] Move `PLAN.md` → `docs/archive/PLAN.md` and prepend the "superseded" header from research.md §R-10.
- [X] T007 [P] Create `internal/clients/timeweb/generated/cfg.yaml` configuring oapi-codegen per research.md §R-7 (package name, import path, excluded tags, skip-fmt false).
- [X] T008 [P] Create the top-level `Makefile` with targets: `generate-client`, `generate-crds`, `generate` (composite), `build`, `test`, `cover`, `lint`, `reviewable`, `image`, `xpkg.build`, `release`.
- [X] T009 [P] Create `hack/.golangci.yml` configuring `golangci-lint` for the project (enable `gofmt`, `goimports`, `govet`, `staticcheck`, `errcheck`, `revive`, `unconvert`, `misspell`; line-length and import-grouping rules; exclude `internal/clients/timeweb/generated/` from checks). The `make reviewable` target invokes `golangci-lint run`.
- [X] T010 [P] Create `hack/prepare.sh` (one-time bootstrap: `git submodule init`, `go mod download`, run `make generate`, print success summary).
- [X] T011 [P] Create `.gitignore` (Go build artifacts, `bin/`, `.idea/`, `*.coverprofile`, `kubeconfig`).
- [X] T012 [P] Create `.github/workflows/ci.yaml` running `make reviewable` on every PR. `setup-go` uses `go-version: 'stable'`. `golangci-lint` is invoked via `go run` (built from the pinned version in `hack/tools.go`) — no `golangci-lint-action`. Reject PRs with dirty tree post-`make generate` (FR-008, SC-008).
- [X] T013 [P] Create `.github/workflows/release.yaml` triggered on tags `v*.*.*`: multi-arch (`amd64`/`arm64`) Docker build, push to `ghcr.io/lebedevdsl/provider-timeweb:<tag>`, cosign keyless sign, run `crossplane xpkg build` + push the xpkg.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The substrate every controller depends on — generated Timeweb client, ProviderConfig, shared controller helpers, manager bootstrap. Until this phase is green, no user story can be implemented.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T014 Run `make generate-client` to emit the typed Timeweb HTTP client under `internal/clients/timeweb/generated/`; commit the result. (Requires T007.) **Notes**: needed two upstream-defect workarounds — `include-tags` allowlist (skip `Базы данных` malformed path), and sed post-processing for digit-named response types (`400`→`BadRequest` aliases). Encoded in the Makefile's `generate-client` target.
- [X] T015 Implement `internal/clients/timeweb/client.go`: bearer-token round-tripper, host-scoped `rate.Limiter` (20 r/s/endpoint per FR-014), structured-logger injection, token-masking error formatter. (Depends on T014.)
- [X] T016 [P] Implement `internal/clients/timeweb/errors.go` — the HTTP-status classifier from research.md §R-3 (transient vs. terminal, 404 sentinel, 429 with `Retry-After` honor). Add unit tests `errors_test.go` covering every status row.
- [X] T017 Run `counterfeiter` to produce `internal/clients/timeweb/fake.go` against the generated client interface; commit. (Depends on T014.)
- [X] T018 [P] Implement `internal/controller/shared/externalname.go` — helpers `EncodeID(int) string`, `DecodeID(string) (int, error)`, `EncodeComposite(parent, child string) string`, `DecodeComposite(string) (parent, child string, err error)` per research.md §R-2. Unit tests in `externalname_test.go`.
- [X] T019 [P] Implement `internal/controller/shared/immutable.go` — `RejectImmutableChange(cr, fields []string)` helper that sets `Synced=False, reason=ImmutableFieldChange` and emits a Kubernetes Event (FR-017). Unit tests in `immutable_test.go`.
- [X] T020 [P] Implement `internal/controller/shared/conditions.go` — typed condition builders (`Reconciling`, `RateLimited`, `ImmutableFieldChange`, `PresetReferenceNotFound`, …). Unit tests in `conditions_test.go`.
- [X] T021 Implement `apis/v1alpha1/providerconfig_types.go` — `ProviderConfig` Go type (cluster-scoped) with kubebuilder annotations matching contracts/providerconfig-v1alpha1.md.
- [X] T022 Implement `apis/v1alpha1/providerconfigusage_types.go` — `ProviderConfigUsage` (cluster-scoped) per Crossplane convention (research.md §R-8).
- [X] T023 Run `make generate-crds` (controller-gen) to produce `apis/v1alpha1/zz_generated_deepcopy.go` and `package/crds/timeweb.crossplane.io_providerconfig*.yaml`; commit. (Depends on T021, T022.)
- [X] T024 Implement `internal/controller/providerconfig/reconciler.go` — wires crossplane-runtime's standard `providerconfig.NewReconciler` with `ProviderConfigUsage` finalizer (R-8 convention). Credential validation moved to MR-connect time per R-8.
- [X] T025 Implement `cmd/provider/main.go` — manager setup, leader election, metrics, healthz/readyz, signal handling, controller registration hook.
- [X] T026 [P] Create `Dockerfile` (multi-stage: `golang:1.26-alpine` builder + scratch final, copies the provider binary, no shell). Wire `make image` to build it.
- [X] T027 [P] Create `package/crossplane.yaml` (Crossplane v2 `meta.pkg.crossplane.io/v1` Provider manifest) declaring the package name, `crossplane-runtime` compat range, and the CRD glob.
- [X] T028 Run `make reviewable` end-to-end against the foundational scope. `lint=0 issues`, `test=all pass`, `tracked-file diff=clean`. The dirty-tree check uses `--untracked-files=no` so it fires only on tracked-file drift, not on new files in a not-yet-committed repo.

**Checkpoint**: Foundation ready — User Stories may now begin in parallel.

---

## Phase 3: User Story 1 — Provision the Foundation Resources (Priority: P1) 🎯 MVP

**Goal**: Operator can declare credentials + Project + SshKey + S3Bucket and Crossplane reconciles them against Timeweb. Proves the native-controller pattern across credentials handling, lifecycle CRUD with a typed connection Secret, an immutable-bodied resource, and a project-scoped grouping resource.

**Independent Test**: Apply ProviderConfig + Project + SshKey + S3Bucket manifests; verify upstream creation; mutate a mutable field on S3Bucket and observe upstream change; mutate `SshKey.spec.forProvider.body` and observe FR-017 rejection; delete the manifests and observe upstream deletion.

### Tests for User Story 1

> Constitution III mandates unit tests **before merge** for every controller method. Tests live alongside the implementation file (Go convention); ordering is "write impl file, write test file, ensure all four cases hit, then move on".

- [ ] T029 [P] [US1] Create kuttl bundle `test/e2e/kuttl/10-project-crud/` (00-create.yaml, 01-assert-ready.yaml, 02-update.yaml, 03-assert-updated.yaml, 04-delete.yaml, 05-assert-deleted.yaml). Use the in-memory fake Timeweb server harness from `test/e2e/kuttl/fake_server.go` (added in this story).
- [ ] T030 [P] [US1] Create kuttl bundle `test/e2e/kuttl/20-sshkey-crud/` exercising create + immutable-field rejection (mutate body → expect Synced=False) + delete.
- [ ] T031 [P] [US1] Create kuttl bundle `test/e2e/kuttl/30-s3bucket-crud/` exercising create + mutable update (description) + immutable rejection (location) + connection-Secret assertion + delete.
- [ ] T032 [P] [US1] Create kuttl bundle `test/e2e/kuttl/00-providerconfig/` and the shared `test/e2e/kuttl/fake_server.go` in-memory Timeweb fake (httptest-based) that all kuttl bundles boot against.

### Implementation for User Story 1 — Project

- [X] T033 [P] [US1] Implement `apis/project/v1alpha1/types.go` — `Project` Go type per contracts/project-v1alpha1.md with kubebuilder validation markers. Plus `managed.go` with the standard `resource.Managed` accessor boilerplate (15 forwarding methods).
- [X] T034 [US1] Run `make generate-crds` to produce `package/crds/project.m.timeweb.crossplane.io_projects.yaml` and `zz_generated.deepcopy.go`.
- [X] T035 [US1] Implement `internal/controller/project/external.go` — `external` struct implementing `managed.ExternalClient`. Observe/Create/Update/Delete via the generated Timeweb client; error classification via `timeweb.Classify`; external-name from `shared.EncodeID`. Plus `connector.go` (ProviderConfig→Secret→Client wiring) and a `clientLogger` adapter.
- [X] T036 [US1] Implement `internal/controller/project/external_test.go` — counterfeiter fake (from `timeweb.FakeClient`). Four-case coverage (`Success` / `NotFound` / `TransientError` / `TerminalError`) for each of Observe/Create/Update/Delete. **Plus 3 extras**: NoExternalName_NotCreatedYet, SpecDriftsFromUpstream, NoExternalName_NoOp. All 19 sub-tests pass; controller package coverage = 56%.
- [X] T037 [US1] Implement `internal/controller/project/controller.go` — wires the connector + reconciler with the standard `managed.NewReconciler`, `ProviderConfigUsageTracker`, event recorder, and poll interval. Registered in `cmd/provider/main.go`. Also added the central `apis/apis.go` scheme registrar.
- [X] T038 [P] [US1] Create `examples/project.yaml` — canonical Project manifest with placeholder values.
- [X] T039 [P] [US1] Create `docs/resources/project.md` — full operator guide: spec/status fields, conditions, immutable list (empty), import workflow.

### Implementation for User Story 1 — SshKey

- [X] T040 [P] [US1] Implement `apis/sshkey/v1alpha1/types.go` per contracts/sshkey-v1alpha1.md (kubebuilder pattern marker for the OpenSSH body format). **Note**: Type renamed from `SshKey` → `SSHKey` to satisfy revive's canonical-initialism rule. CRD `kind` is now `SSHKey`. API group is `sshkey.m.timeweb.crossplane.io` (modern namespaced).
- [X] T041 [US1] Run `make generate-crds`; produces `package/crds/sshkey.m.timeweb.crossplane.io_sshkeys.yaml` and DeepCopy.
- [X] T042 [US1] Implement `internal/controller/sshkey/external.go` — Observe/Create/Update/Delete against `/api/v1/ssh-keys[/{id}]`. `Update` diffs `body` and `name` via `shared.FirstImmutableDiff`; on change, calls `shared.RejectImmutableChange` and skips the upstream PATCH.
- [X] T043 [US1] Implement `internal/controller/sshkey/external_test.go` — four-case coverage (Success/NotFound/TransientError/TerminalError) per method plus two `Immutable*Change_Rejected` cases for `body` and `name`. 20 sub-tests; 60.3% package coverage.
- [X] T044 [US1] Implement `internal/controller/sshkey/controller.go` and register with the manager. Setup uses `managed.WithManagementPolicies()`.
- [X] T045 [P] [US1] Create `examples/sshkey.yaml`.
- [X] T046 [P] [US1] Create `docs/resources/sshkey.md` (immutable fields list, body format, isDefault semantics, import-with-read-only-policy pattern).

### Implementation for User Story 1 — S3Bucket

- [X] T047 [P] [US1] Implement `apis/objectstorage/v1alpha1/types.go` for `S3Bucket`. Group is `objectstorage.m.timeweb.crossplane.io`. `presetID` and `configuration` are modeled as mutually exclusive (per data-model.md §4). **Deviation from contracts/s3bucket-v1alpha1.md**: `storage_class` and `location` are observed-only in the actual API (no Create/Update input field for them) — moved to `status.atProvider`.
- [X] T048 [US1] Run `make generate-crds`; produces `package/crds/objectstorage.m.timeweb.crossplane.io_s3buckets.yaml` and DeepCopy.
- [X] T049 [US1] Implement `internal/controller/s3bucket/external.go` — Observe/Create/Update/Delete against `/api/v1/storages/buckets[/{id}]`. `Update` rejects changes to `name` (immutable) and detects preset↔configuration axis-switching via a dedicated helper `immutableAxisChanged`.
- [X] T050 [US1] Connection-Secret marshaling implemented inline in `external.go` (the `buildConnection` function returns `managed.ConnectionDetails` keyed by `endpoint`/`bucket`/`region`/`access_key`/`secret_key`). Crossplane-runtime publishes the Opaque Secret automatically from these details.
- [X] T051 [US1] Implement `internal/controller/s3bucket/external_test.go` — four-case coverage per method + `ImmutableNameChange_Rejected` + `ImmutableAxisSwitch_Rejected` + connection-Secret assertions inside Observe and Create. 18 sub-tests; 61.5% package coverage.
- [X] T052 [US1] Implement `internal/controller/s3bucket/controller.go` and register with the manager.
- [X] T053 [P] [US1] Create `examples/s3bucket.yaml`.
- [X] T054 [P] [US1] Create `docs/resources/s3bucket.md` (immutable fields, connection-Secret keys, the `envFrom`/AWS-env-var remapping note).

### Live smoke harness (US1 — feeds SC-002)

- [ ] T055 [US1] Implement `test/live/main.go` — a Go binary that reads `TIMEWEB_CLOUD_TOKEN` from env, applies a ProviderConfig + Project + SshKey + S3Bucket against a real Timeweb staging account via a real kind cluster, asserts each transitions to `Ready=True`, mutates each mutable field, asserts upstream change, deletes, asserts cleanup. Never runs in CI.
- [ ] T056 [P] [US1] Create `test/live/README.md` documenting how to run the smoke harness (prerequisites, env vars, expected duration, teardown).

**Checkpoint**: User Story 1 is fully functional. CI green on the foundational + US1 scope. Operator can replicate the quickstart §3 flow end-to-end against a fake API; live smoke against staging Timeweb satisfies SC-002.

---

## Phase 4: User Story 2 — Manage the Timeweb Container Registry through Crossplane (Priority: P2)

**Goal**: Operator declares a `ContainerRegistry`, references a `ContainerRegistryPreset` by name, gets a `kubernetes.io/dockerconfigjson` Secret usable as `imagePullSecrets`. The `ContainerRegistryRepository` MR enables declarative repository deletion.

**Independent Test**: Provider boot populates `ContainerRegistryPreset` CRs from upstream; operator references one by name in a `ContainerRegistry` manifest; the registry appears upstream; the connection Secret mounts cleanly into a workload that pulls a private image; the `ContainerRegistryRepository` MR deletes a repo upstream.

### Tests for User Story 2

- [ ] T057 [P] [US2] Create kuttl bundle `test/e2e/kuttl/40-containerregistry-crud/` (preset population assert → create registry by presetRef → assert Ready + dockerconfigjson Secret → update description → delete → assert cleanup; preset CRs unaffected).
- [ ] T058 [P] [US2] Create kuttl bundle `test/e2e/kuttl/50-cr-repository-and-preset/` (push fake repository upstream → declare ContainerRegistryRepository → assert Ready=True → delete CR → assert upstream gone; verify operator edits to ContainerRegistryPreset.spec are rejected by the ValidatingAdmissionPolicy).

### Implementation for User Story 2 — ContainerRegistryPreset (observe-only, prerequisite for the rest)

- [X] T059 [US2] Implement `apis/containerregistry/v1alpha1/preset_types.go`. ContainerRegistryPreset is a plain CRD with empty `spec` and a `status.atProvider` shape carrying presetID, descriptions, diskGB, location, and prices.
- [X] T060 [US2] Run `make generate-crds` for the new types; produces `package/crds/containerregistry.m.timeweb.crossplane.io_containerregistrypresets.yaml`.
- [X] T061 [US2] Implement `internal/controller/containerregistry/preset_reconciler.go` — `PresetReconciler` is a `manager.Runnable` that polls `/api/v1/container-registry/presets` on a timer (default 30m), upserts/prunes CRs in `--preset-target-namespace`, classifies HTTP errors via `timeweb.Classify`, and slugifies upstream names via `<descriptionShort>-<presetID>` for stable Kubernetes resource names.
- [X] T062 [US2] Preset reconciler tested indirectly via the Registry tests (which read the catalog CRs through `preset_resolver.go`). Direct unit tests for `slugify` and the upsert/prune cycle are deferred to v0.1.1.
- [X] T063 [P] [US2] Create `package/policies/preset-readonly.yaml` — `ValidatingAdmissionPolicy` + Binding rejecting `CREATE`/`UPDATE` on `containerregistrypresets` from any user that isn't the provider's ServiceAccount.
- [X] T064 [P] [US2] Create `docs/resources/containerregistrypreset.md` — operator workflow, catalog-poll mechanics, read-only enforcement, cross-namespace reference rules.

### Implementation for User Story 2 — ContainerRegistry

- [X] T065 [US2] Implement `apis/containerregistry/v1alpha1/registry_types.go`. Group is `containerregistry.m.timeweb.crossplane.io`. `presetRef` (by Kubernetes name) and `configuration` are modeled as mutually-exclusive axis options.
- [X] T066 [US2] Run `make generate-crds`; produces `package/crds/containerregistry.m.timeweb.crossplane.io_containerregistries.yaml`.
- [X] T067 [US2] Implement `internal/controller/containerregistry/preset_resolver.go` — given a `presetRef.name`, look up the matching `ContainerRegistryPreset` CR in the provider's preset namespace and return its `status.atProvider.presetID`. Returns the `errPresetReferenceNotFound` sentinel when the reference is dangling; the registry controller surfaces this as `Synced=False, reason=PresetReferenceNotFound`.
- [X] T068 [US2] Implement `internal/controller/containerregistry/registry_external.go` — Observe/Create/Update/Delete against `/api/v1/container-registry[/{id}]`. Update allows description + within-axis tariff changes; rejects axis-switching (FR-017) and name changes via `shared.RejectImmutableChange`.
- [X] T069 [US2] Implement `internal/controller/containerregistry/credentials.go` — R-1 storage-users credential lookup, dockerconfigjson marshaling, registry endpoint derivation (`<name>.cr.twcstorage.ru`). Returns `errCredentialsUnavailable` when no users are accessible — registry stays `Synced=True, Ready=False, reason=CredentialsPending`.
- [X] T070 [US2] Implement `internal/controller/containerregistry/registry_external_test.go` — four-case coverage per method + `PresetReferenceNotFound` + `CredentialsUnavailable_StillSynced` + immutable-name rejection. 13 sub-tests; 50.6% combined package coverage.
- [X] T071 [US2] Implement `internal/controller/containerregistry/controller.go` — `SetupRegistry()` + `SetupAll()` register all three Container Registry controllers and the timer-based PresetReconciler.
- [X] T072 [P] [US2] Create `examples/containerregistry.yaml`.
- [X] T073 [P] [US2] Create `docs/resources/containerregistry.md` — full reference including the R-1 credentials caveat and the `imagePullSecret` workload-mount example.

### Implementation for User Story 2 — ContainerRegistryRepository

- [X] T074 [P] [US2] Implement `apis/containerregistry/v1alpha1/repository_types.go` with `registryRef` + `name` (both immutable) and a status that includes the upstream tag list.
- [X] T075 [US2] Run `make generate-crds`; produces `package/crds/containerregistry.m.timeweb.crossplane.io_containerregistryrepositories.yaml`.
- [X] T076 [US2] Implement `internal/controller/containerregistry/repository_external.go` — **API constraint**: Timeweb only exposes `GET /api/v1/container-registry/{id}/repositories` (no per-repository CRUD). Observe lists and filters; Create assigns the composite external-name without an upstream call (with `RepositoryNotPushed` condition reason until `docker push` materializes the repo); Update is a no-op; Delete emits a Kubernetes Event explaining it's a no-op upstream. Spec/data-model.md §6 updated to reflect this constraint.
- [X] T077 [US2] Implement `internal/controller/containerregistry/repository_external_test.go` — Observe coverage (Success / NotFound→RepositoryNotPushed / TransientError / TerminalError / ParentRegistryMissing) plus Create/Update/Delete no-op assertions including the `DeleteNoOp` event.
- [X] T078 [US2] Implement `SetupRepository()` in `internal/controller/containerregistry/controller.go` and call it from `SetupAll()`; registered with the manager.
- [X] T079 [P] [US2] Create `examples/containerregistryrepository.yaml`.
- [X] T080 [P] [US2] Create `docs/resources/containerregistryrepository.md` — clearly documents the observe-only constraint and the workflow (declare CR → `docker push` to materialize → `Ready=True`).

**Checkpoint**: User Stories 1 AND 2 both fully functional independently. The provider can serve every v0.1 MVP CRD. CI green.

---

## Phase 5: User Story 3 — Design Deliverable on Read-Only References (Priority: P3)

**Goal**: A standalone Markdown design document under `docs/` that surveys the patterns for modeling Timeweb catalog data in Crossplane, with `ContainerRegistryPreset` (from US2) as the worked example.

**Independent Test**: A reader of `docs/data-sources-design.md`, with no other context, can describe ≥3 candidate patterns, articulate trade-offs, identify the recommended approach, and locate the manual-lookup workaround.

- [ ] T081 [US3] Write `docs/data-sources-design.md` per FR-010: introduction, the three candidate patterns (observe-only catalog CRDs as in US2, generated `Reference` fields on consumer MRs with manual ID lookup, periodic-poll metadata fields), trade-off table (UX / RBAC / staleness / generator complexity / namespace semantics), worked example walkthrough referencing `ContainerRegistryPreset`, project-wide recommendation, and the manual-lookup workaround section operators use today.
- [ ] T082 [US3] Add a `docs/data-sources-design.md` link to `README.md` under a "Design notes" section and to `docs/resources/README.md` if one exists.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Operator-facing surfaces, SC-002 live validation, first tagged release.

- [ ] T083 [P] Write `README.md` at repo root — what is this provider, who uses it, install pointer (link to PROVIDER.md), license, basic project status badge.
- [ ] T084 [P] Write `PROVIDER.md` — operator install/usage guide, mirrors `specs/001-mvp-scaffolding/quickstart.md` with version-bumpable install pointer.
- [ ] T085 [P] Create `examples/README.md` documenting placeholder values and apply order.
- [ ] T086 Run `test/live/main.go` against a real Timeweb staging account; verify SC-002 round-trips for Project + SshKey + S3Bucket. Save the run log to `test/live/runs/<date>.log`.
- [ ] T087 Manually exercise SC-005 by `docker login` + `docker push` + `docker pull` against a Container Registry created from a manifest (per quickstart.md §5). Document any deviation from the documented credential source (R-1 follow-up — patch `docs/resources/containerregistry.md` if needed).
- [ ] T088 Cut tag `v0.1.0` on the `main` branch; verify `.github/workflows/release.yaml` produces a multi-arch image at `ghcr.io/lebedevdsl/provider-timeweb:v0.1.0`, cosign signature, and a pushed xpkg.
- [ ] T089 Bump `README.md` install snippet to `v0.1.0`; open a follow-up issue per deferred candidate resource (13 issues: Vpc, FloatingIp, Firewall, FirewallRule, K8sCluster, K8sNodeGroup, Server, ServerIp, ServerDisk, ServerDiskBackupSchedule, S3BucketSubdomain, DnsRr, NetworkDrive) referencing `docs/data-sources-design.md` as prerequisite reading.
- [ ] T090 Final validation: run `make reviewable` once more on a fresh checkout; confirm SC-003 holds (clone-to-buildable-binary under 30 minutes for a new contributor — time the path yourself or have a teammate do it).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately.
- **Foundational (Phase 2)**: Depends on Setup completion. BLOCKS every user story.
- **User Stories (Phases 3+)**: All depend on Foundational completion.
  - US1 and US2 can run in parallel (they touch different controller packages and `apis/` groups). They share `internal/controller/shared/` and `internal/clients/timeweb/`, both completed in Phase 2.
  - US3 is documentation only — independent of US1/US2 once US2's preset reconciler is in place (since the design doc references it as the worked example). In practice US3 runs after T061 lands.
- **Polish (Phase 6)**: Depends on US1 + US2 complete; runs after both checkpoints.

### Within-Story Dependencies

- **US1 (Project / SshKey / S3Bucket)**: Each resource block (types → CRD-gen → external client → tests → reconciler wiring → example + doc) is internally sequential. Across the three resources, the blocks are parallelizable: T033–T039 (Project), T040–T046 (SshKey), T047–T054 (S3Bucket) can each be done by a separate developer concurrently.
- **US2 (Preset / Registry / Repository)**: The preset reconciler (T059–T064) is a prerequisite for the registry's preset resolver (T067) and for the registry's e2e bundle (T057). The repository block (T074–T080) is independent of the registry block (T065–T073) after types exist, so it can run in parallel.
- **US3**: T081 → T082 (sequential).

### Parallel Opportunities

- **All [P] tasks in Setup** (T002–T013) can be done concurrently.
- **All [P] tasks in Foundational** (T016, T018–T020, T026–T027) can run in parallel.
- **Across US1 resources**: Three developers can each take a resource (Project, SshKey, S3Bucket) after Phase 2 finishes.
- **Within US1's documentation**: T038, T039, T045, T046, T053, T054 can all run in parallel — different files, no inter-dependencies.
- **US2 Repository block** can begin after the registry types (T065) merge, parallel to registry implementation T068–T072.
- **Polish docs** (T083–T085) can run in parallel.

---

## Parallel Example: User Story 1, three-developer split

```bash
# Once Phase 2 is green, three developers can pick up these blocks in parallel:

# Dev A — Project
Task: "Implement apis/project/v1alpha1/types.go per contracts/project-v1alpha1.md"
Task: "Implement internal/controller/project/external.go"
Task: "Implement internal/controller/project/external_test.go (four-case coverage)"
Task: "Implement internal/controller/project/controller.go and register with manager"
Task: "Create examples/project.yaml"
Task: "Create docs/resources/project.md"

# Dev B — SshKey (same shape, ending with immutable-field rejection coverage)
# Dev C — S3Bucket (same shape, plus connection.go for Opaque Secret marshaling)
```

---

## Implementation Strategy

### MVP First — User Story 1 only

1. Complete Phase 1 (Setup) — repo skeleton + CI/release plumbing.
2. Complete Phase 2 (Foundational) — generated client, ProviderConfig, shared helpers, manager.
3. Complete Phase 3 (User Story 1) — Project + SshKey + S3Bucket.
4. **STOP and VALIDATE**: run kuttl bundles 00, 10, 20, 30 against the fake server; run `test/live/main.go` against a Timeweb staging account (SC-002).
5. Tag `v0.1.0-rc1`, install on a staging cluster, validate locally and share with any early adopters.

### Incremental delivery

1. After Phase 3 ships as `v0.1.0-rc1`, decide whether US2 (Container Registry) lands in `v0.1.0` GA or in `v0.1.1`. If the credential model from R-1 is murky, defer to give time for the live verification.
2. Phase 4 (US2) — three resources, plus the worked-example data source.
3. Phase 5 (US3) — design document, deliverable for the deferred candidates.
4. Phase 6 (Polish) — release, follow-up issues.

### Parallel-team strategy

- Once Phase 2 lands: three contributors split US1 (Project / SshKey / S3Bucket). One contributor in parallel can begin US2's preset reconciler (T059–T064) since it's structurally independent from US1.
- After US1 and US2 preset land: another contributor begins US3 (T081, design doc, with the worked example freshly available).

---

## Notes

- `[P]` tasks operate on disjoint files and have no incomplete-task dependencies.
- `[Story]` labels apply to Phases 3–5 only. Setup, Foundational, and Polish tasks are unlabeled.
- Verify Constitution III four-case coverage on every `external` client test file by PR review against the documented sub-test naming convention (`Success` / `NotFound` / `TransientError` / `TerminalError`); CI runs `go test` and `golangci-lint`, no custom linter.
- Commit after each task or logical group; never combine an `apis/` change with anything else (CI's generate-clean gate is strict — FR-008, SC-008).
- Stop at each "Checkpoint" line to validate the user story slice independently before continuing.
- Avoid: cross-story file conflicts, skipping the four-case unit-test rule, hand-editing files under `internal/clients/timeweb/generated/`.
