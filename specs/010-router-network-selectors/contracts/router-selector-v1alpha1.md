# Contract: Router `networkSelector` (CRD surface)

**Kind**: `Router` · **Group/Version**: `network.m.timeweb.crossplane.io/v1alpha1`
**Change type**: additive (one optional field + tightened CEL on an existing struct)

## New field

`spec.forProvider.networks[].networkSelector` — `metav1.LabelSelector` (optional).

Selects **every** `Network` in the Router's namespace whose labels match, attaching
each as its own network ("to-many"). Mutually exclusive with `networkRef` and
`networkID`. Cannot be combined with `natFloatingIP`. Must constrain at least one
label.

The `dhcp`, `gateway`, and `reservedIPs` on the same entry become the defaults applied
to every network the selector resolves to.

## CEL validation (admission)

Per `networks[]` element:

| Rule | Expression (abridged) | Message | Spec |
|------|-----------------------|---------|------|
| Exactly-one selection mode | `count(networkRef, networkID, networkSelector) == 1` | "exactly one of networkRef, networkID, or networkSelector must be set" | FR-001 |
| Non-empty selector | `!has(networkSelector) \|\| size(matchLabels)>0 \|\| size(matchExpressions)>0` | "networkSelector must specify at least one matchLabels entry or matchExpressions term" | FR-015 |
| No NAT with selector | `!(has(networkSelector) && has(natFloatingIP))` | "natFloatingIP cannot be combined with networkSelector; use networkRef/networkID for NAT'd networks" | FR-009 |

`spec.forProvider.networks` keeps `MinItems=1` and gains `MaxItems=64` (both bound
declared entries; unchanged at runtime). `MaxItems` is required to keep the per-entry
CEL rules within the apiserver's CEL **cost budget** — without it the non-empty-selector
rule's `size()` calls, multiplied by an unbounded array, exceed budget and the apiserver
rejects the CRD (research R-9). It does not cap the resolved set (FR-014).

## Conditions (status)

| Situation | `Synced` | `Ready` | Reason | Spec |
|-----------|----------|---------|--------|------|
| Selector resolved ≥1 Ready network; converged | True | True | Available | FR-002 |
| Some matches not yet Ready (others attached) | True | True/False* | — | FR-007 |
| Declared entries resolve to **zero** networks | False | False | `NoNetworksResolved` | FR-008, SC-005 |
| Match set drained to zero on a live router (last-detach skipped) | False | True** | `NoNetworksResolved` | US3-2 |
| Large set still converging (paced) | True | False | progress (no error) | FR-014 |
| Invalid config caught at admission (trio / empty / NAT) | n/a (rejected) | n/a | CEL message | FR-001/009/015 |

\* Router becomes Ready once at least one network is attached and the appliance is up;
not-yet-Ready matches don't block readiness (FR-007).
\** The upstream still has its prior network(s) attached (never-detach-last), so the
appliance stays up; the condition signals the unsatisfiable declared intent.

## Events (FR-016)

| Reason | Type | Message | When |
|--------|------|---------|------|
| `AttachedNetwork` | Normal | `attached network <network-<hex>>` | a network is attached on the Update path (selector grew, or a previously not-Ready match became Ready) |
| `DetachedNetwork` | Normal | `detached network <network-<hex>>` | a network is detached (selector shrank / network unlabelled or deleted) |

Create-batch attachments are covered by `CreatedExternalResource`; per-network events
are emitted only for Update-path attach/detach (the dynamic, no-spec-edit changes). The
`network-<hex>` in the message equals the `Network`'s external-name / upstream id —
surfaced as a default `Network` print column (FR-017) for correlation.

## Backward compatibility

Existing Routers using only `networkRef`/`networkID` are unaffected (FR-012): the new
CEL term is satisfied by exactly-one-of as before, and no defaulting changes behavior.
No conversion, no migration.

## Example

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Router
metadata:
  name: edge
  namespace: team-a
spec:
  forProvider:
    name: edge-router
    location: ru-3
    presetName: router-1x1-1gb-ru-3
    networks:
      # to-many: attach every Ready Network labeled router-attach=true
      - networkSelector:
          matchLabels:
            router-attach: "true"
        dhcp: true
      # explicit + NAT for one specific network (wins on overlap)
      - networkRef:
          name: db-net
        natFloatingIP:
          ref:
            name: db-egress
  providerConfigRef:
    name: default
```
