# Contract: Router NAT-on-update auto-bind (Part 1)

## Upstream calls (all probe-verified 2026-07-24)

| Call | Use | Notes |
|---|---|---|
| `GET /api/v1/routers/{uuid}` | existing Update-pass read | `router.ips[].ip` = ownership authority; `ips[].nat.id` = NAT'd network |
| `GET /api/v1/floating-ips` | FIP identity + never-steal check | match by address → `id` (uuid), `resource_type`/`resource_id` |
| `POST /api/v1/floating-ips/{uuid}/bind` | the attach | body `{"resource_type":"router","resource_id":"<router uuid>"}` → 204; enum value undocumented (stale docs — quirk-captured); `resource_id` is the STRING arm |
| `PATCH /routers/{uuid}/networks/{net}/nat` | existing NAT enable | only after ownership observed; never issued for unowned addresses |
| `DELETE /routers/{uuid}/networks/{net}/nat` | existing NAT disable | unchanged; NO unbind afterwards |

## Behavior contract

1. NAT enable/change is gated on `declared IP ∈ observed router.Ips` (FR-001/003).
2. Unowned + FIP free → bind (paced mutation, `ops++`); NAT enable left to the
   next reconcile (Observe-as-sole-authority) (FR-002, D-4).
3. Unowned + FIP bound elsewhere or unresolvable → NO bind, NO steal;
   condition + Event; the per-attachment loop continues; pass returns nil
   (poll-cadence requeue) (FR-004/005, D-5).
4. Freed/owned later → converges automatically, condition clears (FR-006).
5. Create path and NAT disable byte-identical to v0.9.1; disable leaves the IP
   bound (FR-007).
6. Idempotence: re-running any step is safe — bind is skipped once bound
   (either via router.Ips or FIP bound_to=this-router), NAT PATCH is diffed
   against observed natIP as today.

## Condition

Blocked bind: shared upstream-failure vocabulary, message pattern
`natFloatingIP <ip> (network <id>): <cause: bound to <holder> | no floating IP
with this address>; NAT will converge automatically once the address is
bindable`. Emitted once per state change (no flap between paced reconciles).

## Unit test matrix (Constitution III)

| Scenario | Expect |
|---|---|
| unowned declared IP, FIP free | Bind called with router uuid (string arm); NO NAT PATCH same pass |
| owned declared IP, drifted natIP | NAT PATCH only, no Bind, no FIP list read |
| unowned, FIP bound to other server | no Bind, condition set, OTHER attachment's DHCP patch still issued |
| unowned, no FIP matches address | no Bind, condition set, pass returns nil |
| NAT removed from spec | DeleteRouterNat, no Unbind call |
| bind API error (transport/5xx) | classified error returned (existing semantics) |
