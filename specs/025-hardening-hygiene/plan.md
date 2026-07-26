# Implementation Plan: Hardening + hygiene (audit remediation)

**Branch**: `025-hardening-hygiene` | **Date**: 2026-07-26 | **Spec**: [spec.md](./spec.md)

Single MINOR release **v0.12.0** (clarified). Ordered so each phase is
independently revertable; refactor lands before the create-guards rollout.

## Constitution Check

I: CEL validation additions only (no fields) — CRDs regenerate same PR. PASS.
II: FR-003 (`no_paid`) and FR-002 (honest immutability) strengthen the
classification rules; the shared do/decode helper must preserve
Classify-before-Close exactly (019 correctness). PASS.
III: every behavior change gets fake-client tests; refactors keep existing
tests passing unmodified except call shape. PASS.

## Phases (commit-separable)

**P1 — correctness (US2)**
- `no_paid` → billing condition on S3Bucket / S3User / Addon; fold the four
  divergent state vocabularies into one shared classifier returning a
  category (per-kind message text stays local).
- CEL `self == oldSelf` for non-convergeable refs: Server ssh-keys/network/
  project, `KubernetesCluster.networkRef|projectRef`, `Addon.clusterRef`.
- Stubbed selectors excluded from the required exactly-one-of CEL on
  Nodepool/Addon (+ Cluster/Server selector fields documented as unimplemented).
- Docs: `docs/servers.md` false enforcement claim removed; guard-scope tags in
  `docs/conditions.md` (nodepool-only) + honest note in the new GitOps doc.

**P2 — security (US3)**
- Bind the Authorization header to the configured API host + reject
  cross-host redirects (`client.go` authTransport/CheckRedirect).
- Suppress raw-body fallback for secret-bearing writes (CDN cert upload,
  config PATCHes) + scrub PEM/`*_key` values from any surfaced message.
- Release workflow: pinned crank download (copy `ci.yaml` pattern) + sha256
  check; pin actions to SHAs; SBOM/provenance attestation; documented
  identity-constrained `cosign verify` in SECURITY.md.
- Hygiene: e2e token off argv (stdin), digest-pinned install manifests,
  delete the inert `deploy/policies/preset-readonly.yaml`.

**P3 — operator UX (US1, US4)**
- MESSAGE printcolumn on every kind (source `.conditions[Ready].message`).
- `docs/troubleshooting.md` hub + links from README and every per-kind guide;
  `docs/gitops.md` (external-name ownership, ArgoCD `ignoreDifferences`,
  create-wedge runbook) + `examples/argocd/application.yaml`.
- `docs/containerregistry.md` rewrite: scoped-token + dedicated ProviderConfig
  first, explicit exposure statement (FR-001).
- `examples/private-cluster.yaml` (from the e2e bundle); fix
  `examples/network/router.yaml` staticRoutes dangling refs.
- Add missing `Upgrading` reason row; `docs/upgrading.md` + `make preflight`.

**P4 — refactor (US5, behavior-preserving)**
- `timeweb.DoJSON[T]` / `Do` helper; migrate ~90 sites; drop the T029 comment
  class and the two reinvented `doJSON`s.
- Split `routerExternal.Update`; pacing `budget` type.
- Fetch floating-IP list once per pass (N+1 fix) + result struct; make
  `httpResp` test helper serve repeated bodies so the multi-attachment case
  is coverable.
- One `autoscalingUpToDate` predicate; route metadata PATCH through
  `patchNodeGroup`; hoist `clientLogger`; kill local ptr helpers; delete dead
  condition constants (or implement the claimed PC mapping); remove the
  unreachable router `no_paid` branch; "superseded by 022" note in 020 tasks.

## Validation

Per phase: build/vet/lint/tests. Full gate + `make generate` clean at the end.
Live: dev-tag → staging, apply the fixed examples (router + private-cluster)
and one registry-scoped-ProviderConfig smoke; verify a parked resource is
visibly distinct in `kubectl get` (SC-002). No new kuttl bundle needed —
existing bundles must pass unchanged (behavior preservation is the point).
Release v0.12.0 with the admission-rejection callout.
