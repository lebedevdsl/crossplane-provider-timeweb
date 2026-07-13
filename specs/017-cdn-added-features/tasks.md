# Tasks: CDN Added Features

**Input**: specs/017-cdn-added-features/ (plan, spec, research, data-model, contracts)
**Tests**: unit mandatory (constitution §III); kuttl 23 extended; live gate minus LE.
**Release**: v0.8.0. LE-unverified warning required in docs + notes.

## Phase 1: Foundational

- [X] T001 Extend `apis/cdn/v1alpha1/cdn_types.go`: `Domains []string` (≤2,
      pattern, set), `SSL *CdnSSL{Mode enum none;letsEncrypt;custom,
      CertificateSecretRef *CdnSecretRef}` + CEL (secretRef iff custom),
      `CdnSecurity.SecureToken *CdnSecureToken{SecretRef{Name,Key?},
      RestrictByIP}`, `TrafficLimitGBPerMonth *int64 (≥1)`,
      `CdnOrigin.AWSAuthSecretRef *CdnSecretRef` + CEL (forbidden w/ bucketRef),
      status: `Certificate *CdnCertificateStatus`, `ManagedCertificateID`,
      `SSL *CdnSSLStatus{State, IssueAttempts, LastIssueAttemptAt, BudgetKey}`,
      `TrafficLimitBytes` — per data-model.md
- [X] T002 `make generate`; verify CEL via validate-examples + server dry-run
- [X] T003 Client `internal/clients/timeweb/cdn.go`: `ListCDNCertificates`,
      `ListCDNCertificateTasks`, `UploadCDNCertificate`, `IssueCDNCertificate`,
      `DeleteCDNCertificate`; config additions: `CDNConfigSecurity.SecureToken
      *CDNSecureToken{SecretKey, RestrictByIP}` (explicit-null capable),
      `CDNConfigOrigin` aws write support, resource write `TrafficLimitBytes`
      (null-capable) — per contracts/timeweb-cdn-certificates.md

## Phase 2: Controller

- [X] T004 `internal/controller/cdn/ssl.go`: certificate lifecycle state machine
      (research R-2) + LE retry budget (R-3) + retry-ssl annotation reset +
      x509 parse helpers; success = materialization; Events per contract
- [X] T005 `external.go`: domains diff ({tech}∪declared vs aliases readback),
      secure-token resolve+diff (presence+restrictByIP, key write-through),
      traffic-limit diff (GiB→bytes, null clear), awsAuthSecretRef resolve+write
      (never for bucketRef), status mirrors incl. certificate + ssl + limit
- [X] T006 Unit tests: four-case per changed path + budget table (spacing/cap/
      reset via budgetKey + annotation) + rotation (parse-vs-readback) +
      delete-if-ours ownership + 409 ordering + domains never-detach-tech +
      secret-resolution failures (clear conditions, deletion never blocked)

## Phase 3: e2e + docs + release

- [X] T007 kuttl 23 admission additions: >2 domains; custom w/o secretRef;
      secretRef w/ mode letsEncrypt; awsAuthSecretRef+bucketRef; limit 0
- [X] T008 Docs: `docs/cdn.md` (domains/ssl/secure-token/limit/external-aws
      sections, **⚠ LE-unverified warning**, signed-URL algorithm kept),
      `examples/cdn.yaml`, README kinds row update, e2e README bundle row update
- [X] T009 `make reviewable`-level checks (lint, tests, validate-examples)
- [X] T010 Live gate on inyan-staging — ALL paths verified end-to-end 2026-07-13: token (key echoed → full rotation), traffic limit (100 GiB), forceHTTPS, cache, custom-cert upload (untrusted-root rejection + budget pacing), Let's Encrypt (attach→issue→materialize→auto-bind→managed), mode:none cleanup (unbind→delete-if-ours, 409-ordered). Findings folded into R-8.
      cert self-signed upload→bind→rotate→`mode: none` cleanup; secure token +
      signed-URL 403/200; traffic limit set/clear (null verify); aliases write
      form verified FIRST (R-5); NO LE issuance. Findings → research.md
- [X] T011 Release v0.8.0: notes (house style, ⚠ LE warning), commit/push/tag
      per repo git conventions

## Dependencies
T001→T002→{T004,T005}; T003∥T001; T006 after T004/T005; T007/T008 anytime after
T002; T010 after T009; T011 after T010.
