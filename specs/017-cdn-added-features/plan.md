# Implementation Plan: CDN Added Features

**Branch**: `017-cdn-added-features` | **Date**: 2026-07-13 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/017-cdn-added-features/spec.md`

**Release target**: **v0.8.0**

## Summary

Extend the `Cdn` kind (016, v0.7.0–v0.7.2) with the remaining panel surface, all
wire-captured 2026-07-13 in-session (no separate probe phase needed): custom
delivery domains (`domains []string` ≤2; technical domain untouchable), ONE
certificate per resource (`ssl {mode: none|letsEncrypt|custom,
certificateSecretRef}` → upstream certificate objects + `security.certificate_id`
binding), secure token (`security.secureToken {secretRef, restrictByIP}` →
`security.secure_token {secret_key, restrict_by_ip} | null`), outbound traffic
limit (`trafficLimitGBPerMonth` → `traffic_limit_bytes`, GiB multiples), and
external-origin AWS auth (`origin.awsAuthSecretRef` → `origin.aws`, forbidden on
bucketRef origins). LE issuance uses a bounded spaced retry budget (≥15 min, max
4, reset via spec change or self-clearing `retry-ssl` annotation) with success
detected by certificate MATERIALIZATION — the **LE path ships UNVERIFIED**
(upstream fails issuance with a correct CNAME and no error reason; ticket filed
2026-07-13) and is flagged as such in docs + release notes per the operator's
directive. Custom-cert rotation converges by comparing the Secret's parsed PEM
(CN/SANs/notAfter) with the readback `{cn, domains, expires_at}`.

## Technical Context

**Language/Version**: Go (latest stable); Crossplane v2 namespaced MR model.

**Primary Dependencies**: existing `internal/clients/timeweb` hand-written client
(+ new `cdn_certificates.go` methods); `crypto/x509`/`encoding/pem` (stdlib) for
local certificate parsing; NO new third-party deps.

**Testing**: constitution four-case per changed method + new grammar/budget/diff
tests against the fake client; kuttl bundle 23 extended with new admission cases;
live gate on `inyan-staging` (standing install) covering custom cert (self-signed),
secure token (signed-URL 403/200 check via the documented algorithm), traffic
limit, domain aliases; LE excluded from the gate (upstream-broken, warned).

**Constraints**: Qrator discipline (poll-paced retries only, bounded LE budget);
secret hygiene extended to 3 new Secret surfaces (TLS key, signing key, AWS keys)
— never logged/evented/mirrored; certificate delete ordering guarded by upstream
409 `certificate_in_use` (transient-classified → self-healing).

**Scale/Scope**: no new kind; ~5 spec fields + ssl/status structs on
`apis/cdn/v1alpha1`; client: certificates CRUD + config field additions;
controller: cert lifecycle state machine + 3 secret resolutions + budget logic;
CRD/DeepCopy regen; docs/READMEs per release checklist.

## Constitution Check

- **§I CRD Contract Stability — PASS.** Additive fields on v1alpha1 `Cdn`;
  regen committed same PR.
- **§II Idempotent Reconciliation — PASS.** Observe read-only (existing 2 GETs +
  certificates list only when ssl owned); cert lifecycle idempotent (materialize-
  then-bind; upload keyed by parsed identity vs readback; delete only
  `managedCertificateID`); budget state in status prevents retry storms; all
  mutations verified by re-observation.
- **§III Test Discipline — PASS.** Four-case per changed method + budget/rotation/
  ownership tests; no live HTTP in unit tests.
- **Provider Constraints — PASS.** All keys from Secrets via refs with the
  never-block-deletion guard; nothing secret in spec/status/logs/Events.

## Project Structure

```text
specs/017-cdn-added-features/   plan.md, research.md, data-model.md,
                                contracts/ (cdn-v1alpha1-additions.md /
                                timeweb-cdn-certificates.md), quickstart.md, tasks.md
apis/cdn/v1alpha1/cdn_types.go  + Domains, CdnSSL, CdnSecureToken,
                                TrafficLimitGBPerMonth, AWSAuthSecretRef + status
internal/clients/timeweb/cdn.go + certificates methods, config field additions
internal/controller/cdn/        external.go (+ ssl.go for cert lifecycle),
                                external_test.go (+ ssl_test.go)
test/e2e/kuttl/tests/23-cdn/    00-admission.yaml extended
docs/cdn.md, examples/cdn.yaml, README.md, test/e2e/README.md,
docs/release-notes/v0.8.0.md   (LE-unverified warning in doc + notes)
```

**Structure Decision**: same package layout as 016; certificate lifecycle isolated
in `internal/controller/cdn/ssl.go` (state machine: desired mode × observed
{certificate_id, inventory, latest task, budget} → one action per reconcile).

## Complexity Tracking

No violations — empty.
