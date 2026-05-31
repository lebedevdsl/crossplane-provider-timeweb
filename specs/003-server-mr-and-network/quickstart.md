# Quickstart — Cloud Server + Network + FloatingIP

**Feature**: `003-server-mr-and-network` | **Plan**: [./plan.md](./plan.md)

**Audience**: Platform operators upgrading from feature 002 (`v0.2` — Container Registry + S3 + dual-PC pair) to the v0.3 release that ships the three new MRs.

## What's new in v0.3

| Kind | Group | Status | Notes |
|---|---|---|---|
| `Server` | `compute.m.timeweb.crossplane.io/v1alpha1` | NEW | Cloud server (VM). Sized via `presetName`; OS via `os.image + os.version`. |
| `Network` | `network.m.timeweb.crossplane.io/v1alpha1` | NEW | VPC. `subnetCIDR` + `location`. Independently usable. |
| `FloatingIP` | `network.m.timeweb.crossplane.io/v1alpha1` | NEW | Floating IPv4. Owns its bind/unbind to a Server. |
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
    presetName: premium-2-2-40-msk-1    # → slugified from /api/v1/presets/servers
    location: msk-1
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
# web-01   True    True     msk-1      premium-2-2-40-msk-1         5.6.7.8     on      6m

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
    location: msk-1                     # MUST match the location of every attached Server
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

```yaml
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
    location: msk-1
    serverRef:
      name: web-01                      # binds to the Server above
```

Flow:
1. Controller allocates the floating IP upstream (status `ip: 5.6.7.99`).
2. Resolves `serverRef` → waits for the Server to reach `Ready=True`.
3. Calls `POST /floating-ips/{id}/bind` with `resource_type=server, resource_id=<server_id>`.
4. Server's `status.atProvider.boundFloatingIPs` lists the new IP's upstream ID (observed from upstream, not from the FloatingIP MR's spec).

Re-pointing the IP to a different Server: edit `serverRef.name` to the new target. The controller calls unbind + bind in sequence. `status.atProvider.resolvedServerID` updates.

Releasing the binding: clear `serverRef`. Controller unbinds upstream; the FloatingIP stays allocated (so you can re-bind later without losing the address).

Returning the IP to the pool: `kubectl delete floatingip web-01-stable-ip`. Controller unbinds first (if bound), then releases the upstream allocation.

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

Cost-aware: the suite uses the **smallest** premium preset available (the wrapper script discovers it at runtime). Per `docs/presets.md` (added in this feature), the cheapest msk-1 cloud server is currently ≈800 ₽/мес (≈1.09 ₽/час); a full e2e run takes under 10 minutes so cost is under €0.05 per run.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Server` stuck at `Synced=False, reason=Reconciling` with message naming a `Network` | The referenced Network MR isn't `Ready=True` yet | Wait. If the Network is `Ready=False` for >2 min check its conditions + provider logs. |
| `Synced=False, reason=ReconcileError` with `PresetNotFound` | `presetName` slug doesn't match any preset visible to your token. | The message lists valid slugs. Pick one, re-apply. |
| `Synced=False, reason=ReconcileError` with `os not found` | `os.image` + `os.version` doesn't match any entry in `/api/v1/os/servers` for your location. | Check the dashboard's OS list for that region; types are lowercased + version-exact. |
| `Synced=False, reason=ReconcileError` with `location mismatch` | Server.location ≠ resolved-Network.location. | Pick a Network in the same region, or move the Server to the Network's region. |
| `FloatingIP` stuck at `Synced=False, reason=Reconciling` | Bound target Server hasn't reached `Ready=True` yet. | Wait. The allocation succeeded (IP is in `status.atProvider.ip`); only the bind is pending. |
| `FloatingIP` shows IP in status but `resolvedServerID` is empty | Spec's binding trio is empty (unbound by design). | Add a `serverRef`, `serverSelector`, or `serverID` if you want it bound. |
| Server `Ready=False, reason=Deleting` while CR still exists | Kubernetes deletion is in flight. Standard behavior per `feedback_no_unsolicited_commits`-companion memory. | Wait for cascade; investigate with `kubectl describe`. |

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
