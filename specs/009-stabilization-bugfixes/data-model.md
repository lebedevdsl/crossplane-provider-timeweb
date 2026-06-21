# Phase 1 Data Model — Stabilization & Bugfixes

All changes are **additive** to `status.atProvider` + printcolumn edits
(Constitution I). No `spec` field is removed or renamed. CRDs + `zz_generated_*`
regenerate via `make generate-crds` and commit in the same change.

## KubernetesClusterNodepool

### Status additions / population
| Field | Type | Change | Notes |
|---|---|---|---|
| `status.atProvider.clusterID` | `*string` | **populate on Observe** (field already exists) | Currently set only in Create (`:231`), wiped by Observe's `populateNodepoolStatus`. Set it from the resolved parent on every Observe so the column is never blank in steady state. (FR-001) |
| `status.atProvider.nodes[].publicIP` (or `publicAddress`) | `*string` | **add IF R-2 confirms a public address exists** | Surface the node's public address alongside the existing private `node_ip`. Parsed from the upstream `NodeOut.network` field (currently unparsed in `groupNodeBody`). (FR-003) |

### Printcolumn changes
| Column | Change | JSONPath |
|---|---|---|
| `PUBLIC-IP` → **`PUBLIC`** | rename (boolean intent, not an address) | `.spec.forProvider.publicIP` (unchanged) |
| `CLUSTER` | now populated (no JSONPath change) | `.status.atProvider.clusterID` |

**Validation rule**: `CLUSTER` empty is legitimate only until the cluster ref
resolves; once resolved, it MUST be populated (distinguishes "pending" from the bug).

## Server

| Field | Type | Change | Notes |
|---|---|---|---|
| `status.atProvider.availabilityZone` | `*string` | **add** | Mirror the resolved/effective AZ. A preset can override a requested AZ (bug-11: requested `spb-1`, landed `spb-3`); recording the observed AZ makes placement observable. (FR-004) |

**Optional signal**: when `spec.forProvider.availabilityZone` is set and the
observed AZ differs (preset override), the controller MAY surface a condition/event
noting the override. (FR-004 SHOULD)

**Printcolumn**: no change (server already shows `LOCATION`; AZ is `-o wide`/describe detail or an optional new wide column — design choice in tasks).

## KubernetesCluster

| Field | Type | Change | Notes |
|---|---|---|---|
| `status.atProvider.autoCreatedNetworkID` | `*string` | **add** | For a network-less cluster, record the id of the VPC Timeweb auto-creates, for operator-driven cleanup traceability. **Read-only** (mirror what Observe sees); provider does NOT delete it and does NOT sweep. Source field confirmed in R-7. (FR-011) |

**Printcolumn**: none required (traceability via describe/status; an optional wide
column is a tasks-level choice).

## Configurator catalog entry (resolver-internal, no CRD change)

| Attribute | Use |
|---|---|
| `tags[]` (e.g. `msk_nvme`, `discount35`, `ssd_2022`, `spb_gpu`, `*promo*`) | Classify **standard** vs **promo/legacy** for selection ranking (FR-010) and the non-orderable error (FR-009). Maintained as a tag-prefix allow/deny list in `dimensions.go`/`select_configurator.go`, NOT hardcoded ids. |

## Server / k8s worker create body (no CRD change)

| Body | Field | Change |
|---|---|---|
| k8s nodegroup `configuration` | `gpu` | **conditional on R-1**: if the worker endpoint enforces gpu like `/servers`, always emit `gpu` (default 0); else no change. Master `configuration` keeps **no** gpu field. |

## E2E config (no CRD change)

| Item | Change |
|---|---|
| `TWE_LOCATION` / `TWE_AZ` | new seeded env (default `ru-3` / `msk-1`); bundle manifests use `${TWE_LOCATION}`/`${TWE_AZ}` (FR-006) |

## Relationships

```
KubernetesCluster ──(parent)── KubernetesClusterNodepool
   │  status.autoCreatedNetworkID (US4 traceability)        status.clusterID (US1, populated on Observe)
   │                                                        status.nodes[].publicIP? (US1, R-2)
Server  status.availabilityZone (US1)
Resolver(configurator) ── tag classification ── Server / Nodepool custom sizing (US3)
```
