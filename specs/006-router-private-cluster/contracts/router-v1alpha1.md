# Contract — Router v1alpha1 (`network.m.timeweb.crossplane.io`)

Operator-facing CRD contract. Field semantics in `../data-model.md`; upstream
mapping in `timeweb-router-endpoints.md`.

## Spec (`forProvider`)

| Field | Type | Required | Mutable | Notes |
|---|---|---|---|---|
| `name` | string | ✅ | ✅ | 1–250 chars; converges via rename |
| `comment` | *string | — | ✅ | |
| `availabilityZone` | enum `msk-1\|spb-3\|ams-1\|fra-1` | ✅ | ❌ | zone↔tier-location validated pre-create (FR-003) |
| `presetName` | string | ✅ | FR-002a | tier slug via `DimRouterPreset`; carries node count (HA); mutable once resize op captured, otherwise rejected |
| `networks[]` | attachment list | ✅ minItems=1 | ✅ set-diff | CEL: each entry exactly-one-of `networkRef`/`networkID` |
| `networks[].natFloatingIP` | *selector | — | ✅ | exactly-one-of `ref`/`ip`; absent = NAT off; presence = the admission guarantee (no NAT-without-address) and the explicit IP↔network mapping; addresses never ordered by the Router |
| `networks[].dhcp` | bool | — | ✅ | per-attachment PATCH |
| `networks[].gateway`, `networks[].reservedIPs` | optional | — | ❌ create-only v1 (drift ignored, documented) | |
| `projectRef`/`projectID` | trio | — | ❌ | standard project idiom |

## Status (`atProvider`)

`upstreamID` (router UUID = external-name), `state` (raw), `lockedPresetID`,
`networks[]` (id, name, gateway, `natIP` — nil means NAT off, `dhcpEnabled`,
`reservedIPs`), `ips[]` (address + `natNetwork`), `parentServices[]`
(id+type, e.g. the bound K8s cluster), `resolvedProjectID`.
SC-004 contract: NAT/gateway/IP questions answerable from status alone.

## Conditions

| Situation | Synced | Ready | Reason |
|---|---|---|---|
| Running, converged | True | True | Available |
| Provisioning / resizing | True | False | Creating |
| Upstream failed/error state | True | False | `UpstreamFailed` |
| Account unfunded | True | False | `PaymentRequired` |
| Tier not in zone catalog | False | — | resolver vocabulary (`PresetNotFound`/ambiguous) |
| Delete requested while `parentServices` non-empty | — | False | per FR-012/R-5 outcome (pending-with-reason unless upstream blocks itself) |

## Events

Attach/detach/DHCP/NAT convergence steps and rejected mutations emit Events
(operator-visible even where the runtime overwrites condition reasons —
feature-005 lesson).
