# Data Model — Router & Private Kubernetes Cluster Networking

## 1. Router (NEW kind — `network.m.timeweb.crossplane.io/v1alpha1`)

```go
type RouterParameters struct {
    // Name is the upstream display name. Mutable (PATCH).
    Name string `json:"name"` // 1–250 chars (upstream-validated; mirrored in CRD)

    // Comment is the upstream free-text comment. Mutable.
    // +optional
    Comment *string `json:"comment,omitempty"`

    // AvailabilityZone pins the router's zone (msk-1|spb-3|ams-1|fra-1 enum,
    // same vocabulary as KubernetesCluster). The upstream derives zone from
    // the tier; the provider resolves tier-in-zone and validates the pairing
    // BEFORE create (FR-003). Immutable post-create.
    AvailabilityZone string `json:"availabilityZone"`

    // PresetName selects the size tier by slug, resolved against
    // /presets/routers via DimRouterPreset, location-filtered by the zone
    // (azLocation). Tier carries node count (1-node vs 2-node HA).
    // Mutable IF the resize capture lands (R-4); until then edits are
    // rejected with the immutability vocabulary.
    PresetName string `json:"presetName"`

    // Networks declares the attached private networks. minItems=1 (upstream
    // requires a router to always have one). Order-insensitive set semantics.
    // +kubebuilder:validation:MinItems=1
    Networks []RouterNetworkAttachment `json:"networks"`

    // ProjectRef / ProjectID — standard project-assignment trio (as on
    // Server/KubernetesCluster).
    // +optional
    ProjectRef *xpv2.Reference `json:"projectRef,omitempty"`
    // +optional
    ProjectID *int64 `json:"projectID,omitempty"`
}

type RouterNetworkAttachment struct {
    // Exactly one of NetworkRef / NetworkID (CEL exactly-one-of, same idiom
    // as the KubernetesCluster network attach).
    // +optional
    NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`
    // +optional
    NetworkID *string `json:"networkID,omitempty"`

    // NATFloatingIP enables internet egress for this network through the
    // referenced floating IP. Absent = NAT off. The explicit per-attachment
    // reference makes the IP↔network mapping declarative (one IP serves one
    // network — FR-004a/SC-004) and is itself the admission guarantee: NAT
    // cannot be declared without an address (the upstream silently leaves
    // NAT off in that case). The Router never orders addresses (spec Q1).
    // +optional
    NATFloatingIP *FloatingIPSelector `json:"natFloatingIP,omitempty"`

    // DHCP serves addresses on this network (upstream is_dhcp_enabled,
    // PATCH-converged per attachment).
    // +optional (default false)
    DHCP bool `json:"dhcp,omitempty"`

    // Gateway / ReservedIPs — optional create-time fields (verified in the
    // dashboard's captured create payload). Create-only in v1: documented as
    // such, drift on these two fields is IGNORED by Observe (no CEL
    // transition enforcement — list items keyed by network ref can't be
    // sanely tracked by oldSelf rules; revisit only if operators trip on it).
    // +optional
    Gateway *string `json:"gateway,omitempty"`
    // +optional
    ReservedIPs []string `json:"reservedIPs,omitempty"`
}

// FloatingIPSelector targets a FloatingIP by resource reference or raw
// address (a ref/ID pair, consistent with RouterNetworkAttachment — no label
// selector in v1).
type FloatingIPSelector struct {
    // Exactly one of Ref / IP (raw address). CEL exactly-one-of.
    // +optional
    Ref *xpv2.Reference `json:"ref,omitempty"`
    // +optional
    IP *string `json:"ip,omitempty"`
}

type RouterObservation struct {
    UpstreamID *string `json:"upstreamID,omitempty"` // router UUID (external-name)
    State      *string `json:"state,omitempty"`      // raw upstream status
    LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

    // Networks mirrors GET /routers/{id}/networks (the dashboard table):
    Networks []RouterNetworkStatus `json:"networks,omitempty"`
    // IPs mirrors router.ips: each public address and which network it NATs.
    IPs []RouterIPStatus `json:"ips,omitempty"`
    // ParentServices surfaces upstream bindings (e.g. a K8s cluster).
    ParentServices []RouterParentService `json:"parentServices,omitempty"`
    ResolvedProjectID *int64 `json:"resolvedProjectID,omitempty"`
}

type RouterNetworkStatus struct {
    ID          string   `json:"id"`
    Name        *string  `json:"name,omitempty"`
    Gateway     *string  `json:"gateway,omitempty"`
    NATIP       *string  `json:"natIP,omitempty"`   // nil = NAT off (SC-004)
    DHCPEnabled *bool    `json:"dhcpEnabled,omitempty"`
    ReservedIPs []string `json:"reservedIPs,omitempty"`
}

type RouterIPStatus struct {
    IP         string  `json:"ip"`
    NATNetwork *string `json:"natNetwork,omitempty"` // network id this IP NATs
}

type RouterParentService struct {
    ID   string `json:"id"`
    Type string `json:"type"` // e.g. "k8s"
}
```

**Lifecycle**: external-name = router UUID from the create response. Observe =
`GET /routers/{id}` + `GET /routers/{id}/networks` (read-only) and is the
**sole convergence authority**: `isRouterUpToDate` compares the FULL declared
state — name, comment, attachment set membership, per-attachment DHCP and
NAT-IP, and the resolved tier vs `lockedPresetID` (populated by **Observe**
from the GET, never Create-only — the critical-annotation refresh wipes
Create-set status). Update applies the diff in one pass (PATCH name/comment →
attach missing → detach extra → PATCH drifted DHCP → NAT op) and returns
WITHOUT claiming convergence — a silently-dropped write simply yields
`upToDate=false` on the next poll (the managed reconciler IS the
re-observation loop; no in-Update verification reads). Special case: while
observed `state == "starting"`, Update returns early without writing
(probe-verified: such writes are dropped). Delete: refuse/pend while
`parentServices` non-empty per FR-012 outcome (R-5), else `DELETE
/routers/{id}` (attached networks survive by upstream design).

**Conditions**: `Ready` from upstream state (started → Available;
failed/*error* → `UpstreamFailed`; else Creating); `no_paid` →
`PaymentRequired`; unsatisfiable tier → `PresetNotFound`-family from the
resolver. Both Synced and Ready meaningful (feature-005 lesson: never derive
Ready from an echoed field — the per-network/NAT status comes from actual
sub-resource reads).

### 1a. NAT convergence (re-plan 2026-06-17)

The official spec resolved R-3: NAT is toggled by a dedicated endpoint with a
typed body, not by create-time `nat` (which is silently ignored — observed
`nat_ip` stays `""` after create).

```go
// NatIn is the body of PATCH /api/v1/routers/{router_id}/networks/{network_name}/nat
type NatIn struct {
    NatIP string `json:"nat_ip"` // required — the floating-IP address to NAT this network through
}
```

- **Enable NAT**: `PATCH …/networks/{network_name}/nat` with `NatIn{nat_ip}`.
- **Disable NAT**: `DELETE …/networks/{network_name}/nat`.

`convergeNAT` semantics: **Observe is the sole convergence authority** — it
compares, per attachment, the observed `nat_ip` (from
`GET /routers/{id}/networks`) against the declared `natFloatingIP` (resolved to
an address). Update's `convergeNAT` step then enables (PATCH `NatIn`) where
declared-but-absent, disables (DELETE) where present-but-undeclared, and
returns WITHOUT claiming convergence (re-observed next poll). Because
create-time `nat` never applies, **without this toggle the observed `nat_ip`
stays `""`, `isRouterUpToDate` is perpetually false, and the controller loops
on `Update`/`NATConvergencePending`** until `convergeNAT` lands (proven live:
manually enabling NAT in the dashboard converged the router and stopped the
loop). NAT path-param identity (`{network_name}` upstream vs the
`network-xxxx` id the controller passes) is flagged for live confirmation
(research re-plan).

## 2. Resolver dimension change

| Dimension | Before | After |
|---|---|---|
| `DimRouterPreset` | (did not exist) | `{kind: Preset, fetch: fetchRouterPresets}` over `GET /api/v1/presets/routers` (hand-patched path; envelope `router_presets`) |

Slug rule: `Slugify(description-equivalent)-<location>` consistent with server
presets; entries carry `location` (azLocation-paired with the CRD zone) and
`node_count` (HA tier). `TestDefaultRegistry_Discoverable` gains the row.

## 3. Touched existing kinds

- **FloatingIP** (feature 003): **F-5 schema change (re-plan 2026-06-17)** —
  `FloatingIPBindingObservation` gains a string `resourceUUID` (decoded from
  `AsFloatingIpResourceId1`). Router bindings are UUID-keyed, so the existing
  int64 `resourceID` / `BOUND-TO` print column cannot render them; the string
  field carries the UUID. The `resourceType` doc enum gains `router` (it
  currently lists only `server/balancer/database/network`). The controller
  also learns router-related bind states are possible — Observe must not treat
  those as drift.
- **KubernetesCluster** (feature 004): no schema change (R-5 resolved: no
  explicit binding op). Controller changes: error-yet-created adoption guard
  on Create (D-2) and post-create AZ-echo verification (D-4).
  - **(as-built 2026-06-17)** Router→cluster binding decision (T004): KEEP the
    derived `parentServices` status mirror rather than a create-time
    `RouterIn.parent_service`. In the private-cluster flow the Router is created
    **before** the cluster exists (it attaches to the network first), so there
    is no cluster id to bind at router-create time. The binding forms when the
    cluster is later created on the router's network, and the provider observes
    it in `status.atProvider.parentServices`. The explicit create-time
    `parent_service` field stays unused for US3.
- **KubernetesClusterNodepool** (feature 004/005): **DROPPED (re-plan
  2026-06-17)** — the previously-planned additive `publicIP *bool` /
  `worker_groups[].public_ip_enabled` is NOT in the official spec and is
  removed from `NodeGroupIn` and `nodepool_external.go`. US3 needs no per-node
  field: private workers come from placement on a router-NAT'd network + a
  default route via the gateway. Bundle 19 validates workers carry no public
  address under this arrangement.
- **K8s preset dimensions** (feature 004): `fetchK8sPresetsByType` gains the
  hidden `location`/`availability_zone` fields (hand-patched into the preset
  schema) and `DimKubernetesMasterPreset`/`WorkerPreset` resolution becomes
  location-first (azLocation), fixing the latent mis-placement bug (FR-007a).
  The e2e `slugByRole` discovery filters by zone accordingly.
- **Network** (feature 003): no change; attachments never delete it (FR-005).

## 4. Relationships

```text
Project ←─(projectRef)── Router ──(networks[].networkRef, minItems=1)──→ Network
                           │  └──(floatingIPs[].ref)──→ FloatingIP (NAT addresses)
                           └──(status.parentServices, derived upstream)──→ KubernetesCluster
KubernetesCluster ──(networkRef)──→ Network   ← the private-cluster arrangement (US3):
                                                cluster on a router-NAT'd network
                                                ⇒ worker nodes get no public IPs
```
