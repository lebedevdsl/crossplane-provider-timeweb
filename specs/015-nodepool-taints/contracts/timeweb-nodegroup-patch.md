# Endpoint Contract: Timeweb node-group taints/labels surface

Probe-derived inventory backing feature 015. The published API documents
none of the taint surface; entries marked *(undocumented)* join the
hand-patched superset in `docs/openapi-timeweb.json`.

## Inventory

| Endpoint | Verb | Documented? | Role here |
|----------|------|-------------|-----------|
| `/api/v1/k8s/clusters/{cid}/groups` | POST | yes (body: partially) | create; body gains `taints` *(undocumented field)*; `labels` already hand-patched |
| `/api/v1/k8s/clusters/{cid}/groups/{gid}` | GET | yes (shape: partially) | Observe; response carries `labels`+`taints` *(undocumented fields)* |
| `/api/v1/k8s/clusters/{cid}/groups/{gid}` | PATCH | **no** *(undocumented verb)* | day-2 metadata convergence |
| `/api/v1/k8s/clusters/{cid}/groups/{gid}` | DELETE | yes | unchanged |

## Probe log (2026-07-10)

1. **Create** (panel host, operator probe):
   `POST .../clusters/1096397/groups` body included
   `"taints":[{"key":"biba","value":"boba","effect":"NoSchedule"}]` → 201;
   created group 117093 echoed the taint verbatim.
2. **Public GET** (api.timeweb.cloud, bearer token, this session):
   `GET .../clusters/1096397/groups` → groups carry `"labels": []`,
   `"taints": []` (group 114393) — public host serializes both.
3. **PATCH** (panel host, operator probe):
   `PATCH .../clusters/1096397/groups/117093` body
   `{name, labels:[{lobel,bobel}], taints:[…], public_ip_enabled, …}` → 200;
   response echoed the added label and the taint set.

## Wire shapes (as observed)

```jsonc
// taint — request and response identical
{"key": "biba", "value": "boba", "effect": "NoSchedule"}

// PATCH request (provider sends OWNED fields only — R-4)
{"name": "<declared>", "labels": [{"key":"…","value":"…"}], "taints": [ … ]}

// response envelope — standard NodeGroupResponse
{"node_group": { …, "labels": […], "taints": […] }, "response_id": "…"}
```

## Behavioral notes / open verifications (live gate)

- **Public-host PATCH**: same path exists publicly with GET/DELETE; the
  PATCH verb is verified there through the provider during validation.
  Fallback if rejected: create-time-immutable CEL (R-3).
- **Absent-field semantics**: panel sends full state; provider sends owned
  fields only — gate asserts autoscaler/count/public_ip survive.
- **Node propagation on PATCH**: join-time vs live re-taint — observed at
  the gate via a read-only kubeconfig node inspection (R-8).
- **Empty-list clear**: `"taints": []` expected to clear; asserted at gate.
- Qrator note: metadata convergence adds no steady-state calls and at most
  one PATCH per reconcile while converging — inside the existing budget.
