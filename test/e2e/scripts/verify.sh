#!/usr/bin/env bash
# Apply the test manifests + assert Project conditions.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_KUBECONTEXT, E2E_NAMESPACE
#
# Reads from environment:
#   TIMEWEB_CLOUD_TOKEN — required; the Timeweb API token

set -euo pipefail

: "${E2E_KUBECONTEXT:?run via 'make e2e.test'}"
: "${E2E_NAMESPACE:?}"

if [ -z "${TIMEWEB_CLOUD_TOKEN:-}" ]; then
  cat >&2 <<'EOF'
ERROR: TIMEWEB_CLOUD_TOKEN is not set.

Generate a token in the Timeweb dashboard under
  Settings → API and integrations → Access tokens
and re-run:

  export TIMEWEB_CLOUD_TOKEN="<your-token>"
  make e2e.test
EOF
  exit 1
fi

KCTL="kubectl --context=$E2E_KUBECONTEXT"
MANIFESTS=test/e2e/manifests

# --- 1. Namespace + Secret ----------------------------------------------------

echo "[e2e] applying namespace + credential Secret"
$KCTL create namespace "$E2E_NAMESPACE" --dry-run=client -o yaml | $KCTL apply -f -
$KCTL -n "$E2E_NAMESPACE" create secret generic timeweb-credentials \
  --from-literal=token="$TIMEWEB_CLOUD_TOKEN" \
  --dry-run=client -o yaml | $KCTL apply -f -

# --- 2. ProviderConfig --------------------------------------------------------

echo "[e2e] applying ProviderConfig"
sed -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" "$MANIFESTS/providerconfig.yaml" | $KCTL apply -f -

# --- 3. Test Project ----------------------------------------------------------

echo "[e2e] applying test Project"
sed -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" "$MANIFESTS/project.yaml" | $KCTL apply -f -

# --- 4. Wait for Project Ready=True ------------------------------------------

PROJECT_NAME=$(sed -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" "$MANIFESTS/project.yaml" \
  | grep '^  name:' | head -1 | awk '{print $2}')
EXPECTED_EXT_NAME=$(sed -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" "$MANIFESTS/project.yaml" \
  | awk -F'"' '/crossplane.io\/external-name/ {print $2}')
echo "[e2e] waiting for Project/$PROJECT_NAME Ready=True (≤ 3 min) — importing upstream id $EXPECTED_EXT_NAME"

# `kubectl wait --for=condition=Ready` matches managed-resource conditions too.
$KCTL -n "$E2E_NAMESPACE" wait \
  --for=condition=Ready "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" \
  --timeout=3m || {
    echo "[e2e] FAILED — Project never became Ready. Diagnostic dump:"
    $KCTL -n "$E2E_NAMESPACE" describe "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" || true
    $KCTL -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb --tail=100 || true
    exit 1
  }

# --- 5. Assert external-name annotation matches the import target ------------

EXT_NAME=$($KCTL -n "$E2E_NAMESPACE" \
  get "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" \
  -o jsonpath='{.metadata.annotations.crossplane\.io/external-name}')
if [ "$EXT_NAME" != "$EXPECTED_EXT_NAME" ]; then
  echo "[e2e] FAILED — external-name = '$EXT_NAME', expected '$EXPECTED_EXT_NAME'" >&2
  exit 1
fi
echo "[e2e] Project external-name: $EXT_NAME (matches expected import target)"

# --- 6. Verify that Observe populated atProvider with live upstream data ----

echo "[e2e] verifying atProvider.id was populated by Observe"
ATPROVIDER_ID=$($KCTL -n "$E2E_NAMESPACE" \
  get "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" \
  -o jsonpath='{.status.atProvider.id}')
if [ "$ATPROVIDER_ID" != "$EXPECTED_EXT_NAME" ]; then
  echo "[e2e] FAILED — status.atProvider.id = '$ATPROVIDER_ID', expected '$EXPECTED_EXT_NAME'" >&2
  exit 1
fi
ATPROVIDER_ACCOUNT=$($KCTL -n "$E2E_NAMESPACE" \
  get "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" \
  -o jsonpath='{.status.atProvider.accountId}')
echo "[e2e] atProvider observed: id=$ATPROVIDER_ID, accountId=$ATPROVIDER_ACCOUNT"

# Update path is intentionally NOT exercised — managementPolicies omits
# Update because the e2e token has read-only project access. If the token
# scope is widened later, broaden the policy list in the manifest and add
# back the Update + description-PATCH assertion here.

# --- 7. SSHKey full lifecycle (always runs) ---------------------------------

echo "[e2e] applying test SSHKey (full lifecycle)"
sed -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" "$MANIFESTS/sshkey.yaml" | $KCTL apply -f -

SSHKEY_NAME=e2e-sshkey
SSHKEY_RES="sshkey.sshkey.m.timeweb.crossplane.io/$SSHKEY_NAME"

echo "[e2e] waiting for SSHKey/$SSHKEY_NAME Ready=True (≤ 2 min)"
$KCTL -n "$E2E_NAMESPACE" wait --for=condition=Ready "$SSHKEY_RES" --timeout=2m || {
  echo "[e2e] FAILED — SSHKey never became Ready. Diagnostic dump:"
  $KCTL -n "$E2E_NAMESPACE" describe "$SSHKEY_RES" || true
  $KCTL -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb --tail=100 || true
  exit 1
}
SSHKEY_EXT=$($KCTL -n "$E2E_NAMESPACE" get "$SSHKEY_RES" \
  -o jsonpath='{.metadata.annotations.crossplane\.io/external-name}')
echo "[e2e] SSHKey created upstream with id=$SSHKEY_EXT"

echo "[e2e] PATCHing isDefault → true to exercise the Update path"
$KCTL -n "$E2E_NAMESPACE" patch "$SSHKEY_RES" --type=merge \
  -p '{"spec":{"forProvider":{"isDefault":true}}}'
# Give the controller a reconcile window then confirm Synced=True remains.
sleep 15
$KCTL -n "$E2E_NAMESPACE" wait --for=condition=Synced "$SSHKEY_RES" --timeout=1m
echo "[e2e] SSHKey Update reconciled cleanly"

# --- 8. S3Bucket env-gated full lifecycle -----------------------------------

S3_ENABLED=false
if [ -n "${TIMEWEB_S3_BUCKET_NAME:-}" ] && [ -n "${TIMEWEB_S3_PRESET_ID:-}" ]; then
  S3_ENABLED=true
  echo "[e2e] TIMEWEB_S3_BUCKET_NAME + TIMEWEB_S3_PRESET_ID set — exercising S3Bucket"
  sed \
    -e "s|__E2E_NAMESPACE__|$E2E_NAMESPACE|g" \
    -e "s|__TIMEWEB_S3_BUCKET_NAME__|$TIMEWEB_S3_BUCKET_NAME|g" \
    -e "s|__TIMEWEB_S3_PRESET_ID__|$TIMEWEB_S3_PRESET_ID|g" \
    "$MANIFESTS/s3bucket.yaml" | $KCTL apply -f -

  S3_NAME=e2e-s3bucket
  S3_RES="s3bucket.objectstorage.m.timeweb.crossplane.io/$S3_NAME"

  echo "[e2e] waiting for S3Bucket/$S3_NAME Ready=True (≤ 3 min)"
  $KCTL -n "$E2E_NAMESPACE" wait --for=condition=Ready "$S3_RES" --timeout=3m || {
    echo "[e2e] FAILED — S3Bucket never became Ready. Diagnostic dump:"
    $KCTL -n "$E2E_NAMESPACE" describe "$S3_RES" || true
    $KCTL -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb --tail=100 || true
    exit 1
  }
  S3_EXT=$($KCTL -n "$E2E_NAMESPACE" get "$S3_RES" \
    -o jsonpath='{.metadata.annotations.crossplane\.io/external-name}')
  echo "[e2e] S3Bucket created upstream with id=$S3_EXT"

  echo "[e2e] asserting connection Secret was populated"
  S3_SECRET_KEYS=$($KCTL -n "$E2E_NAMESPACE" get secret e2e-s3bucket-conn \
    -o jsonpath='{.data}' | tr ',' '\n' | wc -l | tr -d ' ')
  if [ "$S3_SECRET_KEYS" -lt 5 ]; then
    echo "[e2e] FAILED — connection Secret has $S3_SECRET_KEYS keys; expected ≥5 (endpoint,bucket,region,access_key,secret_key)" >&2
    exit 1
  fi
  echo "[e2e] connection Secret has $S3_SECRET_KEYS keys"

  echo "[e2e] PATCHing description to exercise the Update path"
  $KCTL -n "$E2E_NAMESPACE" patch "$S3_RES" --type=merge \
    -p '{"spec":{"forProvider":{"description":"Updated by e2e verify.sh"}}}'
  sleep 15
  $KCTL -n "$E2E_NAMESPACE" wait --for=condition=Synced "$S3_RES" --timeout=1m
  echo "[e2e] S3Bucket Update reconciled cleanly"
else
  echo "[e2e] skipping S3Bucket lifecycle (set TIMEWEB_S3_BUCKET_NAME + TIMEWEB_S3_PRESET_ID to enable)"
fi

# --- 9. Tear down created resources -----------------------------------------

if [ "$S3_ENABLED" = "true" ]; then
  echo "[e2e] deleting S3Bucket — managementPolicies = ['*'] → upstream bucket is removed"
  $KCTL -n "$E2E_NAMESPACE" delete "$S3_RES" --timeout=3m
fi

echo "[e2e] deleting SSHKey — managementPolicies = ['*'] → upstream key is removed"
$KCTL -n "$E2E_NAMESPACE" delete "$SSHKEY_RES" --timeout=2m

echo "[e2e] deleting Project — managementPolicies excludes Delete, so upstream is preserved"
$KCTL -n "$E2E_NAMESPACE" delete "project.project.m.timeweb.crossplane.io/$PROJECT_NAME" \
  --timeout=2m
echo "[e2e] CR removed. Upstream Timeweb project $EXT_NAME is NOT deleted; verify in the dashboard."

# --- 10. Summary --------------------------------------------------------------

echo
echo "[e2e] verify: OK"
echo "[e2e] Project lifecycle (import → observe-only → orphan-on-delete) completed against the live Timeweb API."
echo "[e2e] SSHKey full lifecycle (create → update → delete) completed against the live Timeweb API."
if [ "$S3_ENABLED" = "true" ]; then
  echo "[e2e] S3Bucket full lifecycle (create → update → delete) completed against the live Timeweb API."
else
  echo "[e2e] S3Bucket lifecycle skipped (env vars TIMEWEB_S3_BUCKET_NAME + TIMEWEB_S3_PRESET_ID were not set)."
fi
echo "[e2e] Upstream project $EXT_NAME is preserved per managementPolicies = [Observe]."
echo "[e2e] To inspect the cluster further: kubectl --context=$E2E_KUBECONTEXT -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb"
echo "[e2e] To tear down: make e2e.down"
