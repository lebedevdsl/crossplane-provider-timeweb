# Preface: nodepool `autoscaler_settings` tuning block

Seeded by feature 026 (2026-08-07 wire capture, panel PATCH on
`/k8s/clusters/1096397/groups/117127`).

## Surface

The panel sends an `autoscaler_settings` object on the group PATCH that the
provider has never modeled:

```json
"autoscaler_settings": {
  "scale_down_utilization_threshold": 0.5,
  "scale_down_unneeded_duration": 300,
  "scale_down_unready_duration": 900,
  "max_node_provision_duration": 600,
  "zero_or_max_node_scaling": false,
  "ignore_daemonsets_utilization": false
}
```

The GET echoes it nested as `autoscaler_settings.{enabled, k8s_version,
values{...}}`. Omitting the block on PATCH is tolerated (provider does so today
— upstream defaults apply).

## Candidate feature

Optional `autoscaling.settings` block on `KubernetesClusterNodepool` mapping
these knobs (notably `zero_or_max_node_scaling` for burst pools and the
scale-down timings that govern scale-to-zero latency), single-writer like the
bounds, absent ⇒ never sent (upstream defaults preserved).

## Quirk to re-probe first

The PATCH response's `autoscaler_settings.enabled` read `false` while
`is_autoscaling` was `true` and the pool demonstrably autoscales.
`is_autoscaling` is authoritative (024/026 key on it). Establish what
`autoscaler_settings.enabled` actually means before modeling the block.
