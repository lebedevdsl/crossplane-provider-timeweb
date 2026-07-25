# Contract: cluster↔router linkage guard (Part 2) — SUPERSEDED 2026-07-25

> NOT shipped in v0.9.2. The premises below are partially wrong: the real
> nodepool↔router binding is an explicit `virtual_router_id` in the node-group
> create body (panel capture, handoff Part 3), and post-attach networks are
> rejected by the k8s service even when NAT/DHCP are on (snapshot desync).
> Superseded by `specs/_next-router-features.preface.md` (v0.10.0).

## Upstream facts (verified 2026-07-24)

- `router.parent_services[{id, type:"k8s"}]` forms ONLY at cluster create, and
  only if the cluster network is router-wired WITH NAT at that instant.
  Immutable afterwards; no public re-link API. Recovery = recreate cluster.
- Private (no-public-IP) nodepool create on an unlinked cluster →
  `400`, `error_code: router_required_for_worker_groups_without_public_ip`,
  forever.
- Router CR already mirrors `parentServices` (`status.atProvider`) — the
  observe plumbing for linkage data exists.

## Wiring probe (used by both the precondition and routerLinked)

Given the cluster's resolved network id:

1. `GET /api/v1/routers` (account list — small N).
2. For each router, `GET /api/v1/routers/{uuid}/networks`; the network's
   router = the one whose list contains the network id. NAT state = that
   entry's `nat_ip` (non-empty ⇒ NAT on).
3. Linkage = owning router's `parent_services` contains the cluster
   external-name with `type == "k8s"`.

Failures of these reads are transient (classified) — the precondition NEVER
proceeds on unknown wiring state.

## KubernetesCluster.Create precondition (FR-012/013)

| Wiring | Behavior |
|---|---|
| router + NAT | create proceeds (existing body untouched) |
| router, no NAT | NO create; condition: `network <id> is attached to router <uuid> without NAT; a cluster created now will never link to the router and private nodepools will fail router_required_for_worker_groups_without_public_ip — enable NAT first`; requeue; auto-proceeds once NAT observed |
| no router | create proceeds; Warning event: `network <id> has no router; private nodepools (publicIP: false) will not be possible on this cluster` |

## KubernetesCluster.Observe (FR-015)

`status.atProvider.routerLinked *bool` — true iff linkage found; false when the
network's wiring is readable and linkage absent (incl. router-less networks);
nil while unobservable. Status-only, additive; CRD + DeepCopy regen same PR.

## Nodepool classification (FR-014)

`*timeweb.APIError{Code: "router_required_for_worker_groups_without_public_ip"}`
→ condition message: `cluster→router linkage is missing and is frozen at
cluster-create time (see the Router CR status.atProvider.parentServices);
recreating the cluster on a NAT'd router-wired network is the only remedy`.
Match on exactly this code; all other 400s keep current handling.

## Test matrix

| Scenario | Expect |
|---|---|
| create, network router-wired + NAT | POST issued |
| create, network router-wired NAT-less | no POST; condition; nil error (requeue) |
| create, router-less network | POST issued; Warning event |
| blocked create, NAT appears | later reconcile POSTs |
| observe linked / unlinked / router-less cluster | routerLinked true / false / false |
| private nodepool 400 router_required… | frozen-linkage condition message |
| private nodepool other 400 | existing classification unchanged |

## e2e (live gate, staging)

Canonical ordering: Network → Router (attach + NAT via Part 1 update path) →
KubernetesCluster → private nodepool Ready. Negative: cluster declared before
NAT enabled → blocked condition observed → NAT lands → cluster converges.
