#!/usr/bin/env bash
# Wrapper for `kubectl-kuttl test` against the e2e cluster.
#
# Single required env input: TIMEWEB_CLOUD_TOKEN.
#
# Optional env: TIMEWEB_E2E_TOKEN — a *second* Timeweb account token. When
# set, the wrapper provisions an extra namespace + Secret + dual-PC pair
# bound to it, enabling the multi-PC isolation bundle (test 07-*). When
# unset, that bundle is skipped via a kuttl test-suite filter so the
# single-token e2e path remains the default. The two tokens MUST refer to
# different Timeweb accounts (or at least non-overlapping resource scopes);
# isolation cannot be observed against one account.
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
#     4. If TIMEWEB_E2E_TOKEN is set: create the secondary namespace +
#        Secret + namespaced ProviderConfig + ClusterProviderConfig.
#     5. Generate unique names for every MR (timestamp-suffixed) so the
#        suite is safe to re-run within the same Timeweb account.
#     6. Copy the test bundle to a tmpdir and envsubst the TWE_* values
#        into every YAML file. The git-tracked bundle stays clean.
#     7. Invoke `kubectl-kuttl test` against the substituted tmpdir. The
#        multi-PC bundle (07-*) is skipped when TIMEWEB_E2E_TOKEN is unset.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_KUBECONTEXT, E2E_NAMESPACE

set -euo pipefail

: "${E2E_KUBECONTEXT:?run via 'make e2e.test'}"
: "${E2E_NAMESPACE:?}"
: "${KUTTL:=go run github.com/kudobuilder/kuttl/cmd/kubectl-kuttl}"

# --- 0. Belt-and-suspenders: refuse to run unless E2E_KUBECONTEXT names a
#       k3d cluster. The Makefile derives this from E2E_CLUSTER_NAME, but if
#       the operator overrode it on the CLI we MUST NOT accidentally apply
#       manifests to a production cluster.
if [[ "$E2E_KUBECONTEXT" != k3d-* ]]; then
  cat >&2 <<EOF
ERROR: E2E_KUBECONTEXT="$E2E_KUBECONTEXT" does not start with "k3d-".

This wrapper is hard-locked to k3d clusters to prevent accidentally
applying e2e manifests against a production cluster. If you really do
need to target a non-k3d cluster, the safe path is to rename your
context to start with "k3d-".
EOF
  exit 1
fi

# Verify the named context actually exists locally — otherwise any
# subsequent kubectl call would fail with a confusing "context not found"
# AFTER we've already started creating Secrets etc.
if ! kubectl config get-contexts -o name | grep -qxF "$E2E_KUBECONTEXT"; then
  echo "ERROR: kubectl context $E2E_KUBECONTEXT does not exist on this host (kubectl config get-contexts)." >&2
  exit 1
fi

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

# --- 1b. Optional secondary PCs for the multi-PC isolation bundle ----------
#
# Provisioned only when TIMEWEB_E2E_TOKEN is set. Creates:
#   - namespace            e2e-team-b
#   - secret               timeweb-credentials in e2e-team-b
#   - ProviderConfig       e2e-secondary (namespaced in e2e-team-b)
#   - ClusterProviderConfig e2e-shared (cluster-scoped, bound to the
#                          secondary token's secret in e2e-team-b)
# The kuttl bundle decides what to do with each.

E2E_SECONDARY_NS="e2e-team-b"
if [ -n "${TIMEWEB_E2E_TOKEN:-}" ]; then
  echo "[e2e] TIMEWEB_E2E_TOKEN set → provisioning secondary namespace + dual-PC pair (multi-PC bundle ENABLED)"
  $KCTL create namespace "$E2E_SECONDARY_NS" --dry-run=client -o yaml | $KCTL apply -f -
  $KCTL -n "$E2E_SECONDARY_NS" create secret generic timeweb-credentials \
    --from-literal=token="$TIMEWEB_E2E_TOKEN" \
    --dry-run=client -o yaml | $KCTL apply -f -
  cat <<EOF | $KCTL apply -f -
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: e2e-secondary
  namespace: $E2E_SECONDARY_NS
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      key: token
---
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: e2e-shared
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      namespace: $E2E_SECONDARY_NS
      key: token
EOF
  export TWE_MULTI_PC_ENABLED=1
else
  echo "[e2e] TIMEWEB_E2E_TOKEN unset → multi-PC bundle SKIPPED (set the env var to enable)"
  export TWE_MULTI_PC_ENABLED=0
fi

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

# --- 2b. Discover the cheapest msk-1 cloud-server preset slug ---------------
#
# The Server controller resolves operator-supplied `presetName` (e.g.
# `premium-2-2-40-msk-1`) against /api/v1/presets/servers. The slug shape
# is `<description_short>-<location>` (lowercased, periods → hyphens).
# Picking the cheapest per spec SC-001 + the e2e canary cost cap.

echo "[e2e] discovering cheapest ru-1 cloud-server preset slug"
TWE_SERVER_PRESET=$(curl -fsS \
  -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
  "${TW_API}/api/v1/presets/servers" \
  | jq -er '
      .server_presets
      | map(select(.location == "ru-1"))
      | sort_by(.price)
      | .[0]
      | (.description_short + "-" + .location)
      | ascii_downcase
      | gsub("[^a-z0-9-]+"; "-")
      | gsub("^-+|-+$"; "")
  ')
echo "[e2e]   → $TWE_SERVER_PRESET"

# --- 2a2. Discover a satisfiable custom sizing from /configurator/servers (feat 005) ---
# The cheapest ru-1 configurator's mins give a guaranteed-valid {cpu,ramGB,diskGB}
# for the Server custom-sizing bundle (16). ram/disk are MB upstream → /1024 to GB.
echo "[e2e] discovering a satisfiable ru-1 configurator sizing"
CFG=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/configurator/servers" \
  | jq -er '.server_configurators | map(select(.location=="ru-1")) | .[0].requirements')
TWE_SRV_CPU=$(echo "$CFG" | jq -er '.cpu_min')
TWE_SRV_RAMGB=$(echo "$CFG" | jq -er '(.ram_min/1024)|floor')
TWE_SRV_DISKGB=$(echo "$CFG" | jq -er '(.disk_min/1024)|floor')
echo "[e2e]   → cpu=$TWE_SRV_CPU ramGB=$TWE_SRV_RAMGB diskGB=$TWE_SRV_DISKGB"

# --- 2b. Discover managed-Kubernetes presets + a k8s version (feature 004) ---
# /api/v1/presets/k8s is a discriminated list (type=master|worker); the slug is
# `description_short` (no location — AZ is set at cluster level). Pick the
# cheapest of each role; pick the newest catalog k8s version.
echo "[e2e] discovering cheapest k8s master/worker presets + a k8s version"
K8S_PRESETS=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/presets/k8s")
slugByRole() {
  # K8s presets carry no location, and Timeweb ships several identically-named
  # tiers → the bare slug is ambiguous; emit the `<slug>-<id>` disambiguator
  # the resolver accepts (FR-006). EXCLUDE "promo" tiers: Timeweb caps promo
  # clusters at ONE per account, so they 409 ("Promo cluster already exists")
  # and aren't repeatable for e2e — pick the cheapest non-promo instead.
  echo "$K8S_PRESETS" | jq -er --arg role "$1" '
    .k8s_presets
    | map(select(.type == $role and (.description_short | ascii_downcase | test("promo") | not)))
    | sort_by(.price) | .[0]
    | ((.description_short | ascii_downcase | gsub("[^a-z0-9-]+"; "-") | gsub("^-+|-+$"; "")) + "-" + (.id|tostring))'
}
TWE_K8S_MASTER_PRESET=$(slugByRole master)
TWE_K8S_WORKER_PRESET=$(slugByRole worker)
TWE_K8S_VERSION=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
  "${TW_API}/api/v1/k8s/k8s-versions" | jq -er '.k8s_versions | sort | last')
echo "[e2e]   → master=$TWE_K8S_MASTER_PRESET worker=$TWE_K8S_WORKER_PRESET version=$TWE_K8S_VERSION"

# --- 3. Generate unique names -----------------------------------------------

TS="$(date +%s)"
TWE_SSH_NAME="e2e-ssh-$TS"
TWE_S3_NAME="e2e-s3-$TS"
TWE_CR_NAME="e2e-cr-$TS"
TWE_SERVER_NAME="e2e-srv-$TS"
TWE_NETWORK_NAME="e2e-net-$TS"
TWE_K8S_CLUSTER_NAME="e2e-k8s-$TS"

export TWE_PROJECT_ID TWE_SSH_NAME TWE_S3_NAME TWE_CR_NAME TWE_SERVER_NAME TWE_SERVER_PRESET TWE_NETWORK_NAME
export TWE_K8S_MASTER_PRESET TWE_K8S_WORKER_PRESET TWE_K8S_VERSION TWE_K8S_CLUSTER_NAME
export TWE_SRV_CPU TWE_SRV_RAMGB TWE_SRV_DISKGB

echo "[e2e] generated names:"
echo "[e2e]   SSHKey:        $TWE_SSH_NAME"
echo "[e2e]   S3Bucket:      $TWE_S3_NAME"
echo "[e2e]   ContainerReg:  $TWE_CR_NAME"
echo "[e2e]   Server:        $TWE_SERVER_NAME"
echo "[e2e]   Network:       $TWE_NETWORK_NAME"
echo "[e2e]   K8sCluster:    $TWE_K8S_CLUSTER_NAME"

# --- 3b. Pre-create an out-of-band VPC for the networkID import test --------
#
# US3 (feature 003) lets an operator attach a Server to an existing VPC by
# `forProvider.networkID` WITHOUT modeling it as a Crossplane Network MR.
# The 10b bundle exercises that path, so the VPC must already exist upstream
# and NOT be Crossplane-managed. We create it here via curl (v2 path) and
# pass its id as $TWE_IMPORT_VPC_ID. The Server references it by ID; deleting
# the Server leaves the VPC untouched (that's the whole point), so the
# wrapper deletes the VPC itself at exit (v1 delete path per R-6).
#
# Gate with TIMEWEB_E2E_SKIP_IMPORT=1 to skip the whole bundle (e.g. on
# accounts where VPC quota is tight).
TWE_IMPORT_VPC_ENABLED=1
if [ "${TIMEWEB_E2E_SKIP_IMPORT:-0}" = "1" ]; then
  echo "[e2e] TIMEWEB_E2E_SKIP_IMPORT=1 → networkID-import bundle (10b) SKIPPED"
  TWE_IMPORT_VPC_ENABLED=0
else
  echo "[e2e] pre-creating out-of-band VPC for the networkID-import bundle (10b)"
  TWE_IMPORT_VPC_ID=$(curl -fsS \
    -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
    -H "Content-Type: application/json" \
    -X POST "${TW_API}/api/v2/vpcs" \
    -d "{\"name\":\"e2e-import-$TS\",\"subnet_v4\":\"10.32.0.0/24\",\"location\":\"ru-1\"}" \
    | jq -er '.vpc.id')
  echo "[e2e]   → $TWE_IMPORT_VPC_ID (wrapper will delete it at exit)"
  export TWE_IMPORT_VPC_ID
fi
export TWE_IMPORT_VPC_ENABLED

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
containerregistries.kubernetes.m.timeweb.crossplane.io,\
servers.compute.m.timeweb.crossplane.io,\
networks.network.m.timeweb.crossplane.io,\
floatingips.network.m.timeweb.crossplane.io \
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
# The trap also deallocates the out-of-band import VPC (section 3b). It is
# NOT Crossplane-managed, so kuttl teardown won't touch it; we delete it via
# the v1 path (R-6) after kuttl has already torn down the referencing Server.
trap 'rm -rf "$TMP_BUNDLE" "$TMP_KUBECONFIG"; [ -n "${TWE_IMPORT_VPC_ID:-}" ] && curl -fsS -X DELETE -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/vpcs/${TWE_IMPORT_VPC_ID}" >/dev/null 2>&1; true' EXIT

cp -R test/e2e/kuttl/. "$TMP_BUNDLE/"

# Restrict envsubst to the TWE_* allow-list so unrelated `$` literals
# elsewhere in YAML (e.g. JSONPath expressions in assertions) are not
# clobbered.
TWE_VARS='${TWE_PROJECT_ID} ${TWE_SSH_NAME} ${TWE_S3_NAME} ${TWE_CR_NAME} ${TWE_SERVER_NAME} ${TWE_SERVER_PRESET} ${TWE_NETWORK_NAME} ${TWE_IMPORT_VPC_ID} ${TWE_K8S_MASTER_PRESET} ${TWE_K8S_WORKER_PRESET} ${TWE_K8S_VERSION} ${TWE_K8S_CLUSTER_NAME} ${TWE_K8S_ADDON_TYPE} ${TWE_K8S_ADDON_VERSION} ${TWE_SRV_CPU} ${TWE_SRV_RAMGB} ${TWE_SRV_DISKGB}'

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
#
# `kubectl config view --raw --minify --context=...` strips the kubeconfig
# down to ONLY the named context, its cluster, and its user. The resulting
# file cannot reach any other cluster even if kuttl tried — there are no
# other entries in the served kubeconfig. Verified by two post-render
# assertions below.

kubectl config view --raw --minify --context="$E2E_KUBECONTEXT" > "$TMP_KUBECONFIG"
export KUBECONFIG="$TMP_KUBECONFIG"

# Sanity 1: the rendered kubeconfig's current-context MUST be the one
# we expect. If --minify produced something else (unlikely but possible
# on older kubectl versions), bail before kuttl applies anything.
rendered_ctx=$(kubectl config current-context)
if [ "$rendered_ctx" != "$E2E_KUBECONTEXT" ]; then
  echo "ERROR: rendered kubeconfig current-context is $rendered_ctx, want $E2E_KUBECONTEXT" >&2
  exit 1
fi

# Sanity 2: the cluster's API server URL MUST point at a local host
# (k3d binds to 127.0.0.1 / 0.0.0.0 / localhost). Rejecting anything
# else is the last line of defense against pointing at a remote cluster
# that happens to also be named "k3d-..." in someone's kubeconfig.
api_server=$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')
case "$api_server" in
  https://127.0.0.1:*|https://0.0.0.0:*|https://localhost:*|https://host.docker.internal:*)
    : # ok
    ;;
  *)
    echo "ERROR: kubeconfig API server $api_server is not a local URL — refusing to run e2e against a non-local cluster." >&2
    exit 1
    ;;
esac

echo "[e2e] context safety checks passed: $E2E_KUBECONTEXT → $api_server"

# --- 7. Run kuttl -----------------------------------------------------------

KUTTL_ARGS=()
# Optional scoping: KUTTL_TEST="<name> [<name>...]" restricts the run to the
# matching bundle(s). kuttl's --test is a regex, and (quirk) only the LAST
# --test on the command line is honored — so multiple space-separated names are
# joined into ONE alternation regex rather than emitted as repeated --test flags.
# Handy for iterating on a subset (e.g. KUTTL_TEST="16-server-custom-sizing
# 17-k8s-custom-sizing") without the full suite.
if [ -n "${KUTTL_TEST:-}" ]; then
  kuttl_re=""
  for t in $KUTTL_TEST; do
    kuttl_re="${kuttl_re:+$kuttl_re|}$t"
  done
  KUTTL_ARGS+=(--test "($kuttl_re)")
fi
if [ "$TWE_MULTI_PC_ENABLED" = "0" ]; then
  # kuttl's --skip-delete + --test (regex filter) wouldn't suppress the
  # bundle from being LOADED; the simplest reliable skip is to physically
  # remove the bundle dir from the tmpdir copy before kuttl runs.
  rm -rf "$TMP_BUNDLE/tests/07-multi-pc-isolation" "$TMP_BUNDLE/tests/07b-invalid-pc-kind"
fi
if [ "$TWE_IMPORT_VPC_ENABLED" = "0" ]; then
  # No pre-created VPC → the networkID-import bundle can't run. Remove it
  # from the tmpdir copy so kuttl doesn't load it.
  rm -rf "$TMP_BUNDLE/tests/10b-server-with-network-id"
fi
if [ -z "${TWE_K8S_ADDON_TYPE:-}" ] || [ -z "${TWE_K8S_ADDON_VERSION:-}" ]; then
  # The addon catalog is per-cluster, so type/version are operator-supplied
  # (TWE_K8S_ADDON_TYPE/_VERSION). Unset → drop the addon bundle (15).
  echo "[e2e] TWE_K8S_ADDON_TYPE/_VERSION unset → k8s addon bundle (15) SKIPPED"
  rm -rf "$TMP_BUNDLE/tests/15-k8s-addon"
fi

echo "[e2e] running kuttl test bundle from $TMP_BUNDLE"
set +e
$KUTTL test --config "$TMP_BUNDLE/kuttl-test.yaml" "${KUTTL_ARGS[@]}"
KUTTL_RC=$?
set -e
report_orphans
exit $KUTTL_RC
