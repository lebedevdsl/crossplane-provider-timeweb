# Investigation: official Timeweb OpenAPI refresh (2026-10-15) vs vendored `docs/openapi-timeweb.json`

Handoff for the next spec-regen feature. Compared the official document downloaded
2026-10-15 (`openapi (3).json`, 3.3 MB, 241 paths, 326 schemas, 24 tags) against the
vendored hand-patched copy (2.8 MB, 208 paths, 270 schemas, 23 tags; last touched
2026-08-07 in `91c4229`). Both say `info.version: 1.0.0`; neither carries a date — the
upstream snapshot date under our patches is unverified (first vendored 2026-05-19,
patch layers from 2026-06-10 on). Method: structural diff of paths/operations and of
resolved request/2xx-response schemas for the 9 generated tags plus `Firewall` and
`Локации`; then `oapi-codegen` v2.3.0 run on the new doc with the exact `make
generate-client` post-processing, and the repo compiled against that output via
`go build -overlay` (nothing in the repo was modified).

## 1. Verdict in one paragraph

Upstream changed almost nothing in the surfaces we generate: no shared operation
changed shape except where OUR hand-patches diverge. Every hand-patch is still needed
(none was adopted upstream); two of them now read as explicit conflicts with the
published text (`min-size`/`max-size` naming + minimum, `storage_class` enum). The one
material upstream addition is a documented **CDN** tag covering exactly the
undocumented surface `internal/clients/timeweb/cdn.go` was written against. A regen
from the raw official doc breaks the build in three places (all hand-patch removals)
and needs a `counterfeiter` re-run for five new server restore-point methods.

## 2. Upstream changes relevant to the provider

| Kind | Change | Evidence (new doc) |
|---|---|---|
| Project | `databases[].type` enum: `redis7`/`redis8_1`/`opensearch` dropped; `valkey7`/`valkey8_1`/`valkey9_1`/`postgres18` added. `databases[].config_parameters` became a 3-way `oneOf` (mysql/postgres/valkey). | `getAllProjectResources`, `getAccountDatabases`, `getProjectDatabases` |
| Project | `PUT /projects/{id}/resources/transfer` unchanged (`resource_type` enum `server,balancer,database,kubernetes,storage,dedicated`). | path present, no diff |
| SshKey | no change | — |
| S3Bucket | no change to create/update/get bodies (`preset_id`/`project_id` still `type: number` → `float32`, the 027 finding stands). `storage_class` enum still `hot,cold` (see hand-patch table). `configurator` object on create was already there. | `createStorage`, `getStorage` |
| S3User | `/api/v1/storages/users` GET/PATCH unchanged; **`/api/v2/storages/users` (what `storages_users_v2.go` uses) is still absent** from the official doc. | grep `v2/storages`: 0 hits in both |
| ContainerRegistry | no change | `createRegistry` props identical |
| Server | 5 new ops under `Облачные серверы`: `GET /restore-points`, `GET/POST /restore-points/{vds_id}{,/create,/commit,/rollback}` (new types `RestorePoint`, `RestorePointStatus`, `VdsId`). `boot_mode: single` marked deprecated in the description. Create body unchanged (`project_id`/`preset_id`/`os_id` still `number`). | `getRestorePoints` … `rollbackRestorePoint`; `updateServerOSBootMode` |
| Network/VPC | no change | — |
| FloatingIP | no change; bind `resource_type` enum still `server,balancer,database,network` (no `router`). | `bindFloatingIp` |
| KubernetesCluster | no change outside our patches: `configuration` still has no `gpu`; `cluster_network_cidr`, `maintenance_slot`, `oidc_provider`, `network_id` unchanged; `availability_zone` enum still `spb-3,msk-1,ams-1,fra-1`. `GET /configurator/k8s` still absent. `PATCH …/clusters/{id}` still has no `virtual_router_id`. | `createCluster`, `updateCluster` |
| Nodepool | `NodeGroupIn` still `min-size`/`max-size` (dash) with `minimum: 2`, no `public_ip_enabled`, no `taints`. `PATCH …/groups/{group_id}` still absent. `GET …/groups` item still only `id,name,created_at,preset_id,node_count` (read-back stays on the hand-written `nodeGroupEnvelope`). `POST …/clusters/{id}/container-registry` still absent. | `createClusterNodeGroup`, `getClusterNodeGroups` |
| Router | no change (static routes `GET/POST …/static-routes`, `GET …/static-routes/available`, `DELETE …/static-routes/{id}` all already present and identical). | — |
| CDN | **New `CDN` tag, 19 operations** (`/api/v1/cdn/http-resources*`, `/cdn/certificates*`, `/cdn/presets`, `/cdn/nodes/*`), 12+ new schemas (`http-resource`, `http-resource-config`, `config-*`, `certificate`, `certificate-task`, `preset`). Matches `cdn.go` field-for-field; extras we do not model: create-time `delivery_domain`, `config.access.allowed_methods`, `/statistics`, `/clear-cache`, `/preload-cache`, `/suspend`, `/resume`, `/nodes/*`. Documented semantics agree with our live findings: cert upload `POST /cdn/certificates` → **204** (no id); LE issue → **202**, tracked via `/cdn/certificates/tasks` (`status: in_progress,success,failed`); `GET /cdn/certificates?resource_id=` filters to certs matching the resource's domains; 422 on cert/key mismatch or missing CNAME. `http-resource.status` enum `created,processing,stopped,failed,no_paid,blocked,traffic_limit_exceeded` (016's "sticks at `processing`" quirk is not contradicted by the doc). New `unprocessable-entity` component response (CDN-only). | `createCdnResource`, `addCdnCertificate`, `issueCdnCertificate` |
| Firewall | was ALREADY in the vendored doc (tag `Firewall`, 7 paths) — "undocumented" in `CLAUDE.md`/013 means "not in our generated tags", not "absent upstream". No diff old→new. `resource_type` enum still `server` only (live: `server|dbaas|balancer|app`, 013). | `/api/v1/firewall/*` |
| Locations | `/api/v2/locations` `location` enum grew: `ru-4`, `ru-5`, `de-1`, `us-4` added (old enum: `kz-1,nl-1,pl-1,ru-1,ru-2,ru-3`). CRD enums (`router_types.go:109`, `floatingip_types.go:37`, `network_types.go:47`) list `ru-1;ru-2;ru-3;nl-1;de-1;kz-1;us-4;pl-1` — missing `ru-4`, `ru-5` (live existence unverified). | `getLocations` |
| Other tags (not ours) | `/api/v1/dbs/*` ops removed in favour of `/api/v2/databases/*`; `Домены` subdomain path param renamed; new `AI Агенты`, persons, DB backups/replicas. Not generated, no effect. | only-old / only-new op lists |

## 3. Hand-patch verdict table

Every patch is `STILL-MISSING` upstream; none can be dropped. "CONFLICT" = the
official text now says something different, not merely nothing.

| # | Hand-patch (vendored doc location) | Verdict | Upstream evidence (new doc) |
|---|---|---|---|
| 1 | `GET /api/v1/configurator/k8s` (whole op, `getK8sConfigurators`; 005) | STILL-MISSING | path absent; only `/configurator/servers`, `/configurator/databases` (new) |
| 2 | `servers-configurator.tags` (string[]; 005) | STILL-MISSING | `getConfigurators` item has no `tags` |
| 3 | `NodeGroupIn.min_size`/`max_size` renamed from `min-size`/`max-size` (006/`675fb09`) | CONFLICT | upstream still publishes dashed `min-size`/`max-size` on both `createCluster.worker_groups[]` and `createClusterNodeGroup`; live API accepts only underscore (2026-06-23) |
| 4 | `min_size` minimum 0 / `max_size` minimum 1 (026) | CONFLICT | upstream `minimum: 2` on both; live PATCH probe 2026-08-07 accepted 0 |
| 5 | `NodeGroupIn.public_ip_enabled` (006) | STILL-MISSING | not in `createClusterNodeGroup` body nor `worker_groups[]` |
| 6 | `NodeGroupIn.taints[]` + `Taint` schema (015) | STILL-MISSING | absent; `Taint` is only-old schema |
| 7 | `PATCH /api/v1/k8s/clusters/{cluster_id}/groups/{group_id}` + `NodeGroupUpdate` (015/020-era, autoscaling triple 2026-07-26) | STILL-MISSING | only `DELETE`/`GET` on that path |
| 8 | `createCluster.configuration.gpu` (nullable int, required; 005 `675fb09`) | STILL-MISSING | `configuration` = `configurator_id,cpu,ram,disk`, required lacks `gpu` |
| 9 | `updateCluster.virtual_router_id` (nullable string; 021 `2a9af40`) | STILL-MISSING | `PATCH /k8s/clusters/{id}` body has no such field |
| 10 | `bindFloatingIp.resource_type` enum `+router` (020 `acad3b3`) | STILL-MISSING | enum `server,balancer,database,network`; `floating-ip.resource_type` read-back enum likewise (controller reads it as a string, unaffected) |
| 11 | `storage_class` enum `+standard` on `storages-preset` and `bucket` (028, 2026-10-15) | CONFLICT | upstream enum still `hot,cold` on all 8 occurrences; live catalog returns `standard` |
| — | `POST /api/v1/k8s/clusters/{id}/container-registry` (memory: deferred, never patched) | STILL-MISSING | absent |
| — | `/api/v2/storages/users` (hand-written `storages_users_v2.go`, never patched) | STILL-MISSING | absent |
| — | Firewall `resource_type` `server\|dbaas\|balancer\|app` (hand-written `firewall.go`, never patched) | STILL-MISSING (enum still `server`) | `/firewall/groups/{id}/resources/{rid}` and `/firewall/service/{resource_type}/{id}` |
| — | CDN surface (hand-written `cdn.go`, never patched) | UPSTREAM-ADOPTED | new `CDN` tag — candidate to move to the generated client |

## 4. Regen risk list (`make generate-client` on the raw official doc)

Generator ran clean (no new invalid identifiers; the existing `sed`/alias
post-processing still applies; `K8sPresetItem` rename and the `v.Type = "worker"`
drop still hit). Output grows 32 863 → 37 723 lines. Compile result via overlay:

| Change in `zz_generated_client.go` | Effect |
|---|---|
| `GetK8sConfigurators*` removed | **build break** `internal/controller/shared/resolver/dimensions.go:100` (+ `resolver_test.go`) |
| `ServersConfigurator.Tags` removed | **build break** `dimensions.go:449-450` |
| `UpdateClusterNodeGroup*`, `NodeGroupUpdate`, `Taint` removed | **build break** `internal/controller/kubernetes/nodepool_external.go` (+ test), `internal/clients/timeweb/fake.go` |
| `NodeGroupIn.PublicIpEnabled`, `.Taints` removed; `ClusterIn.Gpu` removed; `MinSize`/`MaxSize` json tags revert to `min-size`/`max-size` | compile may pass where the controller builds raw maps, but the **wire body would regress** (dash keys, no gpu) — verify with the 027 wire-level create-body tests |
| `BindFloatingIpResourceTypeRouter` const removed | **build break** `internal/controller/network/router_external.go:1261` |
| `BucketStorageClassStandard`, `PresetsStorageStorageClassStandard` consts removed | compiles (controllers compare strings), but `string(bucket.StorageClass)` keeps working only because the type is a string alias; 028 typed-const usage, if any lands, would break |
| 5 new `RestorePoint*` methods on `ClientInterface` | **build break** in `fake.go` (`*FakeClient does not implement … missing method CommitRestorePoint`) until `counterfeiter` is re-run |
| `DbType` consts renamed `Clickhouse` → `DbTypeClickhouse` etc. (new `Mysql`/`Postgres`/`Valkey` schema types collide, so the generator prefixes) ; `ConfigParameters` → `Db_ConfigParameters` union | no controller reference; harmless. Note `generated/cfg.yaml` declares `always-prefix-enum-values: true` but the Makefile passes CLI flags and never reads `cfg.yaml` — the two are out of sync. |
| `Bucket`, `PresetsStorage`, `FloatingIp`, `Vpc`, `Project` structs | unchanged |
| Server `project_id`/`preset_id` still `float32` | unchanged (027 item stands) |

Not a regen risk but adjacent: the `CDN` tag would generate cleanly if added to
`-include-tags` (its `oneOf`s are inside `Db_ConfigParameters`, not CDN); the
`unprocessable-entity` response is a normal named component.

## 5. What to do in the next feature

1. Re-vendor: take the 2026-10-15 official doc as the new base and re-apply patches
   1–11 as a scripted overlay (e.g. a JSON-patch file + `make` step) instead of
   hand-edits, so every future refresh is `download → apply → generate → build`.
   Keep each patch's `HAND-PATCH` note; add the two CONFLICT notes verbatim.
2. Re-run `counterfeiter` for `fake.go` (new restore-point methods) in the same step.
3. Add `CDN` to `-include-tags`, regenerate, and migrate `cdn.go` to the generated
   types where they match; keep the hand-written layer only for the 204-no-id cert
   upload flow and the `?resource_id=` cert listing quirk (both now documented, so the
   017 "LE issue path UNVERIFIED" warning can be downgraded to "documented, our
   `cert_issue_incorrect_dns` handling still live-only").
4. Decide on `Firewall` tag inclusion the same way (schema is stable; hand-written
   client stays for the stale `resource_type` enum unless patched).
5. CRD location enums: add `ru-4`, `ru-5` only after a live `/api/v2/locations`
   probe confirms they are orderable (feeds `specs/_next-location-az-presets.preface.md`).
6. Ticket to Timeweb (RU) listing the 11 doc gaps above, with the two CONFLICTs first
   (`min-size` naming/minimum, `storage_class: standard`) — both are now actively
   wrong, not merely missing.
7. Reconcile `generated/cfg.yaml` with the Makefile (either use `-config` or delete
   the file).

## 6. Open questions

- Upstream base date of the vendored copy: unverified (no `info.x-*` date in either
  file). Treat the 2026-10-15 download as the new baseline.
- Whether `ru-4`/`ru-5` are live-orderable regions or doc-only placeholders: unverified.
- Whether upstream intends `hot`/`cold` to remain the only documented storage classes
  (i.e. `standard` is a catalog-only alias) — support ticket from 028 pending.
- Whether `valkey*` replacing `redis*` in the Project resource listing affects anything
  the Project controller displays: no reference found in `internal/`, unverified for
  `docs/`.
