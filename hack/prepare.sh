#!/usr/bin/env bash
# One-time bootstrap for a fresh checkout of crossplane-provider-timeweb.
#
# Usage: ./hack/prepare.sh
#
# Idempotent: safe to run multiple times.

set -euo pipefail

cd "$(dirname "$0")/.."

echo "[prepare] Downloading Go module dependencies..."
go mod download

echo "[prepare] Running code generation..."
make generate

echo "[prepare] Tidying go.mod..."
go mod tidy

echo
echo "[prepare] Done. Next steps:"
echo "  - make build         # build the provider binary"
echo "  - make test          # run unit tests with race detector"
echo "  - make reviewable    # run the merge gate (lint + generate-clean + test)"
echo
