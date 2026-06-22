# Contract: Selector resolution, pacing & convergence (controller)

Behavioral contract for the `network` controller package. Complements the CRD
contract; covers what the reconciler MUST do at runtime.

## Resolution (Connect/Observe time)

- **Namespace scope**: selectors match only `Network` resources in the Router's own
  namespace (FR-010). Implemented as `client.List(Network, InNamespace(ns), MatchingLabelsSelector{sel})`.
- **Readiness gate**: a matched Network is eligible iff
  `status.atProvider.upstreamID != ""` **and** `Ready == True` (same gate as
  `resolveRouterNetworkRef`). Ineligible matches are skipped, not errored (FR-007).
- **Dedup + precedence**: the effective set is keyed by upstream network id. Explicit
  (`networkRef`/`networkID`) entries are resolved first and win on overlap; selector
  matches fill only ids not already present (FR-005, FR-006). A network matched by
  multiple selectors appears once.
- **No spec mutation**: resolution returns upstream values carried on the external;
  it never writes resolved ids/labels back to the MR spec (preserves the
  exactly-one-of CEL invariant; established idiom).
- **NAT**: selector-sourced attachments always have NAT off (CEL forbids NAT on a
  selector entry; resolver sets `NATIP=""`).

## Zero-resolution guard (FR-008)

- If the resolved set is empty, the controller MUST NOT issue an upstream
  create/converge call. It surfaces `Synced=False`, `reason=NoNetworksResolved`, and
  requeues. It recovers automatically when ≥1 matching Network becomes Ready (SC-005).
- The CRD `MinItems=1` (declared entries) does not imply ≥1 resolved network; this
  runtime guard is the real enforcement of the upstream "≥1 network" invariant.

## Never-detach-last guard (US3-2)

- During Update, the controller MUST NOT apply a detach that would leave the upstream
  router with zero attached networks. It skips that final detach and surfaces
  `reason=NoNetworksResolved` instead of issuing the call that returns the upstream
  `400 router_must_have_at_least_one_network`.

## Pacing (FR-014)

- Attach and detach upstream calls MUST be bounded per reconcile (a small constant,
  `maxAttachOpsPerReconcile`; not operator-tunable in v1 unless trivially so). The
  Update path applies up to that many total mutations in a stable order, then returns
  **without** claiming convergence.
- Observe re-detects the remaining diff; the existing 60s-capped workqueue rate
  limiter re-queues. Large match sets thus converge across successive reconciles.
- Partial convergence is a normal, non-error progress state (no `Synced=False` solely
  because more ops remain). This composes with the "Update never claims done; Observe
  is the authority" rule already governing this controller.

## Reactivity (FR-004)

- `SetupRouter` adds `Watches(&Network{}, EnqueueRequestsFromMapFunc(mapNetworkToRouters))`.
- `mapNetworkToRouters` lists Routers in the changed Network's namespace and returns
  requests only for those with ≥1 selector entry (cheap pre-filter; exact match
  re-evaluated in Observe). Create, label-change, and delete of a Network all enqueue
  candidate routers promptly.
- Correctness does not depend on the watch (the poll re-resolves every interval); the
  watch only reduces latency to meet SC-002.

## Idempotency & errors (constitution II)

- Resolution is a pure function of cluster state; re-invocation yields the same set.
- Attach/detach are set-diff operations; re-running converges without duplication.
- Errors are classified: waiting-on-not-Ready / paced-partial → transient (requeue,
  no terminal failure); genuinely impossible config → caught at admission (CEL).
  Errors are never silently swallowed.

## Test obligations (constitution III)

Unit (fake Timeweb client + fake kube client):
1. Selector resolves to multiple Ready networks → all attached.
2. Selector matches a not-yet-Ready network → that one excluded, no error.
3. Selector + overlapping explicit entry → attached once; explicit DHCP/NAT wins.
4. Two overlapping selectors → network attached once.
5. Zero resolved (no match / all not-Ready) → blocked, `NoNetworksResolved`, no
   upstream create.
6. Live router whose match set drains to zero → last network not detached; blocked.
7. Pacing: resolved set larger than the per-reconcile cap → bounded ops issued per
   Update; convergence completes over multiple Observe/Update cycles.
8. `mapNetworkToRouters` returns only selector-using routers in the right namespace.

Admission (generated CRD / validation test):
9. trio exactly-one-of; empty selector rejected; NAT-with-selector rejected.
