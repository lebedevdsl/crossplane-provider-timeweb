#!/usr/bin/env bash
# Publish (or update) the GitHub release for a tag:
#   1. generate install.yaml — the Provider manifest pinned to this tag,
#   2. build the release body = hand-written notes (docs/release-notes/<tag>.md,
#      if present) + a standard "## Install" footer,
#   3. create-or-update the release, attaching install.yaml as an asset.
#
# Called by .github/workflows/release.yaml on every tag push, so every release
# ends with the same install example + a one-command install.yaml. Also runnable
# locally (needs `gh` authenticated): hack/publish-release.sh v0.4.1
set -euo pipefail

TAG="${1:-${TAG:-}}"
REPO="lebedevdsl/crossplane-provider-timeweb"
IMAGE="ghcr.io/lebedevdsl/provider-timeweb"
[ -n "$TAG" ] || { echo "usage: $0 <tag>" >&2; exit 1; }

# 1. install.yaml — one-apply Provider install, package pinned to the tag.
cat > install.yaml <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  package: ${IMAGE}:${TAG}
EOF

# 2. Release body = notes file (if any) + standard Install footer.
BODY="$(mktemp)"
NOTES="docs/release-notes/${TAG}.md"
[ -f "$NOTES" ] && cat "$NOTES" >> "$BODY"
cat >> "$BODY" <<EOF

## Install

The controller runtime is embedded in the package, so one apply installs the provider:

\`\`\`bash
kubectl apply -f https://github.com/${REPO}/releases/download/${TAG}/install.yaml
\`\`\`

Then point it at your Timeweb token with a \`ProviderConfig\` + \`Secret\` —
see the [README](https://github.com/${REPO}#providerconfig).
EOF

# 3. Create or update the release, attaching install.yaml.
if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
  gh release edit "$TAG" --repo "$REPO" --notes-file "$BODY" --latest
  gh release upload "$TAG" install.yaml --repo "$REPO" --clobber
else
  gh release create "$TAG" install.yaml --repo "$REPO" --title "$TAG" --notes-file "$BODY" --latest --verify-tag
fi

echo "published release $TAG with install.yaml"
