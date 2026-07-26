# Feature Specification: Hardening + hygiene (audit remediation)

**Feature Branch**: `025-hardening-hygiene`

**Created**: 2026-07-26

**Status**: Draft

**Input**: Owner request after a three-way audit (Go code quality, security/CISO, operator usability) of the v0.9.2→v0.11.2 release burst: "turn this into a spec — hardening + hygiene".

**Source**: the three audit reports (2026-07-26). Every item below is a report finding with a file:line origin; nothing here is speculative.

## Framing

The provider is structurally sound — build/vet/lint/15 test packages all green, credential hygiene above par for a v0.x provider, and the Observe-as-sole-authority spine consistently applied. What the fast cadence left is: one credential-exposure design flaw, a small set of states the provider handles wrongly or describes falsely, an operator-facing routing problem (right content, unreachable), and boilerplate that is about to be multiplied by a scheduled rollout. This feature clears that debt in one pass, ordered so the highest-blast-radius items ship first.

## Clarifications

### Session 2026-07-26

- Q: Release shape now that no new spec field exists — one release or a patch/minor split? → A: **One release, MINOR (v0.12.0)**. The CEL immutability additions change the admission contract (manifests that apply cleanly today start being rejected), which is consumer-visible and must not hide in a patch; shipping everything together also avoids a window where docs describe guards that aren't released yet. Release note carries an explicit callout for the new admission rejections.
- Q: How should the ContainerRegistry credential exposure (audit H-1) be resolved — an opt-in spec field gating publication? → A: **No opt-in field.** The published password stays as-is; the remedy is OPERATIONAL and documented: the operator creates a **scoped API token via the account admin panel — scoped to the registry within that project — and uses it in the ProviderConfig that serves the ContainerRegistry resources** (a separate, narrowly-scoped ProviderConfig), so the Secret can only ever carry a registry-scoped credential rather than an account-wide one. No CRD change, no behavior change, no migration burden.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The registry Secret's blast radius is an operator decision, and the docs make it obvious (Priority: P1)

An operator wires `imagePullSecrets` to a ContainerRegistry's connection Secret. The Secret's password is whatever token the serving ProviderConfig holds — so a broad token means broad exposure to every node scheduling the pod and everyone with namespace-level Secret read. The provider does not gate this with a field; instead the documentation makes the correct setup the obvious one: create a registry-scoped token in the account admin panel, put it in a dedicated ProviderConfig, and point ContainerRegistry resources at that ProviderConfig. The docs state plainly what the Secret contains, who can read it, and what the token can do if it is the account-wide one.

**Why this priority**: highest real-world blast radius in the codebase, and the fix costs nothing in API surface — the exposure is a token-scoping decision the operator owns, provided the provider tells them clearly and early.

**Independent Test**: a reader following `docs/containerregistry.md` from the top reaches the scoped-token + dedicated-ProviderConfig setup BEFORE the connection-Secret reference table, and can copy a working example.

**Acceptance Scenarios**:

1. **Given** the shipped docs, **When** an operator sets up a ContainerRegistry, **Then** the recommended path (registry-scoped token created in the account admin panel + dedicated ProviderConfig) appears before any instruction that wires the Secret into workloads.
2. **Given** the docs, **When** the operator reads the connection-Secret section, **Then** it states exactly what the password is, who can read the Secret, and the consequence of using an account-wide token.
3. **Given** an operator following the recommended path, **When** they apply it, **Then** no provider change is required — the same manifests work with a scoped ProviderConfig.

---

### User Story 2 - States the provider currently gets wrong or lies about (Priority: P1)

Four defects where the provider's report and reality diverge — the class that turns into incidents because dashboards stay green:

- **Silently-ignored "immutable" edits**: docs claim editing Server SSH-key/network/project refs (and `KubernetesCluster.networkRef`/`projectRef`, `KubernetesClusterAddon.clusterRef`) surfaces an immutability rejection. No code path does; the edit is accepted, ignored, and reported `Synced=True`.
- **Over-claimed guard scope**: v0.11.1 docs describe the external-name/adoption defenses generically; they exist for the nodepool only, while five other kinds carry the identity record the guard needs.
- **`no_paid` unhandled** on S3Bucket, S3User and Addon: an unfunded resource sits in `Creating` forever with no operator-visible reason (the other four kinds map it to a billing condition).
- **Stubbed selectors accepted at admission**: `clusterSelector` satisfies a *required* exactly-one-of rule yet can never reconcile — an admission-valid resource that is dead on arrival.

**Why this priority**: each one makes the platform's own reporting untrustworthy, which is worse than a loud failure.

**Acceptance Scenarios**:

1. **Given** an edit to a reference the provider cannot converge, **When** applied, **Then** it is rejected at admission (or, where rejection is infeasible, the field's documentation states plainly that the change is ignored — no false enforcement claim survives anywhere).
2. **Given** an unfunded S3Bucket/S3User/Addon, **When** observed, **Then** the resource reports the billing condition its siblings already use, not endless `Creating`.
3. **Given** a manifest whose only parent reference is a stubbed selector, **When** applied, **Then** it is rejected at admission naming the working alternatives.
4. **Given** the shipped docs, **When** an operator reads about the duplicate-create defenses, **Then** the covered kinds are named explicitly.

---

### User Story 3 - Credential and supply-chain hardening (Priority: P2)

- The bearer token must not survive a redirect to a different host (today it is attached per-RoundTrip, defeating the standard cross-host stripping).
- Secret-bearing writes (CDN certificate upload, config PATCHes carrying private keys / S3 keys / secure tokens) must not be able to echo their payload into a condition or Event via the transient-error raw-body fallback.
- The release pipeline must not execute an unpinned remote script while holding the signing identity; consumers must have a documented, identity-constrained verification command; artifacts should carry an SBOM/provenance attestation.
- Lower-risk hygiene: e2e must stop passing the token through argv; install manifests should pin digests, not mutable tags; the inert admission policy targeting a removed kind should go.

**Acceptance Scenarios**:

1. **Given** a redirect to a foreign host, **When** the client follows it, **Then** no Authorization header is sent (and cross-host redirects are refused).
2. **Given** a transient upstream error on a secret-bearing write, **When** the error surfaces, **Then** no payload fragment (PEM block, key value) appears in the message, condition, or Event.
3. **Given** a published release, **When** a consumer follows the documented verification, **Then** the signature verifies against a constrained identity and an SBOM/provenance attestation is present.

---

### User Story 4 - The operator can see and route a failure (Priority: P2)

Today no kind surfaces a condition reason or message at `kubectl get` level, so a resource parked on a guard looks identical to one merely provisioning; and the condition/classification references are reachable only from two release notes. The operator gets: a MESSAGE column on every kind, a single troubleshooting entry point linked from the README and every per-kind guide, and the GitOps/ArgoCD guidance in a findable home rather than mid-way through a one-time install doc.

**Acceptance Scenarios**:

1. **Given** a resource parked on any guard, **When** listed with `kubectl get`, **Then** it is visually distinguishable from a healthy/provisioning one.
2. **Given** an operator starting from the README, **When** they follow the troubleshooting path, **Then** they reach the condition reference, the Events caveat, the fleet-view commands, and the provider-log guidance without prior knowledge of file names.
3. **Given** an ArgoCD user setting up an Application, **When** they follow the docs, **Then** they encounter the external-name ownership rule and a copy-pasteable `ignoreDifferences` example before writing the Application.

---

### User Story 5 - Refactor before the multiplier lands (Priority: P2)

The request/classify/close/decode boilerplate spans ~90 call sites and was independently reinvented twice; the scheduled create-guards rollout (`specs/_next-create-guards.preface.md`) will add 2–3 copies per kind across 7+ kinds. Landing the shared helper first makes that rollout materially smaller. In the same round: split the ~320-line router Update (its pacing budget is hand-checked at nine sites in two directions), fix the floating-IP list N+1 on the NAT path (5–10 list calls per reconcile against the 2 r/s limiter the Qrator mitigation depends on, plus a test-harness limitation that leaves the multi-attachment case uncovered), and unify the autoscaling predicate that exists twice in different shapes.

**Acceptance Scenarios**:

1. **Given** the refactor, **When** the suite runs, **Then** behavior is unchanged (all existing tests pass unmodified except where the helper changes call shape) and the boilerplate comment class disappears.
2. **Given** a multi-attachment router with blocked NAT rows, **When** it reconciles, **Then** the floating-IP list is fetched once per pass, and the case is covered by a test that can serve repeated responses.
3. **Given** the autoscaling logic, **When** drift is evaluated in Observe and converged in Update, **Then** both use one shared predicate.

---

### Edge Cases

- Registry token scoping is operator-side: the provider cannot verify how
  broad a ProviderConfig's token is, so the docs must not promise safety —
  they describe the setup and the consequence, and the operator owns the
  choice. (If the account admin panel's scoping granularity turns out to be
  coarser than per-registry, the doc states the closest achievable scope
  rather than an aspirational one — a doc-time check, not a code dependency.)
- Adding CEL immutability where the provider previously ignored edits will start rejecting manifests that currently apply cleanly — including drifted ones already in git. The rejection message must name the remedy (revert, or delete+recreate).
- The MESSAGE print column must source `.status.conditions[Ready].message` (not `.reason`) because terminal reasons get overwritten by `ReconcileError` — the message survives.
- The shared do/decode helper must preserve the "Classify reads the body before Close" ordering exactly; any regression there re-opens the 019 classification correctness.
- Redirect hardening must not break the legitimate same-host redirects the API may use.
- Removing dead condition constants is safe only if nothing external references them; the one whose doc-comment claims an unimplemented mapping needs a decision — implement the mapping or delete the claim.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: ContainerRegistry documentation MUST lead with the scoped-token
  setup (registry-scoped token created in the account admin panel, used by a
  dedicated ProviderConfig serving the registry resources) and MUST state
  plainly what the connection Secret's password is, who can read it, and the
  consequence of using an account-wide token. No spec-field gate is added and
  publication behavior is unchanged.
- **FR-002**: Any reference/field the provider cannot converge MUST either be rejected at admission or be documented as ignored — no documentation may claim an enforcement that does not exist.
- **FR-003**: `no_paid` (and equivalent billing-refusal states) MUST map to the established billing condition on ALL kinds that can report it.
- **FR-004**: Stubbed selectors MUST NOT be admission-valid as the sole way to satisfy a required reference; the rejection names the working alternatives.
- **FR-005**: Documentation of the duplicate-create defenses MUST name the kinds actually covered.
- **FR-006**: The API token MUST NOT be sent to a host other than the configured API host, including across redirects.
- **FR-007**: Errors from secret-bearing writes MUST NOT include raw response-body fragments; secret material MUST be scrubbed from any surfaced message.
- **FR-008**: The release pipeline MUST NOT execute unpinned remote code in a job holding publish/signing privileges; releases MUST carry an SBOM or provenance attestation and a documented identity-constrained verification command.
- **FR-009**: Every kind MUST surface the Ready condition message at `kubectl get` level.
- **FR-010**: A single troubleshooting entry point MUST exist, linked from the README and every per-kind guide, covering the get→describe→events→logs→reference path, the reason-overwrite caveat, and fleet-view commands.
- **FR-011**: GitOps/ArgoCD guidance (external-name ownership, `ignoreDifferences` example, create-wedge runbook) MUST live in a document an operator reaches before writing an Application.
- **FR-012**: The request/classify/close/decode boilerplate MUST be a shared helper used by all controllers; per-call-site duplication of that pattern ends.
- **FR-013**: The router Update MUST be decomposed and its pacing budget expressed as a single auditable mechanism.
- **FR-014**: Per-reconcile upstream list reads MUST NOT scale with attachment count on the NAT path; the multi-attachment blocked case MUST have test coverage (test harness able to serve repeated responses).
- **FR-015**: Duplicated logic MUST be consolidated: the autoscaling predicate, the upstream-state→Ready vocabularies, `clientLogger`, and the local pointer-helper re-implementations.
- **FR-016**: Dead condition constants MUST be removed, and any doc-comment claiming an unimplemented mapping MUST be made true or deleted.
- **FR-017**: Lower-risk hygiene: e2e must not pass the token via argv; install manifests pin digests; the inert admission policy is removed; the composed private-cluster example and the non-applyable router example are fixed.
- **FR-018**: Behavior-preserving items (FR-012..FR-015) MUST NOT change observable resource behavior; existing tests pass, with changes limited to call shape.
- **FR-019**: The feature ships as a single MINOR release (v0.12.0). The
  release note MUST call out the new admission rejections introduced by the
  CEL immutability rules (FR-002/FR-004), naming the affected fields and the
  remedy, since manifests that apply cleanly today may start failing.

### Key Entities

- **ContainerRegistry**: gains the publication opt-in (new spec field) and create-only publication semantics.
- **Server / KubernetesCluster / KubernetesClusterAddon / Nodepool**: gain admission-level truth about non-convergeable fields and stubbed selectors.
- **S3Bucket / S3User / Addon**: gain the billing-state mapping.
- **All kinds**: gain the MESSAGE print column.
- **Shared client helper**: the single request/classify/close/decode path.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator following `docs/containerregistry.md` end-to-end
  arrives at a registry-scoped credential in the connection Secret without
  needing to know the account-wide alternative existed; the exposure
  statement is present and accurate.
- **SC-002**: For every kind, a parked/blocked resource is distinguishable from a provisioning one in a plain `kubectl get` listing.
- **SC-003**: Zero documentation statements describe enforcement the code does not perform (verified by a doc-vs-code pass over the immutability and guard-scope claims).
- **SC-004**: An unfunded resource of every affected kind reports the billing condition within one poll cycle.
- **SC-005**: No secret material can reach a condition or Event from a secret-bearing write (verified by unit tests feeding payload-echoing error bodies).
- **SC-006**: A released artifact verifies with a documented identity-constrained command and carries an attestation.
- **SC-007**: Post-refactor: all 15 test packages pass, lint 0 issues, and the per-call-site boilerplate comment class is gone; the NAT path issues one floating-IP list per reconcile regardless of attachment count.
- **SC-008**: An operator starting from the README reaches the right diagnosis path for a parked resource without prior knowledge of the repo layout.

## Assumptions

- The registry credential remedy is documentation-only by owner decision
  (2026-07-26): no CRD field, no publication-behavior change, therefore no
  migration burden and no breaking change from this item.
- Where upstream genuinely cannot enforce immutability, honest documentation is the accepted resolution (the Router types already model this tone).
- SBOM/provenance tooling choice is a plan-time decision; the requirement is an attestation plus a verification command, not a specific tool.
- The refactor items are behavior-preserving; they ship in the same feature but as separable commits so a regression can be bisected.
- Release shape (clarified): ONE release, **minor v0.12.0** — no split. The
  only breaking-ish change is the new admission rejections from CEL
  immutability (previously-ignored edits now fail at apply); the release note
  carries that callout with the revert/recreate remedy.
