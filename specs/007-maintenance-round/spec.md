# Feature Specification: Maintenance Round — Placement, Preset & Printcolumn Cleanups

**Feature Branch**: `007-maintenance-round`

**Created**: 2026-06-17

**Status**: Draft

**Input**: Preface doc `specs/_next-location-az-presets.preface.md` (findings F-1…F-10,
distilled from live-API probing during feature 006). Findings F-5…F-9 were already
implemented in 006; this round scopes to the genuinely-deferred operator-experience
cleanups: **F-1…F-4** (location/AZ model + preset slugs + not-found errors) and
**F-10** (printcolumns).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Consistent placement, every region reachable (Priority: P1)

An operator declaring any Timeweb resource specifies *where* it lives the same way
across every kind, and can deploy to **all** of Timeweb's regions — not just a
hand-maintained subset. Today some resources require `location` (Server, Network,
FloatingIP) while others require `availabilityZone` (Router, KubernetesCluster), and
the latter are silently limited to 4 of Timeweb's 8 regions because the
region↔zone table is hardcoded and incomplete.

**Why this priority**: It removes a real functional gap (routers and clusters
cannot be placed in `ru-2`/`pl-1`/`kz-1`/`us-4` today) and makes the whole CRD
surface predictable — the single biggest source of operator confusion.

**Independent Test**: Create a Router and a KubernetesCluster placed in a region
that is currently unreachable (e.g. `ru-2`/`nsk-1`); both provision successfully.
Apply the same manifests pinned to the existing 4 regions and confirm they still work.

**Acceptance Scenarios**:

1. **Given** a Router declared in region `ru-2`, **When** applied, **Then** it
   provisions (today it fails because `ru-2`'s zone is absent from the table).
2. **Given** any managed resource, **When** the operator inspects its spec, **Then**
   placement is expressed with the same field name and vocabulary as every other kind.
3. **Given** an existing manifest using `availabilityZone`, **When** re-applied,
   **Then** it continues to resolve unchanged (no breaking change).

---

### User Story 2 - Simpler preset names with location-scoped errors (Priority: P2)

An operator names a size/tier preset without repeating the location they already
declared (e.g. `ssd-15` instead of `ssd-15-ru-1`), and when they get a preset name
wrong, the error lists the presets that are actually available **for their
location** — not a global, long-form dump.

**Why this priority**: Removes redundant, error-prone duplication from every
manifest and turns a frustrating "preset not found" into a self-service fix.

**Independent Test**: A Server with `presetName: ssd-15` + `location: ru-1` resolves
the same upstream preset as `ssd-15-ru-1`. A typo (`ssd-99`) returns an error that
lists only `ru-1` presets in the simplified form.

**Acceptance Scenarios**:

1. **Given** `presetName: ssd-15` and `location: ru-1`, **When** applied, **Then**
   the provider resolves the same preset as the long-form `ssd-15-ru-1`.
2. **Given** an existing manifest using the long-form slug, **When** re-applied,
   **Then** it still resolves (both forms accepted).
3. **Given** an unknown preset name for `location: ru-1`, **When** applied, **Then**
   the failure message lists the valid presets for `ru-1` only, in simplified form.

---

### User Story 3 - Consistent, decluttered resource listings (Priority: P3)

An operator running `kubectl get` across the provider's kinds sees a consistent,
operator-useful set of columns in a predictable order, with internal identifiers
tucked behind `-o wide` rather than cluttering the default view.

**Why this priority**: Pure quality-of-life; improves day-to-day legibility but
changes no behavior.

**Independent Test**: `kubectl get` on each kind shows a uniform leading column set
and order; a FloatingIP bound to a Router shows its binding in a single column;
internal ids appear only with `-o wide`.

**Acceptance Scenarios**:

1. **Given** any two kinds, **When** listed, **Then** their shared columns appear in
   the same order with the same names (e.g. `LOCATION`, `ID`).
2. **Given** a Router-bound FloatingIP, **When** listed, **Then** exactly one
   `BOUND-TO` column shows the binding (no empty/duplicate id columns).
3. **Given** a nodepool, **When** listed, **Then** its public-IP setting is visible;
   internal upstream ids are absent from the default output and present with `-o wide`.

---

### User Story 4 - "Is it up? Why not?" answerable from kubectl alone (Priority: P1)

An operator diagnosing any resource can tell, from `kubectl get`/`describe` alone,
whether it is healthy, still provisioning, blocked on payment, failed and
unrecoverable, or waiting on a dependency — without reading provider logs or the
Timeweb panel. Today several kinds report `Ready=True` the instant they exist
(masking upstream-degraded/payment-blocked state), a failed Server/addon/cluster-op
spins forever on "creating", and `kubectl describe` is nearly empty because the
provider emits almost no Events.

**Why this priority**: Observability is the primary day-2 surface; these blind
spots turn routine debugging into log-spelunking and let dead resources look alive.

**Independent Test**: Force each failure mode (payment-blocked, upstream failure,
unsupported change, dependency-not-ready) and confirm each surfaces a distinct
condition reason **and** a corresponding Event, with terminal failures reported as
such rather than retried silently.

**Scope**: applies to **all** managed kinds. `PaymentRequired` is wired only where a
confirmed upstream no-pay signal exists (Server's `no_paid`; Router's `status:"error"`
best-effort) — not promised speculatively elsewhere. Events fire on condition
*transitions only*. A single shared condition-reason vocabulary + one
`MapResolverErrorToCondition` helper is used across every kind.

**Acceptance Scenarios**:

1. **Given** an account that can't fund a Server, **When** reconciled, **Then** it
   reports `PaymentRequired` (not `Ready=True`) — and for kinds with no observed
   no-pay signal, it does not falsely claim payment state.
2. **Given** an upstream `failed`/`error` Server/addon/cluster-op, **When** observed,
   **Then** it reports a terminal `UpstreamFailed` (not perpetual "creating").
3. **Given** a condition *transition* (payment, failure, dependency-wait, deferred
   bind, scaling), **When** it happens, **Then** exactly one Event is recorded
   (no per-reconcile spam) and is visible in `kubectl describe`.
4. **Given** a resolver error (preset-not-found, no-configurator) on any kind,
   **When** reconciled, **Then** it surfaces the same typed condition reason across
   all kinds (one shared vocabulary), not a generic `ReconcileError`.

---

### User Story 5 - First-run onboarding actually works (Priority: P1)

A new operator can copy a documented example, apply it, and get a working resource —
and can find how to obtain the API token and create the credential Secret. Today the
first examples an operator hits (ContainerRegistry, S3Bucket) fail admission (wrong
API group, removed fields, dead preset slug, missing required field), and there is no
`providerconfig`/credentials example, no Server or K8s example, and no getting-started
or auth-setup page.

**Why this priority**: A broken first example is the worst possible first impression
and blocks adoption before any feature value is reached.

**Independent Test**: Apply each shipped example manifest unmodified (after substituting
a token) → all are admitted and reconcile; a getting-started doc walks token → Secret →
ProviderConfig → first resource end to end.

**Acceptance Scenarios**:

1. **Given** any example in `examples/`, **When** applied unmodified, **Then** it is
   admitted (correct group/fields/slug) and reconciles.
2. **Given** a new operator, **When** they follow the getting-started doc, **Then** they
   provision a first resource without reading source code.

---

### Edge Cases

- A region that exposes **more than one** availability zone for a given product
  (e.g. `ru-1` has five zones): resolving a placement by region alone must not
  silently mis-place — if the choice is ambiguous, the operator is asked to specify
  the zone.
- A preset name that, after location filtering, matches multiple upstream entries:
  the existing disambiguation (`<name>-<id>`) still applies.
- A simplified preset name that collides with a location code: resolution must not
  misinterpret the suffix.
- An operator who omits the optional finer placement: the provider picks the
  catalog-offered default for the region, or errors if none/ambiguous.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every managed resource that is regionally placed MUST express placement
  with a uniform, required `location` (region) field, plus an optional
  `availabilityZone` for finer placement. `location` and `availabilityZone` are a
  region→zone hierarchy and MUST NOT be merged into one field.
- **FR-002**: The provider MUST support every region and zone reported by Timeweb's
  authoritative locations source, not a hardcoded subset — Router and Kubernetes
  placement MUST NOT be limited to the 4 currently-mapped zones.
- **FR-003**: Operators MUST be able to name a preset in a simplified form that omits
  the location already given by the placement field; the provider resolves it by
  filtering the catalog to the operator's location.
- **FR-004**: Existing manifests MUST continue to resolve unchanged — the long-form
  preset slug and the current required placement fields remain accepted (no breaking
  change for any already-applied resource).
- **FR-005**: When a preset name does not resolve, the failure MUST list the presets
  available **for the operator's declared location**, in the simplified form.
- **FR-006**: `kubectl get` output MUST be uniform across kinds: a fixed leading
  column order, internal identifiers (upstream id / external-name, unified as `ID`)
  shown only with `-o wide`, the FloatingIP binding collapsed to a single column, and
  a nodepool public-IP column added. Crossplane's default `READY`/`SYNCED`/`AGE`
  columns are kept and standardized identically across kinds.
- **FR-007**: No change to which upstream resources are created or their defaults —
  this round is presentation, validation, and addressing-vocabulary only.

*Review-sourced requirements (operator-facing maintenance):*

- **FR-008**: Every kind MUST gate `Ready` on actual upstream state rather than
  reporting `Ready=True` the instant it exists, and MUST report a terminal
  `UpstreamFailed` on an upstream `failed`/`error` state (rather than spinning on
  "creating"). `PaymentRequired` MUST be reported only where a confirmed upstream
  no-pay signal exists — Server (clean `no_paid`) and Router (the ambiguous
  `status:"error"`, already best-effort via 006 F-7); it MUST NOT be promised for
  kinds where no such signal has been observed (no speculative detection).
- **FR-009**: The provider MUST emit Events at key condition **transitions only**
  (on a condition *change*, never per-reconcile): payment-blocked, upstream-failed,
  dependency-wait, deferred bind, scaling — so `kubectl describe` is a usable
  debugging surface without becoming event spam.
- **FR-009a**: The provider MUST use a **single shared condition-reason vocabulary**
  (one `shared` reason set) and a single `shared.MapResolverErrorToCondition` helper,
  applied at **all** resolver call-sites and across **all** kinds — replacing the
  current ad-hoc reasons (`"ParentNotReady"` literal, `xpv2.ReasonCreating` as a Ready
  reason) and the four controllers that don't map resolver errors to conditions at all.
- **FR-010**: *(DEFERRED — out of 007 scope.)* Destructive-delete guards (opt-in
  protection against deleting non-empty data-bearing resources — S3 buckets,
  databases, servers, clusters / any dataplane object) are deferred to a future
  `extra-annotations` feature; see `specs/_next-extra-annotations.preface.md`. 007
  adds **no** provider-side delete guard.
- **FR-011**: Status mirrors MUST be complete enough to operate from `kubectl` alone:
  Network gains an observed `state`; ContainerRegistry gains `state` + the registry
  endpoint/hostname; Addon mirrors its installed version.
- **FR-012**: Dependency gating MUST be consistent — dependent kinds gate on the
  parent's `Ready=True` with shared reason constants, and the Router `Watches` its
  Network/FloatingIP dependencies to re-reconcile promptly (matching the K8s kinds).
- **FR-013**: Immutability enforcement MUST be consistent — fields documented immutable
  (Network/FloatingIP `location`) carry the same `self == oldSelf` CEL guard as Server.
- **FR-014**: Cross-kind API consistency — `projectID` typed uniformly across kinds,
  the standard reference type + `*Selector` trios available where missing, and one
  free-form-note field name (`comment`/`description`) standardized.
- **FR-015**: All shipped examples MUST apply unmodified; the example set MUST cover the
  compute + Kubernetes surface and a `ProviderConfig`+credentials example; a
  getting-started + auth-setup doc MUST exist.
- **FR-016**: Stale docs/comments MUST be corrected (registry endpoint host, removed-field
  references, post-005 resize-deferral comments, the missing Repository `AGE` column).
- **FR-017**: Code-level maintenance from the Go review (see Maintenance Backlog) MUST
  be addressed where small/safe, with the four-case test pattern preserved.

### Key Entities

- **Location (region)**: the coarse placement unit (e.g. `ru-1`, `ru-2`, `de-1`);
  the universal required placement field.
- **Availability zone**: a finer placement within a region (a region may have
  several); optional selector. Region→zone is 1-to-many.
- **Preset / tier**: a named size/sizing for a billable resource, resolved against
  the live catalog, filtered by the operator's location.
- **Managed resource kinds**: Server, Network, FloatingIP, Router, KubernetesCluster,
  KubernetesClusterNodepool, KubernetesClusterAddon, ContainerRegistry +
  Repository, S3Bucket — the surfaces whose placement, preset, and columns are
  normalized.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Operators can place Routers and Kubernetes clusters in **all 8**
  Timeweb regions (today only 4 are reachable).
- **SC-002**: A wrong preset name yields an error that names **only** the presets
  valid for the declared location — zero cross-location entries in the message.
- **SC-003**: Preset names in manifests can be written without the location suffix,
  while **100%** of already-applied manifests continue to resolve unchanged.
- **SC-004**: Across all kinds, the default `kubectl get` shows the same shared
  columns in the same order, with no internal-id columns in the default view.
- **SC-005**: **Zero** existing manifests break and **zero** change in which upstream
  resources or defaults are produced (verified against the existing e2e bundles).

## Assumptions

- `location` and `availabilityZone` remain a hierarchy (live-API verified: `ru-1`
  has five zones) — they are standardized in shape and coverage, NOT merged.
- The region↔zone catalog is sourced from Timeweb's authoritative locations endpoint
  rather than a hardcoded table.
- Both the simplified and long-form preset slugs are accepted indefinitely
  (backward-compatibility is permanent, not a deprecation window).
- Findings F-5…F-9 (FloatingIP UUID binding, router Observe guard, no_paid mapping,
  router delete, FIP timeout) were already delivered in feature 006 and are out of
  scope here.
- Related internal cleanups surfaced during 006 (e.g. the kuttl positional
  condition-assert ordering in the e2e bundles) may be folded in during planning as
  non-operator-facing maintenance, but are not part of these user-facing outcomes.
- **Destructive-delete guards are DEFERRED** to a future `extra-annotations`
  feature (`specs/_next-extra-annotations.preface.md`); 007 adds no delete guard.

## Maintenance Backlog — Code-Review Findings (FR-017 detail)

Two independent Go reviews (Sonnet + Opus passes) plus a devops review, correlated.
Confidence tag: **[BOTH]** = both Go passes flagged it (high confidence) · **[Go]**
single Go pass · **[Ops]** devops pass (also covered by FR-008…FR-016). Already-
planned 007 items (US1–US3, US4, US5) are not repeated here.

### P1 — latent bugs / wrong behavior

- **[BOTH]** `defaultAZByLocation` has WRONG entries (`ru-2`→msk-1 and `ru-3`→spb-3
  are inverted vs the real `ru-2`→nsk-1 / `ru-3`→msk-1) and is missing `us-4`/`pl-1`
  → zone-less FloatingIPs silently mis-placed — `network/floatingip_external.go`. Fix
  with US1's live-sourced location table; correct the inverted pair regardless.
- **[BOTH]** Router `Update` closes `resp.Body` *before* `Classify` reads it →
  every attachment/DHCP/NAT non-2xx returns a generic "unexpected status" and the
  403-`networks_location_mismatch`-transient reclassification never fires; bodies also
  aren't drained — `network/router_external.go` (~6 sites). Use `defer`-close /
  Classify-before-close; add a `closeBody` helper.
- **[Go]** Resolver errors (`ErrPresetNotFound`/`ErrNoConfiguratorAvailable`/…) are
  NOT mapped to typed conditions in Server/Cluster/Nodepool/Router (s3bucket &
  containerregistry do it) → generic ReconcileError, no actionable reason. Extract
  `shared.MapResolverErrorToCondition` and call it at all six sites.
- **[Go]** Addon `version`/`type` documented immutable but no `self==oldSelf` CEL and
  Observe hard-returns up-to-date → a `version` edit lands in etcd and is silently
  swallowed — `kubernetesclusteraddon_types.go` / `addon_external.go`. Add CEL.
- **[Go]** SSHKey `name`/`body` immutability is Update-side-only **dead code** (no
  CEL; `isUpToDate` ignores them) → editing `body` silently succeeds in etcd —
  `sshkey/types.go`. Add `self==oldSelf`.
- **[Go]** Cluster version-change path: a downgrade returns a raw error (no typed
  condition) AND a lateral/build-metadata change (`+k0s.N`) is misclassified as a
  downgrade; `leadingInt` treats non-numeric as 0 — `kubernetes/cluster_upgrade.go`.
  Set a terminal condition; classify lateral as "unsupported version change."

### P2 — correctness gaps / reconcile churn / tests

- **[Go]** Server deferred-IP-bind path returns a hard error every poll while the VM
  installs (normal provisioning window) → Synced=False/Event churn —
  `compute/server_external.go` + `floatingip_bind.go`. Treat unbindable-while-creating
  as benign.
- **[Go]** Network `Name`/`SubnetCIDR` and nodepool `Name`: documented-immutable, no
  CEL (same dead-rejection class). Add `self==oldSelf`.
- **[Go]** Cluster adoption guard matches name-only → can adopt the WRONG cluster
  (Timeweb names aren't globally unique) — `cluster_external.go:findClustersByName`.
  Require AZ (and project) match.
- **[Go]** Cluster kubeconfig re-fetched + connection-Secret republished every Observe
  once Ready — `cluster_external.go:131`. Gate on the Ready transition / Secret-absent.
- **[Go]** Repository 404-on-parent (registry gone) sets no condition → Ready=Unknown
  forever — `repository_external.go`. Set `ParentNotReady`.
- **[Go]** Addon `findAddon` can't distinguish mid-install from deleted → spurious
  re-Create — `addon_external.go`. Gate `!found ⇒ not-exists` on cluster active.
- **[Go]** s3bucket/containerregistry `isUpToDate` never compare resolved preset/size
  → a size-only change never reconciles; the `LockedPresetID` re-lock is unreachable.
- **[BOTH]** `splitDisambiguator` accumulates `id*10+…` with no overflow guard → a
  long digit suffix overflows int64 → spurious `PresetNotFound` —
  `resolver/slug.go`. Use `strconv.ParseInt` / cap length.
- **[Go]** `classifyUpstream` dead `default` branch == the `≥500` branch → a non-401/403
  4xx (persistent 404/422) is classified transient → infinite retry on a permanent
  catalog error — `resolver/dimensions.go`. Split 4xx-permanent vs 5xx-transient.
- **[Go]** Catalog cache memoizes an empty-slice 200 OK for the full TTL → sticky
  `PresetNotFound (valid: <none>)` (the ×77 pattern) — `resolver/cache.go` + fetchers.
  Return transient on an empty decoded slice.
- **[Go]** Test gaps on the riskiest branches: floating-IP bind/unbind/deferred helpers,
  cluster adoption guard (0/1/2 matches), AZ-echo zombie path, version-downgrade.

### P3 — consistency / cleanup

- **[BOTH]** Trivial helpers duplicated (`ptrEqString`/`stringPtr`/`derefString`/
  `derefBool`) across s3bucket/containerregistry/project/compute/kubernetes/sshkey →
  hoist to `shared/ptr.go`.
- **[Go]** `network/controller.go` builds 2 unused resolver caches (Network/FloatingIP
  connectors don't use them) → drop.
- **[Go]** Active/failed-state heuristic triplicated with *different* token sets
  (cluster/nodepool/addon) → shared `isActiveState`/`isFailedState`.
- **[Go]** Nodepool `Available()` with zero declared/zero nodes (`0<0`=false → Available
  on an empty node list) → guard the `declared==0 && len(nodes)==0` case.
- **[Go]** S3 `presetID`/`projectID` are `*float32` (precision loss >~16.7M);
  registry uses `int` — note the generated-client constraint.
- **[Go]** `NodepoolAutoscaling.MinSize/MaxSize` enforce `Minimum=2` even when
  autoscaling is disabled → fold the bound into the enabled-gated CEL.
- **[Go]** Stale type-comments operators copy: `K8sVersion` example `1.31.2` (real:
  `v1.31.x+k0s.0`), Server Update godoc lists non-existent `hostname` (→ FR-016).
- **[Go]** `nodepool` uses `xpv2.ReasonCreating` as a `ReadyFalse` reason (inconsistent
  with the shared reason constants); `errIs` wrapper in `dimensions.go` is redundant.
