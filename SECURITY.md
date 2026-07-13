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

## Scope notes

- The provider authenticates to Timeweb Cloud with a bearer token sourced only
  from a Kubernetes `Secret` via `ProviderConfig`; tokens are never logged,
  written to status, or emitted in events.
- Scoped object-storage credentials are published only to connection Secrets;
  see `docs/s3user.md`.
