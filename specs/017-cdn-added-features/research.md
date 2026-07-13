# Research: CDN Added Features (017)

All wire evidence was captured IN-SESSION during specify/clarify (2026-07-13,
panel devtools against live resource 22209) — spec.md "Wire facts" is the
authoritative inventory. This file records decisions only.

## R-1 — Certificates client surface

**Decision**: extend `internal/clients/timeweb/cdn.go` (same doV2 pattern):
`ListCDNCertificates(resourceID)`, `ListCDNCertificateTasks(resourceID)`,
`UploadCDNCertificate(certPEM, keyPEM)` (204, no id returned — the created id is
discovered from the inventory delta/parsed identity), `IssueCDNCertificate({resource_id})` (payload captured),
`DeleteCDNCertificate(id)`. Binding/unbinding rides the existing resource PATCH
(`config.security.certificate_id`).

## R-2 — Certificate lifecycle state machine (ssl.go)

**Decision**: one action per reconcile, derived from (mode, bound certificate_id,
inventory entry, latest task, budget):
- custom: Secret PEM parsed (x509) → identity {CN, SANs set, NotAfter};
  no matching inventory entry → upload; entry exists but unbound → bind;
  bound-but-identity-mismatch (rotation) → upload new → bind → delete old managed;
  managed old cert unbound → delete (409 ⇒ transient retry).
- letsEncrypt: cert materialized → bind if unbound; else if latest task
  in_progress → wait; else if budget allows → issue (202/422-classify) +
  record attempt; else → exhausted state.
- none: unbind if bound; delete bound-or-orphaned cert IFF id ==
  status.managedCertificateID.
- ssl absent: no action, mirror only.
Success detection = MATERIALIZATION (inventory/bound id), never a task-state
string (unobserved). Latest task = max(id). Failed tasks carry no reason (quirk,
ticket filed) — Events say so.

## R-3 — LE retry budget

**Decision**: `status.atProvider.ssl {state, issueAttempts, lastIssueAttemptAt,
budgetKey}`; attempt allowed iff attempts < 4 AND now-lastAttempt ≥ 15 min;
budgetKey = hash(domains + ssl block) — spec change rotates the key and resets
the budget; the self-clearing `cdn.timeweb.crossplane.io/retry-ssl` annotation
resets it explicitly (purge-annotation idiom: act, then remove via merge patch).

## R-4 — Secret resolutions

**Decision**: three resolvers in the external (kube Get, never blocking deletion):
TLS Secret (`tls.crt`/`tls.key`, kubernetes.io/tls), signing-key Secret
(configurable key, default `secret`), AWS keys Secret (`access_key`/`secret_key`).
Values held only in-reconcile memory; never in status/Events/logs. Secure-token
diff: presence + restrict_by_ip (readback echo of secret_key unknown → key is
write-through; rotation limitation documented; revisit if readback echoes).

## R-5 — Traffic limit & domains

**Decision**: `trafficLimitGBPerMonth` × 2^30 → `traffic_limit_bytes` (top-level
PATCH; readback on the resource GET — captured mirrored value). Domains: desired
aliases = {technical domain} ∪ declared (the readback always includes the tech
domain; writing the full set including tech is assumed safe — the panel's
`aliases: []` write predates customs; FIRST live-gate step verifies the write
form before anything else and adjusts to customs-only if the full-set write
errors or detaches).

## R-6 — LE automation VERIFIED end-to-end (2026-07-13 live gate)

LE issuance failed twice reasonlessly (tasks 6669/6671 — DNS propagation to
LE's resolvers, most likely), then SUCCEEDED. The PROVIDER's full automation was then verified end-to-end
on the live gate (gate.inyan.pro): the controller attached the domain,
issued once (att=1, task in_progress), the certificate materialized
(`type: lets_encrypt`), the platform auto-bound it, and the controller
settled to `ssl.state: bound` with `managedCertificate.id` self-adopted.
Docs/notes now state LE works, with the transient-DNS-propagation caveat
absorbed by the bounded retry budget. Certificate ids are per-resource/reused
(LE cert took the deleted upload's id 1) — managedCertificateID must be
cross-checked against identity (type/cn), not trusted as globally unique.
Live gate still excludes LE issuance (burns real LE quota on test domains).

## R-9 — v0.8.1 post-release bugfixes (first real-resource apply)

Two settings-WRITE shape bugs the gate under-sampled (both terminal 400s):
- `config.delivery.packaging.mp4` must be an OBJECT or null, NEVER a bool.
  The differ always emitted `mp4: <bool>` when a `performance` block was
  declared. Fix: `MP4 json.RawMessage`; packaging sent only when video state
  changes (enable → `{}`, disable → null), omitted otherwise.
- `config.domains.aliases` must contain ≤2 entries and must NOT include the
  technical domain — upstream manages it and counts it in the limit. The
  differ wrote `{technical} ∪ declared` (2 customs + technical = 3 → reject).
  Fix: write only the declared customs; diff against observed-minus-technical.
Escape analysis: the v0.8.0 gate had NO `performance` block (delivery differ
never ran) and only ONE custom domain (1 + technical = 2, at the limit) —
each bug sits exactly one notch outside the gate's field combination. Lesson:
sample the mundane settings-write matrix, not only the hard async paths.

## R-8 — live-gate findings (2026-07-13, relaxed gate on inyan-staging)

Verified live under the dev build: traffic limit (100 GiB → 107374182400),
forceHTTPS redirect, secure token (key from Secret, restrict_by_ip), cache TTL.
New wire facts, all folded into code + contract:
- Certificate UPLOAD body must be EXACTLY `{certificate, private_key}` —
  adding `resource_id` → 400 "property resource_id should not exist".
- Uploaded chain must terminate in a SYSTEM-TRUSTED root — self-signed →
  422 `cert_add_root_not_trusted`. The controller surfaces this
  (`SSLUploadFailed` Event, `ssl.state: failed`) and budget-paces uploads
  (share the LE budget) so a permanently-rejected cert can't starve the
  settings PATCH.
- `secure_token` readback DOES echo `secret_key` in cleartext (like
  `origin.aws`) ⇒ key rotation is fully diffable (better than the documented
  presence-only fallback); the diff compares the key when present. Secret-
  hygiene: still never logged/mirrored.
- LE issuance operates on the resource's delivery domains ⇒ declared custom
  domains must be ATTACHED (aliases) BEFORE issuing; the controller defers LE
  until domainsAttached, letting the settings PATCH add them first.

## R-7 — e2e

Bundle 23 admission additions: >2 domains; `ssl.mode: custom` without
certificateSecretRef; awsAuthSecretRef with bucketRef origin. Live gate
(inyan-staging, dev tag): domains attach/detach; self-signed custom cert
upload+bind+rotate+`mode: none` cleanup; secure token enable + signed-URL
403/200 check (algorithm from panel docs); traffic limit set/clear. LE: absent.
