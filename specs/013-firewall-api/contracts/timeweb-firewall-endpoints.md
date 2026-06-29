# Contract — Timeweb firewall endpoints (probe-verified 2026-06-28)

Base: `https://api.timeweb.cloud`. Auth: `Authorization: Bearer <token>`. All paths are present in
`docs/openapi-timeweb.json` under the **`Firewall`** tag (the client is hand-written, not
regenerated — see research R-8). Envelopes carry `response_id` + `meta` like the rest of the API.

## Groups

### `POST /api/v1/firewall/groups?policy=DROP|ACCEPT`
Body `FirewallGroupInAPI`:
```json
{ "name": "ingress-lockdown", "description": "InYan default firewall" }
```
→ `201 { "group": firewall-group }`. `policy` is a **query param** (default-deny = `DROP`); it is
**not** in the body and **cannot be changed later**.

`firewall-group`:
```json
{ "id": "<uuid>", "created_at": "...", "updated_at": "...",
  "name": "...", "description": "...", "policy": "DROP" }
```

### `GET /api/v1/firewall/groups` → `{ meta:{total}, groups:[firewall-group] }`
List; used for the create-time adoption guard (match by `name`). *(Live: this account returns
`total:0`.)*

### `GET /api/v1/firewall/groups/{group_id}` → `{ group }`  (404 if absent)

### `PATCH /api/v1/firewall/groups/{group_id}`
Body `{ name, description }` — **policy not patchable** → policy immutable.

### `DELETE /api/v1/firewall/groups/{group_id}`
Delete the group. Expected to cascade rule + resource removal (**verify on first live delete**;
if not, detach resources first). 404 → treat as success.

## Rules

### `GET /api/v1/firewall/groups/{group_id}/rules` → `{ meta, rules:[firewall-rule] }`

### `POST /api/v1/firewall/groups/{group_id}/rules`
Body `FirewallRuleInAPI`:
```json
{ "direction": "ingress", "protocol": "tcp", "port": "22",
  "cidr": "100.64.0.0/10", "description": "ssh from CGNAT" }
```
→ `201 { "rule": firewall-rule }`. `direction ∈ {ingress,egress}`, `protocol ∈ {tcp,udp,icmp}`,
`port` string (omit for icmp), `cidr` IPv4/IPv6.

`firewall-rule` adds `id` + `group_id`.

### `PATCH|DELETE /api/v1/firewall/groups/{group_id}/rules/{rule_id}`
Edit / remove a rule. The controller diffs by the `{direction,protocol,port,cidr}` tuple and
add/removes; PATCH is an optional optimization for description-only edits.

**Quirk**: `port` range delimiter not pinned in the spec (likely `"start-end"`) — confirm on first
live create.

## Resources (service attachment)

### `GET /api/v1/firewall/groups/{group_id}/resources` → `{ meta, resources:[{id,type}] }`
The attached services. (Spec types `id` as integer, but balancer ids are strings — treat as opaque
string; verify rendering for `balancer`.)

### `POST /api/v1/firewall/groups/{group_id}/resources/{resource_id}?resource_type=<type>`
Attach a service. `resource_type ∈ {server, dbaas, balancer, app}` (**live-verified** — the
published enum's `server`-only is stale). `resource_id` is a **string** path param.

### `DELETE /api/v1/firewall/groups/{group_id}/resources/{resource_id}?resource_type=<type>`
Detach a service.

### `GET /api/v1/firewall/service/{resource_type}/{resource_id}` → groups for a resource
Reverse lookup — used to detect 1:1 exclusivity (is this service already in another group?).

**Live enum proof**:
```
GET /api/v1/firewall/service/__invalid__/x
  → 400 "resource_type value is not a valid enumeration member; permitted: 'server','dbaas','balancer','app'"
GET /api/v1/firewall/service/balancer/<id>     → 200
GET /api/v1/firewall/service/load_balancer/<id>→ 400   (it is "balancer", not "load_balancer")
```

## Error classification (reuse `timeweb.Classify`)

| Upstream | Maps to | Reconcile effect |
|---|---|---|
| 200/201 | nil | success |
| 404 (group/rule) | `ErrNotFound` | Observe→not-exists; Delete→success |
| 400 (bad rule / unknown service id) | `APIError` | `Synced=False reason=APIError` (terminal) |
| 409 / "already attached" on POST resources | `ServiceConflict` (terminal) | `Synced=False` — **verify exact code live** |
| 408/409/425/429/5xx, timeouts, transport | `TransientError` | requeue, no condition flap |

## Probe-at-implementation checklist

Live-verified 2026-06-29 (throwaway groups created + deleted; no orphans left):

1. ✅ `port` range delimiter = **hyphen** (`"8000-9000"`), echoed verbatim on create + GET.
2. ✅ icmp rules: created with no `port` (HTTP 201); GET echoes `"port": null`. The controller's
   canonical key drops the port for icmp, so this round-trips up-to-date.
3. ✅ `DELETE group` **cascades** rule removal (group with 2 rules → 204; GET → 404; list total 0).
   No explicit pre-detach needed.
4. ⏳ Exact HTTP code when attaching a service already bound elsewhere — **not probed** (needs a
   real balancer). The controller uses the `GET /firewall/service/{type}/{id}` reverse lookup to
   detect the conflict proactively, so it does not depend on the link error code.
5. ⏳ `GET …/resources` id rendering for `balancer` — **not probed** (no balancer on this account).
   Decoded as an opaque string via `FlexID` (accepts string or number), so either rendering works.
6. ✅ Group/rule envelopes: `{"group":{...}}`, `{"rule":{...}}`, `{"rules":[...]}` — match the
   hand-written client's decode structs. `description: ""` is accepted on create.
