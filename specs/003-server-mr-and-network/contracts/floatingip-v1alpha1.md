# Contract — `FloatingIP` v1alpha1

**Feature**: 003 | **Group**: `network.m.timeweb.crossplane.io` | **Scope**: Namespaced

A Timeweb floating IPv4 address. Owns both upstream allocation AND its binding to a Server (per R-4 / FR-016).

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
  writeConnectionSecretToRef:           # optional; controller writes `ip`
    name: stable-public-ip
  forProvider:
    location: msk-1                     # required
    comment: "Stable IP for frontend"   # optional, mutable
    availabilityZone: spb-1             # optional, immutable
    isDDoSGuard: false                  # default false

    # Server-binding trio (mutually exclusive, all optional)
    serverRef:
      name: my-server                   # → Server MR in same namespace
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
    resolvedServerID: 1234567           # when bound
    boundAt: 2026-06-01T13:42:00Z
```

## CEL rules

- At most one of `{serverRef, serverSelector, serverID}` SET.
- `location`, `availabilityZone`, `isDDoSGuard` immutable post-create.
- `comment` + binding trio mutable.

## Conditions emitted

| Condition | Status | Reason | Triggered when |
|---|---|---|---|
| `Synced` | `True` | `ReconcileSuccess` | Allocated + bound (or allocated + unbound, matching spec) |
| `Synced` | `False` | `Reconciling` | Waiting on `serverRef` target to reach `Ready=True` |
| `Synced` | `False` | `ImmutableFieldChange` | Operator mutated immutable field |
| `Synced` | `False` | `APIError` | Upstream 4xx (IP quota exceeded, bind to invalid resource, …) |
| `Ready` | `True` | `Available` | Upstream IP allocated (binding state separately tracked in atProvider) |
| `Ready` | `False` | `Creating` | Brief — allocation is typically synchronous |
| `Ready` | `False` | `Deleting` | `metadata.deletionTimestamp` set |

## Lifecycle

1. **Create**:
   1. `POST /api/v1/floating-ips` with `availability_zone` + `is_ddos_guard`. Record `upstreamID` + `ip`.
   2. If binding trio resolves AND target Server is `Ready=True`: `POST /api/v1/floating-ips/{upstreamID}/bind` with `{resource_type: "server", resource_id: <id>}`. Record `resolvedServerID` + `boundAt`.
2. **Observe**: `GET /api/v1/floating-ips/{upstreamID}`. Read `bound_to.resource_id`. Compare with `status.atProvider.resolvedServerID`. Queue action(s) for Update if drift:
   - Operator cleared binding trio + upstream still bound → queue unbind.
   - Operator changed binding target → queue unbind-then-bind.
   - Operator added binding + upstream still unbound → queue bind.
3. **Update**: applies queued action(s). Unbind first if needed; bind second if needed. Each call is idempotent (re-invocations succeed without state change per Constitution §II).
4. **Delete**: if currently bound, unbind first; then `DELETE /api/v1/floating-ips/{upstreamID}`. 404 treated as already-gone.

## Relationships

- `spec.providerConfigRef.{kind,name}` — same as Server.
- `spec.forProvider.serverRef.name` → resolves into a same-namespace `Server` MR's `status.atProvider.upstreamID`. Controller authoritatively binds + unbinds.
- Observed by `Server.atProvider.boundFloatingIPs` (Server controller reads upstream observation, not from any FloatingIP MR).

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
| `BOUND-TO` | `.status.atProvider.resolvedServerID` |
| `LOCATION` | `.spec.forProvider.location` |
| `AGE` | metadata.creationTimestamp |
