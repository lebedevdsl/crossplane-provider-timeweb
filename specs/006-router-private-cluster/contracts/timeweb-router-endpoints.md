# Contract — Timeweb router endpoints (feature 006)

**Source of truth**: `docs/openapi-timeweb.json` after this feature's hand-patch.
The entire router surface is **absent from the published swagger** — every shape
below was verified by live probe (2026-06-10/11) or the feature owner's devtools
capture, following the feature-005 precedent (`/api/v1/configurator/k8s`).

| Endpoint | Method | Status | Used by |
|---|---|---|---|
| `/api/v1/routers` | GET | probed ✅ | (inventory/adoption) `{routers: […]}` |
| `/api/v1/routers` | POST | probed ✅ + capture ✅ | Create |
| `/api/v1/routers/{uuid}` | GET | probed ✅ | Observe `{router: {…}}` |
| `/api/v1/routers/{uuid}` | PATCH | probed ✅ (name/comment only) | Update (rename) |
| `/api/v1/routers/{uuid}` | DELETE | probed ✅ | Delete |
| `/api/v1/routers/{uuid}/networks` | GET | probed ✅ | Observe attachments `{router_networks: […]}` |
| `/api/v1/routers/{uuid}/networks` | POST | probed ✅ | attach `{networks: [{id}]}` → 201 |
| `/api/v1/routers/{uuid}/networks/{network_name}` | PATCH | probed ✅ | per-attachment settings `{is_dhcp_enabled}` |
| `/api/v1/routers/{uuid}/networks/{network_name}` | DELETE | probed ✅ | detach (last → 400 `router_must_have_at_least_one_network`) |
| `/api/v1/routers/{uuid}/networks/{network_name}/nat` | PATCH | official ✅ (re-plan 2026-06-17) | **NAT enable** — body `NatIn{nat_ip}` (required) |
| `/api/v1/routers/{uuid}/networks/{network_name}/nat` | DELETE | official ✅ (re-plan 2026-06-17) | **NAT disable** |
| `/api/v1/presets/routers` | GET | probed ✅ | `DimRouterPreset` fetcher `{router_presets: […]}` |
| `/api/v1/floating-ips` + `/{id}/bind` + `/{id}/unbind` | POST | documented + probed ✅ | FloatingIP refs; bind enum `server,balancer,database,network`,`router` (F-5) |
| NAT activate/deactivate | PATCH / DELETE `/nat` | **RESOLVED (re-plan 2026-06-17)** | FR-004a — see NAT toggle rows above; NOT FIP-bind-to-network |
| Resize (tier change) | — | **CONFIRMED ABSENT (re-plan 2026-06-17)** | FR-002a — `RouterEdit` is name+comment only; immutable-reject is permanent |
| K8s binding | n/a | **resolved (R-5; updated re-plan 2026-06-17)** | private workers = cluster network behind a NAT'd router + a default route via the gateway — **NO `public_ip_enabled`** (dropped, not in official spec). `RouterIn.parent_service{id,type}` is an explicit create-time binding to re-evaluate for US3. K8s presets carry hidden `location`/`availability_zone` (hand-patch + zone-filter `DimKubernetesMasterPreset`/`WorkerPreset`); zone-mismatched preset + `network_id` ⇒ error-yet-created ams-1 zombie (adoption guard required) |

## Create body (verified)

```json
{
  "name": "string (1-250, required)",
  "preset_id": 2009,
  "networks": [{"id": "network-…", "gateway": "10.x.x.4", "reserved_ips": ["10.x.x.5"], "nat": true}],
  "comment": "optional",
  "project_id": 2277851,
  "ips": [{"ip": "create_ip | <existing-floating-ip-address>"}]
}
```

- `networks` min 1; `gateway`/`reserved_ips` optional; **`nat` is silently ignored at create**.
- No zone field — zone derives from the tier (`preset_id`); tiers are per-location.
- `ips[]` accepts an existing floating-IP address (equivalence verified); the
  provider only ever passes referenced FloatingIP addresses, never `create_ip`.

## Official-spec shapes (re-plan 2026-06-17)

After Timeweb published the official OpenAPI spec (merged into
`docs/openapi-timeweb.json`, 207 paths), the following are confirmed:

- **NAT toggle** — `PATCH /api/v1/routers/{router_id}/networks/{network_name}/nat`
  body `NatIn{nat_ip: string}` (**required**) **enables** NAT; `DELETE` on the
  same path **disables** it. Create-time `nat` (in the create body) does NOT
  actually apply — observed `nat_ip` stays `""` after create — so this toggle
  is the only working activation path (was R-3 "capture pending").
- **`RouterEdit`** (the `PATCH /api/v1/routers/{id}` body) is **name + comment
  only** — there is **no resize op** upstream (immutable-reject is permanent).
- **`RouterIn`** (the create body) carries **`parent_service{id, type}`** — an
  explicit create-time service binding (re-evaluate US3 binding vs the prior
  derived-`parent_services` hypothesis; see research re-plan R-5).
- **Network sub-path param is `{network_name}`** upstream (the old hand-patch
  used `{network_id}`). **FLAG**: confirm whether the API keys on the network
  **name** or **id** — the controller currently passes the `network-xxxx` id.
  - **(as-built 2026-06-17)** RESOLVED (T003): the `{network_name}` path param
    is satisfied by the `network-xxxx` id string the controller passes —
    probe-confirmed, since the prior hand-patch used that exact value at the
    same path position and every NAT/DHCP/detach op worked live against it. The
    upstream parameter name is misleading; it is the network id, not the human
    display name (which is a separate field). No controller change needed.
- **`availability_zone`** is now official → drop that hand-patch. The extras
  `/dnat-rules`, `/static-routes`, `/statistics` are official but **out of
  scope** for 006.

## Behavioral contract (probe-verified quirks)

1. **2xx ≠ converged**: router-level PATCH silently ignores `ips`, `networks`,
   `preset_id`; updates while `status=starting` are dropped. Every write is
   verified by re-observation before `ResourceUpToDate` is reported.
2. **New-VPC settle delay**: attach of a seconds-old network → 403
   `networks_location_mismatch` even with matching zones; succeeds ~1 min
   later. Classified transient.
3. **Error-yet-created**: `POST /k8s/clusters` with a `network_id` on a
   router-attached network returns an error body yet creates the cluster
   (reproduced twice). The cluster controller gains a list-by-name adoption
   guard before any create retry; the router controller applies the same
   pattern defensively.
4. Deleting a router detaches (never deletes) its networks; floating IPs
   survive unbound.
