# Contract — `Server.forProvider.resources` (compute.m.timeweb.crossplane.io/v1alpha1)

Custom configurator sizing as an alternative to `presetName`. CEL: exactly one of `{presetName, resources}`.

## spec.forProvider.resources

| Field | Type | Req | Maps to |
|---|---|---|---|
| `cpu` | int (cores) | ✓ | configurator `requirements.cpu_{min,step,max}` (Sizing `cpu`) |
| `ramGB` | int (GB) | ✓ | `requirements.ram_*` (Sizing `ramMB` = ramGB×1024) |
| `diskGB` | int (GB) | ✓ | `requirements.disk_*` (Sizing `diskGB`) |
| `diskType` | string | – | `disk_type` (exact filter) |
| `bandwidthMbps` | int | – | `requirements.network_bandwidth_*` (Sizing `bandwidth`) |
| `gpu` | int | – | `requirements.gpu_*` (Sizing `gpu`) |
| `cpuFrequencyTier` | string | – | `cpu_frequency` (exact filter) |
| `enableLocalNetwork` | bool | – | `is_allowed_local_network` (exact filter) |

`location` filter = the Server's existing `forProvider.location`.

## Behavior

- **Create**: `SelectConfigurator` over `/api/v1/configurator/servers` (hard-filter → capability-filter → tightest-fit by `(cpu, ramMB, diskGB)` → lowest id). POST createServer with `configurator_id` (not `preset_id`). Record `status.atProvider.lockedConfiguratorID`.
- **Unsatisfiable** → `Synced=False, reason=NoConfiguratorAvailable` naming the unmet axis/bound.
- **Sizing-switch** (preset↔resources on a live Server) → `Synced=False, reason=SizingSwitchRequiresRecreate`.
- Presets still work unchanged (additive).

## Example

```yaml
apiVersion: compute.m.timeweb.crossplane.io/v1alpha1
kind: Server
metadata: { name: app, namespace: team-a }
spec:
  forProvider:
    name: app
    location: ru-1
    os: { image: ubuntu, version: "24.04" }
    resources: { cpu: 4, ramGB: 8, diskGB: 80 }   # no presetName
    sshKeyRefs: [{ name: my-key }]
  managementPolicies: ["*"]
```
