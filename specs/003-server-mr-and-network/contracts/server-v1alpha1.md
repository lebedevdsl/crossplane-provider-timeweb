# Contract — `Server` v1alpha1

**Feature**: 003 | **Group**: `compute.m.timeweb.crossplane.io` | **Scope**: Namespaced

Operator-facing contract for the cloud Server MR. Resolves to upstream `/api/v1/servers/{id}`.

## Spec

```yaml
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata:
  name: my-server
  namespace: team-a
spec:
  providerConfigRef:                    # post upstream-alignment (feat 002): kind required
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:           # optional; controller writes publicIP/privateIP/hostname
    name: my-server-conn
  forProvider:
    # Required
    name: my-server                     # max 255 chars
    presetName: premium-2-2-40-msk-1    # slug from /api/v1/presets/servers resolver
    location: msk-1                     # enum
    os:
      image: ubuntu                     # lowercase family slug
      version: "24.04"                  # exact upstream version string

    # Optional plain fields
    hostname: web-01
    comment: "frontend node"
    cloudInit: |                        # max 16 KiB; pass-through
      #!/bin/bash
      echo "hello" >/etc/motd
    availabilityZone: spb-1             # optional; defaults per location

    # Optional cross-resource refs (at most one of each {Ref, Selector, ID} trio set)
    sshKeyRefs:
    - name: dev-key                     # → SshKey MR in same namespace
    networkRef:
      name: shared-vpc                  # → Network MR in same namespace
    projectRef:
      name: team-a-default              # → existing Project MR in same namespace
    floatingIPRefs:                     # OBSERVE-ONLY (FR-017) — does not drive binding
    - name: stable-public
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
    upstreamID: 1234567                 # Timeweb server_id
    lockedPresetID: 234                 # resolved from presetName
    lockedOSID: 47                      # resolved from os
    publicIP: 5.6.7.8
    publicIPv6: 2a00:…::1
    privateIP: 10.20.0.5                # when network-attached
    resolvedNetworkID: vpc-abc123
    resolvedProjectID: 2277851
    resolvedSshKeyIDs: [4242]
    boundFloatingIPs: [42]              # observed from upstream
    state: on                           # one of: installing, starting, on, off, rebooting, transfer, removing
```

## CEL rules

- At most one of `{networkRef, networkSelector, networkID}` SET.
- At most one of `{projectRef, projectSelector, projectID}` SET.
- After create, the following are immutable (mutation → `Synced=False, reason=ImmutableFieldChange`):
  - `presetName`, `location`, `availabilityZone`
  - `os.image`, `os.version`
  - `sshKeyRefs` / `sshKeySelector` / `sshKeyIDs`
  - `networkRef` / `networkSelector` / `networkID`
  - `projectRef` / `projectSelector` / `projectID`
- Mutable post-create: `name`, `hostname`, `comment`, `cloudInit`, `floatingIPRefs` (cosmetic).

## Conditions emitted

| Condition | Status | Reason | Triggered when |
|---|---|---|---|
| `Synced` | `True` | `ReconcileSuccess` | Successful reconcile (upstream state matches spec) |
| `Synced` | `False` | `Reconciling` | Waiting on a `networkRef` target to reach `Ready=True` |
| `Synced` | `False` | `ReconcileError` | Resolver `PresetNotFound`, OS not found, network location mismatch, upstream 4xx |
| `Synced` | `False` | `ImmutableFieldChange` | Operator mutated a locked field |
| `Synced` | `False` | `APIError` | Upstream non-classified terminal error |
| `Ready` | `True` | `Available` | Upstream `state == "on"` |
| `Ready` | `False` | `Creating` | Upstream `state ∈ {installing, starting}` |
| `Ready` | `False` | `Unavailable` | Upstream `state ∈ {off, rebooting, transfer}` |
| `Ready` | `False` | `Deleting` | `metadata.deletionTimestamp` set (standard runtime behavior) |

## Lifecycle

1. **Create** — resolver resolves `presetName` + `os` → upstream IDs (caches via the resolver primitive). Cross-MR refs resolve to flat IDs (via crossplane-runtime's `reference.ResolveOne`/`ResolveMultiple`). `POST /api/v1/servers` with the full body. Record `upstreamID`, `lockedPresetID`, `lockedOSID`, `resolvedNetworkID`, `resolvedProjectID`, `resolvedSshKeyIDs`. Status `state="installing"`. Connection Secret written when populated.
2. **Observe** — `GET /api/v1/servers/{upstreamID}`. Sync mutable + observable fields into `atProvider`. Detect drift on locked IDs (rare — implies an upstream-side change we don't make).
3. **Update** — `PATCH /api/v1/servers/{upstreamID}` for `name`, `hostname`, `comment`, `cloudInit`. Anything else → `ImmutableFieldChange`.
4. **Delete** — `DELETE /api/v1/servers/{upstreamID}`. 404 treated as already-gone (idempotent).

## Relationships

- `spec.providerConfigRef.{kind,name}` — `kind` is `ProviderConfig` or `ClusterProviderConfig`. Hard-switch + no fallback (per feat 002).
- `spec.forProvider.networkRef.name` → resolves into a same-namespace `Network` MR's `status.atProvider.upstreamID`. Controller blocks Create until target `Ready=True`.
- `spec.forProvider.projectRef.name` → resolves into a same-namespace `Project` MR's `status.atProvider.upstreamID`.
- `spec.forProvider.sshKeyRefs[].name` → resolves into same-namespace `SshKey` MRs' `status.atProvider.upstreamID`.
- `spec.forProvider.floatingIPRefs[].name` — observe-only relationship; the `FloatingIP` MR owns mutation per FR-017.

## Printer columns

| Column | Source |
|---|---|
| `READY` | `Ready` condition |
| `SYNCED` | `Synced` condition |
| `LOCATION` | `.spec.forProvider.location` |
| `PRESET` | `.spec.forProvider.presetName` |
| `PUBLIC-IP` | `.status.atProvider.publicIP` |
| `STATE` | `.status.atProvider.state` |
| `AGE` | metadata.creationTimestamp |
