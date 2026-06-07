# Contract — Timeweb `/api/v1/k8s/*` endpoints touched by feature 004

**Feature**: 004 | **Source of truth**: `docs/openapi-timeweb.json` (tag `Kubernetes`). Controller code curl-probes envelopes at impl time per `project_timeweb_underscore_envelopes`.

## Generated client tag-allowlist addition

`Makefile` `-include-tags` (source of truth) + `internal/clients/timeweb/generated/cfg.yaml` add:

```yaml
include-tags:
  - "Проекты"  - "SSH-ключи"  - "S3-хранилище"  - "Реестр контейнеров"  - "Облачные серверы"   # existing
  - "Kubernetes"        # NEW — /api/v1/k8s/* + /api/v1/presets/k8s
```

## Cluster endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/k8s/clusters` | `POST` | `KubernetesCluster.Create` (body `ClusterIn`) |
| `/api/v1/k8s/clusters/{cluster_id}` | `GET` | `KubernetesCluster.Observe` (envelope `{cluster}`) |
| `/api/v1/k8s/clusters/{cluster_id}` | `PATCH` | `KubernetesCluster.Update` — name/description only (`ClusterEdit`) |
| `/api/v1/k8s/clusters/{cluster_id}/versions/update` | `PATCH` | `KubernetesCluster.Update` — in-place upgrade (`{k8s_version}`) |
| `/api/v1/k8s/clusters/{cluster_id}` | `DELETE` | `KubernetesCluster.Delete` (404-idempotent) |
| `/api/v1/k8s/clusters/{cluster_id}/kubeconfig` | `GET` | connection Secret `kubeconfig` (`application/yaml` string) |

### `POST /api/v1/k8s/clusters` (ClusterIn)

| Field | Source on MR | Required? |
|---|---|---|
| `name` | `forProvider.name` | Yes |
| `k8s_version` | `forProvider.k8sVersion` (exact catalog) | Yes |
| `network_driver` | `forProvider.networkDriver` (enum) | Yes |
| `availability_zone` | `forProvider.availabilityZone` (enum) | No (req'd by us) |
| `preset_id` | resolved from `forProvider.presetName` (master) | preset XOR configuration |
| `master_nodes_count` | `forProvider.masterNodesCount` (default 1) | No |
| `network_id` | resolved from `networkRef`/`networkID` | No |
| `project_id` | resolved from `projectRef`/`projectID` | No |
| `worker_groups` | **omitted** (Nodepool-MR-only, R-5) | No |
| `configuration`, `is_ingress`, `is_k8s_dashboard`, `oidc_provider`, `maintenance_slot`, `cluster_network_cidr` | **not emitted** (deferred) | No |

### `GET …/{cluster_id}` response (`{cluster: Cluster}`)

`Cluster{ id, name, created_at, status, description, k8s_version, network_driver, avatar_link, ingress, preset_id, cpu, ram, disk, availability_zone, project_id }` → populates `status.atProvider.{upstreamID,state,k8sVersion,lockedPresetID,cpu,ram,disk,resolvedProjectID}`.

## Nodepool (worker group) endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/k8s/clusters/{cluster_id}/groups` | `POST` | `Nodepool.Create` (body `NodeGroupIn`) |
| `/api/v1/k8s/clusters/{cluster_id}/groups/{group_id}` | `GET` | `Nodepool.Observe` (envelope `{node_group}`) |
| `/api/v1/k8s/clusters/{cluster_id}/groups/{group_id}` | `DELETE` | `Nodepool.Delete` (404-idempotent) |
| `/api/v1/k8s/clusters/{cluster_id}/groups/{group_id}/nodes` | `POST` | scale-up (`IncreaseNodes{count, labels}`) |
| `/api/v1/k8s/clusters/{cluster_id}/groups/{group_id}/nodes` | `DELETE` | scale-down (`ReduceNodes{count}`) |

### `POST …/groups` (NodeGroupIn)

| Field | Source | Required? |
|---|---|---|
| `name` | `forProvider.name` | Yes |
| `node_count` | `forProvider.nodeCount` (1..100) | Yes |
| `preset_id` | resolved from worker `presetName` | preset XOR configuration |
| `labels` | `forProvider.labels` (map → array<{key,value}>) | No |
| `is_autoscaling` | `forProvider.autoscaling.enabled` | No |
| `min-size` / `max-size` | `forProvider.autoscaling.{minSize,maxSize}` (>=2) | with autoscaling |
| `is_autohealing` | `forProvider.autohealing` | No |

`{node_group: NodeGroup}` → `NodeGroup{ id, name, created_at, preset_id, node_count }` → `status.atProvider.{upstreamID,observedNodeCount,lockedPresetID}`.

## Addon endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/k8s/clusters/{cluster_id}/addons` | `POST` | `Addon.Create` (`{type, config_type, yaml_config, version}`) |
| `/api/v1/k8s/clusters/{cluster_id}/addons` | `GET` | `Addon.Observe` (`AddonsResponse.addons[]` `AddonOut`) |
| `/api/v1/k8s/clusters/{cluster_id}/addons/{addon_id}` | `DELETE` | `Addon.Delete` (404-idempotent) |
| `/api/v1/k8s/clusters/{cluster_id}/addons-configs` | `GET` | catalog validation (`AddonConfigOut`) |

`AddonOut{ id, type, status, version, config, yaml_config, config_type }`; `AddonConfigOut{ id, type, version, dependencies, yaml_config }`.

## Catalog endpoints (resolver)

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/presets/k8s` | `GET` | `DimKubernetesMasterPreset` (type=master) + `DimKubernetesWorkerPreset` (type=worker) |
| `/api/v1/k8s/k8s-versions` | `GET` | `DimKubernetesVersion` (exact string list) |
| `/api/v1/k8s/network-drivers` | `GET` | (forward-compat only; CRD enum used instead) |

`k8s_presets[]` = `oneOf{MasterPresetOutApi, WorkerPresetOutApi}` discriminated by `type`. Both: `{id, description, description_short, price, cpu, ram, disk, network, type}` (master adds `limit`). `k8s_versions[]` = plain strings.
