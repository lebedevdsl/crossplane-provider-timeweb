# Feature Specification: S3 bucket storage classes — catalog rotation (`standard`), tier-safe updates, reclassification visibility

**Feature Branch**: `028-s3-storage-classes`

**Created**: 2026-10-15

**Status**: Draft (clarified 2026-10-15)

**Input**: User description: "S3 bucket storage classes catalog rotation (bugfix, NON-BREAKING,
target v0.14.0)" from `specs/_next-s3-storage-classes.preface.md` — live incident
a live incident (2026-10-14): the first `S3Bucket` created after 2026-09 loops forever
on `409 Storage class hot is sold out`; ten of eleven live buckets were silently moved by the
vendor to a class the provider cannot even name.

**Source**: preface (observed live 2026-10-14/15 against the real API, provider v0.13.1 on
the production cluster) + support ticket pending.

## Wire facts (authoritative, captured live 2026-10-15)

Timeweb replaced the S3 storage preset catalog around 2026-10-13 12:00Z (every existing
bucket's `Synced` condition flipped at 12:01–12:03Z). The full current catalog
(`GET /api/v1/presets/storages`, only location `ru-1` exists):

| id | size GB | class | price ₽ | panel name |
|---|---|---|---|---|
| 5127 | 1 | hot | 1 | Premium 1GB |
| 5135 | 1 | standard | 1 | Standard 1GB |
| 5143 | 1 | cold | 2.2 | Cold pay-as-you-go |
| 5129 | 10 | hot | 99 | Premium 10GB |
| 5137 | 10 | standard | 79 | Standard 10GB |
| 5131 | 100 | hot | 449 | Premium 100GB |
| 5139 | 100 | standard | 349 | Standard 100GB |
| 5133 | 250 | hot | 789 | Premium 250GB pay-as-you-go |
| 5141 | 250 | standard | 639 | Standard 250GB pay-as-you-go |

- A **third class `standard`** exists. It is the successor of the old `hot` tier (what the
  five live buckets were bought as) and what the panel sells today as "Standard".
- The API value **`hot` now means the panel's "Premium"** tier. `cold` exists only at 1 GB.
- The old presets 2667/2669/2671 (the 2026-07 buckets, 1/10/100 GB) and 4613
  are gone.
- `POST /api/v1/buckets` with preset 5129 (Premium 10GB) and 5143 (Cold 1GB) both answer
  `409 Conflict: Storage class <class> is sold out` since 2026-10-14 14:43Z. Whether Premium is
  sold out temporarily or withdrawn is unknown (support ticket pending). `standard` is the
  only class the panel sells today.
- Live fleet read 2026-10-15 (plan phase, read-only): **10 of 11 Ready buckets** across
  three namespaces (old presets 2667/2669/2671 — 1/10/100 GB) are reported upstream as
  `storage_class: standard` while their manifests still say `hot`; the 2026-08 bucket (preset 4613)
  still reports `hot`; the incident bucket was never created. The preface's "four" was an
  undercount. Their tariff after the reclassification is an open question for support.

## The gap (code-verified, four holes)

1. **The provider cannot name `standard`.** The `storageClass` schema allows only `hot` and
   `cold`, so every new bucket resolves to Premium or Cold — both unsellable today. Create
   loops forever on the (deliberately transient) `409 sold out`. The loop is harmless
   (nothing is created upstream) but endless, and the GitOps app sits Degraded.
2. **Reclassification is invisible.** Up-to-date detection compares only type, description
   and project, so a bucket whose upstream class silently changed stays `Ready/Synced=True`
   with a spec that no longer describes it.
3. **Any day-2 edit re-picks the tier.** The update path re-resolves the preset from
   `(initialSizeGB, storageClass)` on every update and sends it, even when the operator only
   changed the description, type or project. On a legacy bucket (`spec: hot`, upstream:
   `standard`) that means a silent move to Premium — a sold-out loop today, a paid tier move
   once Premium is sellable. Operators were told not to touch those three fields.
4. **The no-preset hint omits the class.** When no catalog entry matches, the hint lists
   valid combinations without the class, so the next catalog rotation is not self-explaining
   from the condition message.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A new bucket can be ordered in the class the vendor sells (Priority: P1)

An operator declares an `S3Bucket` with `storageClass: standard` (and any catalogued size)
and the bucket is created, becomes Ready, and reports `standard` upstream. The documentation,
examples and the e2e suite recommend `standard` as the default choice and describe the three
classes by the names the vendor's panel uses (`hot` = Premium, `standard` = Standard,
`cold` = pay-as-you-go, 1 GB only).

**Why this priority**: this is the live incident — no S3 bucket can be created by the
provider at all today.

**Independent Test**: apply a `standard` 10 GB bucket manifest against the staging cluster;
it becomes Ready with `status.atProvider.storageClass: standard`; the same manifest with
`storageClass: hot` is still accepted by admission (NON-BREAKING) and surfaces the upstream
`sold out` text in its condition instead of a schema error.

**Acceptance Scenarios**:

1. **Given** the current catalog, **When** an operator applies `storageClass: standard,
   initialSizeGB: 10`, **Then** the bucket is created on the Standard 10GB tier (preset 5137)
   and reaches Ready=True/Synced=True.
2. **Given** a manifest with `storageClass: standard`, **When** it is applied to a cluster
   running this release, **Then** admission accepts it (today it is rejected by the schema).
3. **Given** an existing manifest with `storageClass: hot` or `cold`, **When** the provider is
   upgraded, **Then** the manifest remains valid (no schema break) and its behaviour is
   unchanged.
4. **Given** a class/size combination absent from the catalog (e.g. `cold` 10 GB), **When**
   the operator applies it, **Then** the condition names the requested combination including
   the class and lists the valid combinations including their class.
5. **Given** a class that the vendor reports as sold out, **When** the operator applies it,
   **Then** the condition carries the vendor's `sold out` text and the controller keeps
   retrying at its capped pace (no terminal failure, no upstream object).

---

### User Story 2 - Editing a bucket's description/type/project never changes its tier (Priority: P2)

An operator edits a mutable non-tier field (description, access type, project) on an existing
bucket. The update applies that field only; the bucket's storage tier is untouched regardless
of whether the catalog has rotated since the bucket was bought.

**Why this priority**: this is the latent footgun that turns a harmless description edit
into a sold-out loop today or a paid tier move tomorrow, on ten production buckets.

**Independent Test**: unit-level: an update that changes only the description sends no tier
selection; an update that changes `initialSizeGB` (same class) sends the re-resolved tier.
Live: edit the description of a `standard` bucket on staging; it converges with the upstream
tier unchanged.

**Acceptance Scenarios**:

1. **Given** a Ready bucket, **When** only `description` changes, **Then** the update request
   carries no tier selection and the upstream tier is unchanged after re-observation.
2. **Given** a Ready bucket, **When** only `type` or `projectID` changes, **Then** likewise no
   tier selection is sent.
3. **Given** a Ready `standard` bucket, **When** the operator changes `initialSizeGB` within
   the same class, **Then** the update carries the newly resolved tier (the supported
   within-class resize) and the locked preset is updated.
4. **Given** a bucket whose declared class differs from the upstream class (see User Story 3),
   **When** the operator edits a non-tier field, **Then** the update is refused fail-closed:
   nothing is sent upstream, `Synced=False` with a reason naming both classes ("declared hot,
   upstream standard") and instructing the operator to align `spec.storageClass` to the
   upstream value first; once aligned, the pending edit applies on the next reconcile with no
   tier selection. (Decision 2026-10-15: option A — no accidental paid move is ever possible;
   cross-class moves are not a supported day-2 operation in this release.)

---

### User Story 3 - An upstream reclassification is visible on the resource (Priority: P3)

When the vendor moves a bucket to a different class than the manifest declares, the operator
sees it on the resource (a named condition and a Warning event stating the declared and the
observed class) without the provider attempting to move it back, because a class change is a
paid operation that must stay an explicit human decision.

**Why this priority**: ten production buckets have been in this state since 2026-10-13 and
nobody noticed; the tariff consequence is still being checked. Visibility is cheap and
blocks nothing.

**Independent Test**: unit-level: Observe with `spec: hot` and upstream `standard` sets the
drift condition and emits a Warning event; with equal classes the condition is cleared.
Live on the production cluster after the upgrade: the ten legacy buckets show the condition; aligning
their manifests to `standard` clears it with no update request sent.

**Acceptance Scenarios**:

1. **Given** a bucket with `spec.storageClass: hot` reported upstream as `standard`, **When**
   it is observed, **Then** a condition names both values ("declared hot, upstream standard")
   and a Warning event is recorded; `Ready` and `Synced` are unaffected (the bucket serves).
2. **Given** that bucket, **When** the operator sets `spec.storageClass: standard`, **Then**
   the condition clears on the next observation and no update request is sent (nothing else
   changed).
3. **Given** a bucket whose declared and upstream classes match, **When** it is observed,
   **Then** no drift condition is present.
4. **Given** the drift condition is set, **When** reconciles continue, **Then** the provider
   never sends a tier selection on its own to "repair" the class.

---

### Edge Cases

- A legacy bucket's locked preset (2669/4613) no longer exists in the catalog: the lock stays
  as recorded (it is a historical snapshot), nothing is re-resolved unless the operator
  changes size or class, and the drift condition (if any) is derived from the upstream class,
  not from the stale lock.
- The vendor rotates the catalog again (new ids for the same class/size): existing buckets
  are unaffected (no update is triggered by a catalog change alone); new buckets resolve by
  class/size as before; a missing combination is explained with the class in the hint.
- `cold` with any size other than 1 GB: no catalog entry; the operator gets the class-bearing
  hint rather than a generic "no preset".
- A class is marked sold out mid-flight after a bucket was accepted: unaffected (sold-out is a
  create-time refusal only).
- Upstream omits the storage class in its response: no drift condition is derived from an
  absent value; the mirrored status field is left unset.
- The same spec change hits both a size and a class change at once: the class part makes it a
  mismatch, so the update is refused fail-closed (FR-009) until the class is aligned; the
  size change then applies as a within-class resize.
- Mismatch exists but nothing else changed: no update is attempted (up-to-date detection
  ignores the class), so only the drift condition is visible — the ten legacy buckets are
  untouched by the upgrade until their manifests are edited.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The `S3Bucket` storage class MUST accept `hot`, `standard` and `cold`; existing
  manifests using `hot` or `cold` MUST remain valid (additive, NON-BREAKING).
- **FR-002**: The field documentation, the reference docs, the README, the shipped example
  and the e2e manifest MUST describe the classes by the vendor's panel names (`hot` =
  Premium, `standard` = Standard, `cold` = pay-as-you-go 1 GB only), present the per-class
  size tiers (hot/standard: 1, 10, 100, 250 GB; cold: 1 GB), and recommend `standard` as the
  default choice.
- **FR-003**: Creating a bucket with `standard` MUST resolve to the catalogued Standard tier
  for the requested size (10 GB → 5137 against the 2026-10-15 catalog) and the created bucket
  MUST report `standard` in its observed status.
- **FR-004**: A vendor "sold out" refusal MUST keep its current classification (transient,
  capped retries, no upstream object) and the condition message MUST carry the vendor's text.
- **FR-005**: An update that changes only non-tier fields (description, type, project) MUST
  NOT carry a tier selection; the upstream tier MUST be unchanged after re-observation.
- **FR-006**: An update in which the declared size changes within the same class MUST carry
  the newly resolved tier and MUST refresh the locked preset snapshot.
- **FR-007**: The provider MUST surface a declared-vs-upstream storage class mismatch as a
  named condition on the resource (stating both values) plus a Warning event, and MUST clear
  it once the values match. `Ready` and `Synced` MUST NOT be affected by the mismatch alone.
- **FR-008**: The provider MUST NOT send a tier selection to repair a class mismatch on its
  own (no auto-repair; a class change is a paid move and stays a human decision).
- **FR-009**: When an update is attempted while a class mismatch is present (declared class ≠
  upstream class), the provider MUST refuse it fail-closed: no request is sent upstream,
  `Synced=False` with a reason that names both classes and tells the operator to align
  `spec.storageClass` to the upstream value. After alignment the pending non-tier edit MUST
  apply with no tier selection. Cross-class moves on an existing bucket are NOT supported in
  this release (a deliberate class change is delete/recreate or a panel action followed by
  spec alignment).
- **FR-010**: When no catalog entry matches the requested `(size, location, class)`, the
  condition MUST name the requested combination including its class and list the valid
  combinations including their class (e.g. `10GB/ru-1/standard`).
- **FR-011**: The vendored API description MUST know `standard` as a storage class value in
  both places it enumerates the class (catalog entry and bucket), so generated types and
  docs stop describing a two-class world.
- **FR-012**: The resolver's behaviour MUST be pinned against the 2026-10-15 catalog:
  `(10, "", hot)` → 5129, `(10, "", standard)` → 5137, `(1, "", cold)` → 5143,
  `(10, "", cold)` → not found with a class-bearing hint.
- **FR-013**: The e2e bucket bundle MUST order `standard` so the suite passes against the
  current catalog.
- **FR-014**: Release and rollout: this ships as a minor release (new enum value) and the
  rollout order in the preface is followed — provider bump, then the incident manifest to
  `standard`, then alignment of the ten legacy manifests to `standard` (no update request
  results from that alignment).

### Key Entities

- **Storage class**: the vendor's tier family for a bucket — `hot` (Premium), `standard`
  (Standard), `cold` (pay-as-you-go). Declared by the operator; reported by the vendor;
  the two may diverge after a vendor-side reclassification.
- **Catalog preset**: one orderable `(size, class, location)` combination with a vendor id
  and a price. Ids rotate; class/size are the stable operator-facing coordinates.
- **Locked preset snapshot**: the id the provider chose when the bucket was bought (or on a
  deliberate resize); historical, may no longer exist in the catalog; never a trigger by
  itself.
- **Class mismatch (drift)**: declared class ≠ upstream-reported class; surfaced, never
  auto-repaired.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A `standard` bucket declared on the staging cluster is Ready within 5 minutes
  of apply, and the incident bucket (a live incident) converges after the rollout
  with no manual intervention beyond the one-word class change.
- **SC-002**: 0 tier selections are sent for updates that change only description, type or
  project (pinned by unit tests; confirmed by a live description edit on staging leaving the
  upstream tier unchanged).
- **SC-003**: All ten reclassified production buckets show the mismatch condition within one
  reconcile of the upgrade, and aligning each manifest clears it with 0 update requests.
- **SC-004**: Every existing `S3Bucket` manifest in the fleet remains schema-valid after the
  upgrade (0 admission rejections).
- **SC-005**: For an unmatched class/size, the condition message alone is enough for an
  operator to pick a valid combination (lists class + size + location) — verified by a unit
  test on the message text.
- **SC-006**: The e2e bucket bundle passes against the current catalog.

## Assumptions

- `Ready`/`Synced` stay unaffected by a class mismatch: the bucket is serving and ten
  production buckets are in this state today; degrading them on upgrade would turn a
  visibility fix into an outage signal. The mismatch rides on a dedicated condition type plus
  a Warning event, in the "not a controller failure" style of the existing
  `UpstreamFailed`/`PaymentRequired` vocabulary.
- `storageClass` stays a required field with no server-side default; `standard` is the
  documented recommendation, not a schema default (no behaviour change for existing
  manifests).
- Up-to-date detection keeps ignoring the storage class (otherwise every reclassified bucket
  would trigger an update per reconcile — the very loop this feature removes). Mismatch is
  reported through the condition path, not the update path.
- Cross-class moves on an existing bucket (e.g. `standard` → `hot`) are not a supported day-2
  operation in this release (clarified 2026-10-15, option A: fail closed on mismatch);
  within-class resizes remain supported as today. An explicit opt-in cross-class move is
  deferred to a later round.
- Vendor-side facts that remain open (is Premium withdrawn or temporarily sold out; the
  tariff now billed for the reclassified 2669 buckets and for the 4613 bucket) are tracked in
  the support ticket and do not block this feature; the documentation words `hot` as
  "Premium — may be unavailable to order" until support answers.
- The ten legacy manifests live in the GitOps repo (the GitOps repo), outside this
  repository; their alignment is a rollout step, not provider code.
- No vendor assignment/transfer APIs and no automatic tier repair are introduced (consistent
  with the hard boundary set in feature 027).
