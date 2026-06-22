# Phase 1 Data Model: Router Multi-Network Attachment & Selectors

This feature extends one existing struct (`RouterNetworkAttachment`) and adds no new
kinds. Status is unchanged (R-8). All other Router types from feature 006 carry
forward verbatim.

## Modified entity: `RouterNetworkAttachment`

Located in `apis/network/v1alpha1/router_types.go`. One new optional field plus
tightened CEL. Existing fields unchanged.

```go
// RouterNetworkAttachment is one private network (or, via selector, a set of
// private networks) attached to the Router.
//
// Exactly one selection mode per entry: networkRef OR networkID OR networkSelector.
// +kubebuilder:validation:XValidation:rule="(has(self.networkRef) ? 1 : 0) + (has(self.networkID) ? 1 : 0) + (has(self.networkSelector) ? 1 : 0) == 1",message="exactly one of networkRef, networkID, or networkSelector must be set"
// A selector must constrain at least one label (no accidental match-all).
// +kubebuilder:validation:XValidation:rule="!has(self.networkSelector) || size(self.networkSelector.matchLabels) > 0 || size(self.networkSelector.matchExpressions) > 0",message="networkSelector must specify at least one matchLabels entry or matchExpressions term"
// NAT is per single network; it cannot pair with a to-many selector.
// +kubebuilder:validation:XValidation:rule="!(has(self.networkSelector) && has(self.natFloatingIP))",message="natFloatingIP cannot be combined with networkSelector; use networkRef/networkID for NAT'd networks"
type RouterNetworkAttachment struct {
    // NetworkRef names a single Network resource in the same namespace.
    // +optional
    NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`

    // NetworkID is the raw upstream network id (network-<hex>) for a single
    // network not modeled as a Network resource.
    // +optional
    NetworkID *string `json:"networkID,omitempty"`

    // NetworkSelector attaches EVERY Ready Network in the Router's namespace
    // whose labels match (to-many expansion). The attached set converges
    // continuously as networks are created, (un)labeled, or deleted. Must
    // constrain at least one label. Cannot be combined with natFloatingIP.
    // The DHCP / Gateway / ReservedIPs on this entry become the defaults for
    // every network the selector brings in.
    // +optional
    NetworkSelector *metav1.LabelSelector `json:"networkSelector,omitempty"`

    // NATFloatingIP — unchanged. Valid only on networkRef/networkID entries.
    // +optional
    NATFloatingIP *FloatingIPSelector `json:"natFloatingIP,omitempty"`

    // DHCP — unchanged; applies to the network(s) this entry resolves to.
    // +optional
    DHCP bool `json:"dhcp,omitempty"`

    // Gateway — unchanged (create-only); applies to the resolved network(s).
    // +optional
    Gateway *string `json:"gateway,omitempty"`

    // ReservedIPs — unchanged (create-only); applies to the resolved network(s).
    // +optional
    ReservedIPs []string `json:"reservedIPs,omitempty"`
}
```

Note: adds the `k8s.io/apimachinery/pkg/apis/meta/v1` (`metav1`) import (already used
elsewhere in the package). `DeepCopy` for `*metav1.LabelSelector` is generated.

### Field rules summary

| Field | Type | Required | Mutable | Notes |
|-------|------|----------|---------|-------|
| `networkRef` | `*xpv2.Reference` | one-of trio | ✅ | single Network by name |
| `networkID` | `*string` | one-of trio | ✅ | single raw upstream id |
| `networkSelector` | `*metav1.LabelSelector` | one-of trio | ✅ | **new**; to-many; non-empty; no NAT |
| `natFloatingIP` | `*FloatingIPSelector` | — | ✅ | rejected with `networkSelector` |
| `dhcp` | `bool` | — | ✅ | default for resolved network(s) |
| `gateway` | `*string` | — | ❌ create-only | default for resolved network(s) |
| `reservedIPs` | `[]string` | — | ❌ create-only | default for resolved network(s) |

`RouterParameters.Networks` keeps `+kubebuilder:validation:MinItems=1` AND gains
`+kubebuilder:validation:MaxItems=64` (added during implementation). `MinItems`/
`MaxItems` bound **declared entries**, not **resolved networks**. One selector entry
satisfies `MinItems` yet may resolve to zero networks; the resolved-count invariant is
enforced at runtime (see state transitions). `MaxItems` is required so the per-entry
CEL rules (which call `size()` on the selector's `matchLabels`/`matchExpressions`) stay
within the apiserver's CEL **cost budget** — without it the estimated cost is
multiplied by an unbounded array and the apiserver rejects the CRD (live-verified;
see research R-9). It does NOT cap what a selector resolves to (FR-014).

## Unchanged entity: `FloatingIPSelector`

No change. Remains `ref`/`ip` exactly-one-of. A label-selector for floating IPs is out
of scope (spec Assumptions).

## Unchanged entity: `RouterObservation` (status)

No change (R-8). `status.atProvider.networks []RouterNetworkStatus` already mirrors the
upstream per-network table (id/name/gateway/natIP/dhcpEnabled/reservedIPs) from the
Observe GET, so selector-resolved networks appear there automatically — satisfying
FR-011 and SC-007 with no new field.

## Events (added during implementation — FR-016)

The Router controller emits a Normal `Event` per network attach/detach on the
Update path (the selector-driven, no-spec-edit changes):
- `AttachedNetwork` — message `attached network <network-<hex>>`
- `DetachedNetwork` — message `detached network <network-<hex>>`

Create-batch attachments do not emit per-network events (the existing
`CreatedExternalResource` covers create); only Update-path attach/detach do, which
is where the dynamic selector changes happen.

## Observability on the `Network` kind (added during implementation — FR-017)

Print columns on `Network` are adjusted so the upstream id is visible by default
(to correlate with the events above): `ID`
(`.metadata.annotations.crossplane.io/external-name`, which equals
`status.atProvider.upstreamID` and the `network-<hex>` in the events) is promoted
to a default column; the former `STATE` column — which was sourced from the VPC
`type` and is effectively always `bgp` (no VPC lifecycle state exists) — is
relabelled `TYPE` and demoted to `-o wide` (`priority=1`). No status schema change.

## Internal (non-API) type: `resolvedAttachment`

Located in `internal/controller/network/refs.go` (carried on the external, never
written to spec). Unchanged in shape; now produced by selector expansion as well as
ref/id resolution. One `resolvedAttachment` per **distinct upstream network id** after
dedup.

```go
type resolvedAttachment struct {
    NetworkID   string   // upstream network id (network-<hex>) — dedup key
    NATIP       string   // resolved NAT address; "" = NAT off (always "" for selector-sourced)
    DHCP        bool
    Gateway     *string
    ReservedIPs []string
}
```

## Resolution algorithm (FR-002, FR-005, FR-006, FR-007)

Input: `cr.Spec.ForProvider.Networks`, namespace `ns`. Output: `[]resolvedAttachment`
or a blocking error.

```text
byID := map[string]resolvedAttachment{}        // dedup key = upstream network id

# Pass 1 — explicit entries first (they win on overlap)
for entry in Networks where networkRef or networkID:
    id := resolve(entry)                        # existing Get + Ready/upstreamID gate
    byID[id] = resolvedAttachment{id, NAT?, DHCP, Gateway, ReservedIPs}

# Pass 2 — selector entries fill remaining ids
for entry in Networks where networkSelector:
    sel := LabelSelectorAsSelector(entry.networkSelector)
    for net in List(Network, ns, MatchingLabelsSelector{sel}):
        if net not Ready or net.upstreamID == "":   # FR-007: skip, not error
            continue
        if net.upstreamID in byID:                  # FR-006: explicit/earlier wins
            continue
        byID[net.upstreamID] = resolvedAttachment{
            NetworkID: net.upstreamID, NATIP: "",    # selector entries never NAT
            DHCP: entry.DHCP, Gateway: entry.Gateway, ReservedIPs: entry.ReservedIPs,
        }

attachments := values(byID)
if len(attachments) == 0:                            # FR-008 / SC-005
    return Block("NoNetworksResolved")
return attachments
```

## State transitions / lifecycle

- **Create** — gated on `len(resolved) >= 1`; otherwise blocked
  (`reason=NoNetworksResolved`, requeue). Never sends a zero-network create.
- **Observe** — read-only; re-resolves the selector match set each reconcile and
  compares (by upstream id) against the upstream GET network list. Drift (member added
  / removed / DHCP changed) → not up-to-date → Update. Status mirrors the GET.
- **Update (paced)** — compute attach-diff (resolved − upstream) and detach-diff
  (upstream − resolved). Apply at most `maxAttachOpsPerReconcile` total mutations this
  call (FR-014), in a stable order, then return without claiming convergence. Never
  apply a detach that would drop the upstream to zero networks (skip + block reason).
- **Delete** — unchanged; uses external-name + persisted status only; blocked while
  `parentServices` non-empty (feature 006 FR-012). Selector resolution is skipped on
  delete (existing behavior).

## Relationships

```text
Project ←─(projectRef/ID)── Router ──(networks[].networkRef/ID)──────────→ Network (single)
                              │      └─(networks[].networkSelector, NEW)──→ Network[] (to-many, by label, Ready-gated)
                              │      └─(networks[].natFloatingIP.ref/ip)──→ FloatingIP   (explicit entries only)
                              └─(status.atProvider.networks ⟵ upstream GET; status.parentServices)
```
