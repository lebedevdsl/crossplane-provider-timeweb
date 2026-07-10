#!/usr/bin/env bash
# Build the provider image + xpkg, push to the local registry, install as
# a Crossplane Provider. Waits for the provider Pod to become Ready.
#
# Reads env from test/e2e/Makefile.test:
#   E2E_KUBECONTEXT, E2E_VERSION,
#   E2E_REGISTRY_NAME, E2E_REGISTRY_PORT, E2E_IMAGE_PUSH, E2E_IMAGE_LOCAL,
#   E2E_XPKG_PATH

set -euo pipefail

: "${E2E_KUBECONTEXT:?run via 'make e2e.deploy'}"
: "${E2E_VERSION:?}"
: "${E2E_REGISTRY_NAME:?}"
: "${E2E_REGISTRY_HOST_PORT:?}"
: "${E2E_IMAGE_PUSH:?}"
: "${E2E_IMAGE_LOCAL:?}"
: "${E2E_XPKG_PATH:?}"

for tool in docker kubectl crossplane; do
  command -v "$tool" >/dev/null || {
    echo "ERROR: required tool not found in PATH: $tool" >&2
    exit 1
  }
done

# --- 0b. Cross-compile the runtime binary for the local docker arch ----------
#
# The Dockerfile is COPY-only (bin/provider-linux-<arch>); without this step a
# stale binary from an old `make xpkg.build` run gets shipped silently (the
# feature-015 gate hit exactly that).

DOCKER_ARCH=$(docker version --format '{{.Server.Arch}}')
echo "[e2e] cross-compiling provider for linux/${DOCKER_ARCH}"
CGO_ENABLED=0 GOOS=linux GOARCH="$DOCKER_ARCH" go build \
  -ldflags="-s -w -X github.com/lebedevdsl/crossplane-provider-timeweb/internal/version.Version=$E2E_VERSION" \
  -o "bin/provider-linux-${DOCKER_ARCH}" ./cmd/provider

# --- 1. Build the runtime image ----------------------------------------------

echo "[e2e] building runtime image: $E2E_IMAGE_PUSH"
docker build \
  --build-arg VERSION="$E2E_VERSION" \
  --tag "$E2E_IMAGE_PUSH" \
  --tag "$E2E_IMAGE_LOCAL" \
  .

# --- 2. Push to the local registry -------------------------------------------

echo "[e2e] pushing runtime image to localhost:$E2E_REGISTRY_HOST_PORT"
docker push "$E2E_IMAGE_PUSH"

# --- 3. Build the xpkg with runtime embedded ---------------------------------

echo "[e2e] building xpkg with embedded runtime image"
mkdir -p "$(dirname "$E2E_XPKG_PATH")"
# Examples are NOT embedded: xpkg's example parser is stricter than
# `crossplane beta validate` and rejects the leading comment-doc headers the
# examples/ files carry (e.g. examples/providerconfig.yaml). This mirrors the
# Makefile `xpkg.build` target, which embeds an empty examples dir for the same
# reason. The e2e kuttl bundle applies its own manifests, so embedded examples
# are unnecessary here.
_empty_examples="$(mktemp -d)"
trap 'rm -rf "$_empty_examples"' EXIT
crossplane xpkg build \
  --package-root=package \
  --examples-root="$_empty_examples" \
  --embed-runtime-image="$E2E_IMAGE_PUSH" \
  --package-file="$E2E_XPKG_PATH"

# --- 4. Push the xpkg to the local registry ----------------------------------

XPKG_IMAGE_PUSH="localhost:${E2E_REGISTRY_HOST_PORT}/provider-timeweb-xpkg:${E2E_VERSION}"
XPKG_IMAGE_LOCAL="${E2E_IMAGE_LOCAL%/*}/provider-timeweb-xpkg:${E2E_VERSION}"
echo "[e2e] pushing xpkg (host URL): $XPKG_IMAGE_PUSH"
echo "[e2e] cluster will pull from:  $XPKG_IMAGE_LOCAL"
crossplane xpkg push -f "$E2E_XPKG_PATH" "$XPKG_IMAGE_PUSH"

# --- 5. Install the Provider in Crossplane -----------------------------------

echo "[e2e] applying pkg.crossplane.io/v1 Provider"
cat <<EOF | kubectl --context="$E2E_KUBECONTEXT" apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  package: ${XPKG_IMAGE_LOCAL}
  packagePullPolicy: Always
  revisionActivationPolicy: Automatic
  revisionHistoryLimit: 1
EOF

echo "[e2e] waiting for Provider to be Healthy (≤ 5 min)"
kubectl --context="$E2E_KUBECONTEXT" wait \
  --for=condition=Healthy provider.pkg.crossplane.io/provider-timeweb \
  --timeout=5m

echo "[e2e] waiting for provider Pod to be Ready"
kubectl --context="$E2E_KUBECONTEXT" -n crossplane-system wait \
  --for=condition=Ready pods -l pkg.crossplane.io/provider=provider-timeweb \
  --timeout=2m

echo
echo "[e2e] deploy: OK"
echo "[e2e] kubectl --context=$E2E_KUBECONTEXT -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb -f"
