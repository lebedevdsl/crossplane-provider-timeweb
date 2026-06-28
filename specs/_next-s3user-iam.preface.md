# Feature preface: `S3User` — scoped Timeweb object-storage IAM users

> Build spec for a new managed-resource kind. Self-contained; written for an agent
> with the codebase but none of the discovery conversation. All facts below were
> verified live (panel network capture + direct API probes) on 2026-06-28.

## Goal

Add a Crossplane MR kind **`S3User`** to the Timeweb provider that provisions a
**scoped, least-privilege S3 IAM user** for Timeweb object storage, so consumers
(the MariaDB Operator `Backup` CR, app data-bucket access, etc.) get
**per-bucket-scoped credentials** instead of the **account-admin keys** the
existing `S3Bucket` kind currently hands out.

## Why

The existing `S3Bucket` kind (`apis/objectstorage/v1alpha1`,
`internal/controller/s3bucket`) writes the **account super-user's** keys into its
connection Secret (`buildConnection()` → `b.AccessKey`/`b.SecretKey`). Those are
**full access to every bucket in the account** — over-privileged to deliver into
app/DB namespaces. Timeweb storage *does* support scoped IAM users with per-bucket
policies (verified in the panel); we want them as GitOps-able MRs.

## Backend reality (decisive for the design)

Timeweb object storage is **Ceph RGW (RADOS Gateway)** behind nginx — confirmed by
`x-amz-request-id: tx00000…-…-…-ru-1` (RGW's signature) and an S3
`ListAllMyBucketsResult` at the endpoint. RGW exposes:
- the **S3 API** (objects/buckets), and
- an **AWS-IAM-compatible admin API** — a *subset*: **inline user policies**
  (`PutUserPolicy`/`GetUserPolicy`/`DeleteUserPolicy`) work; roles / STS /
  managed-policies are partial — **do not rely on them**.

Endpoints: S3 + IAM both at **`https://panel.s3.twcstorage.ru/`**, region **`ru-1`**.
Bucket ARNs are plain **`arn:aws:s3:::<bucket>`** (no account id). The S3 *data*
endpoint consumers use is **`https://s3.twcstorage.ru`** (verify; distinct host
from the panel/IAM host).

## The two-protocol model (the core fact)

Provisioning a scoped user = **two calls over two protocols**:

### 1. Create the identity — Timeweb proprietary REST (UNDOCUMENTED, `/api/v2/`)
Not in Timeweb's published OpenAPI (only `GET /storages/users` + a `PATCH`
admin-password call are documented). Must be hand-patched into
`docs/openapi-timeweb.json` (the spec is a hand-patched superset; re-apply on regen).
Auth: `Authorization: Bearer <TIMEWEB_CLOUD_TOKEN>`.

- `POST https://api.timeweb.cloud/api/v2/storages/users`  body `{"name":"<name>"}`
  → `200 {"iam_user":{"id":"<uuid>","name":"<name>","access_key":"<AK>","secret_key":"<SK>","status":"active"},"response_id":"..."}`
- `GET  /api/v2/storages/users`        → `{"meta":{"total":N},"iam_users":[{id,name,access_key,secret_key,status},...]}`
- `GET  /api/v2/storages/users/{id}`   → `{"iam_user":{...}}`  ← **identity only, NO policy field**
- rotate secret: `PATCH /api/v1/storages/users/{id}` body `{"secret_key":"<new>"}`
  (documented as "change storage admin password" — **VERIFY it applies to IAM users**)
- delete: `DELETE https://api.timeweb.cloud/api/v2/storages/users/{id}` → removes the user
  (and, since the inline policy lives on the user, its policy with it). Captured/confirmed.

### 2. Attach a scoped policy — standard AWS IAM Query API against RGW
`POST https://panel.s3.twcstorage.ru/` — form-encoded AWS IAM Query, **SigV4-signed
with the account admin S3 keys** (the Bearer token does NOT sign these):
```
Action=PutUserPolicy & Version=2010-05-08 &
UserName=<name> & PolicyName=iam-user-policy & PolicyDocument=<url-encoded JSON>
```
`GetUserPolicy` (Observe) / `DeleteUserPolicy` (Delete) are analogous. Implement with
**aws-sdk-go v2 `iam` client**: `BaseEndpoint=https://panel.s3.twcstorage.ru/`,
region `ru-1`, static creds = account admin keys. RGW speaks the IAM Query protocol
the SDK emits.

## Access-level → policy templates (RENDER from spec; verified verbatim)

All grants use `PolicyName: iam-user-policy`. Substitute `<bucket>`. The controller
renders the doc from `accessLevel` + bucket(s) so it's declarative.

**`read-write`** (Чтение и запись):
```json
{"Version":"2012-10-17","Statement":[
  {"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
  {"Sid":"AllowFullAccessToObjects","Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::<bucket>/*"]},
  {"Sid":"AllowReadBucketMetadata","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::<bucket>"]}
]}
```

**`read`** (Чтение):
```json
{"Version":"2012-10-17","Statement":[
  {"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
  {"Sid":"AllowReadObjectsInBucket","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::<bucket>/*"]},
  {"Sid":"AllowReadBucketMetadata","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::<bucket>"]}
]}
```
(`read` = `read-write` with the object statement's `s3:*` narrowed to `s3:Get*`/`s3:List*`.)

**`admin` / manage** (Администрирование):
```json
{"Version":"2012-10-17","Statement":[
  {"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
  {"Sid":"AllowFullAccessToObjects","Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::<bucket>/*"]},
  {"Sid":"AllowFullBucketAccess","Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::<bucket>"]}
]}
```

**Level pattern** (all share the `IamListAllMyBuckets` / `s3:ListAllMyBuckets` on `*`):
| level | objects `<bucket>/*` | bucket `<bucket>` |
|-------|----------------------|-------------------|
| `read` | `s3:Get*`,`s3:List*` | `s3:Get*`,`s3:List*` |
| `read-write` | `s3:*` | `s3:Get*`,`s3:List*` |
| `admin` | `s3:*` | `s3:*` |

**`none`** (Нет доступа / remove-user-from-bucket): `PutUserPolicy` (**NOT**
`DeleteUserPolicy`) with the bucket statements dropped — only the base statement remains:
```json
{"Version":"2012-10-17","Statement":[
  {"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]}
]}
```

### Single-policy model (decides the controller's render strategy)
There is **one inline policy per user** (`iam-user-policy`) that holds **ALL** of the
user's bucket grants. Granting / changing / removing access to a bucket = re-render the
**whole** document — the base `IamListAllMyBuckets` statement **plus one statement-pair
per granted bucket** (objects `<bucket>/*` + bucket `<bucket>`, at the level from the
table above) — and `PutUserPolicy` it. "No access to bucket X" = re-PUT without X's
pair; a user with zero grants = just the base statement. So the controller **always
renders the full desired policy from `spec.bucketAccess` and PUTs it** — `DeleteUserPolicy`
is only needed on resource Delete (optional; deleting the user removes its policy anyway).
This is why `spec.bucketAccess` is a **list** — one `S3User` → one policy → many buckets.

### Panel IAM call sequence (informs Observe/Update)
Setting an access level fires, in order: **`ListUserPolicies`(UserName) →
`GetUserPolicy` → `PutUserPolicy`**. So map: **Observe** = `ListUserPolicies` +
`GetUserPolicy` (diff against rendered desired); **Create/Update** = `PutUserPolicy`.
All IAM Query, SigV4-signed, at `https://panel.s3.twcstorage.ru/`.

## Proposed `S3User` kind

- Group `objectstorage.m.timeweb.crossplane.io`, `v1alpha1`, namespaced MR (`.m.`
  convention); Setup needs `managed.WithManagementPolicies()`. Sibling reference:
  the existing `S3Bucket` kind.
- `spec.forProvider`:
  - `name` (string, required, **immutable**) — IAM user name.
  - `bucketAccess` ([]{ `bucketRef`|`bucketName`, `accessLevel`: `read`|`read-write`|`admin` }, required)
    — resolve the bucket name, render the policy from `accessLevel`+bucket(s).
  - optional `policyDocument` raw escape hatch; optional `projectID`.
- `status.atProvider`: `id`, `status`, `accessKeyID` (non-secret), rendered-policy hash (drift).
- **Connection secret** (`writeConnectionSecretToRef`): `access_key`, `secret_key`,
  `endpoint` (the S3 *data* host), `bucket`. **This is what consumers reference —
  scoped, not account-admin.**
- Controller: Observe = GET user + `GetUserPolicy`, diff policy; Create = POST user →
  set external-name=uuid → `PutUserPolicy` → write conn secret; Update = `PutUserPolicy`
  (name immutable); Delete = `DeleteUserPolicy` + delete user.

## Admin-key sourcing (RESOLVED — derive from the v1 super-user)

`PutUserPolicy` must be SigV4-signed with the **account super-user's S3 keys** — the
**always-present account admin** (`gorodvkarmane13` / "Администратор S3"; you cannot
remove it from buckets — it's inherently full-access).

**Key distinction:** this super-user lives on the **v1** endpoint
`GET https://api.timeweb.cloud/api/v1/storages/users` — which returns a *single* admin
user (e.g. id `44415`) **WITH both `access_key` and `secret_key`** (verified). That is
**different** from the **v2** `/api/v2/storages/users` endpoint that manages the scoped
IAM users this kind creates. v1 = the admin signer; v2 = the scoped users.

So the controller **derives the IAM-signing keys at runtime from `GET /api/v1/storages/users`
using the existing Bearer token** — no manual key handling, no extra `ProviderConfig`
field needed. (The Bearer token already has full account scope, so materializing the S3
admin keys it implies is no privilege escalation.) Optionally support an explicit
`ProviderConfig` override (referenced Secret `s3_access_key`/`s3_secret_key`) for
operators who'd rather pin a dedicated admin user than read the super-user's secret.

The super-user is **un-deletable** — the panel's only per-user action is
**"Сброс ключей доступа" (reset/regenerate keys)**; there is no delete for it. So it's a
**stable signer**. And because the controller re-reads `GET /api/v1/storages/users` each
reconcile, an admin key-reset is picked up automatically — **do not cache the admin keys
across reconciles**.

**Do NOT rotate the admin key from the provider.** It is a **shared account credential**
(the panel and any other S3 consumer use it); resetting it would break them all. And the
**Bearer token is already the root secret** that derives these keys, so rotating them buys
no security. Flow is dead simple and stateless: **each reconcile → GET admin keys → SigV4
PutUserPolicy/GetUserPolicy → discard.** No rotation, no caching, no extra state.

## Status / remaining build-time checks

RESOLVED (captured live, baked into this spec): create/list/get/**delete** user (v2);
all four access-level policy templates verbatim (`read`/`read-write`/`admin`/`none`);
the single-policy render-and-PUT model + revoke semantics; the Observe call sequence;
**admin-key sourcing** (derive from v1 super-user, no rotation, stateless).

Remaining (confirm during implementation — minor):
1. SigV4 **service name** RGW wants for IAM (`iam` vs `s3`) + region `ru-1`.
2. The consumer **S3 data endpoint** (`s3.twcstorage.ru`) vs the IAM host
   (`panel.s3.twcstorage.ru`) — which goes in the connection Secret's `endpoint`.
3. **Rotation is OUT of scope** for v1 (the `S3User` kind does not rotate user keys; the
   panel's "Сброс ключей доступа" is a manual op if ever needed). Keep it simple.
4. Optional later: let `S3Bucket` stop surfacing account-admin keys once `S3User` exists.

## Conventions (existing repo)

Hand-patched `docs/openapi-timeweb.json` superset (re-apply v2 IAM on regen);
namespaced `.m.` MRs + `WithManagementPolicies()`; connection-secret pattern as in
`s3bucket/external.go`; external-name = the user UUID; standard error classification.

## Support-ticket note

The v2 storage IAM surface (`POST /api/v2/storages/users` + the RGW `PutUserPolicy`
grant at `panel.s3.twcstorage.ru`) is **undocumented** in Timeweb's published spec.
File a ticket asking them to document it (only user-create is proprietary; the grant
is standard AWS IAM).
