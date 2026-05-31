# Contract — Timeweb endpoints touched by feature 003

**Feature**: 003 | **Source of truth**: `docs/openapi-timeweb.json` (vendored).

Endpoints the new controllers / generated client need access to. Probed at planning time; controller code curl-probes at implementation time per the `project_timeweb_underscore_envelopes` memory.

## Generated client tag-allowlist additions

`internal/clients/timeweb/generated/cfg.yaml` adds:

```yaml
include-tags:
  - "Проекты"                  # existing
  - "SSH-ключи"                # existing
  - "S3-хранилище"             # existing
  - "Реестр контейнеров"       # existing
  - "Облачные серверы"         # NEW — covers /api/v1/servers/* + /api/v1/presets/servers + /api/v1/os/servers + /api/v1/configurator/servers + /api/v1/floating-ips/*
```

Verify post-generate: `/api/v2/vpcs` family is tagged under `Облачные серверы` upstream (probed in the openapi); if it lives under a separate tag we add it too.

## Server endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/servers` | `POST` | `Server.Create` |
| `/api/v1/servers/{server_id}` | `GET` | `Server.Observe` |
| `/api/v1/servers/{server_id}` | `PATCH` | `Server.Update` (mutable subset only) |
| `/api/v1/servers/{server_id}` | `DELETE` | `Server.Delete` |
| `/api/v1/presets/servers` | `GET` | Resolver `ServerPreset` dimension fetcher |
| `/api/v1/os/servers` | `GET` | Resolver `ServerOSImage` dimension fetcher |
| `/api/v1/configurator/servers` | `GET` | (forward-compat only — `ServerConfigurator` dimension stays at `fetchUnwired`) |

### `POST /api/v1/servers` body (createServer)

Operator-supplied via spec; controller-derived from refs:

| Field | Source on MR | Required? |
|---|---|---|
| `name` | `forProvider.name` | Yes |
| `preset_id` | resolver(`forProvider.presetName`) | Yes (the resolved one) |
| `os_id` | resolver(`forProvider.os`) | Yes |
| `availability_zone` | `forProvider.availabilityZone` | Optional |
| `project_id` | resolver(`forProvider.projectRef` / Selector / ID) | Optional |
| `ssh_keys_ids` | resolver(`forProvider.sshKeyRefs` etc.) | Optional |
| `network.id` | resolver(`forProvider.networkRef` etc.) | Optional |
| `is_local_network` | derived (true iff `network.id` resolved) | Optional |
| `cloud_init` | `forProvider.cloudInit` | Optional |
| `comment` | `forProvider.comment` | Optional |
| `hostname` | `forProvider.hostname` | Optional |
| `is_ddos_guard` | hardcoded `false` (out of v0.1 scope) | Optional |
| `image_id`, `software_id`, `avatar_id`, `bandwidth`, `configuration` | (out of v0.1 scope; NOT sent) | n/a |

### `PATCH /api/v1/servers/{server_id}` body

Per R-5 — fields the controller MAY PATCH on a live server:

| Field | Source |
|---|---|
| `name` | `forProvider.name` |
| `comment` | `forProvider.comment` |
| `hostname` | `forProvider.hostname` |
| `cloud_init` | `forProvider.cloudInit` |
| `bandwidth` | (out of scope; never patched) |

## Network (VPC) endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v2/vpcs` | `POST` | `Network.Create` |
| `/api/v2/vpcs/{vpc_id}` | `GET` | `Network.Observe` |
| `/api/v2/vpcs/{vpc_id}` | `PATCH` | `Network.Update` (description only) |
| `/api/v1/vpcs/{vpc_id}` | `DELETE` | `Network.Delete` (v1 path — R-6) |

### `POST /api/v2/vpcs` body

| Field | Source on MR | Required? |
|---|---|---|
| `name` | `forProvider.name` | Yes |
| `subnet_v4` | `forProvider.subnetCIDR` | Yes |
| `location` | `forProvider.location` | Yes |
| `description` | `forProvider.description` | Optional |
| `availability_zone` | `forProvider.availabilityZone` | Optional |

## FloatingIP endpoints

| Endpoint | Method | Used by |
|---|---|---|
| `/api/v1/floating-ips` | `POST` | `FloatingIP.Create` |
| `/api/v1/floating-ips/{floating_ip_id}` | `GET` | `FloatingIP.Observe` |
| `/api/v1/floating-ips/{floating_ip_id}` | `PATCH` | `FloatingIP.Update` (comment only) |
| `/api/v1/floating-ips/{floating_ip_id}` | `DELETE` | `FloatingIP.Delete` |
| `/api/v1/floating-ips/{floating_ip_id}/bind` | `POST` | `FloatingIP.Update` (when binding trio resolves and drift detected) |
| `/api/v1/floating-ips/{floating_ip_id}/unbind` | `POST` | `FloatingIP.Update` (when binding cleared) + `FloatingIP.Delete` (pre-delete) |

### `POST /api/v1/floating-ips` body

| Field | Source on MR | Required? |
|---|---|---|
| `is_ddos_guard` | `forProvider.isDDoSGuard` | Yes (default false in CRD) |
| `availability_zone` | `forProvider.availabilityZone` (or location-default) | Yes per openapi |

(`location` is NOT a createFloatingIP body field — the upstream derives it from `availability_zone`. The CRD carries `location` for operator-facing clarity and uniformity with Server/Network; the controller maps `(location, availabilityZone?)` to the right `availability_zone` value at create time.)

### `POST /api/v1/floating-ips/{id}/bind` body

| Field | Value |
|---|---|
| `resource_type` | `"server"` (hardcoded; balancer/database/network bind targets are out of v0.1 scope) |
| `resource_id` | `resolvedServerID` |

### `POST /api/v1/floating-ips/{id}/unbind` body

Empty body. Idempotent (Constitution §II — re-invoke on already-unbound succeeds).

## Error classification recap

The error-classification helpers from feature 001 (`internal/clients/timeweb/errors.go`) apply uniformly:

| Upstream HTTP | Sentinel | Condition mapping |
|---|---|---|
| `200` / `201` / `204` | n/a | success path |
| `404` (Observe) | `ErrNotFound` | treat as not-yet-created / already-deleted |
| `409` | `ErrConflict` | terminal; surface message in condition |
| `429` | `ErrRateLimited` | transient; runtime requeue |
| `5xx` | `ErrTransient` | transient; runtime requeue |
| other `4xx` | `ErrTerminal` | terminal; surface as `Synced=False, reason=ReconcileError` |

The unique-to-this-feature error path: `bind`/`unbind` on a FloatingIP that the upstream rejects with "already bound to another resource" → `ErrConflict`. The FloatingIP controller treats this as drift, queues an Observe, and lets the next reconcile decide whether to unbind-then-rebind or surface to the operator.
