# Feature Specification: Fix false not-found → resource recreation

**Feature Branch**: `019-fix-false-notfound-recreate`

**Created**: 2026-07-21

**Status**: Draft

**Input**: User description: "check work item #124 (postmortem: operator recreated staging VPC, empty duplicate) and fix the bug all across the resource base in the provider"

## Context

On 2026-07-20 ~11:46 UTC the provider recreated the `staging` VPC: the `external-name`
on `Network/staging` jumped from the live production VPC (`network-f30f4ab…`, holding all
of staging) to a freshly created empty duplicate (`network-52a845…`). The production VPC was
orphaned but stayed physically alive; the `shared` Router then spent 20+ hours failing to
detach the production network from what the provider believed was gone.

Root cause (from the postmortem): during a single reconcile, `GetVPC` returned **one** HTTP
404 for the live VPC. The provider's shared error classifier turns **any** 404 into the
"resource deleted" sentinel purely by HTTP status code, so `Observe` reported the resource as
absent and the managed reconciler (Create allowed) immediately recreated it. The Timeweb API
is documented as intermittently flaky behind Qrator/edge protection; a transient edge 404 is
therefore indistinguishable, today, from a genuine deletion.

This classification lives in a single shared code path used by the `Observe` of **every**
managed kind in the provider (Network, Router, FloatingIP, Firewall, Server, Kubernetes
cluster/nodepool/addon, CDN, S3 bucket/user, Container Registry, Project, SSH key). The bug is
therefore latent for the entire resource base, not just Network — the fix must be made in the
shared path so every kind is protected at once.

The canonical distinguishing signal already exists in the API contract: a genuine Timeweb 404
carries a mandatory error envelope (`error_code`, `status_code`, `response_id` are `required`
in the API's `not-found` response schema), whereas an edge/gateway/DDoS-protection 404 arrives
as HTML, empty, or otherwise without that envelope. The provider already parses this envelope
for the human-readable message but ignores it when deciding "deleted vs. flapped".

## Clarifications

### Session 2026-07-21

- Q: How should the bug class be documented so future resources don't reintroduce it? → A:
  Add a binding principle to the project Constitution AND a durable reference doc under `docs/`
  (enforceable rule + the "why").
- Q: What automated guard should prevent a future resource from reintroducing the bug class? →
  A: Both a contract test on the classifier's 404 branch AND a bypass guard asserting no
  hand-written controller/client derives "absent" from a raw 404 outside a canonical
  classifier — but the rule is the *general* one (canonical not-found signal, never
  status-code-alone), relaxed per client for non-Timeweb APIs that carry a different canonical
  signal (e.g. the RGW/AWS IAM client `rgwiam` uses the exact `NoSuchEntity` error code, not
  the Timeweb envelope, and is already compliant).
- Q: How do we know the envelope-presence rule is valid for every managed type, including the
  undocumented endpoints (CDN, Router)? → A: For each managed type, check the **official
  Timeweb API documentation first** to confirm the canonical not-found envelope is actually
  defined for that endpoint's 404 before relying on envelope-presence; where the official docs
  do not define it (or the endpoint is undocumented), the type MUST be handled explicitly
  (verified by a live 404 capture) rather than silently assuming the envelope.

**Audit finding (recorded during clarification):**
Every hand-written controller routes its upstream 404 through a shared classifier
(`errors.Is(err, timeweb.ErrNotFound)` for the Timeweb path; `errors.Is(err,
rgwiam.ErrNoSuchEntity)` for the S3User grant path). No controller inspects a raw 404 or sets
`ResourceExists:false` from an HTTP status directly (the only raw `StatusCode == 404` checks
live in the generated client's body-decoders, which still hand the response to the classifier).
The `rgwiam` client already classifies precisely on the exact `NoSuchEntity` code and needs no
change. Therefore the defect is confined to the single Timeweb classifier, and fixing it there
eliminates the bug class across all current managed kinds.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A flaky 404 never destroys a live resource (Priority: P1)

An operator is running the provider in steady state against live Timeweb infrastructure. The
upstream API intermittently returns a 404 from behind its edge/DDoS layer (HTML page, empty
body, or any response without the canonical error envelope) for a resource that in fact still
exists. The provider must treat this as a transient failure and retry, never as a deletion —
so it never creates a duplicate and never orphans the live resource.

**Why this priority**: This is the incident. A single misclassified 404 caused a production
network to be orphaned, a duplicate to be created, and 20+ hours of a wedged Router. The blast
radius is every managed kind. Preventing recreation-on-flap is the entire point of the fix.

**Independent Test**: Feed the shared classifier a 404 response with an HTML body / empty body
/ JSON without `error_code`, and assert the result is a transient (retryable) error, not the
not-found sentinel. Feed the same through a controller `Observe` and assert it does **not**
report the resource as absent.

**Acceptance Scenarios**:

1. **Given** a live resource whose upstream GET returns a 404 with no canonical error envelope
   (HTML / empty / envelope-less JSON), **When** `Observe` runs, **Then** the reconcile
   surfaces a transient error and requeues, and the resource is neither reported absent nor
   recreated.
2. **Given** the same misclassified 404 recurs across several reconciles, **When** each
   reconcile runs, **Then** the provider keeps retrying and never issues a Create for a
   resource whose recorded upstream ID is still populated.
3. **Given** any managed kind in the provider (not only Network), **When** its upstream GET
   returns an envelope-less 404, **Then** the same protection applies without per-kind code
   changes.

---

### User Story 2 - A genuine deletion is still recognized (Priority: P2)

An operator deletes a resource out-of-band in the Timeweb panel, or the resource is genuinely
gone. The upstream GET returns a proper Timeweb 404 carrying the canonical error envelope. The
provider must still recognize the resource as absent so that adoption / recreation / orphan
handling continues to work exactly as before.

**Why this priority**: The fix must not overcorrect into never trusting any 404 — that would
break legitimate deletion detection and drift adoption. Deletion recognition must remain
behaviorally unchanged for canonical 404s.

**Independent Test**: Feed the shared classifier a 404 carrying the canonical envelope
(`error_code` present, `status_code` 404, `response_id` present) and assert the not-found
sentinel is returned, preserving the existing message/`response_id` enrichment.

**Acceptance Scenarios**:

1. **Given** an upstream GET that returns a 404 with a canonical Timeweb error envelope,
   **When** `Observe` runs, **Then** the resource is reported absent exactly as it is today.
2. **Given** a canonical 404 on a delete path, **When** the controller tolerates not-found on
   delete, **Then** deletion still completes cleanly (finalizer removed).
3. **Given** a canonical 404, **When** the error is surfaced, **Then** the human-readable
   message and `response_id` continue to be included in the surfaced reason.

---

### User Story 3 - Suspected flaps are observable (Priority: P3)

When the provider treats a 404 as a suspected transient flap (rather than a deletion), an
operator investigating a future incident must be able to see that this happened, rather than
having it swallowed into a silent requeue.

**Why this priority**: The postmortem explicitly notes provider logs were effectively absent
in steady state (successful Observe/Create go out as Kubernetes Events, not log lines), which
made the incident hard to reconstruct. Making the "I saw a 404 but decided it was a flap"
decision visible closes that observability gap without changing control-flow correctness.

**Independent Test**: Trigger an envelope-less 404 through a controller `Observe` and assert an
observable signal (Event and/or log line) records that a 404 was treated as transient, naming
the resource and, when present, the `response_id`.

**Acceptance Scenarios**:

1. **Given** an envelope-less 404 is reclassified as transient, **When** the reconcile
   requeues, **Then** an operator-visible signal records the reclassification with enough
   detail (kind, external ID/name, `response_id` if any) to correlate with the upstream.

---

---

### User Story 4 - A future resource cannot silently reintroduce the bug (Priority: P2)

A contributor adds a new managed kind (or a new client for an existing one) months from now.
The project must make the "a bare/edge 404 is not a deletion" rule discoverable and enforced,
so the new resource cannot quietly repeat the incident — whether it uses the Timeweb API or a
different backend (e.g. the RGW/AWS IAM path).

**Why this priority**: The user's explicit ask is that the bug class be *documented for future
resources and eliminated across all current ones*. Centralizing the current fix eliminates it
today; documentation plus an automated guard is what keeps it eliminated as the provider grows.

**Independent Test**: (a) The Constitution and the reference doc state the rule and its
rationale; (b) a guard test fails if a controller/client is added that derives "resource
absent" from a raw HTTP status instead of a canonical, precisely-classified not-found signal.

**Acceptance Scenarios**:

1. **Given** a new contributor reads the Constitution and `docs/`, **When** they implement a
   new kind's `Observe`, **Then** the canonical-not-found rule is stated plainly with the
   incident as motivation.
2. **Given** a hypothetical new controller that maps a raw 404 straight to `ResourceExists:false`,
   **When** the test suite runs, **Then** the bypass guard fails.
3. **Given** a non-Timeweb client that classifies not-found by its own canonical signal (e.g.
   `rgwiam`'s exact `NoSuchEntity` code), **When** the guard runs, **Then** it passes — the
   rule is "canonical signal, never status-alone", not "must use the Timeweb envelope".

---

### Edge Cases

- **404 with an empty body**: no envelope → transient (retry), not deletion.
- **404 with an HTML body** (edge/Qrator/gateway page): no envelope → transient.
- **404 with a JSON body that is not the canonical envelope** (e.g. missing `error_code`):
  treated as envelope-absent → transient.
- **404 with the canonical envelope**: deletion recognized (unchanged behavior).
- **Genuine deletion that upstream reports as a bare 404** (no envelope): the provider will
  keep retrying instead of recognizing the deletion. This is the deliberate
  safety-over-liveness trade-off — see Assumptions. It must be confirmed against a live
  deletion capture that real Timeweb deletions carry the envelope.
- **Non-404 errors** (5xx, 408, 409, 425, 429, network, other 4xx): classification unchanged.
- **`external-name` annotation lost** so the default derives the external name from the CR's
  Kubernetes name and the resulting GET 404s: out of scope for this fix — see Assumptions.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The shared error classifier MUST treat an HTTP 404 as "resource deleted" (the
  not-found sentinel) **only** when the response carries the canonical Timeweb error envelope;
  any 404 without that envelope MUST be classified as a transient (retryable) error.
- **FR-002**: The presence of the canonical envelope MUST be determined by parsing the response
  body for the required Timeweb error fields (`error_code`, and corroborating `status_code` /
  `response_id`), not by the HTTP status code alone.
- **FR-003**: For a canonical-envelope 404, the classifier MUST preserve today's behavior:
  return the not-found sentinel, still enriched with the upstream message and `response_id`.
- **FR-004**: The fix MUST be made in the single shared classification path so that every
  managed kind's `Observe` is protected without per-kind changes; no controller may
  independently downgrade a bare 404 to "resource absent".
- **FR-005**: A 404 reclassified as transient MUST cause the reconcile to requeue with the
  Synced condition treated as it is for other transient errors (i.e. not flipped to a terminal
  failure purely because of a suspected flap), consistent with existing transient handling.
- **FR-006**: Classification of all non-404 responses (2xx success, 5xx/408/409/425/429 and
  network transients, other terminal 4xx, the existing `networks_location_mismatch` settle
  case) MUST remain unchanged.
- **FR-007**: When a 404 is reclassified as transient, the provider MUST emit an
  operator-visible signal (Event and/or log line) identifying the affected resource and, when
  available, the `response_id`, so the decision is auditable after the fact.
- **FR-008**: The change MUST be covered by unit tests for the classifier spanning: canonical
  envelope present (→ not-found), envelope-less JSON (→ transient), HTML body (→ transient),
  empty body (→ transient); and the existing classification cases MUST continue to pass.
- **FR-009**: The governing rule MUST be stated as a general principle, not a Timeweb-only
  detail: a resource is "deleted" only on a **canonical, precisely-classified not-found signal
  for that API** (Timeweb: the error envelope; RGW/AWS IAM `rgwiam`: the exact `NoSuchEntity`
  error code); a not-found MUST NEVER be concluded from the HTTP status code alone.
- **FR-010**: The bug class MUST be documented durably as a binding **Constitution** principle
  (so it governs future kinds) AND as a reference doc under `docs/`, both citing the incident
  (work item #124) as motivation and stating the canonical-not-found rule of FR-009.
- **FR-011**: The project MUST include a **contract test** covering the Timeweb classifier's
  404 branch per FR-008.
- **FR-012**: The project MUST include a **bypass-guard test** that fails if any hand-written
  controller or client concludes "resource absent" (`ResourceExists:false` or an equivalent
  not-found sentinel) from a raw HTTP status rather than a canonical classifier. The guard MUST
  permit non-Timeweb clients that classify not-found by their own canonical signal (FR-009) —
  it MUST NOT require them to use the Timeweb envelope.
- **FR-013**: Clients that already classify not-found precisely (the `rgwiam` AWS IAM path via
  the exact `NoSuchEntity` code) MUST be left unchanged; the fix MUST NOT weaken or reshape
  their existing correct behavior.
- **FR-014**: Before relying on envelope-presence for a managed type, the official Timeweb API
  documentation (`docs/openapi-timeweb.json`) MUST be checked to confirm the canonical
  not-found envelope is defined for that endpoint's 404. Endpoints whose official docs define
  the envelope inherit the shared rule; endpoints that are undocumented or do not define the
  envelope (e.g. the CDN `/cdn/*` and Router `/routers*` surfaces) MUST be verified by a live
  404 capture and handled explicitly, so envelope-absent never wedges a type whose genuine
  deletions legitimately return a bare 404.

### Key Entities

- **Canonical Timeweb error envelope**: the API's mandatory error response shape carrying
  `error_code`, `status_code`, `message`, and `response_id`. Its presence is the discriminator
  between a genuine upstream not-found and an edge/gateway/DDoS 404.
- **Not-found sentinel**: the classifier result that a controller's `Observe` interprets as
  "resource does not exist upstream", which (under a Create-enabled management policy) drives
  recreation/adoption.
- **Transient error**: the classifier result that drives a requeue without asserting the
  resource is absent.
- **Shared classifier**: the single function through which every controller routes upstream
  HTTP responses to decide deleted-vs-flapped-vs-error. The Timeweb classifier is the one with
  the defect; the `rgwiam` (AWS IAM) classifier is a second, already-correct canonical
  classifier for the S3User grant path.
- **Canonical not-found signal (per API)**: the API-specific evidence that a resource is
  genuinely gone — for Timeweb, the error envelope; for RGW/AWS IAM, the exact `NoSuchEntity`
  error code. The general rule (FR-009) is defined against this concept, not any single API's
  shape.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A 404 without the canonical envelope (HTML, empty, or non-envelope JSON) never
  results in a resource being reported absent, for **any** managed kind — verified by the
  shared classifier returning a transient error in every such case.
- **SC-002**: A 404 with the canonical envelope still results in the resource being reported
  absent, preserving deletion detection and adoption — verified by unchanged behavior on
  canonical 404s.
- **SC-003**: No controller can independently turn a bare 404 into "resource absent" — the
  protection is centralized and applies uniformly across all kinds with zero per-kind
  classification changes.
- **SC-004**: A reproduction of the incident (a single flaky envelope-less 404 for a live
  resource in steady state) no longer produces a duplicate or an orphan; the reconcile
  requeues instead.
- **SC-005**: Every reclassification of a 404 to transient is observable to an operator (Event
  and/or log), eliminating the silent-requeue gap called out in the postmortem.
- **SC-006**: The canonical-not-found rule (FR-009) is discoverable by a new contributor via
  the Constitution and a `docs/` reference, each citing work item #124.
- **SC-007**: A regression that maps a raw 404 straight to "resource absent" — in the shared
  classifier or in any hand-written controller/client — is caught by an automated test before
  merge.
- **SC-008**: The `rgwiam` not-found path (exact `NoSuchEntity`) is verified unchanged and the
  guard confirms it is compliant without being forced onto the Timeweb envelope.

## Assumptions

- **Real deletions carry the envelope (per-type, documentation-verified)**: The fix assumes a
  genuine Timeweb deletion returns a 404 with the canonical envelope, while flaky/edge 404s do
  not. For documented endpoints this matches the API contract
  (`error_code`/`status_code`/`response_id` are `required` on the not-found response). Per
  FR-014, each managed type is checked against the official docs first; documented endpoints
  inherit the rule, while undocumented surfaces (CDN, Router) are confirmed by a live 404
  capture. If a real deletion for some type is found to return a bare 404, that type needs an
  additional corroboration step (a second read / list lookup before concluding "deleted").
- **Safety over liveness**: When in doubt (404 without envelope), the provider retries rather
  than recreates. The worst case of this trade-off is delayed recognition of a genuine
  deletion; the worst case it prevents is destroying/orphaning a live production resource.
  Safety is preferred, consistent with the incident.
- **List/second-read corroboration is out of scope for this fix**: The industry survey in the
  postmortem shows envelope-based classification is itself the standard (ACK, Upjet, KCC,
  crossplane-runtime all trust a single well-classified not-found and none perform a second
  read). List-corroboration is an optional belt-and-suspenders reserved for the case where
  real deletions turn out to lack the envelope; it is not implemented here unless the live
  capture demands it.
- **`NameAsExternalName` suppression is a separate task**: The postmortem's secondary path
  (controllers registered without initializers fall back to deriving the external name from the
  CR's Kubernetes name, which can 404 → Create) is a distinct latent risk tracked separately
  and is not addressed by this classification fix.
- **The client is hand-written and the OpenAPI spec already models the envelope**: no client
  regeneration is required for this change.
