# Data Model: Nodepool autoscaling scale-to-zero (minSize: 0)

**Feature**: 026-nodepool-min-zero | **Date**: 2026-08-07

One kind touched: `KubernetesClusterNodepool`
(`kubernetes.m.timeweb.crossplane.io/v1alpha1`). No new kinds, no new groups.

## Spec changes (relaxation only)

### `spec.forProvider.autoscaling` (existing `NodepoolAutoscaling`)

| Field | Before | After | Notes |
|-------|--------|-------|-------|
| `enabled` | bool, required | unchanged | |
| `minSize` | int, `Minimum=1`, CEL floor 2 when enabled | int, **`Minimum=0`**, no CEL floor | 0 = scale-to-zero |
| `maxSize` | int, `Minimum=1`, CEL floor 2 when enabled | int, `Minimum=1` (unchanged), no CEL floor | floor 1 pending P-2 |

### Kind-level CEL (line-261 rule)

Before:

```text
!has(autoscaling) || !enabled ||
  (minSize >= 2 && maxSize >= 2 && maxSize >= minSize)
```

After:

```text
!has(autoscaling) || !enabled || maxSize >= minSize
```

Message: `when autoscaling is enabled: maxSize must be >= minSize`. Floors move
entirely to the field-level `Minimum` markers (cheap, better error locality, no
CEL cost-budget exposure). All other kind-level rules unchanged.

### Validation matrix (autoscaling enabled)

| minSize | maxSize | Verdict | Rejected by |
|---------|---------|---------|-------------|
| 0 | 3 | **accept** (new) | — |
| 0 | 1 | accept (new, P-2-gated) | — |
| 2 | 3 | accept (unchanged) | — |
| 2 | 1 | reject | CEL `maxSize >= minSize` |
| 0 | 0 | reject | `maxSize` `Minimum=1` |
| -1 | 3 | reject | `minSize` `Minimum=0` |

`nodeCount` (`Minimum=1`) is untouched — still required, still ignored while
autoscaling is on, still the convergence target after a day-2 disable (024).

## Status changes (additive)

### New: `status.atProvider.autoscaling` (`NodepoolAutoscalingStatus`)

```go
// NodepoolAutoscalingStatus mirrors the upstream group's autoscaler state as
// reported by the last observation (is_autoscaling/min_size/max_size).
// Mirror-only — never an input to convergence.
type NodepoolAutoscalingStatus struct {
    Enabled bool `json:"enabled"`
    // +optional
    MinSize *int `json:"minSize,omitempty"`   // upstream nulls bounds when off
    // +optional
    MaxSize *int `json:"maxSize,omitempty"`
}
```

`status.atProvider.autoscaling` itself is `+optional` (absent until first
observation). Populated in `populateNodepoolStatus` from the group GET.
DeepCopy + CRD YAML regenerated same PR (Constitution I).

## Readiness state table (`setNodepoolReadyCondition` + new `zeroOK` input)

`zeroOK := upToDate && spec.Autoscaling != nil && spec.Autoscaling.Enabled &&
spec.Autoscaling.MinSize == 0` (computed in Observe; `upToDate` already implies
observed flag+bounds match the declaration).

| State | Condition | Change |
|-------|-----------|--------|
| any node in error/fail state | Ready=False `UpstreamFailed` | unchanged |
| `!upToDate` (drift converging) | Ready=False `Reconciling` | unchanged |
| 0 declared + 0 actual, `zeroOK` | **Ready=True `Available`** | **NEW** (scale-to-zero steady state; event on transition) |
| 0 declared + 0 actual, `!zeroOK` | Ready=False `Creating` | unchanged (024 T034 guard: manual pools, mid-provision, drained-with-floor>0) |
| active < declared | Ready=False `Reconciling` | unchanged |
| all declared nodes active | Ready=True `Available` | unchanged |

## Upstream wire shape (unchanged, reference)

`min_size`/`max_size` ride the existing group create body and the 024 bounds
PATCH as `*int` — a pointer to 0 serializes as `0` (pinned by unit test, R-5).
GET mirror source: `node_group.is_autoscaling` / `.min_size` / `.max_size`
(nullable when off).
