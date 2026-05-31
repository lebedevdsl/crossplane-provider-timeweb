# Contract — `Network` v1alpha1

**Feature**: 003 | **Group**: `network.m.timeweb.crossplane.io` | **Scope**: Namespaced

A Timeweb VPC. Resolves to upstream `/api/v2/vpcs/{id}` (with delete via `/api/v1/vpcs/{id}` — R-6).

## Spec

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Network
metadata:
  name: shared
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  forProvider:
    name: team-a-shared                 # appears in dashboard; max 255 chars
    subnetCIDR: 10.30.0.0/24            # IPv4 CIDR, validated by regex; semantics by upstream
    location: msk-1                     # same enum as Server / FloatingIP
    description: "Shared VPC for team-a frontend tier"   # optional, mutable
    availabilityZone: spb-1             # optional, immutable
```

## Status

```yaml
status:
  conditions:
  - type: Synced
    status: "True"
  - type: Ready
    status: "True"
  atProvider:
    upstreamID: vpc-abc123               # string per /api/v2/vpcs
    assignedCIDR: 10.30.0.0/24
```

## CEL rules

- `name`, `subnetCIDR`, `location`, `availabilityZone` immutable post-create.
- `description` mutable.

## Conditions emitted

| Condition | Status | Reason | Triggered when |
|---|---|---|---|
| `Synced` | `True` | `ReconcileSuccess` | Reconcile succeeded |
| `Synced` | `False` | `ImmutableFieldChange` | Operator mutated immutable field |
| `Synced` | `False` | `APIError` | Upstream 4xx (overlapping CIDR, invalid subnet, etc.) |
| `Ready` | `True` | `Available` | Upstream VPC exists |
| `Ready` | `False` | `Creating` | Brief — VPC create is typically synchronous |
| `Ready` | `False` | `Deleting` | `metadata.deletionTimestamp` set |

## Lifecycle

1. **Create** — `POST /api/v2/vpcs` with `name`, `subnet_v4`, `location`, `description`, `availability_zone`. Record `upstreamID`.
2. **Observe** — `GET /api/v2/vpcs/{upstreamID}`.
3. **Update** — `PATCH /api/v2/vpcs/{upstreamID}` for `description` only.
4. **Delete** — `DELETE /api/v1/vpcs/{upstreamID}` (note v1 path).

## Relationships

- `spec.providerConfigRef.{kind,name}` — same as Server.
- Referenced by `Server.forProvider.networkRef`. Cross-MR ownership is non-blocking per Constitution §II — deleting a Network with attached Servers is allowed by Kubernetes; the controller logs a warning per affected Server.

## Printer columns

| Column | Source |
|---|---|
| `READY` | `Ready` condition |
| `SYNCED` | `Synced` condition |
| `CIDR` | `.spec.forProvider.subnetCIDR` |
| `LOCATION` | `.spec.forProvider.location` |
| `UPSTREAM-ID` | `.status.atProvider.upstreamID` |
| `AGE` | metadata.creationTimestamp |
