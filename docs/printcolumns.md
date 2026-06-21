# Printcolumn Conventions

This document describes the standard `kubectl get` column layout used across all
managed-resource kinds in this provider.

---

## Fixed Column Order

Every kind follows this schema:

```
READY    type=string   .status.conditions[?(@.type=='Ready')].status
SYNCED   type=string   .status.conditions[?(@.type=='Synced')].status
LOCATION type=string   .spec.forProvider.location           (placed kinds only)
<kind-specific columns, most operator-useful order>
ID       type=string   .metadata.annotations.crossplane.io/external-name   priority=1
AGE      type=date     .metadata.creationTimestamp
```

Rules:

- `READY` and `SYNCED` are always first (standard Crossplane convention).
- `LOCATION` (or `CLUSTER` for child kinds) follows immediately after `SYNCED`
  when applicable.
- `AGE` is always last.
- `ID` (`priority=1`) is always the penultimate column — shown only with
  `-o wide` because it is diagnostic, not operational.
- `STATE` is placed near the right of the kind-specific block (diagnostic, not
  operational).

---

## The Single `ID` Column

All kinds that have a stable upstream identifier expose it through a single `ID`
column sourced from the Crossplane external-name annotation:

```
JSONPath: .metadata.annotations.crossplane\.io/external-name
priority: 1   (wide-only; pass -o wide to see it)
```

This replaces the previous inconsistent mix of `UPSTREAM-ID` and `EXTERNAL-NAME`
columns. The external-name annotation is the single canonical ID surface for all
managed resources — set by the controller at Create time and never changed
thereafter.

---

## Child Kinds — `CLUSTER` Substitutes `LOCATION`

`KubernetesClusterNodepool` and `KubernetesClusterAddon` are child resources —
their placement is inherited from the parent cluster. For these kinds the third
column is `CLUSTER` (the resolved parent cluster ID) rather than `LOCATION`.

---

## Per-Kind Column Tables

### Server

| Column     | Type    | JSONPath                                          | Priority |
|------------|---------|---------------------------------------------------|----------|
| READY      | string  | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED     | string  | `.status.conditions[?(@.type=='Synced')].status`  |          |
| LOCATION   | string  | `.spec.forProvider.location`                      |          |
| PRESET     | string  | `.spec.forProvider.presetName`                    |          |
| PUBLIC-IP  | string  | `.status.atProvider.publicIP`                     |          |
| STATE      | string  | `.status.atProvider.state`                        |          |
| ID         | string  | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE        | date    | `.metadata.creationTimestamp`                     |          |

### Network

| Column   | Type   | JSONPath                                          | Priority |
|----------|--------|---------------------------------------------------|----------|
| READY    | string | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED   | string | `.status.conditions[?(@.type=='Synced')].status`  |          |
| LOCATION | string | `.spec.forProvider.location`                      |          |
| CIDR     | string | `.spec.forProvider.subnetCIDR`                    |          |
| STATE    | string | `.status.atProvider.state`                        |          |
| ID       | string | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE      | date   | `.metadata.creationTimestamp`                     |          |

### FloatingIP

| Column   | Type   | JSONPath                                          | Priority |
|----------|--------|---------------------------------------------------|----------|
| READY    | string | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED   | string | `.status.conditions[?(@.type=='Synced')].status`  |          |
| LOCATION | string | `.spec.forProvider.location`                      |          |
| IP       | string | `.status.atProvider.ip`                           |          |
| BOUND-TO | string | `.status.atProvider.observedBoundSummary`         |          |
| ID       | string | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE      | date   | `.metadata.creationTimestamp`                     |          |

`BOUND-TO` shows `<resourceType>/<id-or-uuid>` (e.g. `server/42` or
`router/abc-uuid`). It is populated by the controller from the upstream
`bound_to` field via the computed `ObservedBoundSummary` status field. Empty
when the IP is unbound.

### Router

| Column   | Type   | JSONPath                                          | Priority |
|----------|--------|---------------------------------------------------|----------|
| READY    | string | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED   | string | `.status.conditions[?(@.type=='Synced')].status`  |          |
| LOCATION | string | `.spec.forProvider.location`                      |          |
| PRESET   | string | `.spec.forProvider.presetName`                    |          |
| STATE    | string | `.status.atProvider.state`                        |          |
| ID       | string | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE      | date   | `.metadata.creationTimestamp`                     |          |

### KubernetesCluster

| Column      | Type   | JSONPath                                          | Priority |
|-------------|--------|---------------------------------------------------|----------|
| READY       | string | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED      | string | `.status.conditions[?(@.type=='Synced')].status`  |          |
| LOCATION    | string | `.spec.forProvider.location`                      |          |
| K8S-VERSION | string | `.status.atProvider.k8sVersion`                   |          |
| PRESET      | string | `.spec.forProvider.presetName`                    |          |
| STATE       | string | `.status.atProvider.state`                        |          |
| ID          | string | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE         | date   | `.metadata.creationTimestamp`                     |          |

### KubernetesClusterNodepool

| Column    | Type    | JSONPath                                          | Priority |
|-----------|---------|---------------------------------------------------|----------|
| READY     | string  | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED    | string  | `.status.conditions[?(@.type=='Synced')].status`  |          |
| CLUSTER   | string  | `.status.atProvider.clusterID`                    |          |
| PRESET    | string  | `.spec.forProvider.presetName`                    |          |
| PUBLIC    | boolean | `.spec.forProvider.publicIP`                      |          |
| DESIRED   | integer | `.spec.forProvider.nodeCount`                     |          |
| OBSERVED  | integer | `.status.atProvider.observedNodeCount`            |          |
| ID        | string  | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE       | date    | `.metadata.creationTimestamp`                     |          |

### KubernetesClusterAddon

| Column            | Type   | JSONPath                                          | Priority |
|-------------------|--------|---------------------------------------------------|----------|
| READY             | string | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED            | string | `.status.conditions[?(@.type=='Synced')].status`  |          |
| CLUSTER           | string | `.status.atProvider.clusterID`                    |          |
| TYPE              | string | `.spec.forProvider.type`                          |          |
| VERSION           | string | `.spec.forProvider.version`                       |          |
| INSTALLED-VERSION | string | `.status.atProvider.installedVersion`             |          |
| AGE               | date   | `.metadata.creationTimestamp`                     |          |

No `ID` column — addon IDs are not stable across reinstalls and are not
surfaced as external-names in the same way as standalone resources.

### ContainerRegistry

| Column   | Type    | JSONPath                                          | Priority |
|----------|---------|---------------------------------------------------|----------|
| READY    | string  | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED   | string  | `.status.conditions[?(@.type=='Synced')].status`  |          |
| STATE    | string  | `.status.atProvider.state`                        |          |
| SIZE-GB  | integer | `.spec.forProvider.initialSizeGB`                 |          |
| ENDPOINT | string  | `.status.atProvider.endpoint`                     |          |
| ID       | string  | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE      | date    | `.metadata.creationTimestamp`                     |          |

No `LOCATION` — Container Registry placement is region-optional by design.

### ContainerRegistry Repository

| Column   | Type    | JSONPath                                          | Priority |
|----------|---------|---------------------------------------------------|----------|
| READY    | string  | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED   | string  | `.status.conditions[?(@.type=='Synced')].status`  |          |
| REGISTRY | string  | `.spec.forProvider.registryRef.name`              |          |
| NAME     | string  | `.spec.forProvider.name`                          |          |
| TAGS     | integer | `.status.atProvider.tagCount`                     |          |
| AGE      | date    | `.metadata.creationTimestamp`                     |          |

No `ID` — repositories are observe-only and don't have a stable upstream ID
distinct from their name.

### S3Bucket

| Column  | Type    | JSONPath                                          | Priority |
|---------|---------|---------------------------------------------------|----------|
| READY   | string  | `.status.conditions[?(@.type=='Ready')].status`   |          |
| SYNCED  | string  | `.status.conditions[?(@.type=='Synced')].status`  |          |
| SIZE-GB | integer | `.spec.forProvider.initialSizeGB`                 |          |
| CLASS   | string  | `.spec.forProvider.storageClass`                  |          |
| ID      | string  | `.metadata.annotations.crossplane\.io/external-name` | 1     |
| AGE     | date    | `.metadata.creationTimestamp`                     |          |

No `LOCATION` — S3 storage region is derived from preset selection.

### SshKey / Project

No ID column and no LOCATION column — account-scoped resources without a
meaningful upstream ID to expose.

---

## Wide-Only Diagnostics (`-o wide`)

Columns with `priority=1` are hidden in the default `kubectl get` output. They
appear when `kubectl get -o wide` is used:

- **ID** — the upstream resource ID (from `crossplane.io/external-name`
  annotation). Useful when correlating with the Timeweb dashboard or API logs.

---

## Implementation Notes

- Markers are written EXPLICITLY in each `*_types.go` file — `controller-gen`
  does not auto-inject READY, SYNCED, or AGE. There must be exactly one of each
  per kind to avoid the "double age" display bug.
- Marker order in the source file equals column display order in `kubectl get`.
- `make generate` regenerates CRDs from these markers; always run it after
  editing printcolumns.
