# Implementation Plan: S3User — scoped Timeweb object-storage IAM users

**Branch**: `012-s3user-iam` | **Date**: 2026-06-28 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/012-s3user-iam/spec.md`

## Summary

Add a namespaced managed-resource kind **`S3User`** (`objectstorage.m.timeweb.crossplane.io/v1alpha1`)
that provisions a scoped, least-privilege object-storage IAM user for Timeweb's Ceph-RGW
storage, replacing the account-admin keys the existing `S3Bucket` connection Secret hands
out (US1–US3). Provisioning is **two operations over two protocols**: (1) create/delete the
identity via Timeweb's proprietary `/api/v2/storages/users` REST, and (2) attach a scoped
policy via the **AWS IAM Query API** (`PutUserPolicy`/`GetUserPolicy`/`ListUserPolicies`/
`DeleteUserPolicy`) SigV4-signed against `https://panel.s3.twcstorage.ru/` (region `ru-1`)
with the account super-user's S3 keys (derived at runtime from `GET /api/v1/storages/users`,
never cached). All of a user's grants render to **one merged inline policy named
`iam-user-policy`** — the convention the Timeweb panel reads/writes (verified live
2026-06-28; RGW *supports* multiple inline policies, but we use the single merged policy to
coexist with the panel). The same change **redesigns `S3Bucket`** (US4): drop
`access_key`/`secret_key` from its connection Secret (breaking, alpha-acceptable) and add a
read-only `status.attachedUsers` mirror for the bucket-side view.

## Technical Context

**Language/Version**: Go (latest stable, tracked by `go.mod` per `project_go_tooling_policy`);
Crossplane v2 namespaced MR model (`.m.` groups).

**Primary Dependencies**: `crossplane-runtime/v2`, `sigs.k8s.io/controller-runtime`,
`internal/clients/timeweb` (oapi-codegen client from `docs/openapi-timeweb.json` + hand-written
wrapper), the catalog `resolver` primitive (not needed here — `S3User` has no preset sizing),
`counterfeiter` fakes. **New**: `github.com/aws/aws-sdk-go-v2` (core, for the `aws/signer/v4`
SigV4 signer) + transitive `smithy-go` — confined to a new `internal/clients/rgwiam` package.

**Storage**: N/A (external state is Timeweb's API; MR status mirrors it). Stateless reconciler —
admin signer keys are re-derived each reconcile, never cached.

**Testing**: `go test` four-case pattern per Constitution §III (success / not-found / transient /
terminal) with a fake `timeweb` client and a fake `rgwiam.Client`; kuttl/k3d e2e optional and
gated on an explicit context.

**Target Platform**: Linux (Crossplane provider pod, amd64, distroless/static:nonroot); k3d/Timeweb
for e2e.

**Project Type**: Crossplane provider (single Go module).

**Performance/Constraints**: Per-host conservative rate limiting (Qrator DDoS protection —
`project_timeweb_qrator_ddos_egress_block`); the IAM host `panel.s3.twcstorage.ru` gets the same
conservative treatment. Observe issues 2–3 read calls (GET user + `ListUserPolicies` +
`GetUserPolicy`); steady state must not thrash the policy.

**Scale/Scope**: 1 new kind (`S3User`), 1 modified kind (`S3Bucket`); new package
`internal/clients/rgwiam`; hand-patch `docs/openapi-timeweb.json` for v2 `storages/users`
CRUD; touch points: `apis/objectstorage/v1alpha1/{types.go,groupversion_info.go}`,
`internal/controller/s3user/*` (new), `internal/controller/s3bucket/external.go` (drop admin
keys + populate `attachedUsers`), `cmd/provider/main.go` (register). Regenerate CRDs + DeepCopy
in the same PR.

## Open clarifications (resolved)

- **Grant model**: User-centric `S3User.bucketAccess[]` with typed `bucketRef` (or `bucketName`);
  the `S3User` is the sole writer of the user's single merged policy (2026-06-28, spec Clarifications).
- **Policy storage**: one merged `iam-user-policy` per user, matching the panel; Observe diffs
  statements semantically (panel reuses Sids) (2026-06-28, live-verified — see research R-1/R-2).
- **S3Bucket redesign**: stop emitting `access_key`/`secret_key`; keep `endpoint`/`bucket`/`region`;
  add read-only `status.attachedUsers` mirror (2026-06-28, option A).
- **AWS-SDK footprint**: signer-only (`aws/signer/v4`) behind `internal/clients/rgwiam`; controller
  imports no AWS packages (2026-06-28 — see research R-4).
- **Duplicate bucket / raw policy escape hatch**: reject duplicates as invalid; defer raw escape
  hatch (spec FR-016 / Out of Scope).

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.0.0.*

- **§I CRD Contract Stability — PASS.** `S3User` is a new `v1alpha1` CRD (additive). `S3Bucket`'s
  connection-Secret change drops two keys — a breaking change for consumers, but permitted: the kind
  is `v1alpha1` (pre-`v1beta1`, no additive-only guarantee yet), and it is the feature's core security
  purpose. `S3Bucket`'s CRD *schema* change (`status.attachedUsers`) is additive. DeepCopy +
  CRD YAML regenerated and committed in the same PR (`make generate`).
- **§II Idempotent, Side-Effect-Aware Reconciliation — PASS.** `Observe` is read-only (GET user +
  `ListUserPolicies` + `GetUserPolicy`). `Create` is idempotent via external-name = user UUID plus a
  by-name adoption guard; `PutUserPolicy` is a full-document render (re-invocation converges, no
  incremental drift). `Delete` tolerates already-gone (404 → success). Admin signer keys re-derived
  each reconcile, never cached. AWS-SDK errors classified into the existing `TransientError`/`APIError`
  taxonomy (research R-5).
- **§III Controller Test Discipline — PASS.** Four-case unit tests for each of Observe/Create/Update/
  Delete using a fake `timeweb` client + fake `rgwiam.Client`; no live HTTP.
- **Provider Constraints — PASS.** No new credential surface: the account token (already from
  `ProviderConfig.spec.credentials`) derives the admin S3 keys at runtime; keys never logged, never in
  spec/status. The scoped user's keys go only into its own connection Secret (standard pattern).
- **Observability — PASS.** Standard `Synced`/`Ready` conditions + structured logger; reuse
  `shared.RecordConditionChange` and the existing reason vocabulary.

No violations → Complexity Tracking intentionally empty.

## Project Structure

### Documentation (this feature)

```text
specs/012-s3user-iam/
├── plan.md              # This file
├── research.md          # Phase 0 — R-1..R-6 (policy model, endpoints, AWS-SDK choice, errors)
├── data-model.md        # Phase 1 — S3User + S3Bucket deltas, policy render rules
├── quickstart.md        # Phase 1 — operator walkthrough + troubleshooting matrix
├── contracts/           # Phase 1
│   ├── s3user-v1alpha1.md            # CRD contract + conditions table
│   ├── s3bucket-redesign-v1alpha1.md # S3Bucket connection-secret + attachedUsers deltas
│   └── timeweb-s3user-endpoints.md   # v2 REST + IAM Query inventory, bodies, quirks
└── tasks.md             # Phase 2 (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
apis/objectstorage/v1alpha1/
├── types.go                 # + S3UserParameters/Observation/Spec/Status/S3User/S3UserList;
│                            #   + S3BucketObservation.AttachedUsers ([]S3BucketAttachedUser)
├── groupversion_info.go     # + S3UserKind/GVK; SchemeBuilder.Register(&S3User{}, &S3UserList{})
└── zz_generated.deepcopy.go # regenerated

internal/clients/rgwiam/      # NEW — the ONLY place aws-sdk imports live
├── iam.go                   # Client interface + impl: Put/Get/List/DeleteUserPolicy
├── sigv4.go                 # SigV4 signing (aws/signer/v4) + IAM Query form build + XML parse
├── policy.go                # render desired iam-user-policy from grants; semantic diff
└── fake.go                  # counterfeiter fake of rgwiam.Client

internal/clients/timeweb/
├── storages_users_v2.go     # hand-written or generated v2 user CRUD (see research R-3)
└── generated/zz_generated_client.go  # regenerated if openapi hand-patched

internal/controller/s3user/   # NEW
├── controller.go            # Setup(mgr, log, pollInterval) + WithManagementPolicies()
├── connector.go             # Connect: ResolveToken, build timeweb client + rgwiam client,
│                            #   derive admin keys, resolve bucketRefs
├── external.go              # Observe/Create/Update/Delete
└── external_test.go         # four-case unit tests

internal/controller/s3bucket/
└── external.go              # buildConnection drops access_key/secret_key; populate attachedUsers

cmd/provider/main.go          # + s3user.Setup(...)
docs/openapi-timeweb.json     # + /api/v2/storages/users {POST}, /{id} {GET,DELETE} (hand-patch)
```

**Structure Decision**: Mirror the existing `s3bucket` controller layout (controller/connector/
external split) for `s3user`. Isolate all AWS-SDK usage behind `internal/clients/rgwiam` so the
controller depends only on a 4-method domain interface (testable with a fake, per §III). Keep the
Timeweb identity CRUD inside `internal/clients/timeweb` (REST), and the AWS IAM Query grant calls
inside `rgwiam` (AWS protocol) — the two-protocol split is reflected in the package boundary.

## Complexity Tracking

No constitution violations — section intentionally empty.
