# Next feature preface: nodepool autoscaling day-2 convergence

Live gap (2026-07-25, prod workers pool): flipping autoscaling on→off (+
nodeCount) on a live pool does nothing to the flag and starts a count fight —
the provider reduces, the still-enabled upstream autoscaler restores to
min_size, repeat. Owner mitigation in place: declared autoscaling-on with
min=2 (stable — the Update early-return stops all count writes).

## The gap (code-verified)

Autoscaling is CREATE-ONLY in the provider:

1. `is_autoscaling`/`min_size`/`max_size` are sent only in
   `buildCreateNodeGroupBody` (`nodepool_external.go:566–571`).
2. The GET decode struct (`nodeGroupBody`) does not carry the three fields —
   the provider cannot see the upstream flag state.
3. `isNodepoolUpToDate`: declared-on ⇒ unconditionally up-to-date (off→on and
   min/max edits undetectable); declared-off ⇒ compares only nodeCount (flag
   drift invisible).
4. Update's only PATCH (`NodeGroupUpdate`) structurally carries
   name/labels/taints — no autoscaling verb exists anywhere in the codebase.

## Fix shape

- Decode the three fields into `nodeGroupBody`; mirror in status.
- `isNodepoolUpToDate`: drift rows for the flag (both directions) and
  min/max changes.
- Update: converge autoscaling BEFORE the count logic.
- Safety keystone: skip count deltas whenever the OBSERVED (not declared)
  flag is on — ends the fight even mid-transition or under external state.

## Probes required first (015 pattern: capture → hand-patch → verify)

- P-1: panel devtools capture of the live autoscaling toggle — verb + body
  (expected: the same undocumented-superset group PATCH with
  `is_autoscaling`/`min_size`/`max_size`; the documented PATCH schema lacks
  them).
- P-2: is the toggle state-restricted (e.g. while group health is
  Provisioning)? UNKNOWN — an owner guess only; the provider never sent the
  op, so nothing was ever refused. Probe before designing any
  wait-classification; if unrestricted, none is needed.
- P-3: off→on day-2 — does the PATCH accept the min/max pair together with
  the flag; behavior when current count is outside [min,max].
