# Next feature preface: router/k8s remaining traps (post-v0.10.0)

> **Trimmed 2026-07-25 after v0.10.0 shipped**: staticRoutes (§3), multi-router
> peering (§4), clusterNetworkCIDR (§5), NAT declarative release (§6) and the
> x/text vuln (§7) are RELEASED in 021/v0.10.0 — see
> `specs/021-router-peering-routes/`. What remains below is the probe-blocked
> trap-guard work (§1–§2) and the ticket/probe backlog.

Everything router-related from the timeweb-infra#132/#135 rollout that did NOT
ship in v0.9.2 (which carries only the Part 1 bind-before-NAT bugfix). Source:
`specs/_next-router-nat-bind.preface.md` (Parts 2–6 + owner design philosophy,
2026-07-25) and the consolidated findings comment on infra/timeweb-infra#135.
The 020 branch briefly implemented an earlier Part 2 shape (cluster
create-precondition on NAT-less wiring, `routerLinked`, nodepool
"recreate-only" classification) and REVERTED it — reuse the ideas, not the
premises; the reverted code is in the 020 branch history for reference.

## Governing philosophy (owner decision, 2026-07-25)

**Fix provider gaps; guard platform traps.** Where Timeweb's own flows are
inconsistent (post-attach snapshot desync, frozen linkage), the provider must
NOT paper over with attribute-flapping, retries-until-lucky, or hidden
recreates. It DETECTS the known-broken state and fails with a condition naming
the exact trap and the exit (e.g. "network was attached to a live router —
Timeweb's k8s service will not accept worker groups on it (known platform
inconsistency, timeweb-infra#135); wire the network via the router CREATE body
instead").

## 1. Nodepool `virtual_router_id` — the REAL private-pool↔router binding (from Part 3)

Panel capture: node-group create sends undocumented
`virtual_router_id: "<router-uuid>"` (alongside `public_ip_enabled: false`).
Private worker groups bind to a router EXPLICITLY per group — not via any
implicit cluster-create linkage; `router.parent_services` likely gains the
cluster from this, not from cluster create.

- Nodepool Create (when `publicIP: false`): resolve the cluster network's
  router and send `virtual_router_id` (extend the create body — published
  OpenAPI lacks the field; hand-patch + regen or body-extension per the
  established pattern).
- CAVEAT (probe): even the panel's request 400s
  `router_must_have_nat_ip_for_cluster_network` on a cluster whose network got
  NAT post-attach — snapshot desync upstream (response_id
  e98f2f7b-9aef-4281-ad8c-f723de32717d; NAT-flap probe inconclusive; Timeweb
  ticket). So `virtual_router_id` is necessary but NOT sufficient on
  post-attach networks — see trap guard §2.

## 2. Trap guards (reshaped former Part 2)

Known traps, each guarded by DETECT + condition naming trap and exit (never
auto-repair):

- **Post-attach network trap**: a network attached to a LIVE router is
  rejected by the k8s service for private worker groups
  (`router_required_…` → `router_must_have_nat_ip…` → `router_must_have_dhcp…`
  in sequence, even with NAT+DHCP demonstrably on; attribute flap moves the
  chain one step, never closes it). Guard: classify this error-code family on
  the nodepool into the trap message ("wire the network via the router CREATE
  body instead"); NEVER suggest recreate-the-cluster as the first remedy (the
  earlier reverted message did — wrong).
- **Cluster-create ordering**: cluster created before its network's router
  wiring existed → linkage frozen (recovery = recreate). A create-precondition
  on KubernetesCluster remains valuable (handoff: "Part 2's create-precondition
  stays valuable regardless") but must be respecced against the
  `virtual_router_id` model: what EXACTLY must be true at cluster create vs at
  nodepool create needs a fresh live probe matrix (create-body-wired vs
  post-attach vs no-router).
- **k8sVersion drift**: upstream version list drifts between IaC-writing and
  create (v1.35.4 vanished, v1.35.6 stayed). Validate against
  `/k8s/k8s-versions` pre-create and NAME the currently-valid versions in the
  error message (the existing validateVersion check may need only the
  message upgrade).
- Observability (`status.atProvider.routerLinked` or successor) is still
  wanted, but its semantics must follow the `virtual_router_id` model.

## 3. `Router.spec.forProvider.staticRoutes` — declarative router peering (Part 4)

Live production need: shared and production routers peer through a transit
network; static routes are hand-managed in the panel today.

- API (probed): `GET/POST /api/v1/routers/{id}/static-routes`
  (`{subnet: CIDR, nexthop: IPv4}`), `DELETE /routers/{id}/static-routes/{routeId}`.
  `GET …/static-routes/available` returned EMPTY in all probes even with valid
  nexthops — do not build on it.
- Nexthop semantics: a neighbor router's gateway in a COMMON network, or a
  cloud server in one of the router's own networks (managed-k8s nodes do NOT
  count).
- MVP: `staticRoutes: [{subnet, nexthop}]`, observe/create/delete convergence,
  same mutation pacing as networks. Stretch: `{subnet, via: {routerRef,
  networkRef}}` resolving the neighbor Router CR's observed gateway (gateways
  are platform-assigned, unknowable in git).
- e2e: two routers + common net; add route via each form; assert convergence
  and drift-repair after manual panel deletion.

## 4. Multi-router network attachment = supported peering pattern (Part 5)

Verified live: one Network on several routers simultaneously (interconnect
10.13.0.0/24 on both `shared` and `production`).

- Attach/detach/observe must scope strictly to the CR's own router id — never
  treat another router's attachment of the same network as drift.
- e2e: common net on two routers → delete one router → net stays on the
  survivor, no flapping on either CR.
- Platform wart: per-attachment gateway/reserved IPs can LEAK in the network's
  busy list after detach (live: 10.12.0.4 + 10.12.0.9) — never treat leaked
  reservations as provider-owned drift (Timeweb ticket filed).

## 5. `clusterNetworkCIDR` on KubernetesCluster (Part 6)

Upstream create body accepts `cluster_network_cidr.{pods_network,
services_network}` (create-only). Add as immutable optional CRD fields.
Motivation: distinct pod/service CIDRs per cluster disambiguate cross-cluster
debugging (Hubble/logs); production 2026-07-25 shipped on defaults because the
field was missing.

## 6. NAT floating-IP release on disable (owner decision 2026-07-25: not in 0.9.2)

v0.9.2 ships leave-bound: removing `natFloatingIP` disables NAT but the
address stays attached to the router (visible via
`FloatingIP.status.observedBoundTo`; one manual unbind to reclaim). The manual
step is disliked — design the declarative release here. Options on the table:

- **Symmetric transition unbind** (front-runner): the provider unbinds an IP
  ONLY as the second half of a NAT-disable it is itself executing in response
  to a spec change, and only if no other attachment of the router declares the
  address. Never sweeps idle IPs → panel-parked and adopted-router addresses
  are never touched; no ownership tracking needed. Guard: read
  `GET /routers/{id}/dnat-rules` first and skip (+event) if the address serves
  DNAT. Weakness: same-pass best-effort — a transiently failed unbind leaves
  the IP bound with only an event (post-transition the IP is indistinguishable
  from a panel-parked one).
- **Explicit opt-in field**: additive `natFloatingIP.releaseOnDisable: true` —
  unbind is declared intent, zero inference. Pairs well with the symmetric
  mechanism as its execution path.
- **Tracked ownership**: `status.atProvider` records which IPs the provider
  bound; undeclared tracked IPs are unbound eventually (retry-safe). Most
  convergent, but tracking has gaps (status loss, adoption) and still cannot
  see non-NAT uses without the DNAT read.

Constraint carried from 0.9.2: never-steal stays absolute; whatever release
mechanism lands must keep the blast radius at "IPs this reconcile is actively
transitioning."

## 7. Dependency hygiene: GO-2026-5970 (x/text) vuln bump

The post-v0.9.2 `ci` run on main fails the govulncheck gate: **GO-2026-5970**
— infinite loop on invalid input in `golang.org/x/text@v0.37.0`, fixed in
**v0.39.0**; reachable via `timeweb.authTransport.RoundTrip` →
`http.Transport.RoundTrip` → `norm.Form.*` (i.e. every API call). Fix is a
plain `go get golang.org/x/text@v0.39.0` (deps float per project policy) —
ship with 0.10.0; main CI stays red on the vuln gate until then.

## 8. Upstream tickets to file/track (quirk capture)

- Snapshot desync: NAT/DHCP added to a live router invisible to the k8s
  service's checks (response_ids e98f2f7b…, 490867cd…, f76e5d1b…).
- Reserved-IP leak on detach (busy 10.12.0.4/10.12.0.9 case).
- Undocumented but load-bearing surface: `virtual_router_id` on node-group
  create; `router` in the floating-ip bind enum (documented in our spec patch
  since 0.9.2); `…/static-routes/available` always empty.

## Live probe matrix wanted before planning

1. Fresh network wired via router CREATE body → cluster → private nodepool
   with `virtual_router_id` (the canonical happy path — confirm end-to-end).
2. Post-attach network + NAT + `virtual_router_id` → confirm the desync 400
   persists (trap guard justification) or find the settling condition.
3. `parent_services` timing: before/after a `virtual_router_id` group create.
4. Cluster create on create-body-wired vs post-attach vs router-less network —
   what does the create-precondition actually need to check?
