#!/usr/bin/env bash
# Bring up the e2e cluster: k3d cluster + local registry + Crossplane.
#
# Idempotent — safe to re-run; reuses existing registry/cluster when present.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_CLUSTER_NAME, E2E_REGISTRY_NAME, E2E_REGISTRY_PORT,
#   E2E_KUBECONTEXT, E2E_CROSSPLANE_VER

set -euo pipefail

: "${E2E_CLUSTER_NAME:?E2E_CLUSTER_NAME is required (run via 'make e2e.up')}"
: "${E2E_REGISTRY_NAME:?E2E_REGISTRY_NAME is required}"
: "${E2E_REGISTRY_HOST_PORT:?E2E_REGISTRY_HOST_PORT is required}"
: "${E2E_REGISTRY_PORT:?E2E_REGISTRY_PORT is required}"
: "${E2E_KUBECONTEXT:?E2E_KUBECONTEXT is required}"
: "${E2E_CROSSPLANE_VER:?E2E_CROSSPLANE_VER is required}"

for tool in docker k3d kubectl helm; do
  command -v "$tool" >/dev/null || {
    echo "ERROR: required tool not found in PATH: $tool" >&2
    exit 1
  }
done

# --- 1. Local registry --------------------------------------------------------

if k3d registry list -o json 2>/dev/null | grep -q "\"name\": \"k3d-$E2E_REGISTRY_NAME\""; then
  echo "[e2e] registry $E2E_REGISTRY_NAME already exists; reusing"
else
  echo "[e2e] creating k3d registry $E2E_REGISTRY_NAME (host port $E2E_REGISTRY_HOST_PORT → container port $E2E_REGISTRY_PORT)"
  k3d registry create "$E2E_REGISTRY_NAME" --port "$E2E_REGISTRY_HOST_PORT"
fi

# --- 2. k3d cluster -----------------------------------------------------------

if k3d cluster list -o json 2>/dev/null | grep -q "\"name\": \"$E2E_CLUSTER_NAME\""; then
  echo "[e2e] cluster $E2E_CLUSTER_NAME already exists; reusing"
else
  # In-cluster URL uses port 5000 (the registry container's internal port),
  # NOT the host port — k3d/containerd configures the cluster nodes to reach
  # the registry container directly via the docker network.
  echo "[e2e] creating k3d cluster $E2E_CLUSTER_NAME (cluster will pull from k3d-${E2E_REGISTRY_NAME}:${E2E_REGISTRY_PORT})"
  k3d cluster create "$E2E_CLUSTER_NAME" \
    --registry-use "k3d-${E2E_REGISTRY_NAME}:${E2E_REGISTRY_PORT}" \
    --wait \
    --timeout 120s
fi

# --- 3. kubectl context sanity ------------------------------------------------

echo "[e2e] waiting for control plane to be Ready"
kubectl --context="$E2E_KUBECONTEXT" wait --for=condition=Ready node --all --timeout=120s

# --- 4. Crossplane ------------------------------------------------------------

if helm --kube-context="$E2E_KUBECONTEXT" -n crossplane-system status crossplane >/dev/null 2>&1; then
  echo "[e2e] Crossplane already installed; reusing"
else
  echo "[e2e] installing Crossplane v$E2E_CROSSPLANE_VER"
  helm --kube-context="$E2E_KUBECONTEXT" repo add crossplane-stable \
    https://charts.crossplane.io/stable >/dev/null
  helm --kube-context="$E2E_KUBECONTEXT" repo update >/dev/null
  helm --kube-context="$E2E_KUBECONTEXT" install crossplane crossplane-stable/crossplane \
    --namespace crossplane-system --create-namespace \
    --version "$E2E_CROSSPLANE_VER" \
    --wait --timeout 5m
fi

echo "[e2e] waiting for Crossplane Pods to be Ready"
kubectl --context="$E2E_KUBECONTEXT" -n crossplane-system wait \
  --for=condition=Ready pods --all --timeout=180s

echo
echo "[e2e] up: OK"
echo "[e2e] context: $E2E_KUBECONTEXT"
echo "[e2e] kubectl --context=$E2E_KUBECONTEXT get pods -A"
