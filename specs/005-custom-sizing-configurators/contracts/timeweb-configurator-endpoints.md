# Contract — Timeweb configurator endpoints touched by feature 005

**Source of truth**: `docs/openapi-timeweb.json` (tag `Облачные серверы` — already in the allowlist; no codegen change needed).

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/configurator/servers` | `GET` | `DimServerConfigurator` fetcher (`GetConfiguratorsWithResponse`) — Server + K8s custom sizing |

## `servers-configurator` item shape

```
id                              number   → ConfiguratorEntry.UpstreamID (configurator_id)
location                        string   → Filters.location
disk_type                       string   → Filters.disk_type
is_allowed_local_network        bool     → Filters.is_allowed_local_network
cpu_frequency                   string   → Filters.cpu_frequency
requirements:
  cpu_min / cpu_step / cpu_max          → Bounds.cpu   {Min,Step,Max}
  ram_min / ram_step / ram_max          → Bounds.ramMB  (probe units: server presets use MB)
  disk_min / disk_step / disk_max       → Bounds.diskGB (normalize: upstream MB /1024 → GB)
  network_bandwidth_{min,step,max}      → Bounds.bandwidth
  gpu_{min,max,step}                    → Bounds.gpu
```

## Create-body wiring

- **Server** (`CreateServerJSONRequestBody`): set `configurator_id` (resolved) instead of `preset_id`. (Server presets path unchanged.)
- **KubernetesCluster** (`ClusterIn`): set `configuration {configurator_id, cpu, ram, disk}` (ram/disk in upstream MB) instead of `preset_id`.
- **KubernetesClusterNodepool** (`NodeGroupIn`): set `configuration {configurator_id, cpu, ram, disk, gpu}` instead of `preset_id`.

**Probe at impl** (per `project_timeweb_underscore_envelopes`): exact upstream units of `requirements.ram_*`/`disk_*`, and whether K8s `configuration.configurator_id` accepts ids from `/api/v1/configurator/servers` or needs a dedicated K8s configurator list.
