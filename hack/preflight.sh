#!/usr/bin/env bash
# Read-only post-upgrade verification. Creates nothing, costs nothing, safe
# against production (feature 025: the e2e suite is a CI gate, not this).
#
#   make preflight KUBECONTEXT=inyan-staging
set -uo pipefail

CTX="${KUBECONTEXT:-}"
K="kubectl"
[ -n "$CTX" ] && K="kubectl --context=$CTX"
fail=0
note() { printf '  %-8s %s\n' "$1" "$2"; }

echo "[preflight] context: ${CTX:-<current: $($K config current-context 2>/dev/null)>}"

echo "[preflight] provider package"
healthy=$($K get provider.pkg.crossplane.io provider-timeweb -o jsonpath='{.status.conditions[?(@.type=="Healthy")].status}' 2>/dev/null)
pkg=$($K get provider.pkg.crossplane.io provider-timeweb -o jsonpath='{.spec.package}' 2>/dev/null)
if [ "$healthy" = "True" ]; then note "OK" "healthy: $pkg"; else note "FAIL" "not healthy (got '${healthy:-<none>}')"; fail=1; fi

echo "[preflight] CRDs established"
notest=$($K get crd -o jsonpath='{range .items[?(@.spec.group)]}{.metadata.name} {.status.conditions[?(@.type=="Established")].status}{"\n"}{end}' 2>/dev/null | grep 'timeweb.crossplane.io' | grep -v ' True$' || true)
count=$($K get crd 2>/dev/null | grep -c 'timeweb.crossplane.io' || echo 0)
if [ -z "$notest" ]; then note "OK" "$count CRDs established"; else note "FAIL" "not established:"; echo "$notest" | sed 's/^/           /'; fail=1; fi

echo "[preflight] ProviderConfigs resolve their credentials"
while IFS='|' read -r ns name sns sname skey; do
  [ -z "${name:-}" ] && continue
  # Namespaced ProviderConfigs have no secretRef.namespace (it is namespace-
  # local by design) — fall back to the PC's own namespace.
  ns="${sns:-$ns}"
  if [ -z "$sname" ] || [ -z "$skey" ]; then
    note "FAIL" "${name}: incomplete credentials.secretRef"; fail=1; continue
  fi
  if $K -n "$ns" get secret "$sname" -o jsonpath="{.data.$skey}" 2>/dev/null | grep -q .; then
    note "OK" "${name}: secret ${ns}/${sname} key '${skey}' present"
  else
    note "FAIL" "${name}: secret ${ns}/${sname} key '${skey}' MISSING"; fail=1
  fi
done < <(
  $K get providerconfigs.timeweb.crossplane.io -A -o jsonpath='{range .items[*]}{.metadata.namespace}|{.metadata.name}|{.spec.credentials.secretRef.namespace}|{.spec.credentials.secretRef.name}|{.spec.credentials.secretRef.key}{"\n"}{end}' 2>/dev/null
  $K get clusterproviderconfigs.timeweb.crossplane.io -o jsonpath='{range .items[*]}|{.metadata.name}|{.spec.credentials.secretRef.namespace}|{.spec.credentials.secretRef.name}|{.spec.credentials.secretRef.key}{"\n"}{end}' 2>/dev/null
)

echo "[preflight] provider logs (last 15m)"
pod=$($K -n crossplane-system get pods -o name 2>/dev/null | grep provider-timeweb | tail -1)
if [ -z "$pod" ]; then
  note "FAIL" "no provider pod found"; fail=1
else
  errs=$($K -n crossplane-system logs "${pod#pod/}" --since=15m 2>/dev/null | grep -ciE '"level":"error"|panic' || true)
  if [ "${errs:-0}" -eq 0 ]; then note "OK" "no errors/panics"; else note "WARN" "$errs error lines — inspect: $K -n crossplane-system logs ${pod#pod/} --since=15m"; fi
fi

echo "[preflight] managed resources not Ready"
notready=$($K get managed -A --no-headers 2>/dev/null | awk '$3!="True" && NF>3 {print "           " $0}' || true)
if [ -z "$notready" ]; then note "OK" "all managed resources Ready (or none exist)"; else note "WARN" "not Ready:"; echo "$notready"; fi

echo
[ "$fail" -eq 0 ] && echo "[preflight] PASS" || echo "[preflight] FAIL — see above"
exit "$fail"
