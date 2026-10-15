# Tasks: S3 storage classes — catalog rotation (`standard`), tier-safe updates, drift visibility

**Input**: Design documents from `specs/028-s3-storage-classes/`

**Prerequisites**: plan.md (D-1..D-9), spec.md (clarified 2026-10-15, Q1 = A fail-closed),
research.md (R-1..R-10), data-model.md (Update decision table), contracts/

**Tests**: unit tests are MANDATORY (Constitution III); kuttl + live-gate tasks implement
the plan's Validation section.

**Organization**: US1 = a `standard` bucket can be ordered; US2 = non-tier edits never move
the tier (fail-closed on class mismatch); US3 = upstream reclassification is visible.

## Phase 1: Setup

No setup tasks — existing provider layout, no new packages or tooling.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the schema, generated client and shared vocabulary every story reads.

- [x] T001 Enum + docs + condition type in `apis/objectstorage/v1alpha1/types.go`:
      `StorageClass` marker `Enum=hot;cold` → `Enum=hot;standard;cold` (L45); field doc
      (L43) → "`hot` = panel Premium; `standard` = panel Standard (recommended);
      `cold` = pay-as-you-go, 1 GB only. Create-time choice — if upstream later reports a
      different class, updates are refused until the spec is aligned (see
      `StorageClassDrift`)"; `InitialSizeGB` doc → "mutable within the same class";
      observation `StorageClass` doc (L98) → "upstream-reported tier (`hot` / `standard` /
      `cold`); may differ from spec after a vendor reclassification";
      `LockedPresetID` doc → "historical snapshot; may name a preset no longer in the
      catalog; never consulted for decisions". Add
      `const TypeStorageClassDrift xpv2.ConditionType = "StorageClassDrift"` with a doc
      comment (True/`UpstreamReclassified`, False/`Aligned`, absent = never drifted).
      Update `apis/objectstorage/v1alpha1/doc.go:20` (`hot | standard | cold`).
- [x] T002 Regenerate CRDs: `make generate-crds`; verify
      `package/crds/objectstorage.m.timeweb.crossplane.io_s3buckets.yaml` enum lists
      `hot`, `standard`, `cold` and the new doc strings; tree otherwise clean.
- [x] T003 [P] Hand-patch `docs/openapi-timeweb.json`: add `"standard"` to the
      `storage_class` enum of `PresetsStorage` (~L46478) and of `Bucket` (~L46732); append
      to each `description` "(HAND-PATCH feature 028: live catalog 2026-10-15 returns
      `standard`; published enum is stale.)" following the existing HAND-PATCH note
      convention.
- [x] T004 Regenerate the client after T003: `make generate-client`; verify
      `internal/clients/timeweb/generated/zz_generated_client.go` gains
      `BucketStorageClassStandard` and `PresetsStorageStorageClassStandard`; build passes.
- [x] T005 [P] Shared vocabulary in `internal/controller/shared/conditions.go`: add
      `ReasonStorageClassMismatch` (Synced=False, Update refused: declared class ≠ upstream
      class), `ReasonUpstreamReclassified` (StorageClassDrift=True),
      `ReasonStorageClassAligned = "Aligned"` (StorageClassDrift=False) with doc comments
      referencing feature 028; in `internal/controller/shared/events.go` add
      `ReasonStorageClassAligned` to `normalReasons`.
- [x] T006 [P] Comment-only: `internal/controller/shared/resolver/match_size.go:34` and
      `slug.go:34` → `S3: "hot" | "standard" | "cold"`.

**Checkpoint**: `go build ./...` green; CRD enum shows `standard`.

---

## Phase 3: User Story 1 — A new bucket can be ordered in the class the vendor sells (Priority: P1) 🎯 MVP

**Goal**: `storageClass: standard` is admitted, resolves to the Standard tier (10 GB → 5137
on the 2026-10-15 catalog), creates, and reports `standard`; docs/example/e2e recommend it.

**Independent Test**: unit — Create with `standard`/10 POSTs `preset_id: 5137`; resolver
fixture pins FR-012; kuttl 04 passes on `standard`; a `hot` manifest is still admitted and
shows the vendor's `sold out` text.

- [x] T007 [P] [US1] Fake resolver + Create test in
      `internal/controller/s3bucket/external_test.go`: change `fakeResolver` to key by
      `(DiskGB, StorageClass)` (e.g. `map[string]int64` keyed `"10/standard"`), record the
      last `PresetBySizeInput` and a call counter; `newBucket` gains a `storageClass`
      parameter (default callers pass `hot`); add `sampleBucketJSON` variants via a helper
      `bucketJSON(storageClass string, sizeKB int)`; new case
      `TestCreate/Standard_Resolves5137`: spec `standard`/10, fake map `{"10/standard":5137}`
      → `CreateStorage` body `PresetId == 5137`, `LockedPresetID == 5137`,
      `atProvider.storageClass == "standard"` (response JSON says `standard`).
- [x] T008 [P] [US1] Resolver fixture test `internal/controller/shared/resolver/match_size_test.go`
      (new): `catalog20261015 := []PresetEntry{...}` = the nine entries from
      data-model.md (ids 5127/5135/5143/5129/5137/5131/5139/5133/5141, location `ru-1`);
      cases `(10,"",hot)`→5129, `(10,"",standard)`→5137, `(1,"",cold)`→5143,
      `(10,"",cold)`→`*PresetNotFoundError` whose `Error()` contains
      `storageClass="cold"` and `10GB/ru-1/standard` (FR-010/FR-012 pin — no code change
      expected, D-5).
- [x] T009 [P] [US1] `internal/controller/shared/resolver/dimensions_test.go`: extend the
      S3 fetch test (or add one using the existing fake catalog client) so a
      `/presets/storages` payload with `storage_class: "standard"` maps to
      `PresetEntry.StorageClass == "standard"` and `DiskGB == disk/1024`.
- [x] T010 [US1] `docs/presets.md`: S3 row of the size table → per-class tier table
      (`hot` Premium 1/10/100/250 — "not orderable since 2026-10-14, `409 … sold out`";
      `standard` Standard 1/10/100/250 — recommended; `cold` 1 only); example bucket →
      `storageClass: standard`; "What the controller does" failure sample → a
      class-bearing message (`storageClass="cold"` … `valid: 1GB/ru-1/hot, 1GB/ru-1/standard,
      1GB/ru-1/cold, 10GB/ru-1/hot, 10GB/ru-1/standard, …`); add a short "Sold-out class"
      paragraph (transient, retried, nothing created, switch class — no recreate).
- [x] T011 [P] [US1] `README.md` sizing table (~L111): S3Bucket row → `1, 10, 100, 250`
      for `hot`/`standard`, `1` for `cold`; one sentence naming `standard` as the default
      recommendation and linking `docs/presets.md`.
- [x] T012 [P] [US1] `examples/s3bucket.yaml`: `storageClass: standard`; header comment
      → "Mutable: type, description, projectID, initialSizeGB (within the same class).
      storageClass is a create-time choice; a mismatch with upstream refuses updates";
      inline comment lists the three classes by panel name; fix the stale
      `docs/resources/s3bucket.md` link → `docs/s3bucket.md`.
- [x] T013 [US1] `docs/s3bucket.md` field reference: `initialSizeGB` → Mutable **within
      class** ("re-resolved + PATCHed; cross-class not supported"); `storageClass` →
      Mutable **no (create-time)**, values with panel names, "if upstream reports another
      class, see `StorageClassDrift`"; `status.atProvider.storageClass` values
      `hot`/`standard`/`cold`; example manifest (~L34) → `standard`.
- [x] T014 [US1] `test/e2e/kuttl/tests/04-s3bucket/01-apply.yaml`: `storageClass: standard`;
      header comment notes the 2026-10-13 catalog rotation (Premium sold out).

**Checkpoint**: `go test ./internal/controller/s3bucket/... ./internal/controller/shared/resolver/...`
green; docs/example on `standard`.

---

## Phase 4: User Story 2 — Editing description/type/project never changes the tier (Priority: P2)

**Goal**: Update sends `preset_id` only for a within-class resize; a declared-vs-upstream
class mismatch refuses the update fail-closed (clarify Q1 = A); `lockedPresetID` is never
an input.

**Independent Test**: unit — description-only Update has `PresetId == nil` and makes no
resolver call; size 10→100 Update carries the re-resolved id and refreshes the lock; spec
`hot` vs upstream `standard` → `ErrStorageClassMismatch`, 0 PATCH calls, Warning Event.
kuttl — a description patch leaves `atProvider.storageClass` unchanged.

- [x] T015 [US2] Implement D-3 in `internal/controller/s3bucket/external.go` `Update()`:
      after the `name` immutability check — (i) `observed := string(bucket.StorageClass)`;
      if `observed != "" && observed != cr.Spec.ForProvider.StorageClass` → build msg
      `fmt.Sprintf("storage class mismatch: spec declares %q but upstream reports %q; set
      spec.forProvider.storageClass to %q to align (cross-class moves are not supported
      day-2)", spec, observed, observed)`, `cond := shared.SyncedFalse(shared.ReasonStorageClassMismatch, msg)`,
      `shared.RecordConditionChange(e.recorder, cr, cond)`, `cr.Status.SetConditions(cond)`,
      `return managed.ExternalUpdate{}, ErrStorageClassMismatch` (new package-level
      `var ErrStorageClassMismatch = errors.New("s3bucket: storage class mismatch")`), no
      request; (ii) `sizeKnown := !bucket.DiskStats.IsUnlimited && bucket.DiskStats.Size > 0`,
      `observedGB := int64(bucket.DiskStats.Size) / 1048576`; if `sizeKnown && observedGB !=
      cr.Spec.ForProvider.InitialSizeGB` → `resolvePresetID`, set `body.PresetId`, refresh
      `LockedPresetID` after the PATCH succeeds; (iii) else no `PresetId`, no resolver call,
      lock untouched. Update the package doc comment (L18-24: "`presetName`" is stale →
      `initialSizeGB`+`storageClass`; describe the tier rule) and the Update doc comment.
- [x] T016 [US2] Update test matrix in `internal/controller/s3bucket/external_test.go`:
      `TestUpdate/DescriptionOnly_NoPresetSent` (GET returns `standard`/10485760, spec
      `standard`/10, description differs → `UpdateStorage` arg `PresetId == nil`, resolver
      call count 0, `LockedPresetID` unchanged); `TestUpdate/Resize_SendsPreset` (GET
      `standard`/10485760, spec `standard`/100, fake `{"100/standard":5139}` →
      `PresetId == 5139`, lock 5139); `TestUpdate/ClassMismatch_Refused` (GET `standard`,
      spec `hot` → `errors.Is(err, ErrStorageClassMismatch)`, `UpdateStorageCallCount()==0`,
      Synced condition reason `StorageClassMismatch` + message contains both classes, a
      Warning event on the fake recorder); `TestUpdate/UnlimitedSize_NoPresetSent`
      (`is_unlimited: true` → no `PresetId`); keep the existing Success/NotFound/
      Transient/Terminal/ImmutableName cases (adjust fixtures to equal class/size).
- [x] T017 [US2] kuttl step `test/e2e/kuttl/tests/04-s3bucket/02-patch-description.yaml`
      (`kubectl patch` the `description` of `e2e-s3`) + `02-assert.yaml` (`kubectl wait
      --for=condition=Synced=True` / `Ready=True`, then a script step asserting
      `.status.atProvider.storageClass == "standard"` and the description echoed — the 021
      fail-closed script idiom, context pinned).
- [x] T018 [US2] Docs for the tier rule: `docs/s3bucket.md` — replace the
      "Immutable-field handling" section with "Tier changes" (within-class resize via
      `initialSizeGB`; class mismatch → update refused with the `StorageClassMismatch`
      message, align the spec; cross-class moves unsupported; `name` still
      `ImmutableFieldChange`); Conditions table `Synced` False reasons += `StorageClassMismatch`
      (note the runtime shows `ReconcileError` + message; Event carries the reason).
      `docs/presets.md` "Locked-preset semantics": size edits re-resolve within the class;
      `lockedPresetID` is a snapshot never consulted (R-1); PATCH carries `preset_id` only
      on a resize.
- [x] T019 [P] [US2] `docs/conditions.md`: row `StorageClassMismatch` | Synced=False |
      "(S3Bucket) spec `storageClass` ≠ upstream class; nothing was sent. Align the spec to
      the upstream value (cross-class moves are not supported)".

**Checkpoint**: US2 unit matrix green; kuttl 04 step 02 passes on staging.

---

## Phase 5: User Story 3 — An upstream reclassification is visible on the resource (Priority: P3)

**Goal**: Observe sets `StorageClassDrift=True/UpstreamReclassified` (+ Warning Event) when
upstream class ≠ spec, flips it to `False/Aligned` once matched, never adds it to a clean
bucket; `Ready`/`Synced` and `isUpToDate` unaffected; no auto-repair.

**Independent Test**: unit — four Observe states (drift / aligned-after-drift / never
drifted / upstream class empty); live — the ten legacy buckets show True, aligning a
manifest flips to False with 0 PATCH calls in provider logs.

- [x] T020 [US3] Implement D-4 in `internal/controller/s3bucket/external.go`: new
      `setStorageClassDriftCondition(recorder, cr, observedClass string)` called in
      `Observe` after `populateStatus`: `drift := observedClass != "" && observedClass !=
      spec.StorageClass`; if drift → condition `{Type: TypeStorageClassDrift, Status: True,
      Reason: ReasonUpstreamReclassified, Message: fmt.Sprintf("declared %q, upstream reports
      %q; set spec.forProvider.storageClass to %q to align (no automatic re-tiering — a class
      change is a paid move)", ...)}`; else if `cr.GetCondition(TypeStorageClassDrift).Reason
      != ""` (condition present) → `{Status: False, Reason: ReasonStorageClassAligned,
      Message: "spec and upstream storage class match"}`; else no-op.
      `shared.RecordConditionChange` before `SetConditions` (Warning on True, Normal on
      Aligned). `isUpToDate` and `setBucketReadyCondition` untouched. Add a one-line doc
      comment that the condition is an additional type and never gates Ready/Synced.
- [x] T021 [US3] Observe test matrix in `internal/controller/s3bucket/external_test.go`:
      `TestObserve/Drift_SetsCondition` (GET `standard`, spec `hot` → condition True /
      `UpstreamReclassified`, message names both classes, Warning event, `ResourceUpToDate`
      still true, Ready Available); `TestObserve/Aligned_AfterDrift` (pre-set True
      condition, GET `standard`, spec `standard` → False/`Aligned`, Normal event);
      `TestObserve/NeverDrifted_NoCondition` (GET `standard`, spec `standard`, no prior →
      `GetCondition(TypeStorageClassDrift)` empty); `TestObserve/EmptyUpstreamClass_NoCondition`
      (GET `storage_class: ""`).
- [x] T022 [US3] Docs: `docs/s3bucket.md` new section "Vendor reclassification
      (`StorageClassDrift`)" with the condition YAML from quickstart.md, the align-the-spec
      remedy, "Ready/Synced unaffected", "no automatic re-tiering"; Conditions table gains a
      `StorageClassDrift` row. `docs/conditions.md`: rows `UpstreamReclassified` |
      StorageClassDrift=True and `Aligned` | StorageClassDrift=False (note: additional
      condition type, S3Bucket only, Argo health unaffected).

**Checkpoint**: all three stories unit-green; `make reviewable` clean.

---

## Phase 6: Polish, validation, release, rollout

- [x] T023 [P] `docs/release-notes/v0.14.0.md` (example-first, short): intro — catalog
      rotation + `standard`; YAML example on `standard`; one-liners: Update no longer
      re-tiers on non-tier edits, class mismatch refused (`StorageClassMismatch`), new
      `StorageClassDrift` condition, `hot` = Premium currently sold out; upgrade note
      "align legacy `hot` manifests to `standard`"; link `docs/s3bucket.md`.
- [x] T024 [P] `docs/upgrading.md` safe-upgrade checklist: one line for v0.14.0 — after
      the bump, expect `StorageClassDrift=True` on buckets bought before 2026-09 and align
      their `storageClass` to `standard` (no PATCH results).
- [x] T025 `make reviewable` (generate + lint + race tests; tree clean) and
      `make validate-examples`; fix anything it raises.
- [x] T026 Staging live gate (`inyan-staging`, e2e context pinned; provider logs scanned):
      `make xpkg.push VERSION=dev-$(date +%s)` + annotation bump; run kuttl 04 (steps 01+02);
      manually apply a `hot`/10 bucket → condition carries `Storage class hot is sold out`,
      nothing in the panel, delete; record results in `specs/028-s3-storage-classes/research.md`
      under R-9 (first API-level confirmation that 5137 is orderable).
- [x] T027 (ticket text drafted in research.md — sending it is the owner's action) Remove the consumed preface `specs/_next-s3-storage-classes.preface.md` (its
      open vendor questions now live in research.md); update the support ticket (RU) with
      the three open questions from research.md.
- [ ] T028 Owner-gated release: tag `v0.14.0` on `main` → `.github/workflows/release.yaml`
      → `ghcr.io/lebedevdsl/provider-timeweb:v0.14.0` (cosign by digest). No push without
      an explicit ask.
- [ ] T029 Production rollout in timeweb-infra (separate repo, direct push to main with
      `(#255)` one-liners): bump
      `deploy-manifests/kubernetes-configuration/crossplane/inyan-infra/provider.yaml` →
      Argo sync → `make preflight KUBECONTEXT=inyan-infra` (CRD enum shows `standard`;
      logs clean); confirm `StorageClassDrift=True` on the 10 legacy buckets;
      `cloud-infra/flick/staging/s3/flick-media.yaml` → `storageClass: standard` →
      bucket Ready (closes #255); then align the 10 legacy manifests (`cloud-infra` ×4,
      `loki` ×1, `s3-provisioning` ×5) → `Aligned`, 0 PATCH calls in logs; `flick-static`
      stays `hot`.

---

## Dependencies & Execution Order

- **Phase 2** blocks everything: T001→T002 (regen), T003→T004 (regen); T005, T006
  parallel with T001-T004.
- **US1 (Phase 3)**: T007/T008/T009 parallel after Phase 2; T010/T011/T012/T013/T014 docs
  parallel with tests (T013 and T018/T022 all touch `docs/s3bucket.md` — run in story
  order, not concurrently).
- **US2 (Phase 4)**: T015 after T005 (reasons) and T007 (fake resolver shape); T016 after
  T015; T017 after T014; T018 after T013; T019 any time after T005.
- **US3 (Phase 5)**: T020 after T001 (condition type) + T005; T021 after T020 and T007;
  T022 after T018.
- **Phase 6**: T023/T024 any time after US3 docs; T025 after all code; T026 after T025;
  T027 after T026; T028 owner; T029 after T028.

### User Story Dependencies

- **US1**: independent (schema + resolver + docs).
- **US2**: shares the fake-resolver refactor (T007) with US1; otherwise independent.
- **US3**: independent of US2 at code level (different functions in `external.go`); the
  kuttl bundle and live gates exercise all three together.

## Parallel Example: Phase 2 + US1

```bash
# Phase 2, concurrently:
Task: "T001 enum + condition type in apis/objectstorage/v1alpha1/types.go"
Task: "T003 hand-patch docs/openapi-timeweb.json"
Task: "T005 reasons in internal/controller/shared/conditions.go + events.go"
Task: "T006 resolver comments"
# then T002 + T004 regen; then US1 tests concurrently:
Task: "T007 fake resolver + Create test"
Task: "T008 match_size_test.go fixture"
Task: "T009 dimensions_test.go standard mapping"
```

## Implementation Strategy

1. Phase 2 → `go build` green, CRD enum shows `standard`.
2. US1 (MVP): `standard` orderable + docs/example/kuttl 04 → unblocks incident #255 on its
   own (a dev tag + the one-word manifest change would already converge it).
3. US2: tier-safe Update — required before anyone edits a legacy bucket's description.
4. US3: drift visibility — makes the fleet-wide alignment auditable.
5. Phase 6: reviewable → staging gate → release → rollout.

## Notes

- Same-file sequencing: `external.go` (T015, T020), `external_test.go` (T007, T016, T021),
  `docs/s3bucket.md` (T013, T018, T022), `docs/conditions.md` (T019, T022) — do in task
  order, never in parallel.
- 027 work is in `git stash@{0}` and overlaps `types.go`/`external.go`/`docs/s3bucket.md`/
  `examples/s3bucket.yaml`/the CRD YAML — never `git stash pop` mid-implementation
  (conflicts expected later, R-10).
- No commits/pushes are made by tasks; the owner batches commits.
