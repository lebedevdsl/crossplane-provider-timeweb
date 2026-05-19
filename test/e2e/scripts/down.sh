#!/usr/bin/env bash
# Tear down the e2e cluster + registry.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_CLUSTER_NAME, E2E_REGISTRY_NAME

set -euo pipefail

: "${E2E_CLUSTER_NAME:?run via 'make e2e.down'}"
: "${E2E_REGISTRY_NAME:?}"

if command -v k3d >/dev/null && k3d cluster list -o json 2>/dev/null \
    | grep -q "\"name\": \"$E2E_CLUSTER_NAME\""; then
  echo "[e2e] deleting k3d cluster $E2E_CLUSTER_NAME"
  k3d cluster delete "$E2E_CLUSTER_NAME"
else
  echo "[e2e] cluster $E2E_CLUSTER_NAME not found; skipping"
fi

if command -v k3d >/dev/null && k3d registry list -o json 2>/dev/null \
    | grep -q "\"name\": \"k3d-$E2E_REGISTRY_NAME\""; then
  echo "[e2e] deleting k3d registry $E2E_REGISTRY_NAME"
  k3d registry delete "$E2E_REGISTRY_NAME"
else
  echo "[e2e] registry $E2E_REGISTRY_NAME not found; skipping"
fi

echo "[e2e] down: OK"
