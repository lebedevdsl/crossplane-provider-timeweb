# Tasks: S3User — scoped Timeweb object-storage IAM users

**Input**: Design documents from `/specs/012-s3user-iam/`

**Prerequisites**: plan.md, spec.md, research.md (R-1..R-7), data-model.md, contracts/ (s3user-v1alpha1, s3bucket-redesign-v1alpha1, timeweb-s3user-endpoints), quickstart.md

**Tests**: Unit tests are **mandatory** here — Constitution §III requires the four-case pattern
(success / not-found / transient / terminal) for every `external` client, with fakes (no live HTTP).
They are included per story below.

**Organization**: Tasks grouped by user story (US1–US4) for independent implementation/testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4 (maps to spec.md user stories); Setup/Foundational/Polish carry no story label

## Path Conventions

Crossplane provider, single Go module. Real paths: `apis/objectstorage/v1alpha1/`,
`internal/clients/{timeweb,rgwiam}/`, `internal/controller/{s3user,s3bucket}/`, `cmd/provider/`,
`docs/openapi-timeweb.json`, `package/crds/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Dependencies and generated-client surface for the two protocols.

- [X] T001 [P] Add `github.com/aws/aws-sdk-go-v2` (core, for `aws/signer/v4`) via `go get`, floating latest per `project_go_tooling_policy`; tidy `go.mod`/`go.sum`
- [X] T002 Hand-patch `docs/openapi-timeweb.json`: add `POST /api/v2/storages/users` (body `{name}`, resp `iam_user`), `GET /api/v2/storages/users/{user_id}`, `DELETE /api/v2/storages/users/{user_id}` under the `S3-хранилище` tag, with an `IamUser{id,name,access_key,secret_key,status}` schema (per `contracts/timeweb-s3user-endpoints.md`)
- [X] T003 Run `make generate-client`, re-apply superset patches (`project_openapi_handpatched_superset`), and verify the generated v2 user methods + existing v1 `GetStorageUsers` compile

**Checkpoint**: deps present; client exposes v1 admin-user GET + v2 scoped-user CRUD.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: API types, the AWS-isolated `rgwiam` package, and controller scaffolding that ALL stories need.

**⚠️ CRITICAL**: No user-story work begins until this phase is complete.

- [X] T004 [P] Define S3User API types (`S3UserParameters`, `BucketGrant`, `S3UserObservation`, `ResolvedGrant`, `S3UserSpec`, `S3UserStatus`, `S3User`, `S3UserList`) with kubebuilder markers (enum `read;read-write;admin`, `MaxItems=64`, immutable `name`, status print columns) in `apis/objectstorage/v1alpha1/types.go`
- [X] T005 Register `S3UserKind`/`S3UserGroupVersionKind` + `SchemeBuilder.Register(&S3User{}, &S3UserList{})` in `apis/objectstorage/v1alpha1/groupversion_info.go`
- [X] T006 Run `make generate` to produce `zz_generated.deepcopy.go` + `package/crds/` for S3User
- [X] T007 [P] Create `internal/clients/rgwiam/iam.go`: the `Client` interface (`PutUserPolicy`/`GetUserPolicy`/`ListUserPolicies`/`DeleteUserPolicy`) + `PolicyDocument`/`Statement` types + constructor taking endpoint/region/static creds + an HTTP client
- [X] T008 [P] Implement `internal/clients/rgwiam/sigv4.go`: SigV4 signing via `aws/signer/v4` (service `iam`, region `ru-1`, endpoint `https://panel.s3.twcstorage.ru/`), IAM Query form-encoding for the 4 actions, and `encoding/xml` response parsing; conservative timeouts (Qrator-aware — `project_timeweb_qrator_ddos_egress_block`)
- [X] T009 [P] Implement `internal/clients/rgwiam/policy.go`: render merged `iam-user-policy` from grants (base `IamListAllMyBuckets` + per-bucket statement-pairs), **semantic** statement-set diff (Sid-/order-insensitive — R-2), and a stable `PolicyHash`
- [X] T010 Generate the counterfeiter fake of `rgwiam.Client` in `internal/clients/rgwiam/fake.go`
- [X] T011 Implement admin-signer derivation (GET `/api/v1/storages/users` → first user's `access_key`/`secret_key`, **never cached** — FR-011) as a helper used by Connect, in `internal/controller/s3user/connector.go`
- [X] T012 Scaffold `internal/controller/s3user/controller.go`: `Setup(mgr, log, pollInterval)` with `managed.NewReconciler` + `WithManagementPolicies()` + controller rate limiter (s3bucket pattern)
- [X] T013 Implement `internal/controller/s3user/connector.go` `Connect`: `shared.ResolveToken`, build `timeweb` + `rgwiam` clients, derive admin keys (T011), resolve each `bucketRef` via `client.Get` requiring `UpstreamID`+`Ready=True` (Router/Nodepool idiom; hold resolved names on the `external`, not written back to spec)
- [X] T014 Register `s3user.Setup(mgr, log, pollInterval)` in `cmd/provider/main.go` after the S3Bucket registration

**Checkpoint**: types registered, `rgwiam` isolated + fakeable, controller wired — stories can begin.

---

## Phase 3: User Story 1 - Scoped single read-write credential (Priority: P1) 🎯 MVP

**Goal**: One `S3User` → identity + read-write policy on one bucket → scoped connection Secret.

**Independent Test**: Apply an `S3User` with one `read-write` `bucketRef`; it goes Synced+Ready, the
Secret holds scoped keys + data endpoint + bucket, those keys read/write the bucket and are denied elsewhere.

### Tests for User Story 1 ⚠️

- [X] T015 [P] [US1] Four-case unit tests for `Observe` + `Create` (success / not-found / transient / terminal) with fake `timeweb` + fake `rgwiam` in `internal/controller/s3user/external_test.go`
- [X] T016 [P] [US1] Unit tests for read-write template render + `PolicyHash` stability + semantic up-to-date in `internal/clients/rgwiam/policy_test.go`

### Implementation for User Story 1

- [X] T017 [US1] Implement read-write statement-pair + base statement rendering in `internal/clients/rgwiam/policy.go`
- [X] T018 [US1] Implement `Observe` (GET v2 user existence/status; `ListUserPolicies`+`GetUserPolicy`; semantic diff vs rendered desired; populate status + connection details) in `internal/controller/s3user/external.go`
- [X] T019 [US1] Implement `Create` (POST v2 user → external-name = `iam_user.id`; `PutUserPolicy` merged doc; write connection Secret `access_key`/`secret_key`/`endpoint`(bucket data host)/`bucket`) in `internal/controller/s3user/external.go`
- [X] T020 [US1] Map conditions/errors (`ParentNotReady`, `APIError`, transient requeue) via `shared.RecordConditionChange` + reason constants in `internal/controller/s3user/external.go`
- [X] T021 [P] [US1] Add example manifest `examples/objectstorage/s3user.yaml` (single read-write grant + `writeConnectionSecretToRef`)

**Checkpoint**: MVP — a scoped single-bucket credential is fully functional and independently testable.

---

## Phase 4: User Story 2 - One user, several buckets at mixed levels (Priority: P2)

**Goal**: `bucketAccess[]` with multiple grants (incl. `bucketName` fallback) → one merged policy.

**Independent Test**: Apply an `S3User` with rw on A and read on B; the credential does exactly the
granted actions on each and the resource is Synced+Ready.

### Tests for User Story 2 ⚠️

- [X] T022 [P] [US2] Unit tests for `read` + `admin` templates and N-bucket merge (ordering-insensitive) in `internal/clients/rgwiam/policy_test.go`
- [X] T023 [P] [US2] Unit test for duplicate-resolved-bucket → `Synced=False InvalidConfiguration` in `internal/controller/s3user/external_test.go`

### Implementation for User Story 2

- [X] T024 [US2] Extend policy render with `read` (`AllowReadObjectsInBucket`) and `admin` (`AllowFullBucketAccess`) levels + multi-grant merge in `internal/clients/rgwiam/policy.go`
- [X] T025 [US2] Resolve multiple grants — `bucketRef` (require Ready+UpstreamID) and `bucketName` fallback — in `internal/controller/s3user/connector.go`
- [X] T026 [US2] Detect duplicate resolved bucket → terminal `Synced=False` (`InvalidConfiguration`, FR-016) in `internal/controller/s3user/external.go`
- [X] T027 [US2] Populate `status.atProvider.resolvedBuckets` from the applied grant set in `internal/controller/s3user/external.go`

**Checkpoint**: US1 + US2 both work; one identity spans multiple buckets at mixed levels.

---

## Phase 5: User Story 4 - S3Bucket stops emitting admin keys + attachedUsers mirror (Priority: P2)

**Goal**: `S3Bucket` connection Secret drops admin keys (breaking, alpha); add read-only
`status.attachedUsers` bucket-side view.

**Independent Test**: Reconcile an `S3Bucket`; its Secret has `endpoint`/`bucket`/`region` and no
`access_key`/`secret_key`. Attach an `S3User`; the bucket's `status.attachedUsers` reflects it.

### Tests for User Story 4 ⚠️

- [X] T028 [P] [US4] Update `internal/controller/s3bucket/external_test.go`: assert connection Secret omits `access_key`/`secret_key`; assert `attachedUsers` derivation from a faked policy listing

### Implementation for User Story 4

- [X] T029 [P] [US4] Add `S3BucketObservation.AttachedUsers []S3BucketAttachedUser{Name,AccessLevel}` + `ATTACHED` print column in `apis/objectstorage/v1alpha1/types.go`
- [X] T030 [US4] Drop `access_key`/`secret_key` from `buildConnection` (keep `endpoint`/`bucket`/`region`) in `internal/controller/s3bucket/external.go`
- [X] T031 [US4] Populate `status.attachedUsers` best-effort, non-blocking in `S3Bucket.Observe` (list v2 users → `GetUserPolicy` → derive level for this bucket; log if truncated under rate limits — no silent caps) in `internal/controller/s3bucket/external.go`
- [X] T032 [US4] Run `make generate` (deepcopy + CRDs for the new S3Bucket status/printcolumn) and verify a clean tree
- [X] T033 [P] [US4] Document the breaking change + migration (S3Bucket no longer emits creds → create an `S3User`) in `README.md`

**Checkpoint**: no kind emits account-admin keys; bucket-side attachment view available.

---

## Phase 6: User Story 3 - Day-2 lifecycle: change access, then delete (Priority: P3)

**Goal**: In-place level changes (credential unchanged), immutable `name`, clean delete.

**Independent Test**: Change a grant's level → same Secret, new access; delete the `S3User` → upstream
identity gone and old credential no longer authorizes.

### Tests for User Story 3 ⚠️

- [X] T034 [P] [US3] Unit tests for `Update` (in-place level change, Secret unchanged), immutable-`name` rejection, and `Delete` (DeleteUserPolicy + delete user, 404→success) in `internal/controller/s3user/external_test.go`

### Implementation for User Story 3

- [X] T035 [US3] Implement `Update` (reject `name` via `shared.FirstImmutableDiff`/`RejectImmutableChange`; re-render + `PutUserPolicy` full doc; keep connection keys stable — SC-004) in `internal/controller/s3user/external.go`
- [X] T036 [US3] Implement `Delete` (best-effort `DeleteUserPolicy` then DELETE v2 user; `ErrNotFound`→success; set `Deleting`) in `internal/controller/s3user/external.go`
- [X] T037 [US3] Implement Create-path adoption guard (external-name empty but a user with `spec.name` exists upstream → follow `project_adoption_reattaches_failed_orphan` rules; do not re-adopt a failed identity) in `internal/controller/s3user/external.go`

**Checkpoint**: full lifecycle correct; all four stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T038 [P] Run `golangci-lint`/`gosec`/`govulncheck` via `go run` (no host install) and `crossplane beta validate` on the regenerated CRDs
- [X] T039 Verify `make generate` leaves the working tree clean (Constitution §I/CI gate) — deepcopy + CRD YAML committed with `apis/` changes
- [X] T040 [P] Add an opt-in kuttl/k3d e2e bundle for `S3User` (assert both `Synced=True` and `Ready=True` via `wait --for=condition`; pin explicit context) under `test/e2e/`
- [ ] T041 [P] File a Timeweb support ticket documenting `POST /api/v2/storages/users` + the RGW `PutUserPolicy` grant (`feedback_capture_upstream_quirks`)
- [ ] T042 Live re-observation pass (twc-staging / inside Timeweb): apply quickstart scenarios — scoped keys read/write the granted bucket and are denied elsewhere; `S3Bucket` Secret carries no admin keys; `attachedUsers` reflects the grant (`feedback_verify_by_reobservation`)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)**: no deps — start immediately (T002→T003 ordered; T001 parallel).
- **Foundational (P2)**: depends on Setup. T004→T005→T006 ordered; T007/T008/T009 parallel; T010 after T007; T011/T012/T013 after the types+client exist; T014 after T012. **Blocks all stories.**
- **US1 (P3 phase)**: depends on Foundational. **MVP.**
- **US2 (P4)**, **US4 (P5)**: depend on Foundational; independent of each other (US4 touches only `s3bucket` + types). Both P2.
- **US3 (P6)**: depends on Foundational; builds on US1's `external.go` (Update/Delete extend Create/Observe). P3.
- **Polish (P7)**: after the desired stories.

### User Story Dependencies

- US1 — no dependency on other stories (MVP).
- US2 — extends US1's policy render + connector; independently testable.
- US4 — independent (S3Bucket + shared types); can proceed in parallel with US2.
- US3 — extends US1's external client; independently testable.

### Within Each User Story

- Tests first (must fail), then policy/render, then Observe/Create/Update/Delete, then conditions/wiring.

### Parallel Opportunities

- Setup: T001 ∥ (T002→T003).
- Foundational: T007 ∥ T008 ∥ T009 (then T010); types (T004) ∥ rgwiam package.
- US2 and US4 can be developed in parallel by different developers after Foundational.
- All `[P]` test tasks within a story run together.

---

## Parallel Example: Foundational rgwiam package

```bash
Task: "Create rgwiam.Client interface + types in internal/clients/rgwiam/iam.go"      # T007
Task: "Implement SigV4 + IAM Query + XML parse in internal/clients/rgwiam/sigv4.go"   # T008
Task: "Implement policy render + semantic diff in internal/clients/rgwiam/policy.go"  # T009
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → **STOP & VALIDATE** scoped single-bucket
credential (apply, confirm Synced+Ready, scoped keys work, denied elsewhere) → demo.

### Incremental Delivery

US1 (MVP) → US2 (multi-bucket) / US4 (S3Bucket redesign, parallel) → US3 (day-2 lifecycle) → Polish.
Each story is an independently testable increment; the breaking S3Bucket change (US4) ships with its
migration note (T033).

---

## Notes

- All AWS-SDK usage stays inside `internal/clients/rgwiam`; the controller imports only the
  `rgwiam.Client` interface (fakeable — Constitution §III).
- Single merged `iam-user-policy`; Observe diffs **semantically** (panel reuses Sids, R-2).
- Admin signer re-derived each reconcile, never cached (FR-011); never rotated (FR-012).
- Regenerate deepcopy + CRD YAML in the same PR as `apis/` changes (Constitution §I).
- Commit after each task or logical group; the user batches commits manually.
