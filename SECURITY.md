# Security Policy

## Supported versions

This is an alpha (`v0.x`) Crossplane provider. Security fixes are made against
the latest released minor version only. Older tags are not patched — upgrade to
the latest `v0.x` release.

## Reporting a vulnerability

Please report suspected vulnerabilities privately via GitHub's
[private security advisories](https://github.com/lebedevdsl/crossplane-provider-timeweb/security/advisories/new)
for this repository, or by email to the maintainer (lebedevdsl@gmail.com) with
a subject beginning `SECURITY:`.

Do **not** open a public issue for a vulnerability. Please include the affected
version, a description, and reproduction steps. You will get an acknowledgement
within a few days; fixes ship in a patch or minor release with a note in the
release notes (and a GHSA where warranted).

## Verifying a release

Releases are cosign-signed by digest (keyless, GitHub OIDC) and carry an SPDX
SBOM attestation. Verification is only meaningful with identity constraints —
`cosign verify` without them accepts any signature:

```bash
IMAGE=ghcr.io/lebedevdsl/provider-timeweb
TAG=v0.12.0

cosign verify "${IMAGE}:${TAG}" \
  --certificate-identity-regexp '^https://github.com/lebedevdsl/crossplane-provider-timeweb/\.github/workflows/release\.yaml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# SBOM attestation (same digest)
cosign verify-attestation "${IMAGE}:${TAG}" --type spdxjson \
  --certificate-identity-regexp '^https://github.com/lebedevdsl/crossplane-provider-timeweb/\.github/workflows/release\.yaml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

To enforce this cluster-side, Crossplane's `ImageConfig` can require signature
verification before a package is installed.

## Scope notes

- The provider authenticates to Timeweb Cloud with a bearer token sourced only
  from a Kubernetes `Secret` via `ProviderConfig`; tokens are never logged,
  written to status, or emitted in events.
- Scoped object-storage credentials are published only to connection Secrets;
  see `docs/s3user.md`.
