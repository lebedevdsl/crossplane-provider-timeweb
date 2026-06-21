# Contract — Observability (columns + status)

The CRDs and controllers MUST satisfy this. Convention (from 008):
`READY · SYNCED · <domain columns> · [STATE] · ID · AGE`, with `ID` =
`crossplane.io/external-name` at `priority=1` (wide-only).

## KubernetesClusterNodepool

Columns: `READY · SYNCED · CLUSTER · PRESET · PUBLIC · DESIRED · OBSERVED · ID · AGE`
- `CLUSTER` → `.status.atProvider.clusterID` — **MUST be non-empty once the
  parent cluster reference is resolved** (populated on every Observe, not just
  Create).
- `PUBLIC` (renamed from `PUBLIC-IP`) → `.spec.forProvider.publicIP` — boolean
  intent (true / false / unset⇒default-public); MUST NOT be named as if it were
  an address.

Status:
- `status.atProvider.nodes[]` — each node MUST expose its private address (`ip`,
  existing) and, **if a public address exists (R-2)**, its public address. Node
  status MUST NOT imply "private" when a public address is present.

## Server

- `status.atProvider.availabilityZone` — MUST record the resolved/effective AZ so
  a preset-driven override of the requested zone is observable.
- When `spec.forProvider.availabilityZone` ≠ observed AZ, the controller SHOULD
  surface the override (condition or event).

## KubernetesCluster

- `status.atProvider.autoCreatedNetworkID` — for a network-less cluster, MUST
  record the auto-created VPC id (read-only; no delete, no sweep).

## Negative / edge contracts

- A nodepool whose parent ref is unresolved MAY show an empty `CLUSTER` (pending),
  but a resolved-and-Ready nodepool MUST NOT.
- The provider MUST NOT delete the auto-created network and MUST NOT add it to the
  orphan sweep.
