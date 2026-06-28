# Feature Specification: S3User — scoped Timeweb object-storage IAM users

**Feature Branch**: `012-s3user-iam`

**Created**: 2026-06-28

**Status**: Draft

**Input**: User description: "specs/_next-s3user-iam.preface.md — add an `S3User` managed-resource kind that provisions scoped, least-privilege object-storage credentials per bucket instead of the account-admin keys the existing `S3Bucket` kind hands out."

## Clarifications

### Session 2026-06-28

- Q: If the same bucket is listed twice in an `S3User`'s grant list (possibly at conflicting levels)? → A: Reject as an invalid configuration (one unambiguous level per bucket).
- Q: Ship the optional raw policy-document escape hatch in v1? → A: No — defer; v1 ships only the `read`/`read-write`/`admin` templates.
- Q: Is the preface's "each user has exactly ONE inline policy" a hard backend constraint? → A: No — verified live: RGW supports N independent named inline policies per user, but the Timeweb panel persists all of a user's grants as a single merged policy named `iam-user-policy`. To coexist with the panel, the controller MUST converge to that same single merged `iam-user-policy` (per-bucket named policies would be invisible to the panel's bucket-view and clobbered on any panel edit).
- Q: Where is a user↔bucket grant declared ("attaching users to buckets")? → A: User-centric on `S3User.bucketAccess[]`, each entry a typed `bucketRef` (cross-reference to an `S3Bucket`, like the rest of the provider) or a `bucketName` fallback. One `S3User` is the sole writer of one user's single policy — no multi-writer contention. Bucket-centric / join-resource APIs were rejected because they would feed one shared policy document from many resources.
- Q: Provide a bucket-side view of attachments? → A: Yes, in scope of this spec (alpha): a read-only `S3Bucket.status.attachedUsers` mirror listing the users/levels attached to that bucket. It is observational only — never a second writer of grant state.
- Q: What does "redesign `S3Bucket`" do about the account-admin keys it currently emits in its connection Secret? → A: Stop surfacing them entirely (option A). Drop `access_key`/`secret_key` from `S3Bucket`'s connection Secret; keep only non-secret metadata (`endpoint`, `bucket`, `region`). Credentials now come only from `S3User`. Breaking change, accepted because the kind is `v1alpha1` and removing the over-privileged Secret is the feature's core purpose; migration path is "create an `S3User`."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Provision a scoped, single-bucket credential (Priority: P1)

A platform operator declares a single managed resource that names an object-storage
user and grants it read-write access to exactly one bucket. The provider creates the
identity, attaches a policy limited to that bucket, and writes the resulting access
key / secret key into a Kubernetes Secret that an application (or the MariaDB
Operator's `Backup` CR) can mount. The credential can read and write objects in that
one bucket and can see nothing else of substance in the account.

**Why this priority**: This is the entire reason the feature exists — replacing
over-privileged account-admin keys with per-bucket-scoped credentials. A single
read-write grant on one bucket is the smallest slice that delivers that value and is
the dominant real-world case (backup target, app data bucket).

**Independent Test**: Apply an `S3User` with one `read-write` bucket grant; confirm
the resource becomes Synced and Ready, a connection Secret is written with a working
access key / secret key, those credentials can put and get objects in the named
bucket, and they are rejected when attempting the same against a different bucket in
the account.

**Acceptance Scenarios**:

1. **Given** an existing bucket and a valid provider configuration, **When** the
   operator applies an `S3User` granting `read-write` on that bucket, **Then** the
   resource reaches Synced=True and Ready=True and a connection Secret containing the
   scoped access key, secret key, endpoint, and bucket name is created.
2. **Given** a freshly provisioned `S3User` credential, **When** an application uses
   it to write and then read an object in the granted bucket, **Then** both operations
   succeed.
3. **Given** the same credential, **When** it is used against a different bucket the
   user was not granted, **Then** the request is denied.
4. **Given** an `S3User` that already exists and has not changed, **When** the
   provider reconciles it again, **Then** no spurious update is performed (the observed
   policy matches the desired policy) and the resource stays Ready.

---

### User Story 2 - Grant one user access to several buckets at different levels (Priority: P2)

A platform operator needs one identity to reach more than one bucket — for example
read-write on a data bucket and read-only on a shared assets bucket. They list
multiple bucket grants on a single `S3User`, each with its own access level, and the
provider renders one policy covering all of them.

**Why this priority**: Many consumers legitimately touch more than one bucket, and the
panel persists all of a user's grants as a single merged policy. Declaring the grants
as a list on one resource matches that convention (one writer → one policy) and avoids
forcing operators to create one identity per bucket. It builds directly on US1 but is
not required for the minimal value slice.

**Independent Test**: Apply an `S3User` listing two buckets at different access levels;
confirm the credential can perform exactly the granted actions on each bucket (and no
more), and that the resource is Synced and Ready.

**Acceptance Scenarios**:

1. **Given** two existing buckets, **When** the operator applies an `S3User` granting
   `read-write` on bucket A and `read` on bucket B, **Then** the credential can write
   to A, read from A and B, and cannot write to B.
2. **Given** that multi-bucket `S3User`, **When** the operator removes bucket B from
   the grant list and re-applies, **Then** the credential retains its A access and can
   no longer read B.
3. **Given** that multi-bucket `S3User`, **When** the operator raises bucket B's level
   from `read` to `admin` and re-applies, **Then** the credential gains the higher
   access on B and the resource returns to Synced=True.

---

### User Story 3 - Day-2 lifecycle: change access, then delete (Priority: P3)

A platform operator changes the access level of an existing grant, and later deletes
the `S3User` entirely. The provider applies the new access level in place (without
re-creating the identity or rotating the credential) and, on delete, removes the user
and its access so the leaked-credential surface is closed.

**Why this priority**: Lifecycle correctness (in-place level changes, clean teardown)
matters for safe operation but is exercised less often than initial provisioning. It
depends on US1/US2 being in place.

**Independent Test**: Change an existing `S3User`'s access level and confirm the same
credential now has the new level without the secret changing; then delete the resource
and confirm the upstream user is gone and the old credential no longer authorizes.

**Acceptance Scenarios**:

1. **Given** an `S3User` with `read` on a bucket, **When** the operator changes it to
   `read-write` and re-applies, **Then** the existing credential gains write access and
   the connection Secret's access key / secret key are unchanged.
2. **Given** an existing `S3User`, **When** the operator deletes the resource, **Then**
   the upstream identity is removed and the previously issued credential no longer
   authorizes against any bucket.
3. **Given** an `S3User` whose `name` is set, **When** the operator attempts to change
   `name` on an existing resource, **Then** the change is rejected as immutable.

---

### User Story 4 - Stop S3Bucket handing out account-admin keys; surface bucket-side attachments (Priority: P2)

A platform operator no longer wants every `S3Bucket` connection Secret to carry the
account super-user's keys (full access to every bucket). After this feature, an
`S3Bucket`'s connection Secret carries only non-secret bucket metadata (endpoint,
bucket name, region); credentials are obtained exclusively by creating an `S3User`. To
keep the bucket-side view operators expect, each `S3Bucket` also exposes a read-only
status mirror of the users currently attached to it and at what level.

**Why this priority**: Removing the over-privileged Secret is the security outcome the
whole feature targets, and it is the half that lives on the existing kind. It is a
breaking change for existing `S3Bucket` consumers, so it must be explicit and
specified, but it depends on `S3User` (US1) existing as the replacement credential
source. The bucket-side attachment mirror is observational (`kubectl`-friendly) and
read-only — it never writes grant state.

**Independent Test**: Reconcile an existing `S3Bucket`; confirm its connection Secret
contains endpoint/bucket/region but no `access_key`/`secret_key`. Attach an `S3User` to
that bucket and confirm the bucket's `status.attachedUsers` reflects the user and its
level, while the bucket never writes or owns that grant.

**Acceptance Scenarios**:

1. **Given** an `S3Bucket` reconciled under this feature, **When** an operator reads its
   connection Secret, **Then** it contains `endpoint`, `bucket`, and `region` and does
   NOT contain `access_key` or `secret_key`.
2. **Given** an `S3User` granting access to a bucket, **When** the operator reads that
   `S3Bucket`'s status, **Then** `status.attachedUsers` lists the user and its access
   level; **and When** the grant is removed, **Then** the user disappears from the
   mirror — with the bucket never acting as a writer of the grant.
3. **Given** an existing deployment that read credentials from an `S3Bucket` Secret,
   **When** it is migrated, **Then** the documented path is to create an `S3User` and
   consume its scoped Secret instead.

---

### Edge Cases

- **Bucket does not exist / not yet ready**: a referenced bucket cannot be resolved to
  a name. The resource MUST surface a clear not-ready condition rather than attaching a
  policy referencing a non-existent bucket.
- **No grants**: an `S3User` with an empty grant list yields an identity that can list
  bucket names but access no bucket contents (the base-only policy). This is a valid,
  if unusual, state — explicitly the "revoked from every bucket" shape.
- **Removing the last/only grant**: reducing a user to zero grants MUST leave a working
  identity with no bucket access, not delete the user.
- **Admin signing credential reset**: the shared account-admin signing credential is
  reset out-of-band. Because the provider re-derives it each reconcile, subsequent
  reconciles MUST continue to work without operator intervention.
- **Duplicate bucket in grant list**: the same bucket listed twice (possibly at
  conflicting levels) MUST be rejected as an invalid configuration so the operator
  declares one unambiguous level per bucket, rather than the system silently emitting
  an ambiguous or merged policy.
- **Orphaned/failed upstream user with the same name**: a prior user of the same name
  exists upstream. Behavior MUST follow the project's established adoption rules and not
  silently attach to a failed identity.
- **Connection Secret consumer endpoint**: consumers need the S3 *data* endpoint, which
  differs from the administrative/IAM host. The Secret MUST carry the data endpoint so
  consumers connect to the right host.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The provider MUST offer a new namespaced managed-resource kind, `S3User`,
  in the object-storage API group, that provisions a scoped object-storage identity.
- **FR-002**: An `S3User` MUST require a user `name` that is immutable after creation.
- **FR-003**: An `S3User` MUST accept a list of bucket grants under `bucketAccess[]`,
  each identifying a bucket by a typed `bucketRef` (a cross-reference to an `S3Bucket`
  resource, following the provider's existing reference idiom) or a `bucketName` direct
  fallback, plus an access level of `read`, `read-write`, or `admin`. The `S3User` is
  the single source of truth (sole writer) for that user's grants.
- **FR-004**: The provider MUST translate each access level into the corresponding
  least-privilege grant: `read` = read objects and bucket metadata; `read-write` =
  full object access plus read of bucket metadata; `admin` = full object and full
  bucket access — always scoped to the named bucket(s) only, plus the ability to list
  bucket names account-wide.
- **FR-005**: The provider MUST model all of a user's grants as a single merged inline
  policy named `iam-user-policy` (the convention the Timeweb panel uses and reads), and
  on create and on any change render the complete desired document from the current
  grant list and apply it as a whole (adding, changing, or removing a bucket grant is a
  re-render of the full document, not an incremental patch, and not a separate per-bucket
  policy). The rendered document MUST consist of the base account-wide list statement
  plus one statement-pair per granted bucket, so the panel's bucket-side view continues
  to reflect provider-managed grants.
- **FR-006**: On creation, the provider MUST create the identity, attach the rendered
  policy, and write a connection Secret containing the user's access key, secret key,
  the S3 data endpoint, and the (primary) bucket name.
- **FR-007**: The connection Secret MUST contain scoped credentials for the created
  user only — never the account-admin credentials.
- **FR-008**: During observation, the provider MUST determine whether the upstream user
  exists and whether its `iam-user-policy` matches the desired rendered document, and
  report drift when they differ. The comparison MUST be semantic (compare the set of
  statements by effect/action/resource), not literal, because the panel reuses statement
  `Sid` values across buckets (duplicate Sids in one document) and statement ordering is
  not guaranteed.
- **FR-009**: Removing a bucket from the grant list (or reducing it to zero grants) MUST
  revoke that bucket's access by re-applying a policy without it, while keeping the
  identity and its credential intact.
- **FR-010**: On delete, the provider MUST remove the upstream identity (and thereby its
  access) so the issued credential no longer authorizes.
- **FR-011**: The provider MUST sign administrative policy operations using the account
  super-user's object-storage credentials, which it MUST derive at runtime from the
  account (using the already-configured account token); it MUST re-derive them each
  reconcile and MUST NOT cache them across reconciles.
- **FR-012**: The provider MUST NOT rotate or reset the shared account-admin signing
  credential, as it is a shared account credential whose reset would break other
  consumers.
- **FR-013**: The provider MUST NOT rotate or reset the scoped user's own keys in this
  version (key rotation is out of scope; see Out of Scope).
- **FR-014**: The provider MUST classify upstream errors so that transient failures
  retry and terminal/configuration failures surface a clear, non-flapping condition
  (e.g. a referenced bucket that cannot be resolved, or an account/billing boundary).
- **FR-015**: The `S3User` MUST expose non-secret status (at minimum the upstream id,
  user status, and the access key id) without exposing the secret key in status.
- **FR-016**: The grant list MUST reject a duplicate bucket (the same bucket appearing
  more than once, whether by `bucketRef` or `bucketName`) as an invalid configuration, so
  each bucket carries exactly one unambiguous access level.
- **FR-017**: The `S3Bucket` kind MUST stop emitting account-admin credentials: its
  connection Secret MUST no longer contain `access_key` or `secret_key`, retaining only
  non-secret metadata (`endpoint`, `bucket`, `region`). This is a breaking change for
  existing `S3Bucket` consumers; the documented migration path is to obtain credentials
  from an `S3User`.
- **FR-018**: The `S3Bucket` kind MUST expose a read-only `status.attachedUsers` mirror
  listing the users currently attached to that bucket and their access levels, derived
  from observed state. The mirror MUST be observational only — `S3Bucket` MUST NOT write,
  own, or be a source of truth for any grant (the `S3User` remains the sole writer).

### Key Entities *(include if feature involves data)*

- **S3User**: A declarative object-storage identity and the sole writer of its grants.
  Key attributes: an immutable `name`, a `bucketAccess[]` list of bucket grants, optional
  project scoping. Holds, in non-secret status, the upstream id, the user status, and the
  access key id. Owns one connection Secret carrying its scoped credentials, the data
  endpoint, and the bucket name. References one or more **S3Bucket** resources via typed
  `bucketRef` (or `bucketName`).
- **Bucket grant** (`bucketAccess[]` entry): A single (bucket, access level) pair within
  an `S3User`. The unit an operator adds, changes, or removes to control which buckets
  the user reaches and at what level. Each renders to one statement-pair in the merged
  `iam-user-policy`.
- **S3Bucket** (redesigned by this feature): The existing bucket kind, with two changes —
  its connection Secret no longer carries account-admin `access_key`/`secret_key` (only
  `endpoint`/`bucket`/`region`), and it gains a read-only `status.attachedUsers` mirror
  (observed users + levels) for the bucket-side view. Never a writer of grants.
- **Account super-user (signer)**: The always-present, un-deletable account-admin
  object-storage identity whose credentials sign administrative policy operations. Not
  managed by this kind; derived at runtime, never rotated, never cached.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can grant a workload scoped access to a single bucket by
  applying one resource, with no manual key handling and no exposure of account-admin
  credentials.
- **SC-002**: A credential issued by an `S3User` can perform exactly the operations its
  access level allows on each granted bucket and is denied every operation on every
  bucket it was not granted — verified by positive and negative tests for all three
  access levels.
- **SC-003**: Removing a bucket grant (or reducing to zero grants) revokes that bucket's
  access on the next reconcile while leaving the credential usable for any remaining
  grants.
- **SC-004**: Changing a grant's access level takes effect without changing the issued
  access key / secret key, so consumers do not need to re-read the Secret.
- **SC-005**: Deleting an `S3User` renders its previously issued credential unable to
  authorize against any bucket.
- **SC-006**: A steady-state `S3User` whose desired policy already matches upstream
  produces no spurious updates across repeated reconciles (no policy thrash).
- **SC-007**: An out-of-band reset of the shared account-admin signing credential does
  not require operator intervention — subsequent reconciles continue to succeed.
- **SC-008**: After this feature, no `S3Bucket` connection Secret contains account-admin
  credentials; every issued object-storage credential is scoped to specific buckets via
  an `S3User`.
- **SC-009**: An operator can see, from an `S3Bucket`'s status alone, which users are
  attached to it and at what level, without that view ever being a source of truth for
  the grants.
- **SC-010**: A user granted access to multiple buckets at mixed levels is represented by
  exactly one upstream policy (`iam-user-policy`), and that policy remains the one the
  panel reads and writes (provider and panel edits do not produce competing policies).

## Assumptions

- The account token already configured for the provider has full account scope, so
  deriving the account super-user's object-storage signing keys from it at runtime is
  not a privilege escalation and needs no additional configuration.
- The account super-user is always present and cannot be deleted, making it a stable
  signer; the only out-of-band action against it is a key reset, which the provider
  tolerates by re-deriving each reconcile.
- The backend exposes the inline-user-policy subset of the IAM admin surface
  (`PutUserPolicy`/`GetUserPolicy`/`ListUserPolicies`/`DeleteUserPolicy`); roles, STS, and
  managed policies are not relied upon. (Verified live 2026-06-28: the backend actually
  supports multiple named inline policies per user, but this feature deliberately uses the
  single-policy convention below to coexist with the panel.)
- By convention this feature keeps exactly one inline policy per user, named
  `iam-user-policy`, holding all of that user's bucket grants — matching what the Timeweb
  panel writes and reads. "No access to bucket X" is expressed by re-applying the policy
  without X's statement-pair, and a user with zero grants keeps only the account-wide
  bucket-listing base statement.
- Connection-secret shape and bucket-reference idiom follow the existing `S3Bucket`
  kind; external identity is the upstream user id; error classification and the
  namespaced-MR / management-policy conventions follow the rest of the provider.
- The identity-management calls and the policy-attachment calls are two distinct
  operations over two protocols against two hosts (an administrative host for identity
  and policy, a data host for object access); the data host is what consumers receive.

## Out of Scope

- **User key rotation/reset**: the `S3User` kind does not rotate or reset the scoped
  user's keys in v1; if ever needed it is a manual operation.
- **Rotating the shared account-admin signing credential**: explicitly never done by
  the provider.
- **Roles / STS / managed policies**: only inline single-user policies are used.
- **Per-bucket named policies**: although the backend supports multiple inline policies
  per user, this feature uses the single merged `iam-user-policy` to coexist with the
  panel; per-bucket named policies are explicitly not used.
- **Raw policy-document escape hatch**: v1 ships only the `read` / `read-write` /
  `admin` templates; a raw override is a possible later addition, not part of this
  feature.

## Dependencies

- An existing, resolvable bucket per grant (by typed `bucketRef` to an `S3Bucket`
  resource or by direct `bucketName`).
- A provider configuration whose account token can both manage scoped users and derive
  the account super-user's signing credentials.
- The administrative identity/policy host and the S3 data host being reachable from the
  controller (subject to the same network/anti-DDoS constraints as the rest of the
  provider).
