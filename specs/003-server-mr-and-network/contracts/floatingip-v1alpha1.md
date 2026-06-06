# Contract — `FloatingIP` v1alpha1

**Feature**: 003 | **Group**: `network.m.timeweb.crossplane.io` | **Scope**: Namespaced

> **Reversed 2026-06-01** (spec.md "FloatingIP reference reversal" session).
> `FloatingIP` is now **pure allocation** — it owns ONLY the upstream
> allocate (`POST /api/v1/floating-ips`) and release (`DELETE
> /api/v1/floating-ips/{id}`). **Binding to a Server is driven by the
> consuming `Server` MR** via `Server.forProvider.floatingIPRefs` — the
> Server controller is the single owner of `bind`/`unbind` (Constitution
> §II). `FloatingIP` has NO `serverRef`/`serverSelector`/`serverID`. This
> mirrors GCP `compute.Address`←`Instance`, Azure `PublicIPAddress`←NIC,
> AWS `Eip`←`Instance`, and matches the upstream bind API's generic
> `resource_type ∈ {server, balancer, database, network}` enum.

## Spec

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: FloatingIP
metadata:
  name: stable-public
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:           # optional; controller writes `ip` + `upstreamID`
    name: stable-public-ip
  forProvider:
    location: ru-1                      # required; same enum as Server / Network
    comment: "Stable IP for frontend"   # optional, mutable
    availabilityZone: spb-1             # optional, immutable
    isDDoSGuard: false                  # default false; immutable
```

There is **no server-binding field on FloatingIP**. To bind this IP to a
server, set it on the Server instead:

```yaml
# Server MR (compute group) — the binding owner:
spec:
  forProvider:
    floatingIPRefs:
    - name: stable-public               # → this FloatingIP MR, same namespace
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
    upstreamID: fip-abc123
    ip: 5.6.7.8
    observedBoundTo:                    # diagnostic mirror of upstream bound_to
      resourceType: server              # one of: server | balancer | database | network
      resourceID: 1234567               # upstream id of the bound resource
```

`observedBoundTo` is **purely diagnostic** (`kubectl describe` parity) —
it reflects the upstream `bound_to` field verbatim. The authoritative
binding state lives on the consuming `Server.status.atProvider.boundFloatingIPs`.

## CEL rules

- `location`, `availabilityZone`, `isDDoSGuard` immutable post-create.
- `comment` mutable.
- (No mutual-exclusion rule — the binding trio moved to `Server`.)

## Conditions emitted

| Condition | Status | Reason | Triggered when |
|---|---|---|---|
| `Synced` | `True` | `ReconcileSuccess` | Allocation reconciled (comment in sync) |
| `Synced` | `False` | `ImmutableFieldChange` | Operator mutated `location`/`availabilityZone`/`isDDoSGuard` |
| `Synced` | `False` | `APIError` | Upstream 4xx (IP quota exceeded, etc.) |
| `Ready` | `True` | `Available` | Upstream IP allocated |
| `Ready` | `False` | `Creating` | Brief — allocation is typically synchronous |
| `Ready` | `False` | `Deleting` | `metadata.deletionTimestamp` set |

## Lifecycle

1. **Create** — `POST /api/v1/floating-ips` with `is_ddos_guard` +
   `availability_zone`. Record `upstreamID` + `ip`. No binding here — the IP
   is allocated unbound; the Server controller binds it later.
2. **Observe** — `GET /api/v1/floating-ips/{upstreamID}`. Populate `ip` and
   mirror `bound_to` into `observedBoundTo` (diagnostic). `ResourceUpToDate`
   compares the mutable `comment` only.
3. **Update** — `PATCH /api/v1/floating-ips/{upstreamID}` for `comment`.
   Immutable drift on `location`/`availabilityZone`/`isDDoSGuard` →
   `ImmutableFieldChange`. **No bind/unbind here** — that's the Server's job.
4. **Delete** — `DELETE /api/v1/floating-ips/{upstreamID}`. 404 idempotent.
   If the IP is still bound upstream, the Server controller is expected to
   have unbound it during the Server's own delete/repoint flow; a bound IP
   delete that the upstream rejects surfaces as a normal APIError to
   investigate (we do NOT have the FloatingIP controller force-unbind, to
   keep bind/unbind single-owner on the Server).

## Relationships

- `spec.providerConfigRef.{kind,name}` — same as Server.
- **Consumed by** `Server.forProvider.floatingIPRefs` (list). The Server
  controller resolves each ref to this MR's `status.atProvider.upstreamID`,
  then owns `POST /floating-ips/{id}/bind` + `/unbind`.
- `Server.status.atProvider.boundFloatingIPs` is the authoritative list of
  bound floating-IP upstream IDs (strings). `FloatingIP.status.atProvider.observedBoundTo`
  is the diagnostic mirror from the IP's own GET.

## Connection Secret

When `writeConnectionSecretToRef` is set, the controller publishes:

- `ip` — the floating IPv4 address (e.g. `5.6.7.8`)
- `upstreamID` — the Timeweb floating-IP ID

## Printer columns

| Column | Source |
|---|---|
| `READY` | `Ready` condition |
| `SYNCED` | `Synced` condition |
| `IP` | `.status.atProvider.ip` |
| `BOUND-RES` | `.status.atProvider.observedBoundTo.resourceType` |
| `BOUND-TO` | `.status.atProvider.observedBoundTo.resourceID` |
| `LOCATION` | `.spec.forProvider.location` |
| `AGE` | metadata.creationTimestamp |
