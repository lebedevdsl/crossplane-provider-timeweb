# Routers

> Stuck? Start at [docs/troubleshooting.md](troubleshooting.md) — the get→describe→events→logs path, then the [condition reference](conditions.md).

`Router` (`network.m.timeweb.crossplane.io/v1alpha1`) manages Timeweb's
NAT/DHCP router appliance for private networks. The upstream API is
undocumented; every operation this provider uses was verified live —
see `specs/006-router-private-cluster/contracts/timeweb-router-endpoints.md`.

## 1. Minimum router

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Router
metadata: {name: edge, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  forProvider:
    name: edge
    availabilityZone: msk-1
    presetName: router-1x1-1gb-ru-3
    networks:
      - networkRef: {name: app-net}
```

- **At least one network is required** — the upstream rejects routers with
  zero attachments, at create and at last-detach alike (enforced at
  admission via `minItems`).
- **Tier slug**: `router-<nodes>x<cpu>-<ramGB>gb-<location>`; 2-node tiers
  are the HA flavors. The tier is resolved within the zone's region —
  `availabilityZone` and tier location MUST pair (msk-1↔ru-3, spb-3↔ru-1,
  ams-1↔nl-1, fra-1↔de-1); a mismatch is rejected before anything is
  created because the upstream mis-places instead of rejecting.
- The zone is immutable; the tier is rejected on edit (the upstream
  `RouterEdit` body is name + comment only — there is no resize operation, so
  recreate to resize).
- **Provisioning takes ~10–20 min** (variable). A newly created router sits in
  `starting` for that window; this is normal, not a stall (see Troubleshooting).

## 2. NAT and DHCP

```yaml
    networks:
      - networkRef: {name: app-net}
        natFloatingIP:
          ref: {name: egress-ip}    # presence = NAT on, via exactly this IP
        dhcp: true
```

- NAT is per attachment: reference a `FloatingIP` (or a raw `ip:`) and that
  address serves that network — one address per network, and the mapping is
  always explicit. No reference = NAT off. The Router never orders or
  releases addresses itself.
- **NAT activation converges automatically.** The provider drives the official
  NAT toggle (`PATCH …/networks/{network}/nat` to enable, `DELETE` to disable):
  setting `natFloatingIP` on an attachment sets that network's NAT to the
  referenced address; removing `natFloatingIP` clears the network's NAT. No
  manual dashboard step is needed. (Create-time `nat` is silently ignored
  upstream — the toggle is the only working activation path — so the provider
  always converges NAT after create by re-observing `status.atProvider` until
  the observed NAT address matches the declared one.) Attach/detach/DHCP
  converge in the same pass.
- **Adding NAT to an existing router auto-binds the IP** (v0.9.2). The NAT
  toggle only accepts addresses the router already owns, so when a declared
  NAT address is not on the router yet the provider first binds the floating
  IP to the router, then enables NAT on the next pass. Two rules:
  - The provider **never steals** an address bound to another resource — the
    Router reports `Ready=False NATIPUnavailable` naming the holder and
    converges by itself once the address is freed.
  - **Removing (or moving) `natFloatingIP` releases the address** (v0.10.0):
    the same transition that disables NAT also unbinds the floating IP from
    the router, so it is immediately reusable elsewhere — no manual unbind.
    The release is strictly transition-scoped and skipped (with an event)
    when the address is still declared on another attachment or a DNAT rule
    forwards it; addresses parked on the router out-of-band are never
    touched.

## 2b. Static routes — declarative peering (v0.10.0)

```yaml
    staticRoutes:
      - subnet: 10.12.0.0/24
        nexthop: 10.13.0.3            # literal next-hop
      - subnet: 10.14.0.0/24
        via:                          # resolved from the neighbor router's
          routerRef: {name: shared}   # observed gateway in the common network
          networkRef: {name: transit}
```

- Set semantics keyed by `subnet`: added entries are created, removed entries
  deleted, a changed nexthop is replaced, and out-of-band panel deletions are
  re-created (single-writer drift repair). Mirrored at
  `status.atProvider.staticRoutes`.
- Upstream nexthop rule: a **neighbor router's gateway in a common network**
  (network attached to both routers — the peering pattern) or a cloud server
  in one of the router's own networks; managed-k8s nodes do not qualify.
- The route's **destination subnet must be a real existing network** — a route
  to a non-existent subnet is rejected with the misleading upstream error
  `invalid_static_route_nexthop: Nexthop is not available` (live-verified
  2026-07-25). If you hit that error with a correct-looking nexthop, check the
  subnet first.
- The `via` form waits (clear not-ready event) until the neighbor Router is
  Ready with its gateway observed, then converges automatically.
- A Network may be attached to **several routers simultaneously** — that is
  the official peering arrangement; each Router CR converges only its own
  attachment and never fights over the shared network.
- `status.atProvider` answers everything the dashboard shows: per-network
  gateway / NAT address / DHCP state, the router's public IPs and what each
  NATs, and `parentServices` (e.g. a Kubernetes cluster running through it).

## 3. Day-2

- **Attach/detach**: edit the `networks` list — attachments converge in
  place (set semantics). A freshly created Network may need ~1 minute
  upstream before it can attach; the provider retries automatically
  (transient `networks_location_mismatch` events are normal).
- **Rename/comment**: converge in place.
- **Deletion**: refused (kept pending, with the dependent named in an
  event) while `parentServices` is non-empty — deleting a router out from
  under a private cluster would cut its egress. When delete does proceed the
  provider issues a single `DELETE` on the router, which **cascades the network
  detach itself**; the networks and floating IPs always survive and become
  deletable immediately after. (The controller does NOT detach networks first —
  a router requires at least one network, so detaching the last one is rejected
  with `400`.)

## Troubleshooting

| Symptom | Meaning | Action |
|---|---|---|
| router stuck in `starting` | normal provisioning window (~10–20 min, variable) | wait; only investigate past ~20 min |
| `Ready=False PaymentRequired` (upstream `status:"error"`) | `no_paid` — billing / month-in-advance funding, NOT a crash | check the panel / account balance and top up; do **not** delete and recreate |
| router deleted out-of-band stranded its network (non-deletable `type:bgp` VPC) | the detach-first order was bypassed, leaving an orphan VPC | open a Timeweb support ticket — the orphan can't be cleared by the provider |
| `PresetNotFound` on the tier | slug wrong, or tier not sold in the zone's region | pick a tier of the zone's region (see slug rule above) |
| transient `networks_location_mismatch` events | new network still settling upstream | wait — retried automatically; persistent ⇒ genuine region mismatch |
| `Ready=False UpstreamFailed` naming two zones | upstream placed the router elsewhere than requested | delete and recreate (upstream mis-placement) |
| deletion pending, event names a k8s service | a bound k8s service blocks router deletion | delete/unbind the cluster first |
| `Ready=False NATIPUnavailable` naming `type/id` | the declared NAT address is bound to another resource (never stolen) or no floating IP has it | free the address (or reference another); NAT converges automatically |
