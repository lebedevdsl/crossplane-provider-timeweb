# CRD Contract: KubernetesClusterNodepool v1alpha1 — taints delta

Additive delta to the existing `kubernetes.m.timeweb.crossplane.io/v1alpha1`
`KubernetesClusterNodepool` contract.

## New field

```yaml
spec:
  forProvider:
    taints:                     # optional, max 12 entries
      - key: dedicated          # required, k8s label-key syntax
        value: ingress          # optional, k8s label-value syntax
        effect: NoSchedule      # required: NoSchedule|PreferNoSchedule|NoExecute
```

## Admission rules

| Rule | Mechanism | Message shape |
|------|-----------|---------------|
| effect ∈ {NoSchedule, PreferNoSchedule, NoExecute} | OpenAPI enum | unsupported value |
| key/value syntax | OpenAPI pattern + length | should match pattern |
| ≤ 12 taints | MaxItems | too many items |
| unique (key, effect) | type-level CEL | `taints must not repeat the same key+effect pair` |

No immutability rule — taints and labels are day-2 mutable (edits converge
in place; the group is never replaced for a metadata change).

## Behavioral contract

| Aspect | Contract |
|--------|----------|
| Create | declared taints/labels are in the upstream create body; nodes join tainted (no post-join window) |
| Observe | order-insensitive set-diff of declared vs upstream-reported taints AND labels; any drift ⇒ `ResourceUpToDate=false` |
| Update | one PATCH carrying only `name`+`labels`+`taints` (full-set replace); runs even when autoscaling owns the count |
| Out-of-band edits | reverted to declared sets on the next reconcile (single-writer) |
| Clear | removing all taints (or labels) converges upstream to `[]` |
| Sync | `Synced=True` only once the upstream group reports the declared sets (FR-014) |
| Node lifecycle | scale-up/autoscale/autoheal nodes carry the group's current sets (upstream guarantee, e2e-verified) |

## Conditions (unchanged vocabulary)

| Condition | Reason | When |
|-----------|--------|------|
| Ready=False | Reconciling | metadata or count converging |
| Ready=False | UpstreamFailed | node in failed state (existing) |
| Synced=False | (classified error reasons) | PATCH rejected terminally |

## Backward compatibility

- No schema change to `labels`; existing manifests valid unchanged.
- Pools without taints: no default, no new upstream traffic, no drift.
- Pools WITH labels: now drift-corrected (release-note item — the one
  intended behavior change).
