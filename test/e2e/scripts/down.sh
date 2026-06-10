#!/usr/bin/env bash
# Tear down the e2e cluster + registry.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_CLUSTER_NAME, E2E_REGISTRY_NAME

set -euo pipefail

: "${E2E_CLUSTER_NAME:?run via 'make e2e.down'}"
: "${E2E_REGISTRY_NAME:?}"

# Use k3d's own name lookup for existence (exit 0 = exists) rather than
# grepping `-o json` — the previous grep hard-coded `"name": "..."` with a
# space after the colon and silently missed the cluster when k3d emitted
# compact JSON, reporting "not found" while a live (billable) cluster lingered.
if command -v k3d >/dev/null && k3d cluster list "$E2E_CLUSTER_NAME" >/dev/null 2>&1; then
  echo "[e2e] deleting k3d cluster $E2E_CLUSTER_NAME"
  k3d cluster delete "$E2E_CLUSTER_NAME"
else
  echo "[e2e] cluster $E2E_CLUSTER_NAME not found; skipping"
fi

# Registries are listed under their k3d- prefix.
if command -v k3d >/dev/null && k3d registry list "k3d-$E2E_REGISTRY_NAME" >/dev/null 2>&1; then
  echo "[e2e] deleting k3d registry $E2E_REGISTRY_NAME"
  k3d registry delete "$E2E_REGISTRY_NAME"
else
  echo "[e2e] registry $E2E_REGISTRY_NAME not found; skipping"
fi

echo "[e2e] down: OK"
