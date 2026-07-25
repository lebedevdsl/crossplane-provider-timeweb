# Data model: 020 — Router NAT bind + cluster linkage guard

NON-BREAKING. No spec-surface changes anywhere. One additive status field.

## Router (`network.m.timeweb.crossplane.io/v1alpha1`) — no schema change

- Spec: unchanged (`networks[].natFloatingIP.{ref|ip}` trio as shipped in 006).
- Status: unchanged (`atProvider.ips[{ip, natNetwork}]` mirror already exists).
- Behavior state machine for one attachment's NAT (update path):

```text
declared NATIP == observed natIP                    → converged (no-op)
declared NATIP ∈ router.Ips, != observed natIP      → UpdateRouterNat (existing branch)
declared NATIP ∉ router.Ips:
    FIP lookup by address →
        free                → BindFloatingIp(router uuid)  [paced; NAT next cycle]
        bound to this router→ skip (observation catches up)
        bound elsewhere     → condition NATIPUnavailable, continue pass
        no such FIP         → condition NATIPUnavailable, continue pass
declared NATIP == ""  && observed natIP != ""       → DeleteRouterNat (existing; NO unbind)
```

- Conditions: reuses the shared vocabulary. Blocked bind surfaces a
  `Synced=False`-family condition (upstream-failure reason) with message
  `natFloatingIP <ip> for network <id>: bound to <type>/<id> — free it (or
  change the declaration); NAT converges automatically once available` + Event.
  Must be stable (no flap) across paced reconciles.

## FloatingIP — no change

Pure-allocation contract intact; `status.atProvider.observedBoundTo` already
mirrors router bindings (UUID-keyed, 006 F-5). The Router is the consuming MR
and owns bind side-effects for its declared NAT IPs (same single-owner rule as
Server).

## KubernetesCluster (`kubernetes.m.timeweb.crossplane.io/v1alpha1`)

- Spec: unchanged.
- Status (additive):

```go
// RouterLinked mirrors whether this cluster appears in its network's
// router parent_services (type "k8s"). The linkage forms ONLY at
// cluster create on a NAT'd router-wired network and is immutable
// upstream; false on a router-wired cluster means private nodepools
// cannot be created (recreate-only recovery). nil = not yet observed /
// no resolved network.
// +optional
RouterLinked *bool `json:"routerLinked,omitempty"`
```

- CRD YAML + DeepCopy regenerated same PR (Constitution I).
- Create-precondition outcomes (before `POST /k8s/clusters`):

| Network wiring | Action |
|---|---|
| router-attached, NAT on the network | proceed |
| router-attached, NO NAT | refuse create; condition (requeue-style) naming network + consequence + enable-NAT remedy; auto-proceed when NAT appears |
| no router | proceed; Warning event: private nodepools will be impossible on this cluster |

## KubernetesClusterNodepool — no schema change

Error classification only: upstream `error_code`
`router_required_for_worker_groups_without_public_ip` → condition message
explaining the frozen cluster↔router linkage (check the Router CR's
`status.atProvider.parentServices`; cluster recreation is the only remedy).
All other errors unchanged.

## Client layer

- New typed constant (hand-written package, NOT zz_generated):
  `BindFloatingIpResourceTypeRouter = "router"`.
- `docs/openapi-timeweb.json`: bind enum gains `router` (hand-patched-superset
  convention; keeps the value across future regens). No regen this patch.
