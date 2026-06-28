# Research — S3User (feature 012)

Phase 0 research. Most facts were verified **live against the production account on
2026-06-28** during the `/speckit-clarify` session (probe user `cp-probe-policymodel`,
panel user `manual-user-test`, buckets `test-account-creds` / `test-account-creds1`),
using `curl` (Bearer token) for the Timeweb REST and the `aws iam` CLI (= aws-sdk-go-v2
IAM client) for the RGW IAM Query surface. The preface `specs/_next-s3user-iam.preface.md`
supplied the starting hypotheses; the items below record where live probing **confirmed**
or **corrected** them.

## R-1 — Inline-policy model: multiple supported, but panel uses one merged policy (CORRECTED)

**Decision**: Render **one merged inline policy named `iam-user-policy`** per user, holding
the base list statement plus one statement-pair per granted bucket. The `S3User` is the sole
writer of that document.

**Rationale**: The preface claimed "exactly one inline policy per user" as a hard backend
constraint. Live probing **disproved the constraint** but **confirmed the convention**:
`PutUserPolicy` with a second `PolicyName` succeeded and `ListUserPolicies` returned
`["iam-user-policy","per-bucket-beta"]` — so RGW supports N inline policies. **However**, when
the **panel** assigns a user to two buckets at different levels, it persists a single
`iam-user-policy` (verified: `ListUserPolicies == ["iam-user-policy"]`, one doc with base +
two statement-pairs). To **coexist with the panel** (its bucket-view reads/writes that one
doc), our controller must converge to the same single merged policy. Per-bucket named policies
would be invisible to the panel and clobbered on any panel edit.

**Alternatives considered**: (a) Per-bucket named policies `bucket-<name>` — rejected: invisible
to panel, clobbered. (b) Bucket-centric / join-resource API feeding one shared doc from many CRs
— rejected: multi-writer contention on a single document (spec Clarifications). (c) Trusting the
preface's "single policy is a constraint" — rejected: empirically false, and the *why* matters
(it's coexistence, not capability).

## R-2 — Access-level → policy templates (verbatim-confirmed)

**Decision**: Render statement-pairs exactly as the panel emits them; base statement always present.

| Level | objects (`arn:aws:s3:::<b>/*`) | bucket (`arn:aws:s3:::<b>`) | Sids |
|---|---|---|---|
| base (always) | — | `s3:ListAllMyBuckets` on `*` | `IamListAllMyBuckets` |
| `read` | `s3:Get*`,`s3:List*` | `s3:Get*`,`s3:List*` | `AllowReadObjectsInBucket` + `AllowReadBucketMetadata` |
| `read-write` | `s3:*` | `s3:Get*`,`s3:List*` | `AllowFullAccessToObjects` + `AllowReadBucketMetadata` |
| `admin` | `s3:*` | `s3:*` | `AllowFullAccessToObjects` + `AllowFullBucketAccess` |
| `none` (zero grants) | — | — | base only |

**Rationale**: A panel-created user with rw on `test-account-creds` + read on
`test-account-creds1` round-tripped to exactly these statements via `GetUserPolicy`. The
templates match the preface verbatim. **Quirk**: the panel **reuses the Sid
`AllowReadBucketMetadata` across buckets** (duplicate Sids in one doc), and statement order is
not guaranteed — so **Observe MUST diff statements semantically** (set of effect/action/resource
tuples), never by Sid or position.

**Alternatives considered**: Unique per-bucket Sids — harmless but pointless; we match the panel
to minimise needless drift writes, and the semantic diff makes Sid choice immaterial anyway.

## R-3 — Identity CRUD: Timeweb proprietary v2 REST (probe-verified; needs openapi hand-patch)

**Decision**: Create/get/delete the identity via `/api/v2/storages/users`; add these to
`docs/openapi-timeweb.json` and regenerate (`make generate-client`), following the
hand-patched-superset convention (`project_openapi_handpatched_superset`). Derive the admin
signer from the **already-generated** `GetStorageUsers` (v1).

| Endpoint | Method | Status | Used by |
|---|---|---|---|
| `/api/v2/storages/users` | POST `{"name":"<n>"}` | probed ✅ → `{"iam_user":{id,name,access_key,secret_key,status}}` | Create |
| `/api/v2/storages/users` | GET | probed ✅ → `{"meta":{total},"iam_users":[...]}` | adoption guard |
| `/api/v2/storages/users/{id}` | GET | documented (identity only, no policy) | Observe (existence) |
| `/api/v2/storages/users/{id}` | DELETE | probed ✅ → `204` | Delete |
| `/api/v1/storages/users` | GET | **already generated** (`GetStorageUsers` → `BucketUser{AccessKey,SecretKey}`) | derive admin signer |

**Rationale**: v2 create returns AK/SK inline (write straight to the connection Secret). Delete
removes the user and its inline policy with it. The v1 admin super-user (id 44415) carries
`access_key`+`secret_key` and is already exposed by the generated client — **no new code to
derive the signer**. Adding v2 paths to the hand-patched OpenAPI yields typed methods + the
`iam_user` envelope, consistent with how the project already patches in `/configurator/k8s` etc.

**Alternatives considered**: Hand-written request builders on the `timeweb.Client` (the package
embeds `generated.ClientInterface`, so structural methods work) — viable and lighter than a regen,
but diverges from the established openapi-superset pattern; keep as fallback if the regen proves
fiddly with the `-include-tags` filter. Caching admin keys across reconciles — rejected (FR-011:
an out-of-band key reset must be picked up automatically).

## R-4 — AWS-SDK footprint: signer-only wrapper, controller stays AWS-free (DECIDED)

**Decision**: New package `internal/clients/rgwiam` exposing a 4-method domain interface
(`PutUserPolicy`/`GetUserPolicy`/`ListUserPolicies`/`DeleteUserPolicy`). Implement with the
**SigV4 signer only** (`github.com/aws/aws-sdk-go-v2/aws/signer/v4` + `aws.Credentials`),
hand-building the form-encoded IAM Query requests, sending over an HTTP client with the same
conservative (Qrator-aware) settings as `internal/clients/timeweb`, and parsing the small XML
responses with `encoding/xml`. **No AWS imports in `internal/controller/s3user`.**

**Rationale**: `go.mod` has no AWS SDK today, so any option is net-new; the signer-only path pulls
just `aws-sdk-go-v2` (core) + transitive `smithy-go`, vs. the full `service/iam` client which also
drags in `credentials`, `internal/configsources`, `internal/endpoints/v2`. We proved the exact wire
works (the `aws iam` CLI — same SigV4 + IAM Query — succeeded against
`https://panel.s3.twcstorage.ru/`, region `ru-1`, service `iam`). Isolating behind the interface
serves Constitution §III (fake the interface; no live HTTP in tests) and §II (endpoint/region/
signing quirks confined). We do **not** hand-roll SigV4 — the AWS signer is the standard tool
(`feedback_use_standard_ecosystem_tools`).

**Alternatives considered**: (B) Full `aws-sdk-go-v2/service/iam` client with `BaseEndpoint`
override — least of our own code and empirically proven, but heavier deps and more machinery for 4
calls; recorded as the fallback if hand-building the Query/XML proves error-prone. (C) Hand-rolled
SigV4 — rejected (reinvents standard tooling). Either A or B keeps the controller AWS-free.

## R-5 — Error classification across two protocols

**Decision**: Reuse the existing `timeweb` taxonomy. REST calls go through
`timeweb.Classify`/`ClassifyNetworkError` (404 → `ErrNotFound`; 408/409/425/429/5xx →
`*TransientError`; other 4xx → `*APIError`). For the IAM Query path, `rgwiam` maps results into
the **same** taxonomy: HTTP 5xx/429/timeouts/transport → `*TransientError`; 4xx (e.g.
`MalformedPolicyDocument`, `NoSuchEntity` on Get/Delete) → `*APIError` or `ErrNotFound` as
appropriate; success → nil. Reference resolution failures (bucketRef not found / not Ready) map to
`shared`-style conditions (`ParentNotReady`-flavoured), gating `Connect()` like the Router/Nodepool
ref idiom.

**Rationale**: One condition vocabulary for both protocols keeps reconcile behaviour uniform and
re-uses `shared.RecordConditionChange` + the existing reason constants. `NoSuchEntity` on
`GetUserPolicy` during Observe means "policy not yet attached" → drift, not error.

**Alternatives considered**: Surfacing raw AWS smithy error types up to the controller — rejected
(leaks AWS concepts past the `rgwiam` boundary, breaks §III fakeability).

## R-6 — S3Bucket redesign: drop admin keys, add attachedUsers mirror

**Decision**: `s3bucket/external.go` `buildConnection` drops `access_key`/`secret_key` (keeps
`endpoint`/`bucket`/`region`). Add `status.atProvider.attachedUsers []S3BucketAttachedUser`
(`{name, accessLevel}`), populated read-only during `S3Bucket.Observe` by listing v2 users and,
for each, `GetUserPolicy` → decode which statements reference this bucket → derive the level.

**Rationale**: Removing the over-privileged Secret is the security outcome (SC-008). The mirror
(SC-009) gives the bucket-side `kubectl` view without `S3Bucket` ever writing grant state — the
`S3User` remains sole writer. **Cost note**: populating the mirror costs one `GetUserPolicy` per
user per bucket Observe; given the conservative rate limits, the mirror computation should be
bounded/best-effort (never block bucket readiness on the IAM host) and may be capped — log if
truncated rather than silently dropping (alpha; refine in tasks).

**Open question for `/speckit-tasks`**: whether the bucket Observe derives the mirror itself
(extra IAM calls on the bucket path) or whether a lighter approach (e.g. the mirror is populated
opportunistically and tolerated-stale) is acceptable for alpha. Defaulting to best-effort,
non-blocking population.

## R-7 — SigV4 service name / region / endpoint (confirmed)

**Decision**: Sign with service name **`iam`**, region **`ru-1`**, against base endpoint
**`https://panel.s3.twcstorage.ru/`**. The consumer S3 **data** endpoint written to the `S3User`
connection Secret is the bucket's `hostname` from the bucket object (the data host, distinct from
the IAM/panel host).

**Rationale**: The `aws iam` CLI calls succeeded with `--region ru-1 --endpoint-url
https://panel.s3.twcstorage.ru/` (the CLI defaults the IAM signing service to `iam`). The bucket's
`status.atProvider.hostname` is what consumers already use for object access.

**Alternatives considered**: service `s3` for IAM signing — unnecessary; `iam` worked. Putting the
panel/IAM host in the connection Secret — rejected (consumers need the data host).
