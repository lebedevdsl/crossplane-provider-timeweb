# Implementation Plan: S3 storage classes — catalog rotation (`standard`), tier-safe updates, drift visibility

**Branch**: `028-s3-storage-classes` | **Date**: 2026-10-15 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/028-s3-storage-classes/spec.md`
(1 clarification recorded 2026-10-15: fail-closed on class mismatch, option A)

## Summary

Target **v0.14.0** (minor — new enum value), NON-BREAKING, bugfix for
infra/timeweb-infra#255. Timeweb rotated the S3 preset catalog on 2026-10-13:
a third class `standard` (the panel's "Standard", successor of the old `hot`
tier) exists, `hot` now means "Premium" and is `409 sold out`, `cold` only at
1 GB. The provider's enum (`hot;cold`) cannot order anything sellable, and a
live read shows **10 of 11** Ready buckets reclassified to `standard` while
their manifests say `hot` (R-1 — the preface's "four" was undercounted). Four
deltas:

1. **Enum + generated client**: `storageClass` ∈ `hot;standard;cold`, field docs
   by panel names, swagger hand-patch + regen (D-1, D-2).
2. **Tier-safe Update**: send `preset_id` only for a within-class resize
   (upstream size KB/1048576 ≠ `initialSizeGB`); refuse fail-closed when the
   declared class ≠ upstream class (`StorageClassMismatch`); never consult the
   stale `lockedPresetID` (D-3).
3. **Drift visibility**: Observe sets a dedicated `StorageClassDrift` condition
   (`True/UpstreamReclassified`, later `False/Aligned`), `Ready`/`Synced`
   untouched, no auto-repair (D-4).
4. **Docs/e2e/tests** on `standard`; resolver hint already class-bearing —
   pinned by test (D-5..D-8).

## Technical Context

**Language/Version**: Go (latest stable, per repo tooling policy)

**Primary Dependencies**: crossplane-runtime v2, controller-runtime, oapi-codegen
generated Timeweb client (`internal/clients/timeweb`), catalog resolver
(`internal/controller/shared/resolver`)

**Storage**: N/A (state lives in the CR + upstream API)

**Testing**: `go test` with fake Timeweb client + fake resolver (Constitution
III), kuttl bundle 04 on `standard`, live gates on `inyan-staging` then
`inyan-infra` post-release

**Target Platform**: Kubernetes (Crossplane provider), amd64 image

**Project Type**: Crossplane provider (single Go module)

**Performance Goals**: N/A — Update makes fewer catalog calls than today

**Constraints**: 2 r/s shared limiter (018); 409 stays transient (006/019);
Observe read-only (Constitution II); no assignment/transfer APIs (027 boundary);
no `Ready` degradation on upgrade day for the 10 serving buckets

**Scale/Scope**: 1 kind (`S3Bucket`), 1 resolver test file, ~6 source files +
regen + 6 docs + 1 kuttl bundle; 12-bucket production fleet in 3 namespaces

## Constitution Check

*GATE: evaluated pre-Phase-0; re-evaluated post-Phase-1 — PASS both.*

- **I. CRD Contract Stability**: v1alpha1; additive enum value; every existing
  manifest stays valid; CRD YAML + DeepCopy regenerated in the same PR. PASS.
- **II. Idempotent, Side-Effect-Aware Reconciliation**: Observe stays read-only
  (drift is computed from the existing GET). Update becomes *more*
  conservative: no tier write unless the size demonstrably differs; the
  mismatch refusal sends nothing. Error classification untouched (409
  transient, message surfaced); not-found rule untouched. The refusal is a
  classified terminal error with a reason + Event, never swallowed. PASS.
- **III. Controller Test Discipline**: fake-client unit matrix for Create /
  Update (3 branches) / Observe (4 drift states) plus the resolver fixture;
  existing not-found/transient/terminal cases kept. PASS.

## Design decisions

- **D-1 enum + docs** (`apis/objectstorage/v1alpha1/types.go:43-45,98`,
  `doc.go:20`): `Enum=hot;standard;cold`; field doc names the panel tiers and
  states "`standard` recommended; `cold` 1 GB only; create-time choice — day-2
  mismatch refuses updates". `make generate-crds`.
- **D-2 swagger + client** (`docs/openapi-timeweb.json` ~L46478, ~L46732):
  add `"standard"` to both `storage_class` enums; `make generate-client` →
  `BucketStorageClassStandard`, `PresetsStorageStorageClassStandard`. Add to
  the hand-patch re-apply inventory. Behaviour unchanged (R-2).
- **D-3 Update** (`internal/controller/s3bucket/external.go` `Update`): after
  the `name` immutability check — (i) `observedClass != "" && observedClass !=
  spec.StorageClass` → `SyncedFalse(shared.ReasonStorageClassMismatch, msg)` +
  Warning Event + return `ErrStorageClassMismatch` (package sentinel), no
  request; (ii) `observedGB := int64(bucket.DiskStats.Size) / 1048576`, size
  known iff `!IsUnlimited && Size > 0`; `observedGB != spec.InitialSizeGB` →
  `resolvePresetID` + `PresetId` in body + refresh `LockedPresetID`;
  (iii) else body without `PresetId`, no resolver call, lock untouched
  (table in data-model.md). `lockedPresetID` is never an input (R-1: stale
  on `gitlab-backup`).
- **D-4 Observe drift** (`external.go` new `setStorageClassDriftCondition`,
  called after `populateStatus`): condition type
  `objectstoragev1alpha1.TypeStorageClassDrift = "StorageClassDrift"` (declared
  in `apis/objectstorage/v1alpha1`, documented in the CRD doc comment);
  reasons `shared.ReasonUpstreamReclassified` / `shared.ReasonStorageClassAligned`
  (`shared/conditions.go`; `Aligned` added to `normalReasons` in
  `shared/events.go`). Set `True` on drift; set `False` only when
  `cr.GetCondition(TypeStorageClassDrift).Status == True`-or-False already
  present (never add to a clean bucket); `RecordConditionChange` for Events.
  `isUpToDate` unchanged (R-4).
- **D-5 resolver hint**: no code change; `describeEntry` already emits
  `10GB/ru-1/standard` (R-5). Pinned by a new `match_size_test.go` on the
  2026-10-15 fixture (FR-012). Comments in `match_size.go:34` / `slug.go:34`
  updated to `hot | standard | cold`.
- **D-6 docs** (R-7): `docs/s3bucket.md` (field table: `initialSizeGB` mutable
  within class, `storageClass` create-time + mismatch rule; status
  `storageClass` values; Conditions table + new `StorageClassDrift` section
  replacing the wrong "Immutable-field handling" claims), `docs/presets.md`
  (per-class tier table, example on `standard`, class-bearing hint sample,
  locked-preset semantics incl. "never consulted"), `docs/conditions.md`
  (`StorageClassMismatch`, `StorageClassDrift`), `README.md` sizing table,
  `examples/s3bucket.yaml` (`standard`, comments), `docs/release-notes/v0.14.0.md`
  (example-first). `hot` worded "Premium — not orderable since 2026-10-14".
- **D-7 tests** (R-8): `external_test.go` fake resolver keyed by
  `(DiskGB, StorageClass)`; `sampleBucketJSON` variants for `standard` and
  for 100 GB; Create/Update/Observe matrix; `resolver/match_size_test.go`
  fixture; `dimensions_test.go` fetch maps `standard`.
- **D-8 e2e**: `test/e2e/kuttl/tests/04-s3bucket/01-apply.yaml` →
  `storageClass: standard`; new `02-patch-description.yaml` + assert
  (`kubectl wait --for=condition` idiom + script check of
  `status.atProvider.storageClass == standard`).
- **D-9 preface seeds**: `specs/_next-s3-storage-classes.preface.md` is
  consumed by this feature (delete on merge, as prior rounds did). Open
  vendor questions (R-10 list) go to the support ticket; an explicit
  cross-class move stays unseeded until the vendor answers whether PATCH
  `preset_id` performs one.

## Project Structure

### Documentation (this feature)

```text
specs/028-s3-storage-classes/
├── plan.md              # This file
├── spec.md              # Feature spec (wire facts, clarify Q1=A, fleet count amended)
├── research.md          # Phase 0 — R-1..R-10
├── data-model.md        # Phase 1 — enum/status/condition deltas + Update decision table
├── quickstart.md        # Phase 1 — operator walkthrough + rollout order
├── contracts/
│   ├── s3bucket-storageclass-v1alpha1.md   # CRD + reconcile contract
│   └── timeweb-storage-presets.md          # wire facts: catalog, POST 409, GET units, PATCH
└── checklists/requirements.md
```

### Source Code (repository root)

```text
apis/objectstorage/v1alpha1/types.go                         # D-1 enum + docs; D-4 condition type
apis/objectstorage/v1alpha1/doc.go                           # D-1 comment
apis/objectstorage/v1alpha1/zz_generated.deepcopy.go         # regen (no-op expected)
package/crds/objectstorage.m.timeweb.crossplane.io_s3buckets.yaml  # regen
docs/openapi-timeweb.json                                    # D-2 hand-patch
internal/clients/timeweb/generated/zz_generated_client.go    # regen (new consts)
internal/controller/shared/conditions.go                     # D-3/D-4 reasons
internal/controller/shared/events.go                         # D-4 normalReasons
internal/controller/shared/resolver/match_size.go, slug.go   # D-5 comments
internal/controller/shared/resolver/match_size_test.go       # D-5/D-7 fixture
internal/controller/shared/resolver/dimensions_test.go       # D-7
internal/controller/s3bucket/external.go                     # D-3 Update, D-4 Observe
internal/controller/s3bucket/external_test.go                # D-7
docs/s3bucket.md, docs/presets.md, docs/conditions.md, README.md   # D-6
docs/release-notes/v0.14.0.md                                # D-6
examples/s3bucket.yaml                                       # D-6
test/e2e/kuttl/tests/04-s3bucket/                            # D-8
```

**Structure Decision**: existing provider layout; no new packages. One new
test file (`match_size_test.go`) if none exists for that unit.

## Validation

- **Unit** (Constitution III): the D-7 matrix; `make reviewable` (regen clean,
  lint, race tests).
- **kuttl 04** on `standard` + description-patch step.
- **Staging live gate** (`inyan-staging`, dev tag `make xpkg.push
  VERSION=dev-$(date +%s)`, annotation bump to re-pull): create
  `standard`/10 → Ready; edit description → tier unchanged by re-observation,
  provider logs clean (read-logs-in-validation convention); apply `hot`/10 →
  condition carries `Storage class hot is sold out`, nothing in the panel;
  delete all.
- **Release**: tag `v0.14.0`; notes example-first; `docs/upgrading.md` gets
  the one-line "align legacy `hot` manifests to `standard`" step.
- **Production rollout** (timeweb-infra, direct-push-to-main repo):
  provider bump → preflight → `flick-media.yaml` → `standard` → 10 legacy
  manifests → `standard`; verify `StorageClassDrift` True→False and 0 PATCH
  calls in logs. Incident #255 closes when `flick-media-staging` is Ready.

## Risks

- `standard`/10 (5137) orderability is asserted from the panel, not yet from
  the API — the staging gate is the first API-level confirmation (R-9).
- Stash overlap with 027 on `types.go` / `external.go` / `docs/s3bucket.md` /
  `examples/s3bucket.yaml` / CRD YAML — expected conflicts when `stash@{0}` is
  popped later (R-10).

## Complexity Tracking

No constitution violations — table not needed.
