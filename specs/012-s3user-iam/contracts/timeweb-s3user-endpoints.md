# Contract — Timeweb S3User endpoints (feature 012)

Two protocols against two hosts. **Source of truth**: `docs/openapi-timeweb.json` for the v2 REST
(hand-patch required — `project_openapi_handpatched_superset`); the IAM Query surface is standard
AWS protocol and is **not** added to OpenAPI. All verified live 2026-06-28.

## A. Identity — Timeweb proprietary REST (`https://api.timeweb.cloud`, Bearer token)

| Endpoint | Method | Status | Used by |
|---|---|---|---|
| `/api/v2/storages/users` | POST | probed ✅ | Create (identity) |
| `/api/v2/storages/users` | GET | probed ✅ | adoption guard / list |
| `/api/v2/storages/users/{id}` | GET | documented (identity only) | Observe (existence/status) |
| `/api/v2/storages/users/{id}` | DELETE | probed ✅ → `204` | Delete |
| `/api/v1/storages/users` | GET | **already generated** (`GetStorageUsers`) | derive admin signer |

### Create body (verified)

```json
{ "name": "manual-user-test" }
```

### Create response (verified)

```json
{ "iam_user": { "id": "255ea0f9-…", "name": "…", "access_key": "<AK>",
                "secret_key": "<SK>", "status": "active" }, "response_id": "…" }
```

### v1 admin signer (verified)

`GET /api/v1/storages/users` → `{"users":[{ "id":44415, "access_key":"<AK>", "secret_key":"<SK>" }]}`
— the always-present, un-deletable account super-user; re-read every reconcile, never cached
(FR-011/FR-012). This is the SigV4 signer for section B.

## B. Grants — AWS IAM Query API (`https://panel.s3.twcstorage.ru/`, SigV4)

Form-encoded POST, SigV4-signed with the **v1 admin keys**; service `iam`, region `ru-1` (R-7).

| Action | Used by | Notes |
|---|---|---|
| `PutUserPolicy` | Create / Update | full-document render of `iam-user-policy` |
| `GetUserPolicy` | Observe | returns the policy doc; `NoSuchEntity` ⇒ not attached |
| `ListUserPolicies` | Observe | expect `["iam-user-policy"]` |
| `DeleteUserPolicy` | Delete | best-effort; deleting the user also drops the policy |

### PutUserPolicy request shape (verified via aws-cli equivalent)

```
POST https://panel.s3.twcstorage.ru/
Action=PutUserPolicy&Version=2010-05-08&UserName=<name>
&PolicyName=iam-user-policy&PolicyDocument=<url-encoded JSON>
```

### Merged policy document (verified round-trip; rw on bucket A + read on bucket B)

```json
{"Version":"2012-10-17","Statement":[
  {"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
  {"Sid":"AllowFullAccessToObjects","Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::A/*"]},
  {"Sid":"AllowReadBucketMetadata","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::A"]},
  {"Sid":"AllowReadObjectsInBucket","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::B/*"]},
  {"Sid":"AllowReadBucketMetadata","Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::B"]}
]}
```

## Behavioral contract (quirks)

1. **Multiple inline policies supported, but DON'T use them.** RGW accepts N named inline policies
   per user, but the panel reads/writes only `iam-user-policy`. Use the single merged policy to
   coexist; per-bucket named policies would be invisible to the panel and clobbered (R-1).
2. **Duplicate Sids + unordered statements.** The panel reuses `AllowReadBucketMetadata` across
   buckets and does not guarantee statement order. Observe MUST diff **semantically** (set of
   effect/action/resource), never by Sid or position (R-2).
3. **No-access ≠ delete-policy.** Zero grants ⇒ re-PUT the base-only document, not
   `DeleteUserPolicy`. `DeleteUserPolicy` is only for resource Delete (and deleting the user removes
   the policy anyway).
4. **Admin key reset is picked up automatically** because the signer is re-derived each reconcile;
   never cache it (FR-011). The provider MUST NOT reset the admin key (shared credential — FR-012).
5. **Qrator-aware pacing.** Treat the IAM host with the same conservative rate limiting as
   `api.timeweb.cloud` (`project_timeweb_qrator_ddos_egress_block`); a SYN timeout is transient, not
   terminal.
6. **Undocumented v2 surface** — file a Timeweb support ticket to document
   `POST /api/v2/storages/users` + the RGW grant (only user-create is proprietary; the grant is
   standard AWS IAM) (`feedback_capture_upstream_quirks`).
