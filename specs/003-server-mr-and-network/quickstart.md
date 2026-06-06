# Quickstart — Cloud Server + Network + FloatingIP

**Feature**: `003-server-mr-and-network` | **Plan**: [./plan.md](./plan.md)

**Audience**: Platform operators upgrading from feature 002 (`v0.2` — Container Registry + S3 + dual-PC pair) to the v0.3 release that ships the three new MRs.

## What's new in v0.3

| Kind | Group | Status | Notes |
|---|---|---|---|
| `Server` | `compute.m.timeweb.crossplane.io/v1alpha1` | NEW | Cloud server (VM). Sized via `presetName`; OS via `os.image + os.version`. |
| `Network` | `network.m.timeweb.crossplane.io/v1alpha1` | NEW | VPC. `subnetCIDR` + `location`. Independently usable. |
| `FloatingIP` | `network.m.timeweb.crossplane.io/v1alpha1` | NEW | Floating IPv4. Pure allocation; the Server binds it via `floatingIPRefs` (2026-06-01 reversal). |
| `ContainerRegistry`, `S3Bucket`, `Project`, `SshKey`, `ProviderConfig`, `ClusterProviderConfig`, `ContainerRegistryRepository` | (unchanged) | EXISTING | Carried forward from features 001/002. |

The dual-PC pair + the shared `ProviderConfigSpec` + `InvalidProviderConfigRef` semantics from feature 002 apply unchanged. The in-controller resolver primitive grows two new dimensions: `ServerPreset` (Preset) and `ServerOSImage` (Enum).

## Minimum viable Server

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: team-a
spec:
  credentials:
    source: Secret
    secretRef:                          # namespace optional — defaults to team-a per feat 002
      name: timeweb-token
      key: token
---
apiVersion: sshkey.m.timeweb.crossplane.io/v1alpha1
kind: SSHKey
metadata:
  name: dev-key
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  forProvider:
    name: dev-key
    body: "ssh-ed25519 AAAA… your-public-key"
    isDefault: false
---
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
    name: web-01-conn
  forProvider:
    name: web-01
    presetName: premium-2-2-40-ru-1    # → slugified from /api/v1/presets/servers
    location: ru-1
    os:
      image: ubuntu
      version: "24.04"
    sshKeyRefs:
    - name: dev-key
```

After `kubectl apply -f`:

```bash
# 1. The SSH key reaches Ready=True in ~30s.
# 2. The Server is "installing" → "starting" → "on" over 5–10 minutes.
# 3. publicIP is published on the connection Secret.

kubectl -n team-a get server web-01 -w
# NAME     READY   SYNCED   LOCATION   PRESET                       PUBLIC-IP   STATE   AGE
# web-01   True    True     ru-1      premium-2-2-40-ru-1         5.6.7.8     on      6m

kubectl -n team-a get secret web-01-conn -o jsonpath='{.data.publicIP}' | base64 -d
# 5.6.7.8

ssh -i ~/.ssh/dev-key root@5.6.7.8
```

## Adding a private network

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
    location: ru-1                     # MUST match the location of every attached Server
```

Then on a `Server`:

```yaml
spec:
  forProvider:
    # …same as before…
    networkRef:
      name: shared                      # → the Network MR above
```

The Server controller waits on the Network to reach `Ready=True` before issuing the upstream create. `Server.status.atProvider.privateIP` lands in the VPC's CIDR (e.g. `10.30.0.5`).

Server-to-server private connectivity: apply a second Server in the same namespace with the same `networkRef`, and SSH from one to the other on the `privateIP`.

## Pinning a public IPv4 across recreates (FloatingIP)

Per the 2026-06-01 reversal, the binding lives on the **Server** (`floatingIPRefs`), not on the FloatingIP. The FloatingIP is pure allocation — it never references a Server.

```yaml
# 1. Allocate the IP (no server reference — allocation only).
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: FloatingIP
metadata:
  name: web-01-stable-ip
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:
    name: web-01-stable-ip-secret
  forProvider:
    location: ru-1
    isDDoSGuard: false
---
# 2. Reference it FROM the Server to bind.
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata:
  name: web-01
  namespace: team-a
spec:
  forProvider:
    # …same as before…
    floatingIPRefs:
    - name: web-01-stable-ip            # → the FloatingIP MR above
```

Flow:
1. The `FloatingIP` controller allocates the IP upstream (`status.atProvider.ip: 5.6.7.99`), unbound.
2. The **Server** controller resolves `floatingIPRefs` → the IP's upstream ID, waits for the Server to reach `state=on`, then calls `POST /floating-ips/{id}/bind` with `resource_type=server, resource_id=<server_id>`.
3. `Server.status.atProvider.boundFloatingIPs` lists the bound IP's upstream ID (confirmed by reading the IP's upstream `bound_to`). The `FloatingIP.status.atProvider.observedBoundTo` mirrors the same upstream binding for diagnostics.

Re-pointing the IP to a different Server: move the `floatingIPRefs` entry to the other Server. The new Server's controller binds; the old Server's controller unbinds — exactly one bind + one unbind.

Releasing the binding: remove the entry from `Server.forProvider.floatingIPRefs`. The Server controller unbinds upstream; the FloatingIP stays allocated (re-bindable later without losing the address).

Returning the IP to the pool: `kubectl delete floatingip web-01-stable-ip`. (Unbind it first by clearing the Server's `floatingIPRefs`; the FloatingIP controller does not force-unbind — single-owner on the Server.)

## Project assignment

If your account has multiple Timeweb projects and you want the Server to land in a specific one, set `forProvider.projectRef`:

```yaml
spec:
  forProvider:
    # …
    projectRef:
      name: team-a-production           # → existing Project MR in the same namespace
```

The Project MR was introduced in feature 001 (observe-only import). The Server controller resolves its `upstreamID` and passes it as `project_id` on createServer. Leaving the trio unset puts the server in the account's default project.

## Verification

```bash
# CRDs present
kubectl api-resources | grep -E '\.m\.timeweb\.crossplane\.io'

# Expected output includes:
# servers          compute.m.timeweb.crossplane.io/v1alpha1     Server
# networks         network.m.timeweb.crossplane.io/v1alpha1     Network
# floatingips      network.m.timeweb.crossplane.io/v1alpha1     FloatingIP
# (plus the existing ContainerRegistry / S3Bucket / Project / SshKey / ContainerRegistryRepository / ProviderConfig / ClusterProviderConfig kinds)

# The new resolver dimensions
kubectl -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb | grep -i "resolver.*registered"
# (provider startup logs each dimension that registered)
```

## E2E run

```bash
export TIMEWEB_CLOUD_TOKEN=<your-token>
make e2e          # runs k3d + install + tests, including the 4 new bundles 08-11
```

The new bundles, in execution order:

| Bundle | What it asserts |
|---|---|
| `08-network-lifecycle/` | Network create/observe/delete |
| `09-server-lifecycle/` | Smallest premium Server with Ubuntu 24.04 + SSH key. PATCH `comment` reconciles. Bogus `presetName` surfaces `ReconcileError` with valid-slug list. |
| `10-server-with-network/` | Two Servers attached to one Network — `privateIP` falls in the CIDR. |
| `11-floating-ip-bind/` | Alloc + bind + rebind to a different server + unbind. |

Cost-aware: the suite uses the **smallest** premium preset available (the wrapper script discovers it at runtime). Per `docs/presets.md` (added in this feature), the cheapest ru-1 cloud server is currently ≈800 ₽/мес (≈1.09 ₽/час); a full e2e run takes under 10 minutes so cost is under €0.05 per run.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Server` stuck at `Synced=False, reason=Reconciling` with message naming a `Network` | The referenced Network MR isn't `Ready=True` yet | Wait. If the Network is `Ready=False` for >2 min check its conditions + provider logs. |
| `Synced=False, reason=ReconcileError` with `PresetNotFound` | `presetName` slug doesn't match any preset visible to your token. | The message lists valid slugs. Pick one, re-apply. |
| `Synced=False, reason=ReconcileError` with `os not found` | `os.image` + `os.version` doesn't match any entry in `/api/v1/os/servers` for your location. | Check the dashboard's OS list for that region; types are lowercased + version-exact. |
| `Synced=False, reason=ReconcileError` with `location mismatch` | Server.location ≠ resolved-Network.location. | Pick a Network in the same region, or move the Server to the Network's region. |
| `Server` stuck at `Synced=False, reason=Reconciling` naming a `FloatingIP` | A referenced FloatingIP (`floatingIPRefs`) isn't `Ready=True` (allocated) yet, or doesn't exist. | Wait for the FloatingIP to allocate; the Server binds it on the next reconcile once the VM is `on`. |
| `Server.status.atProvider.boundFloatingIPs` empty despite `floatingIPRefs` set | The VM hasn't reached `state=on` yet (bind waits for a running server), or the IP is still allocating. | Wait. Binding converges once both the Server is `on` and the FloatingIP is `Ready`. |
| `FloatingIP` has an IP but `observedBoundTo` is empty | Unbound by design — no Server references it via `floatingIPRefs`. | Add the FloatingIP's name to a `Server.forProvider.floatingIPRefs` to bind it. |
| `Server` stuck at `Ready=False, reason=PaymentRequired` | Upstream server state is `no_paid` — the Timeweb account lacks funds/quota to start it. | Top up the account; the server starts once payment clears. Not a controller fault (`Synced` stays `True`). |
| Server `Ready=False, reason=Deleting` while CR still exists | Kubernetes deletion is in flight. Standard behavior. | Wait for cascade; investigate with `kubectl describe`. |

## What's NOT in v0.3

Explicitly out of scope (see spec.md §Clarifications for rationale; each lands as its own feature):

- Custom-configurator sizing path (the dashboard's "Произвольная" tab)
- Dedicated CPU sub-tiers (HighCPU / DedicatedHighCPU)
- Resize / re-image (changing `presetName` or `os` on a live Server)
- Reboot, clone, boot-mode change, performAction
- Backups, auto-backup config, snapshot management
- Server disks beyond the preset-bundled root volume (`network-drives`)
- DDoS guard on Server (`is_ddos_guard` is hardcoded false; ON via FloatingIP only)
- Software_id, avatar_id, server hostname automation
- FloatingIP bind targets other than Server (balancer, database, network)
- Multiple network attachments per Server
- Dedicated servers (`/api/v1/dedicated-servers` — different upstream API)
