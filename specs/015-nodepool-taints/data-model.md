# Data Model: Nodepool Taints (+ label mutability)

Phase 1 for `specs/015-nodepool-taints/plan.md`.

## KubernetesClusterNodepool (existing kind — delta only)

### spec.forProvider (delta)

| Field | Type | Constraints | Notes |
|-------|------|-------------|-------|
| `taints` | `[]NodepoolTaint` | optional; MaxItems=12; CEL: unique (key, effect) | NEW. Day-2 mutable; set-replaced upstream on drift. |
| `labels` | `map[string]string` | unchanged | Contract change only: create-only → day-2 mutable + drift-corrected. |

All other fields unchanged (sizing/count/publicIP/autoscaling/autohealing/
cluster refs keep their existing mutability contracts and XValidations).

### NodepoolTaint (new type)

| Field | Type | Constraints |
|-------|------|-------------|
| `key` | `string` | required; MinLength=1; MaxLength=253; k8s label-key pattern (optional dns-subdomain prefix + name segment) |
| `value` | `*string` | optional; MaxLength=63; k8s label-value pattern; nil ≡ `""` upstream |
| `effect` | `string` | required; enum `NoSchedule` \| `PreferNoSchedule` \| `NoExecute` |

Identity within the list: (key, effect). Same key with two different
effects is legal; exact (key, effect) duplicate is rejected at admission.

### status.atProvider (delta)

| Field | Type | Notes |
|-------|------|-------|
| `labels` | `map[string]string` | upstream-observed node labels, spec shape |
| `taints` | `[]NodepoolTaint` | upstream-observed taints, spec shape; upstream `""` value shown with value omitted |

The mirror shows what the upstream group actually reports (operator request,
gate session 2026-07-10): convergence is auditable from the CR alone —
`spec.forProvider.*` is the declaration, `status.atProvider.*` the observed
truth, and any difference is the drift Update is about to correct.

## Upstream wire model (hand-patched OpenAPI superset)

| Schema | Delta |
|--------|-------|
| `Taint` (new) | `{key: string, value: string, effect: string}` — mirrors `SetLabels` + `effect` |
| `NodeGroupIn` | + `taints?: []Taint` (sibling of the existing hand-patched `labels`) |
| `PATCH /api/v1/k8s/clusters/{cluster_id}/groups/{group_id}` (new op) | body `{name?: string, labels?: []SetLabels, taints?: []Taint}` → 200 `NodeGroupResponse`; operationId `UpdateClusterNodeGroup` |

Controller-side observe struct (`nodeGroupBody`) gains
`Labels []kv` / `Taints []taintBody` decoded from the existing GET.

## Diff & convergence semantics

```
declaredTaints(spec)  = {(key, value|"", effect)}          # set
observedTaints(GET)   = {(key, value, effect)}             # set
declaredLabels(spec)  = map[string]string
observedLabels(GET)   = fold [{key,value}] → map (last write wins)

metadataUpToDate      = declaredTaints == observedTaints
                        && declaredLabels == observedLabels
ResourceUpToDate      = metadataUpToDate && <existing count/sizing logic>

Update (metadata leg, runs BEFORE autoscaling early-return):
  if !metadataUpToDate:
    PATCH {name: spec.name, labels: declaredLabels-as-array (sorted by key),
           taints: declaredTaints (spec order)}
    # full-set replace; idempotent; owned fields only (R-4)
```

## Relationships

Unchanged: `KubernetesClusterNodepool` → parent `KubernetesCluster` via
exactly-one-of clusterRef/clusterSelector/clusterID. Taints/labels introduce
no refs, no selectors, no watches.
