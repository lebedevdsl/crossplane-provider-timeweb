# Implementation Plan: Nodepool autoscaling day-2 convergence

**Branch**: `024-fix-autoscaling-day2` | **Date**: 2026-07-26 | **Spec**: [spec.md](./spec.md)

## Summary

v0.11.2 PATCH. Probe-verified op: the group PATCH accepts
`is_autoscaling`(+`min_size`/`max_size`); the GET echoes all three. Fix =
observe the flag, detect drift both directions, converge the flag BEFORE
count logic, and gate count deltas on the OBSERVED flag.

## Scope call

US3 (status mirror of autoscaling) is DEFERRED: it needs a CRD status field,
and the repo convention is patch = zero CRD change. Ships with the next
minor; this patch is pure behavior. (Spec US3 noted accordingly.)

## Design decisions

- **D-1 observe**: `nodeGroupBody` += `is_autoscaling bool`,
  `min_size *int`, `max_size *int` (nullable when off).
- **D-2 up-to-date**: declared-on ⇒ drift when observed off OR bounds differ;
  declared-off ⇒ drift when observed ON (before any count comparison) or
  count differs.
- **D-3 converge (Update)**: declared-on ⇒ if flag/bounds drift, PATCH
  `{is_autoscaling: true, min_size, max_size}`; return (autoscaler owns
  count). Declared-off ⇒ if observed on, PATCH `{is_autoscaling: false}` and
  RETURN (no count writes same pass — Observe-sole-authority); observed off ⇒
  existing count deltas.
- **D-4 client**: hand-patch `docs/openapi-timeweb.json` `NodeGroupUpdate`
  with the three nullable fields + regen (the bind-enum precedent produced a
  clean 1-line-class diff). The metadata PATCH (name/labels/taints) stays a
  separate call — never mixed with the autoscaling write (015 owned-fields
  rule).
- **D-5 no state-classification**: P-2 disproven; upstream rejections flow
  through the standard classifier.

## Touch points

```text
docs/openapi-timeweb.json + regen                      # NodeGroupUpdate fields
internal/controller/kubernetes/nodepool_external.go    # D-1..D-3
internal/controller/kubernetes/nodepool_external_test.go / router_integration_test.go
docs/kubernetes.md                                     # short day-2 note
```

## Validation

Unit: FR-006 matrix. Live (existing 024 fixtures, autoscaled pool 119657
currently true/2/3 matching its MR): incident shape — declare off +
nodeCount 1 → flag off first, then one reduce, soak without oscillation;
then off→on with bounds; cleanup; release v0.11.2 (tight notes per style
memory).
