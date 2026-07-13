# Feature Specification: CDN Added Features

**Feature Branch**: `017-cdn-added-features`

**Created**: 2026-07-13

**Status**: Draft

**Input**: User description: "CDN-added-features see the preface for cdn resource, which should cover missing features"

Source backlog: `specs/_next-cdn-followups.preface.md` (wire shapes panel-captured
2026-07-13; sections 2a/2b/2c + external AWS auth). Extends the `Cdn` kind shipped in
016 (v0.7.0–v0.7.2).

## Clarifications

### Session 2026-07-13

- Q: How much SSL management goes into 017? → A: **Let's Encrypt AND custom
  certificates**. Model corrected 2026-07-13 (user + 016 research): LE is
  RESOURCE-level — one certificate issued for ALL delivery domains connected at
  issuance time (upstream docs: "на все домены раздачи… на момент выпуска SSL");
  custom certificates are uploaded as ONE PEM chain + private key. Panel-verified
  2026-07-13: a CDN resource holds EXACTLY ONE certificate (LE-issued or one
  custom upload) — no per-domain binding. Spec surface: `domains` is a plain
  string list; one resource-level `ssl {mode: letsEncrypt|custom,
  certificateSecretRef}` block; status mirrors the single certificate.
- Q: AWS auth for EXTERNAL private S3-compatible origins? → A: **Include** —
  `origin.awsAuthSecretRef` (access/secret keys from a Secret) for `domain`/`ip`
  origins; in-account `bucketRef` origins keep the upstream auto-wire (never sent by
  the controller).

- Q: LE issuance blocked by missing CNAME — controller behavior? → A:
  **Attempt-and-classify with a BOUNDED retry budget** (amended 2026-07-13
  after live evidence of tasks piling up): issuance attempts are spaced ≥15
  minutes apart and capped at 4 per budget window (~LE's own failed-validation
  rate limit); `422 cert_issue_incorrect_dns` and async task failures map to a
  clear SSL-pending/failed state while the resource stays Ready; after the cap,
  the state becomes exhausted-until-reset. The budget RESETS on a relevant spec
  change (domains / ssl block edit) or an explicit
  `cdn.timeweb.crossplane.io/retry-ssl` annotation (purge-style, self-clearing).
  Attempt count + last-attempt time live in status. No controller-side DNS
  lookups.

- Q: `ssl` block removal semantics — what happens to a bound certificate? → A:
  **Three-state, delete-if-ours** (confirmed 2026-07-13): block ABSENT = slot
  unowned (never write `certificate_id`, mirror only); `mode: none` = unbind
  (`certificate_id: null`) and DELETE the certificate object only if the
  provider created it (tracked via `status.atProvider.managedCertificateID`);
  panel-created certificates are unbound but never destroyed. Custom-cert
  rotation is detected by parsing the Secret's PEM locally (CN/SANs/notAfter)
  against the readback `{cn, domains, expires_at}` — upload new → rebind →
  delete old managed cert (409 `certificate_in_use` enforces the order and
  self-heals via transient retry). `Cdn` deletion best-effort unbinds+deletes
  the managed certificate first.

### Wire facts carried from the preface (all panel-captured request bodies)

- Custom domains: ≤2 + immutable technical domain; `config.domains.aliases`
  (read includes the technical domain, panel write showed `[]` — asymmetry to resolve
  at plan probe). CNAME → technical domain is operator-side DNS (future DNS kind
  consumes the v0.7.2 connection secret).
- Secure token: enable `config.security.secure_token = {secret_key, restrict_by_ip}`;
  disable = explicit `null`; the panel replaces the security SECTION wholesale
  (`redirect` included). Readback echo of `secret_key` unknown (open).
- Traffic limit: top-level `{traffic_limit_bytes: N}`; panel "ГБ/мес" is GiB
  (3000 → 3221225472000); exceeding SUSPENDS the resource with up to 2 h delay during
  which traffic still bills.
- Signing algorithm (for docs/e2e verification): `urlsafe_b64(md5(secret+path+ip+
  expires))`, `=` stripped, `+`→`-`, `/`→`_`; URL `https://<domain>/md5(<token>,
  <expires>)/<path>`; ip/expires omitted when their checks are off.
- SSL certificates wire (panel-captured 2026-07-13): issue =
  `POST /api/v1/cdn/certificates/issue` → on missing/incorrect CNAME fails
  cleanly with `422 {"error_code": "cert_issue_incorrect_dns", "message": "DNS
  settings are incorrect"}`; inventory = `GET /api/v1/cdn/certificates
  ?resource_id=<id>` → `{certificates: []}`; issuance is ASYNC and trackable via
  `GET /api/v1/cdn/certificates/tasks?resource_id=<id>` → `{certificate_tasks:
  []}`. Custom upload (captured): `POST /api/v1/cdn/certificates` body
  `{certificate: "<PEM chain>", private_key: "<PEM key>"}` → 204; the platform
  PARSES the certificate — inventory entries carry `{id, type: "uploaded", cn,
  domains: [<SANs>], issued_at, expires_at}` (⇒ rotation/expiry detectable from
  the readback). Binding (captured): resource PATCH
  `{config: {security: {certificate_id: <id>}}}` — the single-cert slot IS
  `security.certificate_id` (null = none) and it IS echoed by the regular
  configuration read (captured: `security: {redirect: false, certificate_id: 1,
  secure_token: null}`) — binding diffs need no extra inventory call. Security-section writes are per-key
  PARTIAL (this PATCH sent only certificate_id; the secure-token save sent
  redirect+secure_token) — send owned keys only. Still unprobed: LE issue
  request body, LE task/`type` values, certificate DELETE, unbind
  (certificate_id: null assumed).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Custom delivery domains with SSL (Priority: P1)

The operator declares up to two custom subdomains on a `Cdn` (e.g.
`cdn.example.com`), sets the CNAME at their DNS provider to the technical domain,
and enables SSL with the resource's single certificate: Let's Encrypt (covers all
connected domains at issuance) or one custom certificate from a TLS Secret. The
provider keeps the alias set matched to the declaration (never touching the
technical domain) and mirrors the certificate state in status.

**Why this priority**: Serving from branded domains is the main reason the panel's
domain feature exists; SSL is inseparable for production HTTPS.

**Independent Test**: Declare one custom domain with `ssl: letsEncrypt` on a live
`Cdn`, set the CNAME, verify the alias appears upstream, the certificate is issued,
and HTTPS on the custom domain serves origin content.

**Acceptance Scenarios**:

1. **Given** a Ready `Cdn` and a declared custom domain, **When** reconciled,
   **Then** the upstream alias set equals {technical domain + declared domains} and
   status mirrors each domain.
2. **Given** `ssl.mode: letsEncrypt` and live CNAMEs, **When** reconciled, **Then**
   one issuance is requested covering the connected domains, tracked via the
   upstream certificate task, and the certificate appears in the status mirror.
2b. **Given** `ssl.mode: letsEncrypt` with a missing CNAME, **When** reconciled,
   **Then** the upstream 422 `cert_issue_incorrect_dns` maps to a clear SSL-pending
   state (resource stays Ready), issuance is re-attempted once per reconcile, and
   it converges without operator interaction once DNS is fixed.
3. **Given** `ssl.mode: custom` with `certificateSecretRef` (`kubernetes.io/tls`;
   `tls.crt` = full chain domain→intermediates→root, `tls.key`), **When**
   reconciled, **Then** the certificate is uploaded as the resource's single
   certificate; a missing/invalid Secret surfaces a clear condition.
3b. **Given** a domain ADDED after an LE certificate was issued, **When** reconciled,
   **Then** the provider surfaces that the existing certificate does not cover the
   new domain (re-issuance semantics resolved at plan probe). For the CUSTOM
   certificate no coverage check is made — upstream accepts any cert, and
   coverage is the operator's responsibility.
4. **Given** three custom domains declared, **When** applied, **Then** admission
   rejects it (max 2).
5. **Given** a custom domain removed from the manifest, **When** reconciled,
   **Then** the alias is detached upstream; the technical domain is never removed.

---

### User Story 2 - Signed URLs (secure token) (Priority: P2)

The operator enables signed-URL protection by referencing a Secret holding the
signing key and optionally binding signatures to client IPs. Unsigned requests get
403; the app signs URLs with the documented algorithm using the same Secret.

**Why this priority**: Content protection is the headline security feature; the
whole flow (key in a Secret, app signs, CDN verifies) is cloud-native only if the
provider manages it.

**Independent Test**: Enable secure token on a live `Cdn`; verify an unsigned fetch
returns 403 and a correctly signed fetch returns the content.

**Acceptance Scenarios**:

1. **Given** `secureToken.secretRef` naming a Secret with the signing key, **When**
   reconciled, **Then** upstream secure token is enabled with that key and
   `restrictByIP` as declared.
2. **Given** secure token enabled, **When** the block is removed from the manifest,
   **Then** the upstream feature is disabled (explicit null) and unsigned access
   works again.
3. **Given** a missing Secret or key, **When** reconciled, **Then** a clear
   condition explains it; deletion of the `Cdn` is never blocked by the missing
   Secret.
4. **Given** the Secret's key value is rotated, **When** the platform exposes the
   current value for comparison, **Then** the new key converges within a poll
   interval; otherwise the documented limitation applies (see Edge Cases).

---

### User Story 3 - Outbound traffic limit (Priority: P3)

The operator caps a resource's monthly outbound traffic. On exceeding the cap the
platform suspends the resource; the provider surfaces that as the existing
Suspended readiness state, and raising/clearing the limit resumes delivery.

**Why this priority**: Cost-control feature; small surface, high operational value.

**Acceptance Scenarios**:

1. **Given** `trafficLimitGBPerMonth: 3000`, **When** reconciled, **Then** upstream
   carries the equivalent byte limit and status mirrors it.
2. **Given** the limit removed, **When** reconciled, **Then** the upstream limit is
   cleared (null).
3. **Given** a suspended-over-limit resource, **When** observed, **Then**
   Ready=False reason=Suspended (already shipped) with the limit visible in status.

---

### User Story 4 - External private S3 origins (Priority: P4)

The operator fronts an external (non-Timeweb) private S3-compatible bucket by
declaring a domain origin plus `awsAuthSecretRef`; the CDN authenticates to the
origin with those keys.

**Acceptance Scenarios**:

1. **Given** a domain origin with `awsAuthSecretRef`, **When** reconciled, **Then**
   upstream AWS auth carries the referenced keys; the keys never appear in spec,
   status, Events, or logs.
2. **Given** `awsAuthSecretRef` on a `bucketRef` origin, **When** applied, **Then**
   admission rejects it (in-account buckets auto-wire upstream).
3. **Given** the block removed, **When** reconciled, **Then** upstream AWS auth is
   disabled.

---

### Edge Cases

- Aliases write asymmetry (R-9): the plan-phase probe must establish whether the
  write includes the technical domain; the controller must never emit a write that
  could detach it.
- LE issuance timing: certificates can take up to ~20 min and require the CNAME
  resolving first; per-domain pending state must not flip the whole resource
  Unready or trigger write loops.
- Secure-token readback: if the configuration GET masks `secret_key`, a rotated
  Secret value alone produces no observable diff — the documented recovery is
  toggling `restrictByIP` or re-applying the block (recorded limitation), unless the
  readback echoes (then full diff).
- Secret-bearing surfaces grow: LE/custom certs (private key), secure token key,
  external AWS keys — none may be logged, evented, or mirrored; configuration reads
  remain secret-bearing.
- Ref-gates (secrets) must never block deletion (existing project rule).
- Certificates endpoint wire is unprobed: plan phase needs panel captures for LE
  issue, custom upload, and domain binding before implementation starts.
- Suspension after limit takes up to 2 h and bills during the window — docs must
  state that raising the limit is not instantaneous relief.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `Cdn` MUST accept `domains[]` (max 2) of custom delivery subdomains;
  the alias set converges single-writer; the technical domain is never modified or
  removed by any write.
- **FR-002**: SSL surface: ONE certificate per resource — `ssl.mode:
  none|letsEncrypt|custom` (block ABSENT = unowned/panel-managed; `none` =
  unbind + delete-if-ours per Clarifications); `custom` requires
  `certificateSecretRef` to a `kubernetes.io/tls` Secret (CEL; `tls.crt` = full
  PEM chain, `tls.key` = private key); the certificate state is mirrored in
  status. The upstream accepts certificates WITHOUT validating domain coverage —
  the provider likewise uploads as declared and performs NO SAN/coverage
  validation; coverage is the operator's responsibility (documented).
- **FR-003**: Let's Encrypt issuance MUST be requested only when declared and
  only within the retry budget: attempts spaced ≥15 min, max 4 per window,
  tracked in status (`ssl.issueAttempts`, `ssl.lastIssueAttemptAt`); `422
  cert_issue_incorrect_dns` and `failed` tasks map to SSL-pending/failed states
  + Events while the resource stays Ready; after the cap the state is
  `exhausted` until the budget resets (domains/ssl spec change or the
  self-clearing `cdn.timeweb.crossplane.io/retry-ssl` annotation). Progress is
  observed via `certificate_tasks` (latest task only); success is detected by
  certificate materialization. Domain-set changes after issuance MUST surface a
  coverage gap (re-issue behavior fixed at plan per probe findings).
- **FR-003b**: Custom-certificate rotation MUST converge declaratively: the
  controller compares the referenced Secret's parsed certificate (CN/SANs/
  notAfter) with the upstream readback and performs upload → rebind → delete-old
  (managed certificates only), tolerating the 409 ordering guard.
- **FR-004**: `security.secureToken` MUST take the signing key from
  `secretRef` (never inline), support `restrictByIP`, disable via block removal
  (explicit null upstream), and keep the key out of status/Events/logs.
- **FR-005**: Security-section writes send owned keys only (per-key partial
  PATCHes are wire-verified: certificate_id alone, and redirect+secure_token
  together); disabling secure token uses explicit `secure_token: null`.
- **FR-006**: `trafficLimitGBPerMonth` MUST map to upstream bytes as GiB
  multiples, clear via removal (null), and mirror in status; the existing
  Suspended mapping covers the exceeded state.
- **FR-007**: Domain/IP origins MUST accept `awsAuthSecretRef` (keys `access_key`,
  `secret_key`); the controller MUST reject it on `bucketRef` origins at admission
  and MUST NOT touch upstream AWS auth for bucketRef origins (auto-wired).
- **FR-008**: All new Secret references MUST be resolved with the
  never-block-deletion guard and produce clear conditions when missing/invalid.
- **FR-009**: All new fields follow the established ownership model: declared ⇒
  owned + drift-reverted; omitted ⇒ untouched and mirrored read-only.
- **FR-010**: Constitution test discipline (four-case per changed method + new
  admission cases in kuttl bundle 23) and the release checklist (READMEs, kind doc,
  example, release notes in house style) apply.

### Key Entities

- **Cdn (extended)**: + `domains []string` (≤2), resource-level
  `ssl {mode, certificateSecretRef?}`,
  `security.secureToken {secretRef, restrictByIP}`, `trafficLimitGBPerMonth`,
  `origin.awsAuthSecretRef`; status gains per-domain mirrors (name, sslStatus) and
  the traffic limit and the certificate inventory mirror.
- **Upstream certificate**: standalone object (`{id, type, cn, domains,
  issued_at, expires_at}`) created by LE issuance or PEM upload; a CDN resource
  holds AT MOST ONE via `security.certificate_id`; the platform parses CN/SANs
  and expiry (mirrored in status; enables declarative rotation detection).
- **Referenced Secrets**: TLS Secret (custom cert), signing-key Secret (secure
  token), AWS key Secret (external origins) — consumed, never created or mirrored.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A declared custom domain with a live CNAME and `ssl.letsEncrypt`
  serves origin content over HTTPS with a valid certificate within 30 minutes of
  apply, no panel interaction.
- **SC-002**: With secure token enabled, an unsigned fetch returns 403 and a
  correctly signed fetch returns 200 (live gate).
- **SC-003**: Alias, secure-token, traffic-limit, and AWS-auth drift introduced in
  the panel is reverted within 2 reconcile cycles; the technical domain survives
  every convergence write.
- **SC-004**: Setting/clearing the traffic limit is reflected upstream within 1
  reconcile; a limit-suspended resource reports Ready=False reason=Suspended.
- **SC-005**: Admission rejects: >2 domains, custom SSL without a TLS secretRef,
  awsAuthSecretRef on bucketRef origins (kuttl bundle 23 extended).
- **SC-006**: No signing key, private key, or AWS key ever appears in status,
  Events, or controller logs (reviewed at live gate).

## Assumptions

- Certificates endpoint wire (LE issue, custom upload, domain binding, sslStatus
  values) is captured at plan phase via panel devtools (established workflow);
  captured shapes in the preface are authoritative for domains/token/limit.
- The aliases write asymmetry is resolved by one domains-drawer save capture; until
  then the controller design assumes writes must include the technical domain if and
  only if the probe shows the panel doing so.
- CNAME management stays operator-side in 017; the future DNS kind (see preface)
  will consume the v0.7.2 connection secret.
- Existing conventions carry over: single-writer ownership, poll-paced retries
  (Qrator discipline), secret-hygiene rules, e2e on `inyan-staging` with the
  standing Crossplane install, release flow per the repo git conventions.
