# Data Model — S3User (feature 012)

Group `objectstorage.m.timeweb.crossplane.io`, version `v1alpha1`. One **new** kind (`S3User`)
and **modifications** to the existing `S3Bucket` kind. Field semantics that drive controller
behaviour are noted inline; the operator-facing contract is in `contracts/`.

## 1. S3User (NEW kind — `objectstorage.m.timeweb.crossplane.io/v1alpha1`)

### Spec / Parameters (Go struct sketch)

```go
// S3UserParameters are the operator-settable fields.
type S3UserParameters struct {
    // Name is the IAM user name. Immutable.
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=250
    Name string `json:"name"`

    // BucketAccess lists the buckets this user may reach and at what level.
    // Each bucket may appear at most once (duplicates rejected — FR-016).
    // An empty list yields an identity with only the base list permission.
    // +optional
    // +kubebuilder:validation:MaxItems=64
    BucketAccess []BucketGrant `json:"bucketAccess,omitempty"`

    // ProjectID optionally assigns the user to a Timeweb project.
    // +optional
    ProjectID *int `json:"projectID,omitempty"`
}

// BucketGrant binds one bucket to one access level.
type BucketGrant struct {
    // BucketRef references an S3Bucket in the same namespace. Mutually
    // exclusive with BucketName; exactly one MUST be set.
    // +optional
    BucketRef *xpv2.Reference `json:"bucketRef,omitempty"`

    // BucketName names the bucket directly (for buckets not managed here).
    // +optional
    BucketName *string `json:"bucketName,omitempty"`

    // AccessLevel is the grant level for this bucket.
    // +kubebuilder:validation:Enum=read;read-write;admin
    AccessLevel string `json:"accessLevel"`
}
```

Validation rules (CEL bounded by `MaxItems=64` per `project_cel_cost_budget_crd`):
- `bucketAccess[*]`: exactly one of `bucketRef` / `bucketName` set (CEL on the item).
- duplicate resolved bucket → rejected (controller-side after resolution; CRD cannot see
  resolved names, so the controller emits a terminal `Synced=False` reason on duplicate — FR-016).
- `name` immutable (enforced in `Update` via `shared.FirstImmutableDiff`, not CEL — matches S3Bucket).

### Observation / Status (mirror)

```go
type S3UserObservation struct {
    // ID is the upstream IAM user UUID (also the external-name).
    // +optional
    ID *string `json:"id,omitempty"`
    // Status is the upstream user status (e.g. "active").
    // +optional
    Status *string `json:"status,omitempty"`
    // AccessKeyID is the user's non-secret access key id (the secret key
    // lives only in the connection Secret, never in status).
    // +optional
    AccessKeyID *string `json:"accessKeyID,omitempty"`
    // PolicyHash is a stable hash of the rendered desired policy, for drift.
    // +optional
    PolicyHash *string `json:"policyHash,omitempty"`
    // ResolvedBuckets mirrors the resolved (bucket name, level) grants applied.
    // +optional
    ResolvedBuckets []ResolvedGrant `json:"resolvedBuckets,omitempty"`
}

type ResolvedGrant struct {
    BucketName  string `json:"bucketName"`
    AccessLevel string `json:"accessLevel"`
}
```

### Connection Secret (`writeConnectionSecretToRef`)

| Key | Value | Source |
|---|---|---|
| `access_key` | scoped user access key | v2 create response `iam_user.access_key` |
| `secret_key` | scoped user secret key | v2 create response `iam_user.secret_key` |
| `endpoint` | S3 **data** host | referenced bucket's `status.atProvider.hostname` (R-7) |
| `bucket` | primary bucket name | first grant's resolved bucket name |

### Lifecycle

**external-name** = the upstream IAM user UUID.

- **Connect**: `shared.ResolveToken` → build `timeweb` client; build `rgwiam` client; derive admin
  signer keys from generated `GetStorageUsers` (v1); resolve each `bucketRef` via `client.Get` →
  require `UpstreamID` + `Ready=True` (the Router/Nodepool idiom — `refs.go`), holding resolved
  names on the `external` (not written back to spec).
- **Observe**: if no external-name → not exists. Else GET `/api/v2/storages/users/{id}` (existence +
  status); `ListUserPolicies` + `GetUserPolicy iam-user-policy`; render desired doc from
  `bucketAccess`; `ResourceUpToDate` = **semantic** statement-set equality (R-2). Populate status +
  connection Secret. `NoSuchEntity` on GetUserPolicy → policy missing → not up-to-date (drift).
- **Create**: POST `/api/v2/storages/users {"name":…}` → set external-name = `iam_user.id`; render
  desired policy; `PutUserPolicy iam-user-policy`; write connection Secret from the create response.
  Adoption guard: if external-name empty but a user with `spec.name` already exists upstream (GET
  list), follow project adoption rules (`project_adoption_reattaches_failed_orphan`) — do not blindly
  re-adopt a failed identity.
- **Update**: `name` change → reject (immutable). Otherwise re-render desired policy and
  `PutUserPolicy` (full-document; covers add/change/remove grant, including down to base-only). Secret
  unchanged (keys are stable across level changes — SC-004).
- **Delete**: `DeleteUserPolicy iam-user-policy` (best-effort) then DELETE
  `/api/v2/storages/users/{id}`; 404 → success. Deleting the user removes its policy regardless.

### Conditions

| Situation | Synced | Ready | Reason |
|---|---|---|---|
| Created, policy converged | True | True | Available |
| Awaiting `bucketRef` (not found / not Ready) | False | False | `ParentNotReady` |
| Duplicate resolved bucket in grants | False | — | `InvalidConfiguration` (FR-016) |
| Upstream 4xx (e.g. malformed policy) | False | — | `APIError` |
| Transient (5xx/429/timeout/Qrator) | — | — | requeue (no condition flap) |
| Deleting | False | False | Deleting |

## 2. S3Bucket (MODIFIED — feature 012)

### Connection Secret change (breaking)

`buildConnection` drops `access_key` + `secret_key`; retains `endpoint`, `bucket`, `region`.
Credentials now come only from `S3User`. (FR-017 / SC-008.)

### Status mirror (additive)

```go
// added to S3BucketObservation
type S3BucketObservation struct {
    // ... existing fields ...
    // AttachedUsers is a read-only mirror of users granted access to this
    // bucket and their level. Observational only — S3Bucket never writes grants.
    // +optional
    AttachedUsers []S3BucketAttachedUser `json:"attachedUsers,omitempty"`
}

type S3BucketAttachedUser struct {
    Name        string `json:"name"`
    AccessLevel string `json:"accessLevel"` // read | read-write | admin
}
```

Populated best-effort, non-blocking, during `S3Bucket.Observe` (R-6): list v2 users → for each,
`GetUserPolicy` → which statements reference `arn:aws:s3:::<thisBucket>` → derive level. MUST NOT
block bucket readiness on the IAM host; if truncated/skipped under rate limits, log it (no silent
caps). Print column candidate: `ATTACHED` = `len(attachedUsers)`.

## Resolver dimensions

None. `S3User` has no preset/configurator sizing — the catalog `resolver` is not involved.

## Touched existing kinds

- **S3Bucket** (feature 012): connection-Secret drops admin keys (breaking, alpha); `+status.atProvider.attachedUsers`. Rationale: remove over-privileged credential surface (SC-008) + bucket-side view (SC-009).

## Relationships

```text
ProviderConfig ──token──► S3User.Connect ──derives──► account admin S3 keys (v1, runtime, uncached)
                                   │
S3Bucket ◄──bucketRef── S3User.bucketAccess[]      (typed ref; require Ready+UpstreamID)
   ▲                               │
   │                               ├─ POST /api/v2/storages/users        (identity, REST)
   │                               └─ PutUserPolicy iam-user-policy       (grant, IAM Query/SigV4)
   └──(status.attachedUsers mirror, read-only, derived from GetUserPolicy)
```
