#!/usr/bin/env bash
# Wrapper for `kubectl-kuttl test` against the e2e cluster.
#
# Single required env input: TIMEWEB_CLOUD_TOKEN.
#
# Design:
#   The kuttl test bundle itself is fully DECLARATIVE — every test is an
#   `apply.yaml` + `assert.yaml` pair, no shell. The wrapper handles all
#   dynamism that can't be expressed in static YAML:
#
#     1. Validate TIMEWEB_CLOUD_TOKEN + dep tools (kubectl, jq, curl,
#        envsubst).
#     2. Create the shared namespace + credential Secret in the cluster.
#     3. Query the live Timeweb API for:
#          - the cheapest container-registry preset slug
#          - the cheapest S3 storage preset slug
#          - the first available project ID (for the Project import test)
#        and export each as TWE_* env vars.
#     4. Generate unique names for every MR (timestamp-suffixed) so the
#        suite is safe to re-run within the same Timeweb account.
#     5. Copy the test bundle to a tmpdir and envsubst the TWE_* values
#        into every YAML file. The git-tracked bundle stays clean.
#     6. Invoke `kubectl-kuttl test` against the substituted tmpdir.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_KUBECONTEXT, E2E_NAMESPACE

set -euo pipefail

: "${E2E_KUBECONTEXT:?run via 'make e2e.test'}"
: "${E2E_NAMESPACE:?}"
: "${KUTTL:=go run github.com/kudobuilder/kuttl/cmd/kubectl-kuttl}"

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

for tool in kubectl jq curl envsubst; do
  command -v "$tool" >/dev/null || {
    echo "ERROR: required tool not found in PATH: $tool" >&2
    echo "       envsubst is part of GNU gettext on macOS — \`brew install gettext && brew link --force gettext\`." >&2
    exit 1
  }
done

KCTL="kubectl --context=$E2E_KUBECONTEXT"
TW_API="https://api.timeweb.cloud"

# --- 1. Namespace + Secret + ProviderConfig (shared across all tests) ------
#
# Both the Secret AND the ProviderConfig live in the wrapper because kuttl's
# default per-test cleanup deletes resources it applied — if the PC were a
# kuttl test step, kuttl would delete it after that step finished, and every
# subsequent MR test would fail with "no ProviderConfig 'e2e' …".

echo "[e2e] preparing namespace + credential Secret + ProviderConfig"
$KCTL create namespace "$E2E_NAMESPACE" --dry-run=client -o yaml | $KCTL apply -f -
$KCTL -n "$E2E_NAMESPACE" create secret generic timeweb-credentials \
  --from-literal=token="$TIMEWEB_CLOUD_TOKEN" \
  --dry-run=client -o yaml | $KCTL apply -f -
cat <<EOF | $KCTL apply -f -
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: e2e
  namespace: $E2E_NAMESPACE
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      key: token
EOF

# --- 2. Discover the operator's first project id ----------------------------
#
# The size-based preset discovery moved into the controller (the resolver
# fetches presets at MR-connect time and matches by initialSizeGB). The
# wrapper only needs the project id (for the import-only Project test).

echo "[e2e] discovering first available project id"
TWE_PROJECT_ID=$(curl -fsS \
  -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
  "${TW_API}/api/v1/projects" \
  | jq -er '.projects | sort_by(.id) | .[0].id | tostring')
echo "[e2e]   → $TWE_PROJECT_ID"

# --- 3. Generate unique names -----------------------------------------------

TS="$(date +%s)"
TWE_SSH_NAME="e2e-ssh-$TS"
TWE_S3_NAME="e2e-s3-$TS"
TWE_CR_NAME="e2e-cr-$TS"

export TWE_PROJECT_ID TWE_SSH_NAME TWE_S3_NAME TWE_CR_NAME

echo "[e2e] generated names:"
echo "[e2e]   SSHKey:        $TWE_SSH_NAME"
echo "[e2e]   S3Bucket:      $TWE_S3_NAME"
echo "[e2e]   ContainerReg:  $TWE_CR_NAME"

# --- 3a. Inspect leftover CRs (DO NOT auto-delete) --------------------------
#
# Leftover CRs from prior runs are signal — they typically mean a real
# assertion failure, a killed kuttl run, or a controller stuck on
# finalizer removal. Auto-sweeping them at run start would mask the bugs
# that left them behind. The wrapper REPORTS what it sees and lets the
# operator decide whether to investigate or run `make e2e.cleanup`.
e2e_resources() {
  $KCTL -n "$E2E_NAMESPACE" get \
    projects.project.m.timeweb.crossplane.io,\
sshkeys.sshkey.m.timeweb.crossplane.io,\
s3buckets.objectstorage.m.timeweb.crossplane.io,\
containerregistries.containerregistry.m.timeweb.crossplane.io \
    --no-headers 2>/dev/null || true
}

EXISTING=$(e2e_resources)
if [ -n "$EXISTING" ]; then
  echo "[e2e] WARN: timeweb CRs already exist in $E2E_NAMESPACE before the run:"
  echo "$EXISTING" | sed 's/^/[e2e]   /'
  echo "[e2e]       These were NOT auto-cleaned. Investigate them first"
  echo "[e2e]       (kubectl describe + provider logs) before running"
  echo "[e2e]       \`make e2e.cleanup\` to wipe them. Tests below will"
  echo "[e2e]       use kubectl apply over the existing CRs on name collision."
fi

report_orphans() {
  echo
  echo "[e2e] post-run timeweb CR inventory:"
  ORPHANS=$(e2e_resources)
  if [ -z "$ORPHANS" ]; then
    echo "[e2e]   (none — clean exit)"
  else
    echo "$ORPHANS" | sed 's/^/[e2e]   /'
    echo "[e2e] NOTE: leftover CRs above are signal. Investigate before cleanup:"
    echo "[e2e]   1) kubectl --context=$E2E_KUBECONTEXT -n $E2E_NAMESPACE describe <kind>/<name>"
    echo "[e2e]   2) kubectl --context=$E2E_KUBECONTEXT -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb --tail=200"
    echo "[e2e]   3) Once root cause is known: make e2e.cleanup"
  fi
}

# --- 4. Substitute the test bundle into a tmpdir ----------------------------

TMP_BUNDLE=$(mktemp -d "${TMPDIR:-/tmp}/provider-timeweb-e2e.XXXXXX")
TMP_KUBECONFIG=$(mktemp)
trap 'rm -rf "$TMP_BUNDLE" "$TMP_KUBECONFIG"' EXIT

cp -R test/e2e/kuttl/. "$TMP_BUNDLE/"

# Restrict envsubst to the TWE_* allow-list so unrelated `$` literals
# elsewhere in YAML (e.g. JSONPath expressions in assertions) are not
# clobbered.
TWE_VARS='${TWE_PROJECT_ID} ${TWE_SSH_NAME} ${TWE_S3_NAME} ${TWE_CR_NAME}'

find "$TMP_BUNDLE" -type f \( -name '*.yaml' -o -name '*.yml' \) -print0 \
  | while IFS= read -r -d '' f; do
      envsubst "$TWE_VARS" < "$f" > "$f.new" && mv "$f.new" "$f"
    done

# --- 5. Kuttl needs the test config path INSIDE the substituted tree.
#       Rewrite the testDirs path so it resolves to our tmpdir copy.
sed -i.bak \
  -e "s|test/e2e/kuttl/tests|$TMP_BUNDLE/tests|" \
  "$TMP_BUNDLE/kuttl-test.yaml"
rm -f "$TMP_BUNDLE/kuttl-test.yaml.bak"

# --- 6. One-off kubeconfig (so we don't mutate the operator's) --------------

kubectl config view --raw --minify --context="$E2E_KUBECONTEXT" > "$TMP_KUBECONFIG"
export KUBECONFIG="$TMP_KUBECONFIG"

# --- 7. Run kuttl -----------------------------------------------------------

echo "[e2e] running kuttl test bundle from $TMP_BUNDLE"
set +e
$KUTTL test --config "$TMP_BUNDLE/kuttl-test.yaml"
KUTTL_RC=$?
set -e
report_orphans
exit $KUTTL_RC
