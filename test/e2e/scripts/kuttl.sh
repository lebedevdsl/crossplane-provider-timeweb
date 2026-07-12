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

# --- 0. Context safety. E2E_KUBECONTEXT MUST be set EXPLICITLY (the `:?` above
#       enforces it — there is NO ambient-context default), so the suite never
#       applies to whatever cluster happens to be `current-context`.
#
#       Two modes:
#         * k3d-*  → the local e2e cluster (default). The API-server-must-be-
#                    local check (section 6) also applies.
#         * other  → an operator-provided REMOTE cluster (e.g. `twc-staging`),
#                    so e2e can run against a real cluster from inside Timeweb.
#                    The provider must already be installed from the PUBLISHED
#                    package (deploy/provider.yaml); the local-API check is
#                    skipped (the API server is legitimately remote) and the
#                    minified single-context kubeconfig + current-context
#                    assertion (section 6) are the guard.
E2E_REMOTE=0
[[ "$E2E_KUBECONTEXT" == k3d-* ]] || E2E_REMOTE=1
if [ "$E2E_REMOTE" = 1 ]; then
  echo "[e2e] REMOTE context '$E2E_KUBECONTEXT' (non-k3d) — provider must be installed from the published package; local-API guard skipped."
fi

# Verify the named context actually exists locally — otherwise any
# subsequent kubectl call would fail with a confusing "context not found"
# AFTER we've already started creating Secrets etc.
#
# The lookup is RETRIED: `kubectl config get-contexts` intermittently returns an
# empty/partial list on a transient kubeconfig read race (observed killing
# bundles mid-suite even though the context plainly exists). We retry the
# existence READ only — the explicit-context safety is unchanged; we still abort
# if the context is genuinely absent after all attempts.
_ctx_ok=0
for _attempt in 1 2 3 4 5; do
  if kubectl config get-contexts -o name 2>/dev/null | grep -qxF "$E2E_KUBECONTEXT"; then
    _ctx_ok=1
    break
  fi
  sleep 2
done
if [ "$_ctx_ok" != 1 ]; then
  echo "ERROR: kubectl context $E2E_KUBECONTEXT does not exist on this host (kubectl config get-contexts, after 5 attempts)." >&2
  exit 1
fi

# --- 0b. Remote mode: the provider is installed from the PUBLISHED package
#         (not side-loaded), so verify it is present + Healthy before we create
#         any namespaces/Secrets. Fail fast with guidance otherwise.
if [ "$E2E_REMOTE" = 1 ]; then
  if ! kubectl --context="$E2E_KUBECONTEXT" get provider.pkg.crossplane.io provider-timeweb >/dev/null 2>&1; then
    echo "ERROR: provider 'provider-timeweb' is not installed on $E2E_KUBECONTEXT." >&2
    echo "       Install the published package first:" >&2
    echo "         kubectl --context=$E2E_KUBECONTEXT apply -f deploy/provider.yaml" >&2
    exit 1
  fi
  healthy=$(kubectl --context="$E2E_KUBECONTEXT" get provider.pkg.crossplane.io provider-timeweb \
    -o jsonpath='{.status.conditions[?(@.type=="Healthy")].status}' 2>/dev/null)
  if [ "$healthy" != "True" ]; then
    echo "ERROR: provider 'provider-timeweb' is not Healthy (Healthy=${healthy:-<none>}) on $E2E_KUBECONTEXT." >&2
    echo "       Wait for it ('kubectl --context=$E2E_KUBECONTEXT get providers') or check deploy/provider.yaml + the '${E2E_PULL_SECRET:-registry-creds}' pull secret." >&2
    exit 1
  fi
  echo "[e2e] remote provider 'provider-timeweb' is Installed+Healthy."
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

# --- 2. Lazy + env-overridable discovery ------------------------------------
#
# Each catalog/sizing lookup below hits api.timeweb.cloud FROM THE HOST. It runs
# ONLY when (a) a bundle that needs the value is selected (via KUTTL_TEST) AND
# (b) the value isn't already provided in the env. This lets the cheap bundles
# (sshkey/s3/registry/preset-not-found/network) run with ZERO host→API calls —
# essential when the host can't reach the API (Qrator WAF) and only the
# IN-CLUSTER provider needs it. KUTTL_TEST empty = full run = discover everything.
# Pre-seed any TWE_* in the env (e.g. from an in-cluster discovery run) to skip
# its curl entirely.
_sel="${KUTTL_TEST:-__ALL__}"
needs() {
  [ "$_sel" = "__ALL__" ] && return 0
  local b; for b in "$@"; do case " $_sel " in *"$b"*) return 0 ;; esac; done
  return 1
}

# project id — only the import-only Project bundle (02) needs it.
if [ -n "${TWE_PROJECT_ID:-}" ]; then
  echo "[e2e] TWE_PROJECT_ID from env: $TWE_PROJECT_ID (discovery skipped)"
elif needs 02-project-import; then
  echo "[e2e] discovering first available project id"
  TWE_PROJECT_ID=$(curl -fsS \
    -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
    "${TW_API}/api/v1/projects" \
    | jq -er '.projects | sort_by(.id) | .[0].id | tostring')
  echo "[e2e]   → $TWE_PROJECT_ID"
else
  TWE_PROJECT_ID=""
  echo "[e2e] project-id discovery skipped (no selected bundle needs it)"
fi

# --- 2b. Discover the cheapest ru-1 cloud-server preset slug ---------------
#
# Feature 007 (US2): the Server controller now accepts a bare short slug (e.g.
# `ssd-15`) in addition to the long `<short>-<location>` form. We discover the
# bare form so e2e bundles exercise that path. The long form still works
# (back-compat) but is no longer needed here since `location: ru-1` is set
# in the manifest and the resolver scopes matching to that region.
# Picking the cheapest per spec SC-001 + the e2e canary cost cap.

if [ -n "${TWE_SERVER_PRESET:-}" ]; then
  echo "[e2e] TWE_SERVER_PRESET from env: $TWE_SERVER_PRESET (discovery skipped)"
elif needs 09-server 10-server 11-floating; then
  echo "[e2e] discovering cheapest ru-1 cloud-server preset slug (bare form)"
  TWE_SERVER_PRESET=$(curl -fsS \
    -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
    "${TW_API}/api/v1/presets/servers" \
    | jq -er '
        .server_presets
        | map(select(.location == "ru-1"))
        | sort_by(.price)
        | .[0].description_short
        | ascii_downcase
        | gsub("[^a-z0-9-]+"; "-")
        | gsub("^-+|-+$"; "")
    ')
  echo "[e2e]   → $TWE_SERVER_PRESET (bare form; resolver scopes to ru-1 from the manifest location)"
else
  TWE_SERVER_PRESET=""
  echo "[e2e] server-preset discovery skipped (no selected bundle needs it)"
fi

# --- 2a2. Discover a satisfiable custom sizing from /configurator/servers (feat 005) ---
# The Server CRD takes ramGB/diskGB; the resolver validates ramGB*1024 (MB) and
# diskGB against the configurator's {min,step,max}, requiring exact step
# alignment FROM min. The only request guaranteed step-aligned is the min
# itself — so pick the first ru-1 configurator whose ram_min/disk_min are
# GB-aligned (min/1024 then maps back to exactly min; offset 0 is always
# aligned). A flooring division on a non-aligned min would request BELOW min
# and doom bundle 16 to NoConfiguratorAvailable.
if [ -n "${TWE_SRV_CPU:-}" ]; then
  echo "[e2e] TWE_SRV_* from env: cpu=$TWE_SRV_CPU ramGB=${TWE_SRV_RAMGB:-} diskGB=${TWE_SRV_DISKGB:-} (discovery skipped)"
elif needs 16-server-custom-sizing; then
  echo "[e2e] discovering a satisfiable ru-1 configurator sizing"
  CFG=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/configurator/servers" \
    | jq -er '
        .server_configurators
        | map(select(.location=="ru-1"
              and .requirements.ram_min >= 1024 and .requirements.ram_min % 1024 == 0
              and .requirements.disk_min >= 1024 and .requirements.disk_min % 1024 == 0))
        | .[0].requirements') || {
    echo "ERROR: no ru-1 configurator with GB-aligned ram_min/disk_min — cannot derive a satisfiable {cpu,ramGB,diskGB} for bundle 16." >&2
    exit 1
  }
  TWE_SRV_CPU=$(echo "$CFG" | jq -er '.cpu_min')
  TWE_SRV_RAMGB=$(echo "$CFG" | jq -er '.ram_min/1024')
  TWE_SRV_DISKGB=$(echo "$CFG" | jq -er '.disk_min/1024')
  echo "[e2e]   → cpu=$TWE_SRV_CPU ramGB=$TWE_SRV_RAMGB diskGB=$TWE_SRV_DISKGB"
else
  TWE_SRV_CPU=""; TWE_SRV_RAMGB=""; TWE_SRV_DISKGB=""
  echo "[e2e] server-sizing discovery skipped (no selected bundle needs it)"
fi

# --- 2a3. Discover satisfiable K8s sizings from /configurator/k8s (feat 005) ---
# K8s custom sizing resolves against its OWN catalog — /api/v1/configurator/k8s
# (undocumented upstream; the k8s create endpoint rejects server-catalog ids
# with 400 configurator_not_found). The catalog is tag-partitioned: the
# cluster's master `configuration` needs a `k8s_master_configurator` entry,
# worker groups need a non-master one — a cross-family id makes the upstream
# IGNORE availability_zone and strand the cluster in ams-1 (failed). Bundle 17
# pins the cluster to AZ msk-1 → location ru-3 (Moscow; spb-3↔ru-1,
# ams-1↔nl-1, fra-1↔de-1), so both sizings come from the ru-3 entries of
# their families. Same GB-aligned-mins rule as 2a2.
if [ -n "${TWE_K8S_MASTER_CPU:-}" ]; then
  echo "[e2e] TWE_K8S_MASTER/WORKER sizings from env (discovery skipped)"
elif needs 17-k8s-custom-sizing; then
  echo "[e2e] discovering satisfiable ru-3 (msk-1) K8s master+worker configurator sizings"
  K8S_CATALOG=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/configurator/k8s")
  k8sSizingByRole() {
    # $1 = "master" | "worker" — selects the tag family; emits "cpu ramGB diskGB".
    echo "$K8S_CATALOG" | jq -er --arg role "$1" '
        .k8s_configurators
        | map(select(.location=="ru-3"
              and ((.tags | index("k8s_master_configurator")) != null) == ($role == "master")
              and .requirements.ram_min >= 1024 and .requirements.ram_min % 1024 == 0
              and .requirements.disk_min >= 1024 and .requirements.disk_min % 1024 == 0))
        | .[0].requirements
        | "\(.cpu_min) \(.ram_min/1024) \(.disk_min/1024)"'
  }
  read -r TWE_K8S_MASTER_CPU TWE_K8S_MASTER_RAMGB TWE_K8S_MASTER_DISKGB <<<"$(k8sSizingByRole master)" || {
    echo "ERROR: no ru-3 MASTER K8s configurator with GB-aligned mins — cannot size bundle 17's cluster." >&2
    exit 1
  }
  read -r TWE_K8S_WORKER_CPU TWE_K8S_WORKER_RAMGB TWE_K8S_WORKER_DISKGB <<<"$(k8sSizingByRole worker)" || {
    echo "ERROR: no ru-3 WORKER K8s configurator with GB-aligned mins — cannot size bundle 17's nodepool." >&2
    exit 1
  }
  echo "[e2e]   → master cpu=$TWE_K8S_MASTER_CPU ramGB=$TWE_K8S_MASTER_RAMGB diskGB=$TWE_K8S_MASTER_DISKGB"
  echo "[e2e]   → worker cpu=$TWE_K8S_WORKER_CPU ramGB=$TWE_K8S_WORKER_RAMGB diskGB=$TWE_K8S_WORKER_DISKGB"
else
  TWE_K8S_MASTER_CPU=""; TWE_K8S_MASTER_RAMGB=""; TWE_K8S_MASTER_DISKGB=""
  TWE_K8S_WORKER_CPU=""; TWE_K8S_WORKER_RAMGB=""; TWE_K8S_WORKER_DISKGB=""
  echo "[e2e] k8s-sizing discovery skipped (no selected bundle needs it)"
fi

# --- 2a4. Discover the cheapest ru-3 router tier (feature 006) --------------
# /api/v1/presets/routers is undocumented upstream (probed live). The slug
# rule mirrors the resolver's fetchRouterPresets:
#   router-<node_count>x<cpu>-<ram>gb-<location>
# Bundle 18 pins the router to AZ msk-1 → tiers of location ru-3 only (tiers
# are per-region and the upstream derives the router's zone from the tier).
if [ -n "${TWE_ROUTER_PRESET:-}" ]; then
  echo "[e2e] TWE_ROUTER_PRESET from env: $TWE_ROUTER_PRESET (discovery skipped)"
elif needs 18-router 19-private; then
  echo "[e2e] discovering cheapest ru-3 router tier"
  TWE_ROUTER_PRESET=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/presets/routers" \
    | jq -er '
        .router_presets
        | map(select(.location == "ru-3"))
        | sort_by(.cost) | .[0]
        | "router-\(.node_count)x\(.cpu)-\(.ram)gb-\(.location)"')
  echo "[e2e]   → $TWE_ROUTER_PRESET"
else
  TWE_ROUTER_PRESET=""
  echo "[e2e] router-preset discovery skipped (no selected bundle needs it)"
fi

# --- 2b. Discover managed-Kubernetes presets + a k8s version (feature 004) ---
# /api/v1/presets/k8s is a discriminated list (type=master|worker); the slug is
# `description_short` (no location — AZ is set at cluster level). Pick the
# cheapest of each role; pick the newest catalog k8s version.
if [ -n "${TWE_K8S_MASTER_PRESET:-}" ] && [ -n "${TWE_K8S_VERSION:-}" ]; then
  echo "[e2e] TWE_K8S_MASTER/WORKER_PRESET + TWE_K8S_VERSION from env (discovery skipped)"
elif needs k8s 19-private; then
  echo "[e2e] discovering cheapest k8s master/worker presets + a k8s version"
  K8S_PRESETS=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/presets/k8s")
  slugByRole() {
  # K8s presets carry HIDDEN zone affinity (availability_zone — absent from
  # the published swagger, verified live in feature 006): a preset whose zone
  # mismatches the cluster's AZ makes the upstream MIS-PLACE the cluster as a
  # broken half-created zombie instead of rejecting. The bundles pin AZ msk-1,
  # so discovery MUST filter by that zone. Timeweb also ships several
  # identically-named tiers → emit the `<slug>-<id>` disambiguator the
  # resolver accepts (FR-006). EXCLUDE "promo" tiers: capped at ONE per
  # account (409 "Promo cluster already exists"), not repeatable for e2e.
  echo "$K8S_PRESETS" | jq -er --arg role "$1" '
    .k8s_presets
    | map(select(.type == $role
          and .availability_zone == "msk-1"
          and (.description_short | ascii_downcase | test("promo") | not)))
    | sort_by(.price) | .[0]
    | ((.description_short | ascii_downcase | gsub("[^a-z0-9-]+"; "-") | gsub("^-+|-+$"; "")) + "-" + (.id|tostring))'
}
  TWE_K8S_MASTER_PRESET=$(slugByRole master)
  TWE_K8S_WORKER_PRESET=$(slugByRole worker)
  TWE_K8S_VERSION=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
    "${TW_API}/api/v1/k8s/k8s-versions" | jq -er '.k8s_versions | sort | last')
  echo "[e2e]   → master=$TWE_K8S_MASTER_PRESET worker=$TWE_K8S_WORKER_PRESET version=$TWE_K8S_VERSION"
else
  TWE_K8S_MASTER_PRESET=""; TWE_K8S_WORKER_PRESET=""; TWE_K8S_VERSION=""
  echo "[e2e] k8s-preset/version discovery skipped (no selected bundle needs it)"
fi

# --- 3. Generate unique names -----------------------------------------------

TS="$(date +%s)"
TWE_SSH_NAME="e2e-ssh-$TS"
TWE_S3_NAME="e2e-s3-$TS"
TWE_CR_NAME="e2e-cr-$TS"
TWE_SERVER_NAME="e2e-srv-$TS"
TWE_NETWORK_NAME="e2e-net-$TS"
TWE_K8S_CLUSTER_NAME="e2e-k8s-$TS"
TWE_ROUTER_NAME="e2e-router-$TS"
TWE_FW_NAME="e2e-fw-$TS"
TWE_CDN_NAME="e2e-cdn-$TS"
TWE_ROUTER_NET_NAME="e2e-router-net-$TS"
TWE_PRIV_NET_NAME="e2e-priv-net-$TS"
TWE_PRIV_ROUTER_NAME="e2e-priv-router-$TS"
TWE_PRIV_CLUSTER_NAME="e2e-priv-k8s-$TS"

export TWE_PROJECT_ID TWE_SSH_NAME TWE_S3_NAME TWE_CR_NAME TWE_SERVER_NAME TWE_SERVER_PRESET TWE_NETWORK_NAME
export TWE_K8S_MASTER_PRESET TWE_K8S_WORKER_PRESET TWE_K8S_VERSION TWE_K8S_CLUSTER_NAME
export TWE_SRV_CPU TWE_SRV_RAMGB TWE_SRV_DISKGB
export TWE_K8S_MASTER_CPU TWE_K8S_MASTER_RAMGB TWE_K8S_MASTER_DISKGB
export TWE_K8S_WORKER_CPU TWE_K8S_WORKER_RAMGB TWE_K8S_WORKER_DISKGB
export TWE_ROUTER_PRESET TWE_ROUTER_NAME TWE_ROUTER_NET_NAME
export TWE_PRIV_NET_NAME TWE_PRIV_ROUTER_NAME TWE_PRIV_CLUSTER_NAME
export TWE_FW_NAME
export TWE_CDN_NAME

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
if [ "${TIMEWEB_E2E_SKIP_IMPORT:-0}" = "1" ] || ! needs 10b-server-with-network-id; then
  echo "[e2e] networkID-import bundle (10b) not selected or skipped → no out-of-band VPC"
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
# Every timeweb MR kind, as a single comma-list — shared by the orphan
# inventory and the diagnostics describe so the two never drift.
TWE_KINDS="projects.project.m.timeweb.crossplane.io,\
sshkeys.sshkey.m.timeweb.crossplane.io,\
s3buckets.objectstorage.m.timeweb.crossplane.io,\
containerregistries.kubernetes.m.timeweb.crossplane.io,\
servers.compute.m.timeweb.crossplane.io,\
networks.network.m.timeweb.crossplane.io,\
floatingips.network.m.timeweb.crossplane.io,\
routers.network.m.timeweb.crossplane.io,\
kubernetesclusters.kubernetes.m.timeweb.crossplane.io,\
kubernetesclusternodepools.kubernetes.m.timeweb.crossplane.io"

e2e_resources() {
  $KCTL -n "$E2E_NAMESPACE" get "$TWE_KINDS" --no-headers 2>/dev/null || true
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
  report_api_orphans
}

# report_api_orphans queries the live Timeweb API for billable resources still
# named e2e-* after the run — catches the case where an MR delete succeeded but
# the upstream delete did NOT (the CR is gone, yet the cloud resource lingers
# and bills). REPORT only (investigate-before-cleanup). Best-effort: it shares
# the host's API reachability with the discovery curls above, so if the host
# cannot reach api.timeweb.cloud (e.g. the Qrator WAF) it simply prints nothing.
report_api_orphans() {
  if [ "${TWE_NO_API_SWEEP:-0}" = "1" ]; then
    echo "[e2e] live-API orphan sweep skipped (TWE_NO_API_SWEEP=1 — host can't reach the API)."
    echo "[e2e]   verify no billable orphans from inside Timeweb / the panel after the run."
    return 0
  fi
  echo "[e2e] live Timeweb API orphan sweep (name ~ /^e2e-/):"
  local found=0 names
  _api_sweep() {
    names=$(curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}$1" 2>/dev/null \
      | jq -r "$2 // [] | .[]? | .name // empty" 2>/dev/null | grep -E '^e2e-' || true)
    [ -n "$names" ] && { echo "$names" | sed "s|^|[e2e]   $1 → |"; found=1; }
  }
  _api_sweep /api/v2/vpcs         '.vpcs'
  _api_sweep /api/v1/servers      '.servers'
  _api_sweep /api/v1/routers      '.routers'
  _api_sweep /api/v1/k8s/clusters '.clusters'
  [ "$found" = 0 ] && echo "[e2e]   (none found via API, or host cannot reach api.timeweb.cloud)"
}

# --- 3c. Diagnostics: events + per-MR describe + provider logs ---------------
#
# Crossplane condition fields collapse to a single current state and hide the
# history; the Events feed and provider logs are where the real "why" lives
# (continuous-update loops, transient retries, ordering, no_paid, etc.). We
# capture all three to files so a run is debuggable from the artifacts alone —
# no need to re-derive state by hand after kuttl tears the resources down.
#
# A live `kubectl get events -w` stream runs for the whole test (started in
# section 7) so transient events that age out before the post-run snapshot are
# still recorded. Override the output dir with TWE_DIAG_DIR.
DIAG_DIR="${TWE_DIAG_DIR:-test/e2e/.diagnostics/$TS}"
mkdir -p "$DIAG_DIR"

collect_diagnostics() {
  echo
  echo "[e2e] collecting diagnostics → $DIAG_DIR"
  # Time-sorted namespace events (the why-did-it-fail feed).
  $KCTL -n "$E2E_NAMESPACE" get events --sort-by=.lastTimestamp \
    > "$DIAG_DIR/events.txt" 2>&1 || true
  # Per-MR describe: status conditions + each resource's own Events section.
  $KCTL -n "$E2E_NAMESPACE" describe "$TWE_KINDS" \
    > "$DIAG_DIR/mr-describe.txt" 2>&1 || true
  # Provider controller logs (--prefix survives a pod restart mid-run).
  $KCTL -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb \
    --tail=10000 --prefix --timestamps > "$DIAG_DIR/provider.log" 2>&1 || true
  echo "[e2e]   events.txt ($(wc -l <"$DIAG_DIR/events.txt" 2>/dev/null | tr -d ' ') lines)" \
       "· mr-describe.txt · provider.log · events-stream.txt"
}

# cleanup_mrs deletes every e2e MR so Crossplane tears the upstream resources
# down. kuttl does this itself on a clean run; this is the manual-interrupt
# fallback (SIGHUP path) so a killed run doesn't strand billable cloud resources.
cleanup_mrs() {
  echo "[e2e] deleting e2e MRs in $E2E_NAMESPACE (Crossplane tears down upstream)"
  $KCTL -n "$E2E_NAMESPACE" delete "$TWE_KINDS" --all --ignore-not-found --wait=false 2>&1 \
    | sed 's/^/[e2e]   /' || true
}

# --- 4. Substitute the test bundle into a tmpdir ----------------------------

TMP_BUNDLE=$(mktemp -d "${TMPDIR:-/tmp}/provider-timeweb-e2e.XXXXXX")
TMP_KUBECONFIG=$(mktemp)
# The trap also deallocates the out-of-band import VPC (section 3b). It is
# NOT Crossplane-managed, so kuttl teardown won't touch it; we delete it via
# the v1 path (R-6) after kuttl has already torn down the referencing Server.
trap 'kill "${EVENT_STREAM_PID:-}" 2>/dev/null; rm -rf "$TMP_BUNDLE" "$TMP_KUBECONFIG"; [ -n "${TWE_IMPORT_VPC_ID:-}" ] && curl -fsS -X DELETE -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" "${TW_API}/api/v1/vpcs/${TWE_IMPORT_VPC_ID}" >/dev/null 2>&1; true' EXIT

cp -R test/e2e/kuttl/. "$TMP_BUNDLE/"

# Restrict envsubst to the TWE_* allow-list so unrelated `$` literals
# elsewhere in YAML (e.g. JSONPath expressions in assertions) are not
# clobbered.
TWE_VARS='${TWE_PROJECT_ID} ${TWE_SSH_NAME} ${TWE_S3_NAME} ${TWE_CR_NAME} ${TWE_SERVER_NAME} ${TWE_SERVER_PRESET} ${TWE_NETWORK_NAME} ${TWE_IMPORT_VPC_ID} ${TWE_K8S_MASTER_PRESET} ${TWE_K8S_WORKER_PRESET} ${TWE_K8S_VERSION} ${TWE_K8S_CLUSTER_NAME} ${TWE_K8S_ADDON_TYPE} ${TWE_K8S_ADDON_VERSION} ${TWE_SRV_CPU} ${TWE_SRV_RAMGB} ${TWE_SRV_DISKGB} ${TWE_K8S_MASTER_CPU} ${TWE_K8S_MASTER_RAMGB} ${TWE_K8S_MASTER_DISKGB} ${TWE_K8S_WORKER_CPU} ${TWE_K8S_WORKER_RAMGB} ${TWE_K8S_WORKER_DISKGB} ${TWE_ROUTER_PRESET} ${TWE_ROUTER_NAME} ${TWE_ROUTER_NET_NAME} ${TWE_PRIV_NET_NAME} ${TWE_PRIV_ROUTER_NAME} ${TWE_PRIV_CLUSTER_NAME} ${TWE_FW_NAME} ${TWE_CDN_NAME}'

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
if [ "$E2E_REMOTE" = 0 ]; then
  case "$api_server" in
    https://127.0.0.1:*|https://0.0.0.0:*|https://localhost:*|https://host.docker.internal:*)
      : # ok
      ;;
    *)
      echo "ERROR: kubeconfig API server $api_server is not a local URL — refusing to run e2e against a non-local cluster (k3d mode)." >&2
      exit 1
      ;;
  esac
else
  echo "[e2e] remote mode: API server $api_server (local-URL guard skipped; minified single-context kubeconfig is the guard)."
fi

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
if [ "${TIMEWEB_E2E_PRIVATE:-0}" != "1" ]; then
  # The private-cluster bundle (19) provisions a router + cluster + worker —
  # billable and slow; explicit opt-in only.
  echo "[e2e] TIMEWEB_E2E_PRIVATE != 1 → private-cluster bundle (19) SKIPPED"
  rm -rf "$TMP_BUNDLE/tests/19-private-cluster"
fi
if [ -z "${TWE_K8S_ADDON_TYPE:-}" ] || [ -z "${TWE_K8S_ADDON_VERSION:-}" ]; then
  # The addon catalog is per-cluster, so type/version are operator-supplied
  # (TWE_K8S_ADDON_TYPE/_VERSION). Unset → drop the addon bundle (15).
  echo "[e2e] TWE_K8S_ADDON_TYPE/_VERSION unset → k8s addon bundle (15) SKIPPED"
  rm -rf "$TMP_BUNDLE/tests/15-k8s-addon"
fi

# Start the live event watcher BEFORE kuttl so transient events (retries,
# conflicts, timeouts) are captured as they happen, even if they age out of the
# API before the post-run snapshot. Killed in the trap and after the run.
$KCTL -n "$E2E_NAMESPACE" get events -w --output-watch-events=true \
  > "$DIAG_DIR/events-stream.txt" 2>&1 &
EVENT_STREAM_PID=$!

# Early-exit on signal. kuttl runs in the BACKGROUND + `wait` so these traps can
# fire mid-run (a foreground child defers traps until it exits). All paths
# capture diagnostics first. The signal decides whether to tear down:
#   SIGHUP        → full early cleanup: delete the e2e MRs (Crossplane tears down
#                   upstream) so a killed run doesn't strand billable resources.
#                   Send it with:  kill -HUP <make/kuttl.sh pid>
#   SIGINT/SIGTERM→ stop kuttl but LEAVE the MRs for inspection
#                   (investigate-before-cleanup); operator runs `make e2e.cleanup`
#                   or sends SIGHUP when ready.
on_signal() {
  local sig="$1" do_cleanup="$2"
  echo
  echo "[e2e] received SIG$sig — stopping kuttl early"
  kill "$KUTTL_PID" 2>/dev/null
  kill "$EVENT_STREAM_PID" 2>/dev/null
  collect_diagnostics
  if [ "$do_cleanup" = 1 ]; then
    cleanup_mrs
  else
    echo "[e2e] e2e MRs left in place for inspection — send SIGHUP to delete them, or run 'make e2e.cleanup'"
  fi
  report_orphans
  echo "[e2e] diagnostics + stream saved under $DIAG_DIR"
  exit 129
}
trap 'on_signal HUP 1'  HUP
trap 'on_signal INT 0'  INT
trap 'on_signal TERM 0' TERM

echo "[e2e] running kuttl test bundle from $TMP_BUNDLE"
echo "[e2e] live event stream → $DIAG_DIR/events-stream.txt"
echo "[e2e] (SIGHUP = early cleanup + teardown; SIGINT/SIGTERM = stop + keep MRs)"
set +e
$KUTTL test --config "$TMP_BUNDLE/kuttl-test.yaml" "${KUTTL_ARGS[@]}" &
KUTTL_PID=$!
wait "$KUTTL_PID"
KUTTL_RC=$?
set -e
trap - HUP INT TERM   # normal exit: disarm the early-exit traps
kill "$EVENT_STREAM_PID" 2>/dev/null || true
collect_diagnostics
report_orphans
echo "[e2e] diagnostics + stream saved under $DIAG_DIR"
exit $KUTTL_RC
