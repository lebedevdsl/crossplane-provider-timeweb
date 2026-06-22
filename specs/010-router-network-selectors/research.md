# Phase 0 Research: Router Multi-Network Attachment & Selectors

All Technical Context unknowns are resolved below. The Router upstream surface,
attachment convergence, and reference-resolution idioms were established and
probe-verified in feature 006; this round adds only the selector layer on top, so
research focuses on the selection mechanism, expansion semantics, and the two
operational guards (pacing, zero-resolution) clarified in the spec.

## R-1: Selector field type — standard `metav1.LabelSelector`

**Decision**: Model the new field as `*metav1.LabelSelector` (`matchLabels` +
`matchExpressions`), resolved with `metav1.LabelSelectorAsSelector` and a
namespace-scoped `client.List(..., client.MatchingLabelsSelector{Selector: sel})`.

**Rationale**: It is the canonical Kubernetes selector shape; operators already know
it, generators/CEL handle it, and it composes with the existing `client.Get`-based
resolver by swapping the single Get for a List. Supports both equality and
set-based (`In`/`NotIn`/`Exists`) matching for free.

**Alternatives considered**:
- `xpv2.Selector` (crossplane-runtime selector) — designed for the runtime's
  *reference-resolver* machinery (to-one, writes the resolved ref back to spec). This
  provider deliberately does **not** use that machinery (it uses the custom
  `client.Get` idiom and never mutates spec — see refs.go header). Reusing it would
  fight both conventions and the no-spec-mutation rule. Rejected.
- A bespoke `matchLabels map[string]string` only — simpler CEL but drops
  `matchExpressions`; no real saving since `LabelSelector` is already generated.
  Rejected.

## R-2: To-many expansion, dedup, and precedence

**Decision**: In `resolveRouterRefs`, resolve in two passes into a map keyed by
upstream network id:
1. **Explicit pass** — process every `networkRef`/`networkID` entry first; insert its
   resolved attachment (id + DHCP/NAT/gateway/reservedIPs) into the map.
2. **Selector pass** — for each selector entry, `List` matching Networks in the
   namespace; for each that is `Ready` with a non-empty upstream id, insert into the
   map **only if the id is not already present** (explicit wins, FR-006). The selector
   entry's DHCP/gateway/reservedIPs become the defaults for the networks it brings in.

The final attachment slice is the map's values (order-insensitive, already how the
controller diffs). Networks matched by multiple selectors collapse to one entry
(FR-005).

**Rationale**: Map-keyed-by-id gives dedup and explicit-precedence in one structure
with no extra passes, and matches the controller's existing id-keyed set-diff in
`isRouterUpToDate`/Update. Pure function of cluster state → idempotent (constitution
II).

**Alternatives considered**: Expanding selectors to synthetic attachment entries and
relying on downstream dedup — scatters precedence logic and risks double NAT/DHCP
toggles on overlap. Rejected.

## R-3: Readiness gating reuse

**Decision**: A matched Network is eligible only when
`status.atProvider.upstreamID != ""` **and** `Ready == True` — the exact gate
`resolveRouterNetworkRef` already applies to a single ref. Not-yet-Ready matches are
silently skipped (not an error) and picked up on a later reconcile (FR-007).

**Rationale**: Identical gate keeps ref and selector behavior consistent and avoids
the upstream settle-delay 403 that attaching a half-created VPC triggers (documented
in refs.go). Skipping (vs erroring) keeps a provisioning fleet from wedging the router.

## R-4: Reactivity — `Network → Router` mapping Watches

**Decision**: Add `.Watches(&networkv1alpha1.Network{}, handler.EnqueueRequestsFromMapFunc(mapNetworkToRouters))`
to `SetupRouter`. The map func lists Routers in the changed Network's namespace and
returns reconcile requests for those whose spec contains at least one selector entry
(cheap pre-filter; the actual label match is re-evaluated in Observe). Keep the
existing 60s-capped rate limiter.

**Rationale**: The periodic poll already re-resolves selectors every interval, so
correctness does not depend on the watch — but a watch turns "within one poll
interval" into "promptly after the Network change" and removes any manual-trigger
need (FR-004). This mirrors the feature-005 "parent Watches for dependent kinds"
idiom, here inverted (child Network → parent Router). Enqueuing only routers that use
selectors bounds churn.

**Alternatives considered**:
- Poll-only (no watch) — satisfies the letter of FR-004 (no manual trigger) but adds
  up to one full interval of latency for new matches. Acceptable fallback, but the
  watch is low-cost and matches spec intent (SC-002). Chosen to add the watch.
- Watch all Networks and reconcile all Routers unconditionally — wasteful; rejected
  in favor of the selector pre-filter.

## R-5: Pacing to avoid the Qrator burst-ban

**Decision**: In the Update convergence path, cap the number of attach **and** detach
upstream calls performed per `Update` invocation (a small constant, e.g. configurable
`maxAttachOpsPerReconcile`, default a handful). Apply the diff up to the cap, then
return **without** claiming convergence; Observe re-detects the remaining diff and the
rate-limited workqueue re-queues, so the set drains over successive reconciles.
Partial convergence is a normal progress state, not an error (FR-014).

**Rationale**: Timeweb is behind Qrator, which silently bans call bursts (memory:
`project_timeweb_qrator_ddos_egress_block`; support-confirmed, no published limit).
One POST/DELETE per network attach/detach means a 30-network selector would otherwise
fire 30 calls in a tick. Bounding per-reconcile ops + the existing 60s-capped limiter
spreads the load. "Verify by re-observation" (memory) already governs this controller:
Update never claims success; Observe is the authority — so incremental convergence
fits the existing shape with no new claim-of-done risk.

**Alternatives considered**:
- Hard cap on match-set size (spec Q1 option B) — rejected by requester; would block
  legitimate large fleets.
- No pacing (option C) — risks the egress IP ban that takes the whole provider down.
  Rejected.

## R-6: Zero-resolution & never-detach-last guards

**Decision**: Two runtime guards (the CRD `minItems=1` only bounds *declared* entries,
not *resolved* networks):
1. **Create / general**: if the resolved attachment set is empty, do **not** call the
   upstream create/converge; surface `Synced=False`/`Ready=False` with a clear reason
   (e.g. `reason=NoNetworksResolved`) and requeue. Recovers automatically when a match
   becomes Ready (FR-008, SC-005).
2. **Never-detach-last**: in Update, if applying the detach diff would drop the
   upstream router to zero networks, skip the final detach and surface the same
   blocking reason instead of issuing the upstream call that returns
   `400 router_must_have_at_least_one_network`.

**Rationale**: Pre-empting the verified upstream 400 as a readable condition is the
constitution-II "classify, don't swallow" behavior and avoids leaving the router in a
failed upstream state. Distinguishing declared-entry count from resolved count is the
exact subtlety surfaced during clarification.

**Alternatives considered**: Letting the upstream 400 propagate — opaque to operators
and risks a wedged finalizer. Rejected.

## R-7: CEL admission rules (extending the existing trio)

**Decision**: Update `RouterNetworkAttachment` validation to:
- **Exactly-one-of trio**: `(has(networkRef)?1:0)+(has(networkID)?1:0)+(has(networkSelector)?1:0) == 1`.
- **Non-empty selector** (FR-015): when `networkSelector` is set, it must have at
  least one `matchLabels` entry or one `matchExpressions` entry.
- **No NAT with selector** (FR-009): `has(networkSelector)` implies
  `!has(natFloatingIP)`.

**Rationale**: Admission-time rejection gives operators immediate, actionable errors
and keeps the dangerous match-all / NAT-on-fleet configs out of the reconcile loop.
CEL is already the validation mechanism for this struct (the existing trio rule).

**Alternatives considered**: Controller-side validation only — slower feedback, and
an invalid object would persist and flap. CEL preferred; controller keeps a
belt-and-braces guard mirroring the existing `default:` branch in resolveRouterRefs.

## R-8: Status — no schema change needed

**Decision**: Do not add fields to `RouterObservation`. `status.atProvider.networks`
already mirrors the upstream per-network table from the Observe GET, so every
selector-resolved network appears there with its NAT/DHCP/gateway (SC-007 satisfied).
Selector→explicit provenance in status was explicitly **deferred** during
clarification.

**Rationale**: Keeps the change minimal and avoids speculative status surface. SC-007
("determine the full set from status alone") is met by the existing mirror.

**Alternatives considered**: Add per-network `source: selector|explicit` provenance —
useful for debugging but deferred; would be additive later if needed.

## R-9: CEL cost budget on the per-entry rules (found during live e2e, 2026-06-22)

**Decision**: Bound the declared `networks` array with `+kubebuilder:validation:MaxItems=64`.

**Rationale**: The Kubernetes apiserver enforces a per-rule **CEL cost budget**. The
non-empty-selector rule (R-7 / FR-015) calls `size()` on the selector's unbounded
`matchLabels`/`matchExpressions`, and that per-item cost is multiplied by the
enclosing `networks` array's max length. With no `MaxItems`, the estimate exceeded
budget by 1.7× and the apiserver **rejected the CRD** — the package revision still
reported Healthy while the CRD apply silently failed (visible only on
`kubectl describe mrd routers.network.m.timeweb.crossplane.io` events, NOT on the
Provider/Revision conditions). `MaxItems=64` bounds the multiplier; it limits only
*declared* entries, never the *resolved* set (FR-014 preserved).

**Critical caveat**: `crossplane beta validate` (the T019/T026 gate) does NOT run the
apiserver cost estimator — it passed while the live apiserver rejected the CRD. Verify
CRD acceptance against a real apiserver:
`kubectl apply --server-side --force-conflicts --dry-run=server -f package/crds/<crd>.yaml`.

**Alternatives considered**: simplify the rule (drop `matchExpressions` branch) — would
forbid expression-only selectors; rejected. Add `maxProperties`/`maxItems` to the
embedded LabelSelector — not expressible via field markers on the generated type;
bounding the parent array is the available lever.

## R-10: Observability — events + correlation (found during live e2e)

**Decision**: Emit `AttachedNetwork`/`DetachedNetwork` Normal events on the Update
attach/detach path (FR-016), and promote the `Network` upstream-id print column to
default while demoting the constant VPC-`type` (`bgp`) column to `-o wide` as `TYPE`
(FR-017).

**Rationale**: The selector mutates the attached set with no spec edit, so the only
signals were a changing `status.atProvider.networks` and a generic
`UpdatedExternalResource`. Per-network events make the change legible; the events name
the `network-<hex>` id, and that id was not a default `Network` print column, so it
couldn't be correlated from `kubectl get` — promoting it closes the loop. The old
`STATE` column showed the VPC `type`, which is effectively always `bgp` (VPCs have no
provisioning lifecycle), so it was relabelled and demoted rather than kept as
misleading default noise.
