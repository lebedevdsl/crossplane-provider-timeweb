# Research: Router NAT-on-update bind fix + cluster↔router linkage guard

Phase 0 consolidation. Primary source: `specs/_next-router-nat-bind.preface.md`
(probe-verified live 2026-07-24 against api.timeweb.cloud during the
timeweb-infra#132 production rollout; incident timeweb-infra#135). All
decisions below are settled — no NEEDS CLARIFICATION remain.

## R-1 — FIP→router attach surface (Part 1 core)

**Decision**: attach via `POST /api/v1/floating-ips/{fip_uuid}/bind` with
`{"resource_type": "router", "resource_id": "<router uuid>"}`.

**Rationale**: probe-verified 204; the FIP's `bound_to` becomes
`resource_type=router`; the deployed v0.9.1 provider then converged NAT on its
own (production evidence, no restart). Every alternative is a dead end
(probed): `POST /routers/{id}/ips` does not exist; `RouterEdit` PATCH carries
only name/comment; the NAT PATCH accepts neither unowned addresses nor FIP
UUIDs (404 `ip_not_found` both).

**Alternatives considered**: panel-attach + degraded condition (superseded —
was the spec's baseline before the bind probe); router recreate (rejected:
shared infrastructure, #124 sensitivity).

**Doc conflict resolved**: the published bind enum (`server|balancer|database|
network`) is stale; 006's contracts table (F-5) listing `router` is right.
`resource_id` for router binds is the router UUID string (servers/balancers use
numeric ids) — the generated union type already has a string arm.

## R-2 — Ownership precondition source

**Decision**: observed `router.Ips[].Ip` from the router GET the Update pass
already performs; Observe's drift row unchanged.

**Rationale**: zero extra calls; identical authority to what the NAT branch
diffs against today (`NetworkOut.NatIp`).

## R-3 — Never-steal + FIP identity resolution

**Decision**: one `GET /api/v1/floating-ips` list per bind attempt, matched by
declared address → uuid + current `bound_to`. Free → bind; bound to this
router → skip (observation catches up); bound elsewhere / not found → typed
degraded condition, skip the attachment, continue the pass.

**Rationale**: uniform for both declaration forms (`natFloatingIP.ref` and raw
`.ip`) without touching Connect-time resolution (which deliberately returns
only the address); the list read doubles as the never-steal check (clarified:
never unbind another holder). Rate cost is one paced read only when an unowned
NAT IP is declared (rare, transitional).

**Alternatives considered**: extending `resolveFloatingIPRef` to also return
the referenced MR's external-name (uuid) — rejected: doesn't cover raw-`ip`
declarations and spreads the fix across Connect; the Server controller
precedent (`floatingip_bind.go`) also resolves bindings against upstream reads.

## R-4 — Bind/NAT sequencing and pacing

**Decision**: bind counts against `maxRouterMutationsPerReconcile` (`ops++`);
the same pass does NOT issue the NAT enable for that attachment; next
reconcile's Observe sees ownership and the existing NAT branch fires.

**Rationale**: Observe is the sole convergence authority (router convergence
contract; upstream 2xx ≠ converged). One extra reconcile of latency is the
established trade.

## R-5 — Blocked-attachment isolation + condition vocabulary

**Decision**: bind-blocked is not an error return — record a shared-vocabulary
condition (upstream-failure reason; message names network, IP, holder, remedy)
plus an Event, continue converging the remaining attachments, return nil (poll
cadence, no backoff storm). Real transport/API errors on issued calls keep the
current classified-return behavior. NAT disable never unbinds (clarified;
matches create-path routers after a NAT toggle-off).

**Rationale**: today one failing NAT write aborts the whole per-attachment
loop — on a shared router that freezes unrelated changes (US3). The condition
must not flap across paced reconciles.

## R-6 — Cluster↔router linkage (Part 2)

**Facts** (handoff Part 2, verified live 2026-07-24): linkage
(`router.parent_services[{type:"k8s"}]`) forms ONLY at cluster create, and only
when the cluster network is router-wired WITH NAT at that moment. No public
API re-links; recovery is recreate-only. Private nodepool creates against an
unlinked cluster fail `400 router_required_for_worker_groups_without_public_ip`
indefinitely. The Router CR already mirrors `parentServices` in status
(observe plumbing exists).

**Decision**: three-part defense —
1. Create-precondition on KubernetesCluster: resolve network → find its router
   (`GET /routers` list + `GET /routers/{id}/networks`) → router-attached
   NAT-less ⇒ refuse + condition + requeue (auto-proceeds when NAT lands);
   no router ⇒ allow + Warning event; NAT'd ⇒ proceed.
2. Nodepool-side classification of exactly that `error_code` into a
   frozen-linkage explanation (`*timeweb.APIError.Code` match — the classifier
   already extracts `error_code`).
3. Additive `status.atProvider.routerLinked` on KubernetesCluster, computed in
   Observe from the same router read (router found AND `parent_services`
   contains the cluster id, type `k8s`).

**Rationale**: Part 1 closes the main GitOps path into the trap, but ordering
races (cluster + network in one sync wave) remain; the precondition closes
them. The status mirror makes both broken and healthy states visible without
API spelunking.

**Alternatives considered**: probing for a private re-link endpoint — not
attempted (nothing surfaced; panel mechanism unknown); auto-recreate of
unlinked clusters — rejected outright (destructive).
