# Investigation: Router update can't enable NAT on a newly attached network

Handoff summary for the fix feature. Live incident: infra/timeweb-infra#135,
found 2026-07-24 while activating the production network (timeweb-infra#132).
Everything below is probe-verified against the real API that day.

## Design philosophy for ALL parts (owner decision, 2026-07-25)

**Do not try to repair what the platform itself has broken.** Where Timeweb's
own flows are inconsistent (the per-attribute snapshot desync of Part 2/3, the
frozen linkage), the provider must NOT paper over them with attribute-flapping,
retries-until-lucky, or hidden recreate logic. Instead it must DETECT the
known-broken state and fail with a condition message that names the exact trap
and the exit, e.g.:

> network was attached to a live router — Timeweb's k8s service will not accept
> worker groups on it (known platform inconsistency, timeweb-infra#135); wire
> the network via the router CREATE body instead.

The bind-before-NAT fix (Part 1) stays in scope — that one is OUR gap, the API
supports the operation. The line: fix provider gaps, guard platform traps.

Known traps to guard explicitly ("где мы встряли", each cost us hours live):
1. NAT via update on an IP the router doesn't own → `ip_not_found` loop.
2. Network attached to a live router → k8s worker groups unfixably rejected
   (`router_required_…` / `router_must_have_nat_ip…` / `router_must_have_dhcp…`
   in sequence, even with everything actually enabled).
3. Cluster created before its network's router wiring existed → linkage frozen
   forever, only recreation helps.
4. `k8sVersion` list drifts upstream (v1.35.4 vanished between IaC-writing and
   create) → validate against `/k8s/k8s-versions` pre-create, name the valid
   versions in the error.

## Symptom

Adding a NAT egress entry to an EXISTING Router CR (GitOps update, not create)
loops forever:

```
CannotUpdateExternalResource ... timeweb: resource not found: IP not found
  (error_code=ip_not_found, 404)
```

Network attach and the DHCP patch of the same update converge fine; only the
NAT step fails. The referenced FloatingIP exists, is Ready, and is unbound.

## Root cause

The create and update paths are asymmetric:

- **Create** (`internal/controller/network/router_external.go`,
  `buildCreateRouterBody`): declared NAT addresses are shipped in the router
  create body as `ips: [{ip: "<addr>"}]` — the router takes ownership of the
  floating IPs at birth, and NAT activation then references an address the
  router owns.
- **Update** (`router_external.go` ~line 452): goes straight to
  `UpdateRouterNat(routerID, networkID, {NatIp: <addr>})` —
  `PATCH /api/v1/routers/{id}/networks/{network_name}/nat`. If the router does
  not own that IP, the API answers 404 `ip_not_found`. There is no attach step.

## Probe results (2026-07-24, all against api.timeweb.cloud)

| Call | Result |
|---|---|
| `PATCH .../networks/{net}/nat` body `{"nat_ip":"<FIP uuid>"}` | 404 `ip_not_found` — the endpoint takes addresses, not ids |
| `PATCH .../networks/{net}/nat` body `{"nat_ip":"<addr>"}` (router doesn't own it) | 404 `ip_not_found` |
| `POST /api/v1/routers/{id}/ips` (any body) | 404 page not found — endpoint does not exist |
| `PATCH /api/v1/routers/{id}` | body is `RouterEdit` (name/comment only) — can't carry ips |
| **`POST /api/v1/floating-ips/{id}/bind` body `{"resource_type":"router","resource_id":"<router-uuid>"}`** | **204. Works.** FIP becomes `resource_type=router`; the provider's next reconcile then converges NAT with no further help |

Doc conflict resolved by the probes: the published OpenAPI (and
`specs/006-router-private-cluster/research.md`) claim the bind enum is only
`server, balancer, database, network`; the contracts table
(`specs/006-router-private-cluster/contracts/timeweb-router-endpoints.md`, F-5)
lists `router` as probed — **the contracts table is right**. `resource_id` for
router binds is the router UUID (string), unlike the numeric ids used for
servers/balancers.

## Fix design

In the router Update path, before calling `UpdateRouterNat` with an address the
router does not own (not present in observed `router.Ips`):

1. Resolve the declared NAT address to its FloatingIP id (the FIP CR's
   external-name; for raw-`ip` declarations, look the id up via
   `GET /api/v1/floating-ips`).
2. `BindFloatingIp(id, {resource_type: "router", resource_id: <router uuid>})`.
   The generated client's `BindFloatingIpResourceType` enum lacks `router` —
   extend it on top of the generated code (do not hand-edit zz_generated).
3. Proceed with the existing `UpdateRouterNat` call (or let the next reconcile
   pick it up — pacing via `maxRouterMutationsPerReconcile` already applies).

Mind the reverse path too: NAT disable currently calls `DeleteRouterNat`; decide
whether to unbind the FIP afterwards (probably NOT — the FIP may be shared
intent / re-enabled; unbinding would also change panel-visible state. Leaving it
bound matches how create-path routers look after a NAT toggle-off).

## Tests

- Unit: update on a router whose observed `Ips` lacks the declared NAT address →
  expect Bind then NAT PATCH (in order); router already owning the address →
  NAT PATCH only, no Bind.
- e2e (live): attach a new network + NAT to an existing router — the exact #135
  scenario. Both staging pools of checks exist in `test/e2e`.

## Workaround used in production meanwhile

Manual `POST /floating-ips/{id}/bind` (resource_type=router) — after which the
deployed provider (v0.9.1) converged on its own. No provider restart needed.

---

# Part 3 (2026-07-24, late): the REAL nodepool↔router mechanism — `virtual_router_id`

Panel capture of a node-group create on the affected cluster shows the dashboard
sends an undocumented field in the create body:

```json
{"node_count":1, "name":"test", "preset_id":1679, "public_ip_enabled":false,
 "virtual_router_id":"<router-uuid>", "is_autoscaling":true, ...}
```

So private worker groups are tied to a router EXPLICITLY per group — not via
some implicit cluster-create linkage. The provider's nodepool Create must
resolve the cluster network's router and send `virtual_router_id` (extend the
generated body — the published OpenAPI lacks the field). This likely also
explains `router.parent_services`: it gains the cluster when a group with
`virtual_router_id` is created, not at cluster create.

Caveat found in the same capture: even the panel's request 400s with
`router_must_have_nat_ip_for_cluster_network` on our cluster, although the
router demonstrably has `nat_ip` for that network (set post-attach via the
Part-1 workaround). Suspected Timeweb-side state desync for NAT added to a
live router; NAT flap (DELETE + re-PATCH …/nat) being probed; if it persists —
Timeweb ticket (response_id e98f2f7b-9aef-4281-ad8c-f723de32717d). Part 2's
create-precondition stays valuable regardless.

# Part 4 (2026-07-25): Router.spec.staticRoutes — declarative router peering

Live production need (timeweb-infra#132): the shared and production routers are
peered through a transit network, with static routes currently hand-managed in
the panel. The provider must own them.

API surface (probed live):
- `GET/POST /api/v1/routers/{id}/static-routes`, body `{subnet: CIDR, nexthop: IPv4}`;
  `DELETE /routers/{id}/static-routes/{routeId}`.
- `GET /routers/{id}/static-routes/available` exists but returned empty in all
  our probes even when valid nexthops existed — do NOT build on it; treat as
  informational until understood.
- Nexthop semantics (panel tooltip + live dropdown): **a neighbor router's
  gateway in a COMMON network** (network attached to both routers) **or a cloud
  server in one of the router's own networks**. Managed-k8s nodes do NOT count
  as cloud servers.

CR design:
- MVP: `spec.forProvider.staticRoutes: [{subnet, nexthop}]`, full
  observe/create/delete convergence with the same mutation pacing as networks.
- Stretch (better DX, gateways are platform-assigned and unknowable in git):
  `{subnet, via: {routerRef: <name>, networkRef: <name>}}` — resolve the
  neighbor Router CR's observed gateway in the referenced common network from
  its `status.atProvider.networks[].gateway`.
- e2e: two routers + common net, add route via each form, assert convergence
  and drift-repair after manual panel deletion.

## Developer answers: how nexthops and routes actually get created

Preconditions, in order (all verified live on the shared↔production pair):

1. **Common network exists and is attached to BOTH routers.** Attachment of the
   transit net carries no NAT and no DHCP (`dhcp: false` on both sides is
   fine — routes don't need either). Attach to the second router goes through
   the normal attach op; the desync trap of Part 2 does NOT apply because no
   k8s cluster lives on the transit net and no NAT/DHCP attributes are
   involved.
2. **Each router auto-assigns itself a gateway IP in the common network at
   attach time.** These gateways ARE the nexthops. They are platform-chosen,
   not settable: read them back from `GET /routers/{id}/networks` →
   `router_networks[].gateway` (also observed on the CR as
   `status.atProvider.networks[].gateway`). Live example:
   - shared router in interconnect 10.13.0.0/24 → gateway `10.13.0.4`
   - production router in interconnect → gateway `10.13.0.6`
3. **Route = {subnet of the REMOTE side, nexthop = the OTHER router's gateway
   in the common net}**, created on each router that needs the path:
   - on shared:     `POST /api/v1/routers/{sharedId}/static-routes`
     `{"subnet": "10.12.0.0/24", "nexthop": "10.13.0.6"}`
   - on production: `POST /api/v1/routers/{prodId}/static-routes`
     `{"subnet": "10.10.0.0/24", "nexthop": "10.13.0.4"}`
   Routes are per-router and asymmetric by design — omitting a prefix on one
   side (we deliberately don't route staging 10.11/24 to production) is a
   policy tool, not an error.
4. **Nexthop validity rule** (panel tooltip, dropdown-confirmed): the nexthop
   must be (a) another virtual router's gateway in a network shared with this
   router, or (b) a cloud server (VDS) IP inside one of this router's own
   networks. Managed-k8s nodes are not valid nexthops. There is no
   router-to-router peering WITHOUT a common network.
5. **Resolution algorithm for the `via:` stretch form**: given
   `via.routerRef=R2, via.networkRef=N` on router R1 — assert N ∈ R1.networks
   AND N ∈ R2.networks (else precondition condition, requeue); nexthop :=
   R2.atProvider.networks[N].gateway; create when non-empty. Re-resolve on
   drift: gateways can change if the network is ever re-attached (observed:
   re-attach reassigned the production net's gateway 10.12.0.4→10.12.0.9).

Verified live 2026-07-25 (the production peering was created through exactly
these calls):

- `POST /routers/{id}/static-routes` `{"subnet":"10.12.0.0/24","nexthop":"10.13.0.6"}`
  → 200 `{"static_route":{"id":"<uuid>","nexthop":"…","subnet":"…"},"response_id":"…"}`
  (route id is a UUID string).
- `GET /routers/{id}/static-routes` → `{"meta":{"total":N},"static_routes":[StaticRouteOut…]}`.
- Both directions created first-try; no ordering constraints beyond the common
  network existing.

Still unverified (probe before relying): route UPDATE (assume delete+create);
duplicate-subnet behavior; whether `/static-routes/available` ever populates
(empty in every live probe even with valid nexthops present — do not build on
it).

# Part 5 (2026-07-25): multi-router network attachment is a supported pattern

Verified live: a Network CAN be attached to SEVERAL routers simultaneously
(interconnect 10.13.0.0/24 sits on both `shared` and `production`); this is the
official peering mechanism (see Part 4 tooltip). Requirements:

1. Two Router CRs may list the same `networkRef` — attach/detach/observe must
   scope strictly to the CR's own router id (never "fix" another router's
   attachment of the same network as drift).
2. e2e: common net on two routers → delete one router → the net must stay
   attached to the survivor; no reconcile flapping on either CR.
3. Known platform wart to keep out of the provider's way: per-attachment
   gateway/reserved IPs may LEAK on the network after detach (live case:
   10.12.0.4 + 10.12.0.9 stayed in busy_address after the production net left
   the shared router). Don't treat leaked reservations as provider-owned drift.

# Part 6 (small): expose cluster_network_cidr on KubernetesCluster

The upstream k8s create body accepts `cluster_network_cidr.{pods_network,
services_network}` (create-only). The CRD lacks it — add as immutable optional
fields. Motivation: distinct pod/service CIDRs per cluster make cross-cluster
debugging (Hubble/logs) unambiguous; wanted for the NEXT cluster create
(production 2026-07-25 shipped on defaults because the field was missing).

# Part 2: cluster↔router linkage is frozen at cluster-create time

Second finding from the same rollout. Timeweb links a router to a k8s cluster
(`router.parent_services[{type: "k8s"}]`) **only when the cluster is created**,
and only if the cluster's network is router-wired WITH NAT at that moment. Our
production cluster was created while its network was attached but NAT-less →
`parent_services` never gained the cluster, and every private nodepool create
fails forever:

```
400 router_required_for_worker_groups_without_public_ip
```

No public API re-links an existing cluster (probed nothing; panel unknown). The
only recovery is deleting and recreating the cluster — acceptable for an empty
cluster, catastrophic later. The provider must make this state unreachable:

1. **Create-precondition on KubernetesCluster** (the core fix): before
   `POST /k8s/clusters`, resolve the referenced Network and verify router
   wiring via `GET /routers/…/networks` (entry for the network with non-empty
   `nat_ip`). If the network is attached to a router but NAT-less → refuse to
   create with an explicit condition message ("network X is router-attached
   without NAT; a cluster created now will never link to the router and
   private nodepools will fail router_required_for_worker_groups_without_public_ip
   — enable NAT first"). Requeue, not terminal: once NAT appears the create
   proceeds. A network with NO router at all stays allowed (public-only
   clusters are legitimate) but emits a Warning event.
2. **Friendly classification on the nodepool side**: map error_code
   `router_required_for_worker_groups_without_public_ip` to a condition message
   that names the real cause (cluster→router linkage missing, frozen at
   cluster-create; check `router.parent_services`; recreation is the only fix)
   instead of the raw 400 retry loop.
3. **Observability**: surface `status.atProvider.routerLinked` on
   KubernetesCluster so the broken state is visible on the CR without API
   spelunking. Note: the Router CR ALREADY observes `parentServices` in its
   `status.atProvider` (verified live 2026-07-24) — the observe plumbing for
   linkage data exists; the cluster-side check can reuse the same API read.
4. **e2e**: canonical ordering test (network → router attach + NAT → cluster →
   private nodepool) plus the negative case asserting the precondition blocks
   a NAT-less create.

Part 1's bind fix removes the main way to reach this state via GitOps (NAT
converges before anyone creates a cluster), but ordering races remain possible
— e.g. cluster and network landing in one sync wave — so the precondition is
what actually closes it.
