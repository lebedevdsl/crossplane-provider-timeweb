# Research: S3 storage classes — catalog rotation (feature 028)

Phase 0 output. Decisions R-1..R-10; every one is code- or live-verified on
2026-10-15 unless marked otherwise.

## R-1: Fleet impact is larger than the preface says (live read 2026-10-15)

`kubectl --context inyan-infra get s3buckets -A` (read-only):

| ns | bucket | spec | atProvider.storageClass | lockedPresetID | sizeKB |
|---|---|---|---|---|---|
| cloud-infra | flick-media-staging | hot/10 | — (never created) | — | — |
| cloud-infra | flick-static | hot/10 | **hot** | 4613 | 10485760 |
| cloud-infra | inyan-grafana-dashboards | hot/10 | standard | 2669 | 10485760 |
| cloud-infra | inyan-rolly-images | hot/10 | standard | 2669 | 10485760 |
| cloud-infra | inyan-static | hot/10 | standard | 2669 | 10485760 |
| cloud-infra | volpepizza-images | hot/10 | standard | 2669 | 10485760 |
| loki | loki | hot/100 | standard | 2671 | 104857600 |
| s3-provisioning | bagetter | hot/10 | standard | 2669 | 10485760 |
| s3-provisioning | gitlab-backup | hot/100 | standard | **2669** | 104857600 |
| s3-provisioning | inyan-production-db-backup | hot/100 | standard | 2671 | 104857600 |
| s3-provisioning | inyan-staging-db-backup | hot/100 | standard | 2671 | 104857600 |
| s3-provisioning | vault-file-backup | hot/1 | standard | 2667 | 1048576 |

- **Decision**: the rollout aligns **10** legacy manifests (3 namespaces, sizes
  1/10/100 GB; old presets 2667/2669/2671), not 4. Spec wire facts + SC-003
  amended.
- **`lockedPresetID` is NOT a size signal**: `gitlab-backup` holds 2669 (the
  10 GB preset) while upstream is 100 GB — a past within-class resize left the
  lock stale (the lock is only refreshed by Update, and the 100 GB resize may
  have happened in the panel). The Update rule therefore compares against the
  upstream observation, never the lock (deviation from the preface's "or
  lockedPresetID unset" clause — the clause is dropped).
- **Size unit pinned**: `disk_stats.size` is KB (generated doc comment) and the
  live values are binary: 1 GB ↔ 1048576, 10 GB ↔ 10485760, 100 GB ↔ 104857600.
  `observedGB = sizeKB / 1048576`. The preset catalog's `disk` stays MB
  (`fetchS3BucketPresets` divides by 1024) — two different units, both already
  handled.

## R-2: Enum + generated-client delta is purely additive

- `apis/objectstorage/v1alpha1/types.go:45` → `Enum=hot;standard;cold`; comments
  at `types.go:43` (field doc), `types.go:98` (status mirror), `doc.go:20`.
- `docs/openapi-timeweb.json` `storage_class` enums at ~46478 (`PresetsStorage`)
  and ~46732 (`Bucket`): add `"standard"`. `make generate-client` then emits
  `BucketStorageClassStandard` / `PresetsStorageStorageClassStandard`. Both are
  plain `string` types, so `standard` already flows through Observe today
  (`populateStatus` does `string(b.StorageClass)` — proven by R-1's live
  `atProvider.storageClass: standard` under v0.13.1). Behaviour change: none;
  the constants exist for tests/docs. Add the patch to the hand-patch
  re-apply inventory (project memory: the json is a hand-patched superset).
- Constitution I: v1alpha1, additive — no valid manifest becomes invalid.

## R-3: Update tier rule (fail-closed on class mismatch — clarify Q1 = A)

Today `Update()` always resolves `(initialSizeGB, storageClass)` and sends
`preset_id`. New rule, evaluated after the immutable-`name` check against the
fresh GET:

1. `observedClass := string(bucket.StorageClass)`. If non-empty and
   `!= spec.storageClass` → **refuse**: set
   `SyncedFalse(ReasonStorageClassMismatch, msg)`, Warning Event, return a
   typed sentinel (`ErrStorageClassMismatch`). Nothing is sent upstream. The
   runtime overwrites the Synced reason with `ReconcileError` (documented
   gotcha, `docs/conditions.md`) — the message and the Event carry the real
   reason, same as `ImmutableFieldChange` today.
2. `observedGB := sizeKB / 1048576` (skip when `is_unlimited` or size 0 →
   unknown → never send a tier). If `observedGB != spec.initialSizeGB` →
   resolve and send `preset_id`, refresh `lockedPresetID` (within-class resize,
   supported today — `docs/presets.md` "Locked-preset semantics").
3. Otherwise the PATCH carries `bucket_type` + `description` only; no resolver
   call, `lockedPresetID` untouched.

Rationale: (a) a non-tier edit can no longer move a tier (US2); (b) a drifted
legacy bucket is never silently re-tiered — the operator aligns `storageClass`
first (clarify Q1 = A); (c) fewer catalog calls on the 2 r/s budget (018).
Alternatives rejected: compare resolved id vs `lockedPresetID` (stale lock,
R-1); upstream-wins silent skip (option B — hides the decision from the
operator); spec-wins (option C — today's footgun).

Cross-class day-2 moves: not supported in this release. Whether the upstream
PATCH `preset_id` even performs a class move is **unverified** and stays so —
we never send one. Seeded for a later round if ever needed.

## R-4: Drift surfacing = a dedicated condition type, Ready/Synced untouched

- `isUpToDate` keeps ignoring the class (else 10 production buckets would call
  Update every reconcile and hit the R-3 refusal → Synced=False fleet-wide).
- Observe computes `drift := observedClass != "" && observedClass !=
  spec.storageClass` and sets condition **type `StorageClassDrift`**:
  - drift → `Status=True, Reason=UpstreamReclassified`, message
    `declared "hot", upstream reports "standard"; set
    spec.forProvider.storageClass to "standard" (no automatic re-tiering — a
    class change is a paid move)`; Warning Event on transition
    (`RecordConditionChange` works for any type).
  - no drift AND a `StorageClassDrift` condition already exists → flip to
    `Status=False, Reason=Aligned` (Normal Event; add the reason to
    `normalReasons`). No condition is ever added to a bucket that never
    drifted — the 12-bucket fleet stays clean except the 10 that need it.
- Precedent for provider-defined condition types alongside Ready/Synced: upjet
  (`AsyncOperation`, `LastAsyncOperation`), crossplane-runtime `Healthy`. The
  runtime's `SetConditions` merges by type (verified in
  `crossplane/apis/v2 core/v2/condition.go`), so the extra type persists across
  the reconciler's own Synced/Ready writes.
- Argo CD health scripts for Crossplane MRs read `Ready`/`Synced` only → no
  health change from the drift condition (the R-3 refusal does degrade the
  edited bucket, by design).
- Alternative rejected: Ready=False on drift — would flip 10 serving production
  buckets to degraded on upgrade day.

## R-5: PresetNotFound hint already carries the class — pin, don't change

`describeEntry` prints `<GB>/<location>/<class>` whenever both are set, and
every S3 preset has `location: ru-1`, so the hint is already
`10GB/ru-1/standard`. The requested-slug part prints
`storageClass="cold"`. FR-010 is satisfied by the current code; the resolver
unit test on the 2026-10-15 fixture pins it (no code change expected). The
`docs/presets.md` sample message is updated to show a class-bearing hint.

## R-6: `409 sold out` classification stays

`isTransientStatus` keeps 409 transient deliberately (006 cleanup windows,
`errors.go:179`); `Classify` appends the upstream message, which is why the
incident condition read `Conflict: Storage class hot is sold out`. No change
(FR-004). The capped requeue applies. The docs name the message so operators
recognise it.

## R-7: Docs are internally contradictory today — fix to match code + 028

`docs/s3bucket.md` says `initialSizeGB` and `storageClass` are immutable and
"trigger reject-and-surface" (only `name` does), while `docs/presets.md` says
size edits re-resolve + PATCH (true). After 028:
- `initialSizeGB`: mutable, within the same class (R-3 step 2).
- `storageClass`: create-time choice; day-2 a mismatch with upstream refuses
  the update (`StorageClassMismatch`), drift is reported via
  `StorageClassDrift`.
- Field docs by panel names; per-class tier table; example + kuttl on
  `standard`; `hot` worded "Premium — currently not orderable (409 sold out
  since 2026-10-14; support ticket open)".
- `docs/conditions.md`: rows for `StorageClassMismatch` (Synced=False) and
  `StorageClassDrift` (own type; `UpstreamReclassified` / `Aligned`).
- `README.md` sizing table gains the class column.
- Release notes `docs/release-notes/v0.14.0.md` (example-first, short).

## R-8: Tests (Constitution III)

- `internal/controller/s3bucket/external_test.go`: `fakeResolver` keyed by
  `(DiskGB, StorageClass)` instead of size only. Cases: Create `standard`/10 →
  5137 in the POST body; Update description-only → `PresetId == nil`, resolver
  not called, lock untouched; Update size 10→100 (same class) → `PresetId` =
  resolved id, lock refreshed; Update with spec `hot` vs upstream `standard` →
  `ErrStorageClassMismatch`, 0 PATCH calls, Warning Event; Observe drift →
  `StorageClassDrift=True/UpstreamReclassified` + Event; Observe aligned after
  drift → `False/Aligned`; Observe never-drifted → no such condition; Observe
  upstream class empty → no condition.
- `internal/controller/shared/resolver/match_size_test.go` (new or extended):
  fixture = the nine 2026-10-15 entries; `(10,"",hot)`→5129, `(10,"",standard)`
  →5137, `(1,"",cold)`→5143, `(10,"",cold)`→`PresetNotFoundError` whose message
  contains `storageClass="cold"` and `10GB/ru-1/standard`.
- `dimensions_test.go`: `fetchS3BucketPresets` maps `standard` through (fake
  catalog client with the new value).
- kuttl `04-s3bucket`: `storageClass: standard`; add step 02 — patch
  `description`, assert Synced/Ready via `kubectl wait` and
  `status.atProvider.storageClass == standard` (script step, fail-closed).

## R-9: Live gates

- **Staging** (`inyan-staging`, e2e context pinned): dev-tag build → kuttl 04 →
  manual: create `standard`/10, edit description (tier unchanged by
  re-observation + provider logs clean), apply `hot`/10 and confirm the
  condition shows the upstream `sold out` text and no object appears in the
  panel, delete.
- **Production** (`inyan-infra`, after v0.14.0 + timeweb-infra provider bump):
  `make preflight KUBECONTEXT=inyan-infra` (CRD enum shows `standard`); the 10
  legacy buckets show `StorageClassDrift=True`; `flick-media-staging` manifest
  → `standard` converges; then align the 10 manifests → `Aligned`, provider
  logs show 0 PATCH calls for them.

## R-9 results (staging gate, 2026-10-15, dev tag `dev-1792091653` on `inyan-staging`)

- Provider revision healthy in ~20 s; CRD enum on the cluster reads
  `["hot","standard","cold"]`.
- kuttl `04-s3bucket` PASS (two runs): `standard`/10 created real buckets
  **583087** and **583091** — `preset_id` 5137 confirmed orderable at the API
  level, upstream `storage_class: standard`, `disk_stats.size` 10485760 KB,
  `Locked Preset ID: 5137`, Ready/Synced True within 2–4 s of apply. Step 02
  (description patch) converged with class/size unchanged and no
  `StorageClassDrift` condition. Both buckets deleted by the harness; no MRs
  left in `timeweb-e2e`.
- `hot`/10 probe: `Synced=False/ReconcileError: create failed: timeweb
  transient (Conflict: Storage class hot is sold out)`, Warning Event
  `CannotCreateExternalResource`, no external-name/upstream object; deleted
  cleanly. FR-004 holds.
- Provider log scan over the gate window: 0 error lines.
- Harness note: kuttl renames the rendered single context to `cluster` for
  command steps — the 04 step-02 context lock accepts a single-context
  kubeconfig (bundle-24 idiom would fail with `E2E_KUBECONTEXT` exported).
- Staging was left on the dev tag (previously `ghcr.io/…:v0.13.1`); the
  production rollout bumps it to `v0.14.0` via timeweb-infra.

## Rollout record (2026-10-15, v0.14.0)

- Release: tag `v0.14.0` → release workflow green →
  `ghcr.io/lebedevdsl/provider-timeweb:v0.14.0`
  (`sha256:da38653564ca640a77de11a54849e12e1896868d868d19566a16e9ccba4142de`).
  The `ci` workflow on the same push failed in `make lint` only: the runner's
  Go `stable` is 1.27.2 and golangci-lint v2.12.2 cannot read its export data
  (version 5 > 4) — fixed in a follow-up commit (lint tool bump), unrelated
  to the feature.
- Staging (`inyan-staging`): provider moved dev tag → `v0.14.0`, Healthy,
  preflight PASS.
- Infra (`inyan-infra`, timeweb-infra `6f5f1a77`): Argo rolled the provider
  in ~1 min, preflight PASS, CRD enum `hot/standard/cold`.
- Buckets: 11 manifests aligned (`766bd2aa` flick-media, `b63c8bab` the
  rest; `flick-static` stays `hot`, preset 4613 — upstream still `hot`).
  Fleet is actually **13** MRs (a `tempo/tempo` bucket appeared since the
  morning read). Every aligned bucket shows `StorageClassDrift=False/Aligned`
  or no condition (alignment landed before its first drift observation);
  `UpstreamReclassified` Warning Events were emitted for the ones observed
  first; 0 `StorageClassMismatch` events; provider log errors over the
  window: 0.
- Incident bucket `flick-media-staging`: after the class change the first
  Create attempt hit the runtime's "cannot determine creation result" guard
  (external-create-pending left behind — the Argo selfHeal write raced the
  status update). Verified upstream via an in-cluster read-only `GET
  /api/v1/storages/buckets` (12 buckets, no `flick-media-staging`), removed
  the two create annotations, next reconcile created **583105** on 5137
  (`standard`, Ready/Synced True in 10 s); dependent `S3User
  flick-media-staging` converged 30 s later. infra/timeweb-infra#255 closed
  by convergence.
- Left as-is: `PushSecret/flick-static` OutOfSync + 3 orphaned resources on
  the `cloud-infra-flick-staging` app (pre-existing, unrelated).

## Support ticket draft (RU, project flick — owner action, T027)

> После ротации каталога S3 (13.10.2026) прошу уточнить: (1) класс `hot`
> (Premium) снят с продажи или временно недоступен? `POST /api/v1/buckets` с
> `preset_id` 5129 и 5143 с 14.10 14:43Z отвечает `409 Storage class … is sold
> out`. (2) Бакеты на пресетах 2667/2669/2671 теперь отображаются как
> `storage_class: standard` — какой тариф по ним выставляется, и как
> тарифицируется бакет на пресете 4613 (остался `hot`)? (3) Выполняет ли
> `PATCH /api/v1/storages/buckets/{id}` с `preset_id` другого класса перенос
> между классами, и платный ли он? (4) Просьба обновить публичный OpenAPI:
> enum `storage_class` не содержит `standard`.

## R-10: Branch-overlap risk with the stashed 027 work

`stash@{0}` (027) modifies `apis/objectstorage/v1alpha1/types.go`,
`internal/controller/s3bucket/external.go`, `docs/s3bucket.md`,
`examples/s3bucket.yaml`, the S3Bucket CRD YAML. Popping it after 028 merges
will conflict in those files — expected; resolve by re-applying 027's
project-field edits on top (they touch `projectID` lines, not the tier code).

## Open upstream questions (support ticket, project flick — not blocking)

- Premium (`hot`) sold out temporarily or withdrawn?
- Tariff now billed for the reclassified 2667/2669/2671 buckets; is the 4613
  bucket (`flick-static`) billed as Premium?
- Does PATCH `preset_id` across classes perform a move? (Never exercised.)
