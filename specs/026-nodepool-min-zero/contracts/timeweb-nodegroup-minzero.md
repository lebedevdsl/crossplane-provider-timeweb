# Wire Contract: Timeweb node-group scale-to-zero surface (feature 026)

## Proven (live capture 2026-08-07, panel session)

`PATCH https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups/117127` → 200

Request (panel-shaped; provider sends only its owned subset):

```json
{"name":"ci",
 "labels":[{"key":"inyan.pro/pool","value":"ci"}],
 "taints":[{"key":"inyan.pro/ci","value":"true","effect":"NoSchedule"}],
 "is_autoscaling":true,"is_autohealing":false,
 "min_size":0,"max_size":3,
 "autoscaler_settings":{"scale_down_utilization_threshold":0.5,
   "scale_down_unneeded_duration":300,"scale_down_unready_duration":900,
   "max_node_provision_duration":600,"zero_or_max_node_scaling":false,
   "ignore_daemonsets_utilization":false},
 "public_ip_enabled":false,
 "virtual_router_id":"b09949af-7fa4-40bb-b349-beb814f8cdee"}
```

Response `node_group` (extract): `"is_autoscaling": true, "min_size": 0,
"max_size": 3, "node_count": 2` — bounds echoed synchronously; `node_count`
lags (the autoscaler drains on its own schedule, default
`scale_down_unneeded_duration` 300s).

**Facts established**:

- `min_size: 0` is accepted on the group PATCH and persisted (GET echoes it).
  The published swagger `minimum: 2` on `min_size`/`max_size` is **stale**.
- The provider's owned-fields PATCH discipline (015/024) already excludes
  `autoscaler_settings` and `virtual_router_id` — panel-only fields, never sent
  by the provider, upstream tolerates their absence.

**Quirk (recorded, not acted on)**: the response's `autoscaler_settings.enabled`
reads `false` while `is_autoscaling` is `true` and the pool demonstrably
autoscales. `is_autoscaling` remains the authoritative flag (024 keys on it).
Seeded in `specs/_next-nodepool-autoscaler-settings.preface.md`.

## Provider-owned writes (unchanged shapes)

- Create: `POST /k8s/clusters/{cid}/groups` body includes
  `is_autoscaling: true, min_size, max_size` when declared enabled — `*int`
  pointers, 0 serializes.
- Bounds converge/repair: `PATCH /k8s/clusters/{cid}/groups/{gid}`
  `{"is_autoscaling":true,"min_size":M,"max_size":N}` (024 machinery, 0 now a
  legal M).
- Observe: group GET (`is_autoscaling`/`min_size`/`max_size`/`node_count`) +
  group nodes list (readiness source). No new endpoints.

## Official doc facts (fetched 2026-08-07)

`https://timeweb.cloud/docs/k8s/kubernetes-autoscaling/autoscaling-to-zero-nodes`:

- **Prerequisite**: ≥2 permanently active nodes elsewhere in the cluster (any
  other group(s)) for system components — a `min_size: 0` group cannot be the
  cluster's only capacity (it then never drains; document-only per clarify Q1).
- The doc's walkthrough sets `min_size: 0` **at group creation** (panel).
- Scale-up-from-zero: `Pending` pods targeting the group via
  `nodeSelector`/`nodeAffinity` (group id or custom labels).
- Drain blockers: `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"`,
  PDB restrictions, unsatisfiable rescheduling constraints, controller-less
  pods.
- Drain timing: idle detection (~`scale_down_unneeded_duration`, default 300s),
  then `DeletionCandidateOfClusterAutoscaler` taint, deletion ~2 min later.

## Probes — ALL RESOLVED PASS at the live gate (2026-08-07, inyan-staging
cluster 1096397, gate pool group 122383, provider dev-1786109294)

- **P-1 PASS**: API create with `min_size: 0` accepted; GET echoes 0 (mirror
  `{enabled,0,3}` from first observation).
- **P-2 PASS**: bounds PATCH to `{0,1}` accepted and echoed — `maxSize` floor
  1 stands, no CEL restore needed.
- **P-3 PASS**: pool drained 1→0 in ~8 min after min-0 convergence;
  `Ready=True` at 0 nodes held a 15-min soak with zero corrective writes and
  clean provider logs; `nodeSelector`-pinned Deployment woke the pool from
  zero (pod Running, pool settled at 1 node, Ready=True throughout).
- Also verified live: day-2 bounds 0→2 and 2→0 both converge within one
  reconcile (mirror-asserted); the admission matrix
  (accept `{0,3}`/`{0,1}`/`{2,3}`, reject `{2,1}`/`{0,0}`/`{-1,3}`); delete
  removes the upstream group cleanly.

Original probe table (for the record; shape per clarify Q2):

| ID  | Probe | Pass | Fail handling |
|-----|-------|------|---------------|
| P-1 | `POST` group create with `min_size: 0` (doc-supported, API-unconfirmed) | group created, GET echoes 0 | record wire fact; pivot to explicit two-step (create with upstream-floored bounds, bounds-PATCH to 0 next reconcile) — never a silent spec rewrite |
| P-2 | bounds PATCH / create with `max_size: 1` | 200 + echo | restore `maxSize >= 2` CEL clause pre-release; note in docs |
| P-3 | pinned Deployment scaled to 0 → pool drains to 0 within tolerance (~5 min idle + ~2 min taint-to-delete + deprovision), prerequisite held by the base pool | `node_count: 0`, nodes list empty, pool Ready=True, zero provider count writes | upstream autoscaler gap: RU support ticket + docs warning; provider ships (behavior correct at any settled count) |

## Swagger hand-patch (doc-only, D-4)

`docs/openapi-timeweb.json`: `min_size.minimum` 2→0 and `max_size.minimum`
2→1 on the node-group create (~L42303) and update (~L52269) schemas, each with
the note "(published floor is stale — live probe 2026-08-07 accepts
min_size:0; feature 026)". oapi-codegen ignores validation keywords → no
client regen. Re-apply on any future regen (hand-patched-superset convention).
