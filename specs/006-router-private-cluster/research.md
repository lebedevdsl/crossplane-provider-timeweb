# Phase 0 Research — Router & Private Kubernetes Cluster Networking

All decisions below rest on a live probe session (2026-06-10/11, authorized by
the feature owner, against the production account with disposable resources)
plus the owner's devtools capture of the dashboard's create request. Probe
resources were created cheapest-tier in Russian zones and deleted immediately;
the production router/cluster pair was never touched.

## R-1 — Upstream router API surface (probe-verified)

**Decision**: implement against the following undocumented-but-verified
surface, hand-patched into `docs/openapi-timeweb.json` (feature-005 precedent):

| Operation | Verified shape | Result |
|---|---|---|
| List | `GET /api/v1/routers` → `{routers: […]}` | ✅ 200 |
| Get | `GET /api/v1/routers/{uuid}` → `{router: {…}}` | ✅ 200 |
| Create | `POST /api/v1/routers` `{name (1–250), preset_id, networks: [{id, gateway?, reserved_ips?, nat?*}] (min 1), comment?, project_id?, ips?: [{ip: "create_ip" \| "<existing-floating-ip-addr>"}]}` | ✅ 201; zone derived from preset |
| Rename/comment | `PATCH /api/v1/routers/{uuid}` `{name?, comment?}` | ✅ 200 |
| Resize | `PATCH {preset_id}` | ❌ **silent no-op** — real op uncaptured (R-4) |
| List attachments | `GET /api/v1/routers/{uuid}/networks` → `{router_networks: […]}` (rich: `dhcp{is_enabled,is_available}, nat_ip, gateway, subnet, reserved_ips, busy_addresses`) | ✅ 200 |
| Attach | `POST /api/v1/routers/{uuid}/networks` `{networks: [{id}]}` | ✅ 201 |
| Detach | `DELETE /api/v1/routers/{uuid}/networks/{network-id}` | ✅ 200; last network → 400 `router_must_have_at_least_one_network` |
| Per-attachment settings | `PATCH /api/v1/routers/{uuid}/networks/{network-id}` `{is_dhcp_enabled}` (required) | ✅ 200, verified effective |
| NAT toggle | see R-3 | ⚠️ leading candidate identified, final capture pending |
| Delete | `DELETE /api/v1/routers/{uuid}` | ✅ 200; attached networks survive detached |
| Tier catalog | `GET /api/v1/presets/routers` → `{router_presets: [{id, node_count, cpu, cpu_frequency, ram, bandwidth, cost, location}]}` | ✅ 200 (ru-3: 2009 1-node, 2011/2013/2015 2-node HA) |

\* `nat` on a create-time network entry is **accepted and silently ignored**.

**Upstream quirks the design MUST absorb** (all probe-reproduced):
1. **Silent no-ops everywhere**: router-level `PATCH` ignores `ips`, `networks`,
   `preset_id`; updates while `status=starting` are dropped; `networks: []`
   does not detach. → every write is followed by re-observation; convergence
   is never inferred from a 2xx.
2. **New-VPC settle delay**: attaching a seconds-old network fails 403
   `networks_location_mismatch` even when zones match; retrying after ~1 min
   succeeds. → classify that code as transient (requeue), not terminal.
   The mirror image exists on the way out: after detaching a network from a
   router, deleting that network can fail 409 `conflict` ("Network cannot be
   deleted") for many minutes even with zero attached services — the detach
   cleanup is async upstream. → 409 on VPC delete is transient; relevant to
   the Network kind's delete path when routers are in play.
3. **Min-1 attachment** enforced at create (400 validation) and at detach
   (400 `router_must_have_at_least_one_network`). → CEL `minItems=1` at
   admission; the runtime error is still mapped terminally for drift cases.
4. Validation errors enumerate per-field problems (NestJS-style) — surface
   verbatim in conditions.

## R-2 — Public addresses = FloatingIP references (clarified + verified)

**Decision** (spec Q1): the Router never orders addresses. The operator
declares `FloatingIP` MRs (existing feature-003 kind) and the Router
references them (`ref`/`selector`/raw address-or-id, the provider's standard
trio). Probe verified the **request-side equivalence**: router create accepted
an existing floating-IP address in `ips[]`, and the floating-IP object's
`resource_type`/`resource_id` reflect router/network bindings.
`POST /floating-ips/{id}/bind` enum: `server, balancer, database, network` —
plus router-binding happens implicitly via the router-create `ips[]`. The
`create_ip` sentinel exists upstream but is NOT used by the provider (Q1).
Admission (CEL): any attachment with `nat: true` requires at least one
floating-IP reference on the Router — kills the upstream's silent-NAT-off
footgun before it reaches the API.

## R-3 — NAT toggle mechanism (one capture pending)

**Finding**: NAT is represented as `networks[].nat_ip` / `ips[].nat.{id}` on
the read side. Probed candidates that did NOT activate NAT: router-PATCH
`ips[].nat` (200, no-op), attachment-PATCH `nat_ip` (ignored), bind-FIP-to-
network via `POST /floating-ips/{id}/bind {resource_type: "network"}` (204,
FIP object reflects the binding, but the router's `nat_ip` stayed null for
60+ s — either propagation is slow or a router-side activation is also
needed). **Decision**: the dashboard NAT-dropdown request is the one
remaining REQUIRED capture before implementing the NAT slice (FR-004a).
Leading hypothesis: bind-to-network IS the mechanism and the probe router
simply lagged; the capture confirms or corrects. All non-NAT work proceeds
independently.

## R-4 — Resize op (capture pending; fallback specified)

`PATCH {preset_id}` is a silent no-op. **Decision**: capture the dashboard's
tier-change request (spec Q2 put in-place resize in scope). Until captured,
the controller rejects tier edits with the established immutability vocabulary
— exactly the spec's fallback — so the feature does not block on this.

## R-5 — Kubernetes binding & FR-012 delete-while-bound (experiment)

**Hypothesis under test** (running during planning; results appended below):
`parent_services` on the router is derived automatically when a K8s cluster
is created on a router-attached network — i.e. there is NO explicit bind op,
and the private-cluster arrangement is purely `Network + Router(NAT) +
KubernetesCluster(networkRef)`. The experiment creates a disposable
master-only cluster on the probe router's network, watches `parent_services`,
then attempts router deletion while bound (FR-012), then tears down.
If the hypothesis holds, `KubernetesCluster` needs **no schema change** and
US3 is documentation + e2e. If an explicit bind op exists, it is captured
from the dashboard's "Интеграция с роутерами" flow instead.

**Result (2026-06-11, appended after the experiment)**: see addendum at the
bottom of this file.

## R-6 — Router sizing dimension

**Decision**: promote a new preset-kind resolver dimension `DimRouterPreset`
over `GET /api/v1/presets/routers` (catalog confirmed live). Operator-facing
selection: `availabilityZone` (existing enum vocabulary) + a tier selector;
the fetcher filters by location via the existing `azLocation` mapping
(feature-005: spb-3↔ru-1, msk-1↔ru-3, ams-1↔nl-1, fra-1↔de-1) and the
resolver validates tier-vs-zone before any create (FR-003). Tier slugs follow
the established preset-slug conventions; node count (HA) is a property of the
tier (FR-002).

## R-7 — Attachment modeling: inline list

**Decision**: attachments are an inline `networks[]` list on the Router spec,
NOT a separate kind. Rationale: mirrors the upstream object (router owns its
network list), the min-1 constraint is a natural `minItems` on the list,
convergence is a straightforward set-diff against `GET /routers/{id}/networks`
(attach missing / detach extra / PATCH drifted DHCP), and a separate
attachment kind would add a cross-MR ordering problem with zero upstream
counterpart. Alternative (RouterNetworkAttachment kind) rejected as
over-modeling for v0.x.

## R-8 — Group & package placement

**Decision**: `Router` lives in `network.m.timeweb.crossplane.io` /
`apis/network/v1alpha1` / `internal/controller/network` — alongside `Network`
and `FloatingIP`, its two reference targets. The dashboard files routers under
"Сети". Controller Setup gets the capped rate limiter + (n/a here) no parent
watch needed; nothing depends on Router readiness via Connect-time refs except
possibly US3 documentation flows.

## R-9 — Error classification additions

| Upstream code | Classification |
|---|---|
| `networks_location_mismatch` (403) | **transient** when the target VPC is young (settle delay, probe-verified); terminal in conditions only if it persists past backoff |
| `router_must_have_at_least_one_network` (400) | terminal (admission prevents the declarative path; runtime occurrence = drift, surfaced verbatim) |
| `floating_ip_already_bound` (400) | terminal with actionable message (operator must free the IP or the controller orchestrates unbind-first where the binding is owned by this Router) |
| `network_not_found` (404) | terminal (dangling reference), consistent with existing ref-resolution errors |
| router `status` ∈ {failed, *error*} | `Ready=False reason=UpstreamFailed` (feature-005 vocabulary) |

## R-10 — e2e strategy

Bundle `18-router-lifecycle`: FloatingIP + Network + Router (NAT'd attachment
+ DHCP toggle + rename + second-network attach/detach) — cheapest ru-3 tier,
discovered at runtime via `/presets/routers` (`$TWE_ROUTER_PRESET`); asserts
Synced+Ready and status mirror (SC-004). Bundle `19-private-cluster` (US3,
env-gated like other K8s bundles): Network + Router(NAT) + KubernetesCluster
(networkRef) + Nodepool; asserts every node in `status.atProvider.nodes` has
no public address and the cluster is Ready — the SC-002 proof. The wrapper's
post-run inventory gains routers + floating IPs (lesson: yesterday's e2e
inventory missed K8s kinds).

---

## R-5 addendum — experiment results (2026-06-11)

The binding experiments produced a **stronger and worse finding than either
hypothesis**: `POST /api/v1/k8s/clusters` with a `network_id` is currently
broken upstream for the probed combination (msk-1 AZ, ru-3/msk-1 VPC,
master preset 403, v1.35.4+k0s.0):

1. **Router-attached network, router-first order**: create returned an error
   body yet created cluster 1094189 — a **zombie**: present in the list
   (status `installing`, **`availability_zone: ams-1`** despite msk-1 in the
   request), but its individual GET returns 500. Router `parent_services`
   never populated. Deleted cleanly (204).
2. **Bare network, cluster-first order** (production-like): identical zombie
   (1094191, ams-1, GET 500) — the ordering hypothesis is REFUTED; the
   breakage is `network_id` at create time itself, reproduced 3× today
   (incl. yesterday's configuration+network_id 500).
3. The production pair (staging cluster + Polite Caelum router, working
   private nodes) proves a working arrangement EXISTS — it was built through
   the dashboard, whose create request therefore differs from the documented
   `network_id` field or sequences an undocumented attach op.

**RESOLUTION (owner capture + isolation probe, 2026-06-11)**: the owner
captured the dashboard's create-with-network request, and an isolation probe
with our original minimal body but a **zone-matched master preset** created
cleanly (msk-1 honored, no zombie). Root cause of all three zombies:

> **K8s presets carry hidden zone affinity.** The live `/presets/k8s` items
> include `location` AND `availability_zone` fields the published swagger
> omits (e.g. preset 403 "K8S Base" = ru-1/**spb-3**; 1675 "K8S Base" =
> ru-3/**msk-1**; promo 1673 = msk-1). Creating with a preset whose zone
> mismatches the requested `availability_zone` + a `network_id` produces the
> error-yet-created ams-1 zombie. Zone-matched preset → clean create.
> (`network_id` itself was never the problem.)

Second discovery from the capture: **`worker_groups[].public_ip_enabled`**
exists upstream (absent from the published `NodeGroupIn`) — the dashboard
sends `true`; this is the per-nodepool public-IP toggle. A dashboard
`send-action {action: "PodklyuchenieServisa"}` call observed alongside is
analytics (it 403'd while the create succeeded) — not contract.

**Decisions (final)**:
- **D-1 (resolved)**: NO explicit router↔cluster bind op is needed for US3.
  The private-cluster mechanism is: nodepool `public_ip_enabled: false`
  (additive `publicIP` field on `KubernetesClusterNodepool`, **default true**
  per FR-008/SC-006) + the cluster's network behind a NAT-enabled Router for
  egress. `parent_services` on the router is expected to populate derivedly
  (verify in the US3 e2e; remaining captures: NAT toggle R-3, resize R-4).
  > **SUPERSEDED (re-plan 2026-06-17):** D-1's `public_ip_enabled` half is
  > reversed — that field is NOT in the official spec and is dropped; US3 is
  > now router-NAT + a default route via the gateway with NO per-node flag.
  > See the re-plan section below.
- **D-2 (unconditional)**: KubernetesCluster Create gains the
  **error-yet-created adoption guard** (list-by-name before create retry);
  Router gets the same defensively.
- **D-3 (supersedes)**: feature-004's K8s preset dimensions have a LATENT
  MIS-PLACEMENT BUG: `fetchK8sPresetsByType` discards location ("presets
  carry no location" was wrong — the swagger hid the fields). Fix in this
  feature: hand-patch `location`/`availability_zone` onto the k8s preset
  schema, make `DimKubernetesMasterPreset`/`WorkerPreset` resolution
  **location-first** (azLocation, same as the configurator dims), and fix the
  e2e `slugByRole` discovery to filter by zone.
- **D-4**: any create that can mis-place MUST be zone-validated client-side
  pre-create, and post-create Observe MUST verify the `availability_zone`
  echo, surfacing `UpstreamFailed` on mismatch instead of waiting for the
  inevitable provisioning failure.

---

## Re-plan update (2026-06-17): official spec landed + live findings

Timeweb published the official OpenAPI spec, now merged into
`docs/openapi-timeweb.json` (207 paths; Makefile `-include-tags` gained
`Роутеры`/`Роутеры`). This **supersedes R-3, R-4, and the create-binding
portion of R-5**, and a live e2e session surfaced findings F-5..F-9 plus the
US3 mechanism decision. New extras in the official spec — `/dnat-rules`,
`/static-routes`, `/statistics` — stay **out of scope** for 006.

### R-3 — NAT toggle: **RESOLVED** (re-plan 2026-06-17)

**Decision**: NAT is toggled by a dedicated official endpoint, not by binding
the FloatingIP to the network.
- **Enable**: `PATCH /api/v1/routers/{router_id}/networks/{network_name}/nat`
  with body `NatIn{nat_ip: string}` (`nat_ip` **required**).
- **Disable**: `DELETE` on the same path.

**Rationale**: Create-time `nat` does **not** actually apply — observed
`nat_ip` stays `""` after create. With only create-time NAT, the declared
`natFloatingIP` never matches the observed (empty) `nat_ip`, so
`isRouterUpToDate` is **perpetually false**, producing a perpetual `Update` +
`NATConvergencePending` reconcile loop. Manually enabling NAT in the dashboard
**live-converged** the router and stopped the loop — proving the convergence
comparison logic is correct and only the toggle call was missing. Implementing
`convergeNAT` (the PATCH/DELETE pair) in `router_external.go` closes the loop
and is the load-bearing remaining piece for US3 (NAT egress). This supersedes
the R-3 "leading hypothesis: bind-to-network IS the mechanism" — it is NOT.

**Alternatives considered**: (a) FIP-bind-to-network (`POST /floating-ips/{id}/
bind {resource_type:"network"}`) — rejected: it reflects on the FIP object but
never activates router-side NAT (probe-observed `nat_ip` stayed null 60+s).
(b) create-time `nat` — rejected: silently ignored (observed empty).

### R-4 — Resize: **CONFIRMED ABSENT** (re-plan 2026-06-17)

**Decision**: there is **no upstream router resize op**. `RouterEdit` (the
`PATCH /routers/{id}` body) is **name + comment only** in the official spec.
The immutable-reject behavior (FR-002a fallback) is therefore the **permanent**
behavior, not a stopgap. T021 is a no-op beyond what T017's immutability check
already ships.

**Rationale**: the official `RouterEdit` schema carries no `preset_id`; the
earlier silent no-op on `PATCH {preset_id}` (R-1/R-4) is now explained — the
field simply isn't part of any write op. **Alternatives**: none; tier change =
delete + recreate.

### R-5 — Create-time `parent_service`: re-evaluate binding (re-plan 2026-06-17)

**Finding**: the official create body `RouterIn` carries
`parent_service{id, type}` — an **explicit create-time service binding** —
which the prior R-5 derived-`parent_services` hypothesis did not anticipate.

**Decision**: re-evaluate US3 binding against this explicit field. The status
mirror (`status.atProvider.parentServices`) and the FR-012 delete-while-bound
guard stand regardless. The private-cluster arrangement itself is unchanged
(network placement + NAT, see US3 below); `parent_service` at create is an
additional, possibly-required, binding path to validate in bundle 19 — it does
NOT reintroduce a per-node public-IP toggle.

**Alternatives**: keep relying purely on derived `parent_services` — left open
pending the bundle-19 observation of whether create-time `parent_service` is
needed for the binding to populate.

### NAT path param: `{network_name}` vs `{network_id}` (re-plan 2026-06-17)

**Decision**: the official NAT/detach sub-path param is `{network_name}` (the
old hand-patch and the current controller pass the `network-xxxx` **id**).
**FLAG — confirm at regen/first-live-call** whether the API keys on the
network **name** or the **id**; the controller currently passes the
`network-xxxx` id, which may or may not satisfy a `{network_name}`-typed path.

**Rationale**: a path-param rename in the official spec can be cosmetic (id
still accepted) or load-bearing (name required) — must be verified live before
trusting `convergeNAT`/detach. **Alternatives**: pass the network name if the
id 404s; resolve name↔id from `GET /routers/{id}/networks`.

### US3 mechanism: **DECIDED** — router-NAT + default route (re-plan 2026-06-17)

**Decision**: private workers = placement on a router-NAT'd network **+ a
default route via the gateway**, with **NO per-node flag**.
`public_ip_enabled` is **NOT in the official spec** and is **DROPPED** from
`NodeGroupIn` and from `nodepool_external.go`. Bundle 19 validates that workers
carry no public address under this arrangement.

**Rationale**: the official `NodeGroupIn` has no public-IP toggle, confirming
the captured dashboard `public_ip_enabled: true` (R-5 addendum) was an
undocumented field that is not part of the supported contract. The dashboard's
documented private-cluster model is network-NAT + default route, which the
official surface supports. This **supersedes D-1's `public_ip_enabled` half**
and the additive nodepool `publicIP` field (data-model §3): both are removed.

**Alternatives considered**: keep the hand-patched `public_ip_enabled` —
rejected (not in official spec; unsupported field, risks silent breakage). Per-
node flag of any kind — rejected (no upstream counterpart).

### Live e2e findings F-5..F-9 (re-plan 2026-06-17)

- **F-5 — FloatingIP router binding is UUID-keyed**. The existing int64
  `resourceID` / `BOUND-TO` print column cannot render a router binding (UUID,
  not int). **Decision**: add a string `resourceUUID` to
  `FloatingIPBindingObservation`, decode it from `AsFloatingIpResourceId1`, and
  add `router` to the `resourceType` doc enum (currently lists only
  `server/balancer/database/network`). **Rationale**: routers are addressed by
  UUID upstream; the int column silently drops the binding.

- **F-6 — router Observe lacks the `starting` short-circuit**. Update has a
  `state == "starting"` guard (writes are dropped upstream while starting);
  Observe does not, so it runs a no-op reconcile loop while the router is
  starting. **Decision**: mirror the guard in Observe. **Rationale**:
  symmetry — both methods must respect the `starting` window.

- **F-7 — router `no_paid` surfaces as `status:"error"`** (not a `no_paid`
  string). The controller mis-maps `status:"error"` to `UpstreamFailed`
  ("delete and recreate") instead of `PaymentRequired`. **Decision**: fix the
  mapping so a router `error` caused by billing reports `PaymentRequired`.
  **Rationale**: a billing block is recoverable (pay), not a fatal
  delete-and-recreate. **Alternatives**: distinguish by a secondary signal if
  `error` is overloaded for both billing and genuine failure.

- **F-8 — delete strands networks as non-deletable `type:bgp` VPCs**. Deleting
  a router without detaching its networks first leaves them as `type:bgp` VPCs
  that cannot be deleted (`DELETE /api/v1/vpcs/{id}` → 409 "Network cannot be
  deleted"; the v2 path has no delete = 405). **Decision**: `Router.Delete`
  must **detach all networks, then delete the router**. **Rationale**: the
  detach-then-delete order is the only way to avoid the strand. One orphan
  (`e2e-plan-probe-net2`) is permanently stuck → Timeweb support ticket.

- **F-9 — FloatingIP create intermittently exceeds the 30s client timeout** on
  slow ru-3 IP allocation; it **self-heals on retry**. **Decision (optional)**:
  a 60s create timeout for FloatingIP. **Rationale**: ru-3 allocation latency
  occasionally crosses 30s; the operation succeeds, only the client gives up.

### Out-of-scope confirmation (re-plan 2026-06-17)

`/dnat-rules`, `/static-routes`, `/statistics` are now in the official spec but
remain **out of scope** for 006. `availability_zone` is now official → drop
that hand-patch.
