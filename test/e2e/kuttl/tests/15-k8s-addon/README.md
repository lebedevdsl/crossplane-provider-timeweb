# 15-k8s-addon (env-gated)

Installs + removes a `KubernetesClusterAddon` against a cluster created in the
bundle. The addon `type`+`version` are operator-supplied via
`$TWE_K8S_ADDON_TYPE` / `$TWE_K8S_ADDON_VERSION` (the available-addons catalog
is per-cluster, so it can't be discovered before the cluster exists). When
those vars are unset the wrapper removes this bundle from the run (same
opt-in pattern as `10b-server-with-network-id`). Pick a valid pair from
`GET /api/v1/k8s/clusters/{id}/addons-configs` after the cluster is up.
