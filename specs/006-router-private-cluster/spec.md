# Feature Specification: Router & Private Kubernetes Cluster Networking

**Feature Branch**: `006-router-private-cluster`

**Created**: 2026-06-10

**Status**: Draft

**Input**: User description: "Router & private worker nodes — Timeweb managed-K8s worker nodes come up with public IPs and there is no per-nodepool or per-cluster knob to prevent it. Private-only nodes are achieved by putting the cluster's network behind Timeweb's Router product, which provides per-network NAT egress. Public-by-default stays; the provider gains the Router as a first-class kind. CRaaS pull-secrets are NOT part of this feature — the feature is the private cluster."

## Clarifications

### Session 2026-06-10

Write-side discovery (authorized live probe against the production account, plus
the feature owner's devtools capture of the dashboard's create request) resolved
most of the spec's original "write-side unknown" prerequisite. Facts integrated
throughout this spec:

- Create takes: name (1–250 chars), a size tier id, **at least one network
  attachment** (a bare router cannot exist), and optionally: comment, project,
  public-IP orders (a `create_ip` sentinel allocates a new address), and
  per-attachment gateway / reserved addresses.
- There is **no zone input at create** — the zone is implied by the size tier,
  whose catalog is per-region (the dashboard's region step filters tiers). Size
  tiers also carry a node count (1 or 2 nodes), i.e. HA is a property of the
  chosen tier.
- **Public internet addresses are a separately billed add-on** on the router;
  NAT for a network requires one. Requesting NAT on an attachment without any
  public IP is **silently accepted with NAT left off** (no error).
- Rename/comment converge via partial update; updates sent while the router is
  still starting are **silently dropped**; an empty attachment list in an
  update is **silently ignored** (does not detach).
- Deleting a router succeeds while networks are attached; the networks survive
  detached. Validation errors enumerate missing/invalid fields precisely;
  unknown attachment targets fail with a specific "network not found" error.
- Still uncaptured (narrowed prerequisite): post-create public-IP add/remove,
  NAT bind/unbind on a live router, network attach/detach on a live router, and
  the Kubernetes-cluster binding operation (the dashboard's "router
  integration" on the cluster's network tab).

- Q: How are router public IPs modeled — explicit list, implicit auto-order,
  or v1-minimal single toggle? → A: Neither — reuse the existing FloatingIP
  resource (feature 003): the operator orders a FloatingIP and the Router
  references it (by resource reference, selector, or raw ID, per the
  provider's established cross-resource idiom). The Router never orders IPs
  itself; NAT on an attachment requires a referenced FloatingIP, and
  NAT-without-an-IP is rejected at admission instead of being silently
  ignored the way the upstream does.

- Q: Is the size tier changeable on a live router (in-place resize) or
  create-time immutable? → A: In-place resize is in scope: a tier edit on a
  live Router converges via the upstream's resize operation. The resize
  request shape joins the write-side capture prerequisites; only if the
  upstream turns out to have no resize operation does the provider fall back
  to rejecting tier edits with a clear "recreate required" status.

- Capture follow-up (2026-06-11): the owner captured the dashboard's
  create-cluster-with-network request; an isolation probe confirmed the
  private-cluster mechanism and a root cause. (1) Worker groups have an
  upstream **`public_ip_enabled`** flag (hidden from the published API
  description) — private nodes are an explicit per-nodepool choice, defaulted
  ON to preserve current behavior; the router provides their egress. No
  explicit router↔cluster binding operation exists or is needed. (2) K8s size
  presets carry **hidden zone affinity**; a preset whose zone mismatches the
  requested availability zone mis-places the cluster into ams-1 as a broken
  half-created "zombie" instead of being rejected — so preset selection must
  be zone-filtered exactly like configurator selection (this also fixes a
  latent feature-004 bug), and creates must be guarded against
  error-yet-created responses.

- Q: When a Router serving a bound Kubernetes cluster is deleted, does the
  provider refuse, pass through, or refuse only for K8s bindings? → A:
  Determine empirically first — during the planning-phase binding capture
  (which already requires a disposable cluster+router pairing), test whether
  the upstream itself blocks deleting a bound router. If the upstream blocks,
  pass its refusal through verbatim; if it does not, the provider refuses
  deletion while the binding exists, keeping deletion pending with a status
  naming the bound cluster. Plain attached networks never block deletion
  (probe-verified harmless: networks survive detached). The production
  router/cluster pair is NOT used for this experiment.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Declare a Router (Priority: P1)

A platform operator declares a Router resource in a Kubernetes manifest — a name, an availability zone, a size tier (which also determines node count: 1-node or 2-node HA), at least one attached private network (an upstream constraint: a router cannot exist without one), and optionally a project to bill it under. The provider creates the router in Timeweb Cloud, reports it ready when the upstream router is running, keeps it reconciled (rename/comment drift converges), and deletes it from Timeweb when the manifest is deleted.

**Why this priority**: The Router is the foundation every other story builds on. It is independently valuable on its own — operators already use routers for non-Kubernetes private networking (NAT egress for plain cloud servers) — and it is the smallest slice that exercises the full lifecycle against the upstream API.

**Independent Test**: Apply a Router manifest with zone + size + one network → resource reports ready and the router appears running in the Timeweb dashboard; delete the manifest → the router is removed upstream (the network survives) with nothing left billing.

**Acceptance Scenarios**:

1. **Given** valid credentials and a Router manifest with a zone, a size, and one attached network, **When** the operator applies it, **Then** the router is created upstream and the resource reports both "synced" and "ready" once the router is running.
2. **Given** a ready Router, **When** the operator changes its display name or comment, **Then** the change converges upstream without recreating the router.
3. **Given** a ready Router, **When** the operator deletes the manifest, **Then** the upstream router is deleted and no billable remnant remains.
4. **Given** a Router manifest naming a size that does not exist in the requested zone, **Then** the resource reports a clear, actionable "not available" status naming what was requested, and nothing is created upstream.
5. **Given** an existing router created outside the provider, **When** the operator imports it by its upstream identifier, **Then** the provider adopts it without creating a duplicate.

---

### User Story 2 - Attach private networks with per-network NAT (Priority: P2)

The operator connects one or more existing private networks (the provider's Network resources from feature 003, or pre-existing networks referenced by ID) to a Router, choosing per network whether it gets NAT egress to the internet and whether DHCP is served on it. The resource status mirrors the dashboard: each attachment shows its gateway, whether NAT is on, and which public IP serves it.

**Why this priority**: Attachment + NAT is what makes a router useful. It is separable from User Story 1 (a bare router is already a managed resource) and from User Story 3 (NAT'd networks serve plain servers, not only Kubernetes).

**Independent Test**: Declare a Router with one attached network with NAT enabled and one without → status shows a public NAT address on the first and none on the second, matching the dashboard; flipping the NAT toggle converges without recreating anything.

**Acceptance Scenarios**:

1. **Given** a ready Router and an existing private network in the same region, **When** the operator declares the attachment with NAT enabled, **Then** the network is attached upstream and the resource status shows the network's gateway and its public NAT address.
2. **Given** an attached network with NAT enabled, **When** the operator disables NAT for it, **Then** the change converges and status shows no NAT address for that network.
3. **Given** an attached network, **When** the operator removes it from the manifest, **Then** the network is detached upstream (the network itself is not deleted).
4. **Given** a Router and a network in different regions, **When** the operator declares the attachment, **Then** the mismatch is rejected or surfaced as an actionable error before anything is half-created upstream.

---

### User Story 3 - Private Kubernetes cluster (Priority: P2)

The operator declares a Kubernetes cluster whose worker nodes have **no public IPs**: the cluster's network sits behind a Router that provides NAT egress, so nodes can pull images and reach the internet outbound while remaining unreachable from it. Worker nodes keep coming up with public IPs **by default** — private-only is an explicit opt-in arrangement, and the documented default behavior does not change.

**Why this priority**: This is the end goal that motivated the feature ("feature private cluster"), but it depends on Stories 1–2 and on the existing cluster/network kinds, so it lands after them.

**Independent Test**: Declare network + router (NAT on) + cluster + nodepool wired together per the documented private-cluster arrangement → all resources ready; the nodepool's node list shows local IPs only (no external address per node); outbound connectivity from nodes works.

**Acceptance Scenarios**:

1. **Given** a router-backed private network, **When** the operator declares a cluster attached to that network in the documented private arrangement plus a nodepool, **Then** all resources reach ready and every worker node reports a local IP and **no** public IP.
2. **Given** a private cluster, **When** a workload on a worker node initiates an outbound connection, **Then** it egresses via the router's NAT address.
3. **Given** a cluster and nodepool declared **without** any router arrangement, **Then** behavior is unchanged from today: nodes come up with public IPs (the default), exactly as documented.
4. **Given** a router still serving a live cluster's network, **When** the operator deletes the Router manifest, **Then** deletion does not silently break the cluster: the dependency is surfaced (the deletion is blocked or clearly reported) rather than executed as a silent teardown.

---

### Edge Cases

- Router zone vs. attached-network region mismatch — must be caught before creating/attaching (the upstream is known to mis-place rather than reject mismatched inputs; see feature 005's availability-zone findings).
- NAT requested on an attachment while the router has no public internet address — the upstream silently leaves NAT off (verified by probe). The provider must not report success-with-NAT when NAT is actually off; the dependency must be made explicit.
- Updates sent while the router is still starting are silently dropped upstream (verified by probe) — convergence must be verified by re-observation, never assumed from a successful update response.
- An empty attachment list in an upstream update is silently ignored rather than detaching everything (verified by probe) — detachment must use whatever explicit mechanism the remaining write-capture reveals, and "I asked for zero attachments" must not be reported as converged when one remains.
- The upstream API has been observed to return an error **yet still create the resource** (HTTP 500 on a cluster create that materialized anyway). Creates must be written so a failed-looking response does not produce an unmanaged billable orphan.
- A router that enters a failed/error state upstream must surface as a clear "upstream failed" status, not an eternal "creating" (same pattern as clusters and worker nodes from feature 005).
- An account without funds/quota for the router's size must surface the established "payment required" status rather than appearing stuck.
- Attaching a network already attached to another router — surface the upstream rejection verbatim and actionably.
- Deleting a Router with networks still attached — detach-then-delete ordering must not leave the upstream in a half-detached state.
- Importing a router that already has attachments — the declared attachment list must reconcile against (not blindly overwrite) what exists upstream.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Operators MUST be able to declaratively create, observe, and delete a Timeweb Router with: display name, optional comment, availability zone, size tier, **at least one network attachment** (required at creation by the upstream; enforced at admission), and optional project assignment (by reference to the provider's Project resource or by raw ID, consistent with existing kinds).
- **FR-002**: Router size MUST be selectable the same way operators size other resources of this provider in its v1 form: by a size-tier identifier validated against the live upstream catalog, with an unknown or zone-unavailable size reported as an actionable error before any create. Size tiers carry the node count (1-node or 2-node HA) — HA is selected via the tier, not a separate field.
- **FR-002a**: Editing the size tier on a live Router MUST converge in place via the upstream's resize operation (including 1-node ↔ 2-node HA changes if the upstream supports them); readiness reflects the resize while it is in progress. Fallback only if the upstream proves to have no resize operation: reject tier edits with a clear "recreate required" status using the provider's established immutability vocabulary.
- **FR-003**: The Router's zone vocabulary MUST be the availability-zone terms operators already use on Kubernetes clusters (e.g. `msk-1`, `spb-3`). The upstream derives the router's zone from the chosen size tier (tiers are per-region), so the provider MUST resolve the operator's declared zone to a tier of that region and reject zone/tier or zone/network mismatches before creating or attaching (lesson from feature 005: the upstream mis-places rather than rejects).
- **FR-004**: Operators MUST be able to declare network attachments on a Router — each referencing an existing private network (by resource reference or raw ID) with per-attachment settings: an optional NAT address reference, DHCP on/off, and optional gateway address / reserved addresses. Attachments MUST be addable, removable, and toggleable on a live router without recreating it; convergence is judged solely by re-observation of upstream state (the upstream silently ignores some update shapes; a successful response is not proof of convergence).
- **FR-004a**: NAT is declared per attachment as a reference to the provider's existing FloatingIP resource (by reference or raw address) — absence means NAT off, presence names exactly which address serves that network (one IP per network), and the Router itself never orders or releases addresses. Because the address reference IS the NAT declaration, NAT-without-an-address is structurally impossible (the upstream's silent-NAT-off footgun cannot be expressed).
- **FR-005**: Removing an attachment MUST detach the network upstream without deleting the network itself.
- **FR-006**: The Router resource's status MUST mirror what the dashboard shows: upstream state, per-attached-network gateway / NAT address / DHCP state, and the router's public IPs with which network each one serves.
- **FR-007**: The provider MUST support a declarative, repeatable private-cluster setup using only its own resources: the nodepool gains a public-IP switch (**default: public**, preserving current behavior) and a private nodepool's cluster network sits behind a NAT-enabled Router for egress. No explicit router↔cluster binding step exists or is required (verified); the router's status reflects the served cluster.
- **FR-007a**: Kubernetes size-preset selection (master and worker) MUST be zone-filtered against the preset catalog's zone affinity before any create — a zone-mismatched preset makes the upstream mis-place the cluster as a broken half-created resource instead of rejecting (verified). This corrects a latent defect in the existing preset path and applies the same location-first rule already used for configurator sizing.
- **FR-007b**: Cluster creation MUST tolerate the upstream's error-yet-created responses: a create that returns an error but materializes upstream is adopted on the next reconcile, never duplicated.
- **FR-008**: Worker-node networking defaults MUST NOT change: without an explicit router arrangement, nodes keep coming up with public IPs, and the operator documentation MUST present both the default and the private-only path side by side.
- **FR-009**: An existing router created outside the provider MUST be importable by its upstream identifier and adopted without duplication, including reconciling its already-present attachments.
- **FR-010**: Failure states MUST be surfaced with the provider's established vocabulary: upstream failed-state → "upstream failed" (terminal, operator action needed); missing funds/quota → "payment required"; unsatisfiable size → actionable "not available" naming the request. Both "synced" and "ready" must be meaningful — ready only when the upstream router is actually running.
- **FR-011**: Create operations MUST be safe against the upstream's observed error-yet-created behavior: a create that returns an error but materializes upstream must be adopted on the next reconcile rather than duplicated or orphaned.
- **FR-012**: Deleting a Router bound to a Kubernetes cluster MUST NOT silently cut the cluster's egress. Behavior is settled empirically during planning (see Clarifications): if the upstream blocks deletion of a bound router, its refusal is surfaced verbatim; otherwise the provider keeps deletion pending with a status naming the bound cluster until the binding is removed. Plain attached networks do not block deletion (probe-verified: they survive detached).

### Key Entities

- **Router**: a billable, sized network appliance in one availability zone. Attributes: name, comment, zone, size tier, project, upstream state, public IPs. Owns a set of network attachments.
- **Network attachment**: the link between a Router and one private network. Attributes: target network (reference or ID), NAT on/off (with the serving public address when on), DHCP on/off, gateway address. Lifecycle is subordinate to the Router (modeling as inline list vs. separate resource is a planning decision).
- **Private network** (existing, feature 003): the attachment target. Not owned by this feature; attachments must never delete it.
- **Floating IP** (existing, feature 003): the source of the router's public internet addresses. Referenced from the Router (never ordered by it); a NAT-enabled attachment maps to exactly one referenced floating IP. Whether the upstream treats router addresses and floating IPs as literally the same object is verified during the remaining write-side capture (planning prerequisite).
- **Kubernetes cluster / nodepool** (existing, feature 004/005): the consumer of a router-backed private network in the private-cluster arrangement. The nodepool's existing per-node status list is how "no public IP" is verified.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can go from an empty namespace to a ready Router with one NAT-enabled network using only declarative manifests, in under 5 minutes wall-clock.
- **SC-002**: An operator can stand up a fully private Kubernetes cluster (zero worker nodes with public IPs, outbound internet working) using only this provider's resources, repeatable from the documented walkthrough without consulting the dashboard.
- **SC-003**: Deleting the declared resources of the private-cluster arrangement leaves zero billable remnants upstream (verified against the upstream inventory after teardown).
- **SC-004**: For every attachment, an operator can answer "is NAT on, through which public IP, what's the gateway?" from the resource status alone — without opening the dashboard.
- **SC-005**: 100% of the failure modes enumerated in Edge Cases surface as a status message that names the cause and the corrective action; none manifests as a silent no-op or an eternal "creating".
- **SC-006**: Existing users see zero behavior change: manifests written before this feature produce identical results (public node IPs by default) after it ships.

## Assumptions

- **Remaining write-side captures** (narrowed twice; most of the surface is now probe- or capture-verified — create, rename, attach, detach, per-attachment DHCP, delete, floating-IP equivalence, the cluster create body incl. the worker public-IP flag): only the **NAT toggle** and the **size-tier resize** requests remain to capture; each gates exactly one requirement (FR-004a, FR-002a) and FR-002a has a specified fallback. Captured shapes are hand-patched into the API description, following the feature-005 approach.
- **Size selection is tier/preset-style in v1** (confirmed by probe: create takes a size-tier identifier; the tier carries region and node count). Custom (cores/GB) router sizing is out of scope.
- **Attachments are declared on the Router** (matching the upstream object shape, where the router owns its network list). If planning finds a separate attachment resource is structurally necessary, the operator-facing acceptance scenarios above still apply unchanged.
- **The Kubernetes private binding exists upstream** — evidenced by a live production router bound to a running cluster with private-only nodes (`parent_services` of type `k8s`). The binding's write mechanism is part of the capture prerequisite.
- **Static routes and port forwarding** (further dashboard tabs of the Router product) are out of scope for v1; the feature covers private networks, NAT, DHCP, and the Kubernetes binding only.
- **Container-registry pull-secret integration (CRaaS) is explicitly out of scope** for this feature, per the feature owner's direction; it remains a separately deferred item.
- **One router per network** is assumed to be an upstream constraint (a network shows a single gateway/NAT address); the provider surfaces the upstream's rejection if violated rather than pre-validating it.
- Live verification follows the established practice: short-lived canary resources on a funded account, Russian-region zones (`msk-1`/`spb-3`), cheapest satisfiable sizes, immediate teardown.
