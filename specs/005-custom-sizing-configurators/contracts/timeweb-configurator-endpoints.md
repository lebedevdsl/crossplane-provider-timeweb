# Contract — Timeweb configurator endpoints touched by feature 005

**Source of truth**: `docs/openapi-timeweb.json`. The server endpoint ships under tag `Облачные серверы` (already in the allowlist). The K8s endpoint is **absent from the published swagger** — probed live 2026-06-10 and hand-patched into `docs/openapi-timeweb.json` under tag `Kubernetes` so codegen covers it.

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/configurator/servers` | `GET` | `DimServerConfigurator` fetcher (`GetConfiguratorsWithResponse`) — Server custom sizing |
| `/api/v1/configurator/k8s` (undocumented) | `GET` | `DimKubernetesMasterConfigurator` + `DimKubernetesWorkerConfigurator` fetchers (`GetK8sConfiguratorsWithResponse`) — KubernetesCluster + Nodepool custom sizing |

The two catalogs are **separate**: ids are disjoint, and `POST /api/v1/k8s/clusters` rejects server-catalog ids with `400 configurator_not_found` (observed in the T028 live canary). The K8s envelope key is `k8s_configurators`; item shape is identical to `servers-configurator` below plus a `tags` array (also absent from the published swagger; hand-patched in).

**Role + location contract (T028 follow-up repros)**: the k8s catalog is tag-partitioned into a master family (`k8s_master_configurator` — for the cluster's `configuration`) and worker families (everything else — for node-group `configuration`), one entry per location. The upstream does NOT validate the pairing: a wrong-family or wrong-location id makes it silently ignore `availability_zone` and strand the cluster in ams-1 (failed). Resolution therefore always filters `{location}` first (AZ↔location: spb-3↔ru-1, msk-1↔ru-3, ams-1↔nl-1, fra-1↔de-1; `azLocation` in the kubernetes controller), and nodepools derive the location from the parent cluster's AZ — an unsatisfiable sizing in the parent's region is rejected before any upstream call (`NoConfiguratorAvailable`). Also observed: `configuration` + `network_id` create can return HTTP 500 **and still create the cluster** — treat a 500 from this endpoint as possibly-created.

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

**Probe results** (per `project_timeweb_underscore_envelopes`): `requirements.ram_*`/`disk_*` are MB on both catalogs. K8s `configuration.configurator_id` does **NOT** accept ids from `/api/v1/configurator/servers` — it needs the dedicated `/api/v1/configurator/k8s` list (400 `configurator_not_found` otherwise).
