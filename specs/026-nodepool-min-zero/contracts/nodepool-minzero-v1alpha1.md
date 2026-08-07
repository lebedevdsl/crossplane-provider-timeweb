# CRD Contract: KubernetesClusterNodepool scale-to-zero (v1alpha1, feature 026)

Delta contract against the shipped v0.12.0 schema. Everything not listed is
byte-identical.

## Admission

- `spec.forProvider.autoscaling.minSize`: `Minimum=0` (was 1 + CEL floor 2).
- `spec.forProvider.autoscaling.maxSize`: `Minimum=1` (field marker unchanged;
  CEL floor 2 removed — P-2-gated, see wire contract).
- Kind-level CEL: `!has(autoscaling) || !autoscaling.enabled ||
  autoscaling.maxSize >= autoscaling.minSize`, message
  `when autoscaling is enabled: maxSize must be >= minSize`.
- Guarantee (FR-006/SC-004): every manifest valid under v0.12.0 remains valid —
  relaxation only.

Reject matrix (enabled=true): `{2,1}` → CEL; `{0,0}` → maxSize Minimum;
`{-1,3}` → minSize Minimum. Accept: `{0,3}`, `{0,1}`, all previously-valid.

## Status (additive)

`status.atProvider.autoscaling` — optional mirror of the upstream group's
autoscaler state, absent until first observation:

```yaml
status:
  atProvider:
    autoscaling:
      enabled: true
      minSize: 0        # omitted when upstream nulls them (autoscaling off)
      maxSize: 3
    observedNodeCount: 0
    nodes: []           # empty while scaled to zero
```

Mirror-only: never consulted by convergence logic. This is the assertable
surface for "bounds converged to 0" in kuttl and the live gate.

## Conditions

| Scenario | Synced | Ready | Reason |
|----------|--------|-------|--------|
| Converged autoscaled pool, declared minSize 0, drained to 0 nodes | True | **True** | `Available` |
| Same pool while autoscaler grows it back (pending pods) | True | False | `Reconciling` (n/declared provisioned) |
| Bounds drift (out-of-band edit) | False→True | per node state | drift repaired by bounds PATCH (024 machinery) |
| Manual pool (autoscaling off/absent) at 0 nodes | — | False | `Creating` (T034 guard, unchanged) |
| Autoscaled pool with declared minSize >= 1 at 0 nodes | True | False | `Creating` (transitional; upstream autoscaler restores) |

Contract point: Ready=True at zero nodes REQUIRES all three of — declaration
converged, autoscaling declared enabled, declared `minSize == 0`.

## Behavior guarantees

- The provider issues **zero** count writes while autoscaling is declared
  enabled (unchanged 024 invariant — now explicitly covering the drained-to-0
  state: no scale-up "repair").
- `nodeCount` semantics unchanged: ignored while autoscaling on; convergence
  target after a day-2 disable.
- Day-2 `minSize` 2→0 edit converges via the existing owned-fields bounds PATCH
  (`{is_autoscaling, min_size, max_size}`), no recreate.
- **No prerequisite guard** (clarify 2026-08-07): the upstream ≥2-nodes-elsewhere
  requirement and the drain blockers are documented, never checked — no
  admission rule, condition, or Warning event keys on them.
