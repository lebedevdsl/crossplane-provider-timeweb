# Research: 021 router peering — findings log

Consolidates the probe-sourced facts (from `specs/_next-router-nat-bind.preface.md`
§3–§6, live 2026-07-24/25) plus in-repo verification done during planning.

- **R-1 static-route surface**: generated client already complete —
  `GetStaticRoutes` / `PostStaticRoute` / `DeleteStaticRoute`,
  `StaticRouteIn{subnet, nexthop}` / `StaticRouteOut{id, subnet, nexthop}`,
  `{static_routes: […]}` list envelope. No update op ⇒ replace = delete+create.
  `GetAvailableStaticRoutes` exists but returned empty in all live probes —
  never consulted (pinned by unit test).
- **R-2 DNAT read (release guard)**: list op is `GetDnat` (…/dnat-rules GET),
  `{dnat_rules: [DnatRuleOut]}`; the guard matches `public_ip`. Read-only —
  DNAT stays unmanaged.
- **R-3 multi-router scoping**: the Router controller reads/writes only its
  own router's attachment sub-resource (`GET/POST/PATCH/DELETE
  /routers/{id}/networks…`) — cross-router interference is structurally
  impossible; pinned by `TestRouterUpdate_ConvergedSharedNetwork_NoOps` and
  the live two-router gate. The Network controller has NO reserved/busy-
  address drift logic (grep-verified), so post-detach leaked reservations
  (10.12.0.4/.9 live case) are inert to the provider.
- **R-4 clusterNetworkCIDR**: `cluster_network_cidr{pods_network,
  services_network}` already present in the repo OpenAPI + generated create
  body (anonymous pointer struct). The cluster GET does NOT echo it ⇒ no
  status mirror; immutability is admission-only (field CEL + params-level
  has()==has() transition rule).
- **R-5 k8sVersion drift message (US5)**: already satisfied in-tree —
  `validateVersion` resolves through `DimKubernetesVersion` (enum) and
  `DimensionValueNotFoundError` formats `valid: …` (≤20 values, `joinSample`).
  Pinned by `TestDimensionValueNotFoundErrorNamesValidValues`; no code change.
- **R-6 vuln**: GO-2026-5970 fixed by floating `golang.org/x/text` → v0.40.0
  (≥ v0.39.0); `govulncheck` clean.
- **R-7 NAT release semantics**: default-on, transition-scoped (owner-agreed):
  release runs only as the second half of a NAT disable/move the provider
  executes; guards = declared-elsewhere set, DNAT read, bound-to-this-router
  check; failures are best-effort (Warning event, no retry — post-transition
  the address is indistinguishable from a panel-parked one). Never-steal
  (020) unchanged.

## Live-gate findings (2026-07-25, inyan-staging)

- **R-8 route destinations must be REAL subnets**: a static route to a
  non-existent subnet is rejected `400 invalid_static_route_nexthop: Nexthop
  is not available` — the error blames the nexthop, the actual problem is the
  destination. Verified by flipping only the subnet (same nexthop): fictional
  → rejected, real → converged. Documented in docs/routers.md + bundle 24
  step 3; candidate for the Timeweb ticket.
- **R-9 `network_cidr` IS echoed**: the cluster GET returns the create-time
  `cluster_network_cidr` under the DIFFERENT key `network_cidr`
  (pods_network/services_network) — contrary to the published schema (absent
  from the GET response there). Status mirror added
  (`status.atProvider.clusterNetworkCIDR`); both immutability rules (change +
  add/remove) verified live at admission.
- **R-10 k8sVersion drift observed live**: v1.34.7+k0s.0 (June's
  presets.local.env) is gone; the US5 message named the valid set
  (…v1.34.9, v1.35.6, v1.36.2…) exactly as designed. presets.local.env
  refreshed.
- **R-11 host→API reachability**: the dev host reached api.timeweb.cloud
  directly (drift-repair DELETE returned 200) — the standing WAF-blocked
  assumption is stale, but bundle 24 keeps the guard-first design anyway.
