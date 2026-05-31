#!/usr/bin/env bash
# Standalone orphan-cleanup for the e2e cluster's timeweb-e2e namespace.
#
# ⚠ INVESTIGATE FIRST. Leftover CRs are signal — they usually mean a
# real assertion failure, a killed kuttl run, or a stuck finalizer.
# Wiping them blindly destroys the evidence. Before running this:
#
#   kubectl --context=k3d-provider-timeweb-e2e -n timeweb-e2e \
#     describe <kind>/<name>
#   kubectl --context=k3d-provider-timeweb-e2e -n crossplane-system \
#     logs -l pkg.crossplane.io/provider=provider-timeweb --tail=200
#
# Once you understand why the CRs are there, this script wipes them.
#
# Invoked via `make e2e.cleanup`. Reads env from test/e2e/Makefile.test:
#   E2E_KUBECONTEXT, E2E_NAMESPACE

set -euo pipefail

: "${E2E_KUBECONTEXT:?run via 'make e2e.cleanup'}"
: "${E2E_NAMESPACE:?}"

KCTL="kubectl --context=$E2E_KUBECONTEXT"

e2e_resources() {
  $KCTL -n "$E2E_NAMESPACE" get \
    projects.project.m.timeweb.crossplane.io,\
sshkeys.sshkey.m.timeweb.crossplane.io,\
s3buckets.objectstorage.m.timeweb.crossplane.io,\
containerregistries.containerregistry.m.timeweb.crossplane.io \
    --no-headers 2>/dev/null || true
}

echo "[e2e.cleanup] inventory before:"
BEFORE=$(e2e_resources)
if [ -z "$BEFORE" ]; then
  echo "[e2e.cleanup]   (none — nothing to clean)"
  exit 0
fi
echo "$BEFORE" | sed 's/^/[e2e.cleanup]   /'

echo "[e2e.cleanup] deleting Projects (Observe-only — upstream is preserved):"
$KCTL -n "$E2E_NAMESPACE" delete \
  projects.project.m.timeweb.crossplane.io --all --timeout=60s --ignore-not-found=true 2>&1 \
  | sed 's/^/[e2e.cleanup]   /' || true

echo "[e2e.cleanup] deleting lifecycle MRs (managementPolicies=['*'] — upstream is removed):"
$KCTL -n "$E2E_NAMESPACE" delete \
  sshkeys.sshkey.m.timeweb.crossplane.io,\
s3buckets.objectstorage.m.timeweb.crossplane.io,\
containerregistries.containerregistry.m.timeweb.crossplane.io \
  --all --timeout=2m --ignore-not-found=true 2>&1 \
  | sed 's/^/[e2e.cleanup]   /' || true

echo
echo "[e2e.cleanup] inventory after:"
AFTER=$(e2e_resources)
if [ -z "$AFTER" ]; then
  echo "[e2e.cleanup]   (none — clean exit)"
else
  echo "$AFTER" | sed 's/^/[e2e.cleanup]   /'
  echo "[e2e.cleanup] WARN: some CRs survived deletion — likely stuck on finalizer."
  echo "[e2e.cleanup]       Force-remove with: kubectl patch <resource> -n $E2E_NAMESPACE \\"
  echo "[e2e.cleanup]           -p '{\"metadata\":{\"finalizers\":[]}}' --type=merge"
  exit 1
fi
