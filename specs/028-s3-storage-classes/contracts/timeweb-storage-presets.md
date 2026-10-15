# Wire contract: Timeweb S3 storage catalog + bucket PATCH (feature 028)

All facts observed live 2026-10-14/15 (provider v0.13.1, account inyan).

## `GET /api/v1/presets/storages`

- `storages_presets[]`: `{id, disk (MB), storage_class, price, location,
  description_short, ...}`.
- `storage_class` values seen: `hot`, `standard`, `cold` (vendored swagger
  enumerates only `cold`, `hot` — **hand-patch**: add `standard` to both
  `storage_class` enums, ~L46478 `PresetsStorage` and ~L46732 `Bucket`;
  re-apply on every regen, see the hand-patch inventory).
- Only location `ru-1` exists. Full list = the nine entries in data-model.md.
  Old ids 2667 (1 GB), 2669 (10 GB), 2671 (100 GB), 4613 (10 GB, 2026-08) are
  gone from the catalog but still referenced by live buckets' `preset_id`.

## `POST /api/v1/storages/buckets`

- `{name, type, preset_id, description?, project_id?}`.
- `preset_id` 5129 (Premium 10 GB) and 5143 (Cold 1 GB) → `409 Conflict`,
  body message `Storage class hot is sold out` / `Storage class cold is sold
  out` (since 2026-10-14 14:43Z). 5137 (Standard 10 GB) is what the panel
  orders today — **to be confirmed at the staging gate** (R-9), never probed
  from the dev network (WAF).

## `GET /api/v1/storages/buckets/{id}`

- `bucket.storage_class` reflects the vendor's **current** class, which can
  change without any API call from us (2026-10-13 reclassification:
  `hot` → `standard` for every bucket on 2667/2669/2671; 4613 stayed `hot`).
- `bucket.disk_stats.size` is **KB, binary**: 1048576 ↔ 1 GB, 10485760 ↔
  10 GB, 104857600 ↔ 100 GB. `is_unlimited: false` on all fleet buckets.
- `bucket.preset_id` keeps the historical id even after it leaves the
  catalog.

## `PATCH /api/v1/storages/buckets/{id}`

- Body fields the provider uses: `bucket_type`, `description`, `preset_id`
  (all optional — `omitempty`). After 028 `preset_id` is sent only for a
  within-class resize.
- Whether `preset_id` across classes performs a paid move is **unverified**
  and will stay unexercised by the provider.

## Error classification (unchanged)

- 409 → `TransientError` with the vendor message appended (`errors.go`
  `isTransientStatus`, deliberate since 006).
