# Cloud Servers, Networks & Floating IPs

Operator guide for the v0.3 compute/network kinds: **`Server`** (cloud VM),
**`Network`** (VPC), and **`FloatingIP`** (floating IPv4). For the storage-class
kinds (`S3Bucket`, `ContainerRegistry`) see [`presets.md`](./presets.md).

| Kind | API group | Purpose |
|---|---|---|
| `Server` | `compute.m.timeweb.crossplane.io/v1alpha1` | Cloud server (VM). Sized via `presetName`; OS via `os.{image,version}`. |
| `Network` | `network.m.timeweb.crossplane.io/v1alpha1` | VPC (private network). `subnetCIDR` + `location`. |
| `FloatingIP` | `network.m.timeweb.crossplane.io/v1alpha1` | Floating IPv4. Pure allocation; bound **from a Server**. |

All three are namespaced Crossplane v2 managed resources and use the shared
`ProviderConfig` / `ClusterProviderConfig` pair (see the top-level README).

> **Location codes are the upstream API values, not dashboard labels.** Use
> `ru-1`, `ru-2`, `ru-3`, `nl-1`, `de-1`, `kz-1`, `us-4`, `pl-1` — e.g. the
> dashboard's "Москва · MSK-1" is `ru-1` to the API. The CRD enum rejects the
> dashboard labels.

## Minimum viable Server

```yaml
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata:
  name: web-01
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:
    name: web-01-conn                  # publishes publicIP / publicIPv6 / privateIP / hostname / upstreamID
  forProvider:
    name: web-01
    presetName: ssd-15-ru-1           # resolved against /api/v1/presets/servers
    location: ru-1
    os:
      image: ubuntu                   # matched (case-insensitive) against /api/v1/os/servers
      version: "24.04"
    sshKeyRefs:                       # optional; → SSHKey MRs in the same namespace
    - name: dev-key
```

- `presetName` is the `<description_short>-<location>` slug resolved by the
  in-controller catalog resolver. A bad slug surfaces `Synced=False,
  reason=PresetNotFound` with the list of valid slugs your token can see.
- `os.image` + `os.version` are matched against `/api/v1/os/servers`.
- The Server moves `installing → starting → on`; `Ready=True` flips at `on`
  (typically ≤10 min for the smallest preset).

Mutable post-create: `name`, `hostname`, `comment`, `cloudInit`. Everything
else (`presetName`, `location`, `os`, `availabilityZone`, the SSH-key /
network / project fields) is immutable — editing it surfaces `Synced=False,
reason=ImmutableFieldChange`. Resizing is a delete-and-recreate.

## Custom sizing (configurators)

Instead of a `presetName` slug you can declare the resources you actually want
and let the provider pick the matching upstream configurator — no opaque slug:

```yaml
spec:
  forProvider:
    name: app
    location: ru-1
    os: { image: ubuntu, version: "24.04" }
    resources:                 # exactly one of presetName / resources
      cpu: 4                   # cores
      ramGB: 8                 # GB  (controller normalizes to MB upstream)
      diskGB: 80               # GB
      # optional: diskType, bandwidthMbps, gpu, cpuFrequencyTier, enableLocalNetwork
    sshKeyRefs: [{ name: my-key }]
```

- The controller resolves `resources` to the **tightest-fit** configurator in
  the Server's `location` (filtered by any optional axes) and records it in
  `status.atProvider.lockedConfiguratorID`.
- An unsatisfiable request (e.g. `cpu: 999`) → `Synced=False,
  reason=NoConfiguratorAvailable` naming the unmet axis.
- `presetName` still works and stays supported — `resources` is an additive
  alternative; admission rejects setting both.
- The sizing **variant** is immutable: flipping a live Server between
  `presetName` and `resources` → `Synced=False,
  reason=SizingSwitchRequiresRecreate` (delete + recreate to change).

## Attaching a private network (VPC)

Create a `Network`, then reference it from the Server:

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
    name: team-a-shared
    subnetCIDR: 10.30.0.0/24
    location: ru-1                     # MUST match every attached Server's location
---
# on the Server:
spec:
  forProvider:
    networkRef:
      name: shared                    # at most one of networkRef / networkSelector / networkID
```

- The Server controller blocks `Create` until the Network is `Ready=True`,
  then attaches the VM. `Server.status.atProvider.privateIP` lands inside the
  VPC's CIDR.
- A location mismatch (Server vs Network) is caught pre-flight: `Synced=False,
  reason=ReconcileError` naming the mismatch.
- **Import an externally-managed VPC** without modelling it as a `Network` MR:
  set `forProvider.networkID: <vpc-id>` directly (mutually exclusive with
  `networkRef`). The controller verifies the VPC's location via an upstream GET
  and attaches; deleting the Server leaves the VPC untouched.

`Network` is created on the v2 endpoint and deleted on the v1 endpoint
(research §R-6); only `description` is mutable.

## Pinning a public IPv4 (FloatingIP)

**The binding lives on the Server, not the FloatingIP** (2026-06-01 reversal —
every major cloud provider models the address as the generic resource and the
server as the consumer). The `FloatingIP` MR is pure allocation and carries no
server reference.

```yaml
# 1. Allocate (unbound):
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: FloatingIP
metadata:
  name: web-01-ip
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:
    name: web-01-ip-secret            # publishes ip + upstreamID
  forProvider:
    location: ru-1
    isDDoSGuard: false                # immutable
    # availabilityZone: spb-1         # optional; defaulted per-location when omitted
---
# 2. Bind by referencing it FROM the Server:
spec:
  forProvider:
    floatingIPRefs:                   # at most one of floatingIPRefs / floatingIPSelector / floatingIPIDs
    - name: web-01-ip
```

Lifecycle (Server-owned):

1. `FloatingIP` allocates the address (`status.atProvider.ip`), unbound.
2. The **Server** controller resolves `floatingIPRefs` → the IP's upstream ID
   and, once the VM is `on`, calls `POST /floating-ips/{id}/bind`
   (`resource_type=server`). `Server.status.atProvider.boundFloatingIPs` lists
   the confirmed-bound IDs; `FloatingIP.status.atProvider.observedBoundTo`
   mirrors the upstream `bound_to` for diagnostics.
3. **Re-point** by moving the `floatingIPRefs` entry to another Server — the
   new Server binds, the old unbinds (exactly one of each).
4. **Release** by removing the entry — the Server unbinds; the FloatingIP stays
   allocated (re-bindable). **Deleting** the FloatingIP releases the address
   (unbind it from the Server first; the FloatingIP controller never
   force-unbinds — single-owner on the Server).

The `availabilityZone` is required by the upstream allocate call; when omitted
the controller fills a per-location default (`ru-1→spb-1`, `ru-2→msk-1`,
`ru-3→spb-3`, `nl-1→ams-1`, `de-1→fra-1`, `kz-1→ala-1`). For a location without
a known default, set `forProvider.availabilityZone` explicitly.

> **Same-zone requirement.** Timeweb rejects a bind when the floating IP and
> the server are in different availability zones
> (`different_zones_exception`). A FloatingIP is allocated independently, so
> it can't infer the server's zone — **pin both** to the same
> `forProvider.availabilityZone` (the Server also accepts it). If you let the
> server pick its zone automatically, the IP's (possibly defaulted) zone may
> not match, and the bind surfaces `Synced=False, reason=ReconcileError` with
> the upstream message on the **Server**.

## Project assignment

```yaml
spec:
  forProvider:
    projectRef:
      name: team-a-production         # → existing Project MR; at most one of projectRef/Selector/projectID
```

Resolves to the Project's `upstreamID` and passes it as `project_id` on create.
All three unset → the account's default project.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Server` `Synced=False, reason=ReconcileError` naming a `Network` not found | The referenced `Network` MR doesn't exist in the namespace. | Create it / fix the `networkRef.name`. |
| `Server` `Synced=False` waiting on a `Network` | The Network isn't `Ready=True` (VPC still provisioning). | Wait; check the Network's conditions + provider logs. |
| `Synced=False, reason=PresetNotFound` | `presetName` slug not visible to your token. | The message lists valid slugs; pick one. |
| `Synced=False, reason=ReconcileError` with `location mismatch` | Server.location ≠ resolved VPC location. | Use a same-region Network, or change the Server's location. |
| `Server` `Ready=False, reason=PaymentRequired` (`state=no_paid`) | Account lacks funds/quota to start the VM. | Top up the account; the VM starts once payment clears. `Synced` stays `True` — not a controller fault. |
| `boundFloatingIPs` empty despite `floatingIPRefs` set | VM not yet `on`, or the FloatingIP not yet `Ready`. | Wait — binding converges once both are ready. |
| `FloatingIP` has an `ip` but `observedBoundTo` empty | Unbound by design (no Server references it). | Add its name to a `Server.forProvider.floatingIPRefs`. |
| Any MR `Ready=False, reason=Deleting` while the CR still exists | Kubernetes deletion in flight. | Wait for the finalizer cascade; `kubectl describe` to inspect. |

## What's NOT in v0.3

Each lands as its own follow-up feature (see `spec.md §Clarifications`):

- Custom-configurator sizing (the dashboard's "Произвольная" tab) and dedicated-CPU sub-tiers
- Resize / re-image of a live Server (changing `presetName` or `os`)
- **Network disks** (сетевые диски) — a Server boots with only its preset's
  bundled root volume; there is no `Server` field or `NetworkDisk` kind to
  attach additional network drives
- Backups, snapshots, auto-backup config
- Reboot / clone / boot-mode / `performAction`
- DDoS guard on a Server (`is_ddos_guard` is hardcoded false; protection is via a DDoS-guarded FloatingIP)
- `software_id`, `avatar_id`, hostname automation
- FloatingIP bind targets other than Server (balancer / database / network)
- Multiple network attachments per Server
- Dedicated servers (`/api/v1/dedicated-servers` — a different upstream API)
