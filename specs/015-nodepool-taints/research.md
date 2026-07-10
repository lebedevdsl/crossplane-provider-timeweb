# Research: Nodepool Taints (+ label mutability)

Phase 0 for `specs/015-nodepool-taints/plan.md`. All spec-level unknowns
resolved; two narrow behaviors are deliberately deferred to the
implementation-phase live gate (they can only be observed through the
running provider) and carry recorded contingencies.

## R-1 — Upstream create surface (RESOLVED, live-verified)

**Decision**: send `taints: [{key, value, effect}]` in the node-group create
body, sibling of the already-wired `labels`.

**Evidence** (operator probe, 2026-07-10, panel host — same backend):
`POST /api/v1/k8s/clusters/1096397/groups` → 201 with
`taints: [{"key":"biba","value":"boba","effect":"NoSchedule"}]` accepted and
echoed verbatim in the created `node_group` object (id 117093).

**Alternatives considered**: none needed — the field exists despite being
absent from the published docs.

## R-2 — Public-API GET exposes taints/labels (RESOLVED, live-verified)

**Decision**: Observe parses `labels` + `taints` from the existing
single-group GET — zero new API traffic.

**Evidence** (this session, 2026-07-10, `api.timeweb.cloud` with bearer
token): `GET /api/v1/k8s/clusters/1096397/groups` returns every group with
`"labels": []` and `"taints": []` arrays (group 114393 shown), confirming
the public host serializes both fields. The single-group GET shares the
`NodeGroupOut` shape.

## R-3 — Day-2 update surface (RESOLVED on panel host; public-host exercise in validation)

**Decision**: implement Update-side convergence as
`PATCH /api/v1/k8s/clusters/{cluster_id}/groups/{group_id}` — an
**undocumented verb** on the documented path (published spec lists only
GET/DELETE there). Hand-patch `docs/openapi-timeweb.json` with the PATCH
operation and regenerate the client (the established
`public_ip_enabled`-style superset treatment).

**Evidence** (operator probe, 2026-07-10): panel issued
`PATCH https://timeweb.cloud/api/v1/k8s/clusters/1096397/groups/117093` →
200; body carried full group state (`name`, `labels`, `taints`,
`public_ip_enabled`, autoscaler/autohealing flags); response echoed the
changed `labels` and the `taints` riding along. Public-host PATCH could not
be probed raw this session; it is the first thing the live validation gate
exercises through the provider. **Contingency**: if the public host rejects
the verb (404/405/403), taints/labels fall back to create-time-immutable
(CEL `self == oldSelf` guards) and the gap is recorded as an upstream
quirk + support ticket per project convention — the additive field itself
ships either way.

## R-4 — PATCH body scope: owned fields only (DECIDED)

**Decision**: the provider's PATCH body carries exactly `name` (echo of the
immutable declared name — the panel always sends it, so it is the safest
"anchor" field for an undocumented endpoint), `labels`, and `taints`. Never
autoscaler fields, never `public_ip_enabled`, never sizing.

**Rationale**: the panel sends full group state, so the endpoint's
merge-vs-replace semantics for *absent* fields are unconfirmed. Sending
unowned fields risks clobbering autoscaler state the controller
deliberately does not own (Update already early-returns on the node count
when autoscaling is enabled). The validation gate asserts that
`node_count` / autoscaling / `public_ip_enabled` survive a metadata-only
PATCH unchanged; if absent-field handling proves destructive, the fallback
is echoing the freshly-observed values of those fields (read in the same
reconcile's GET) — still single-writer for the owned pair.

**Alternatives considered**: full-state echo by default (rejected: widens
the blast radius of a race with the autoscaler for no benefit).

## R-5 — Set-diff semantics (DECIDED)

**Decision**: order-insensitive set comparison.
- **Taints**: identity = (key, effect); equality = (key, value, effect).
  Duplicate (key, effect) pairs are rejected at admission, so the set is
  well-formed by construction. nil value marshals as `"value": ""`
  upstream? — normalized: compare with empty-string coalescing (upstream
  echoes `value` as a string; a value-less declared taint equals an
  upstream `""`).
- **Labels**: declared `map[string]string` ⇄ upstream `[{key,value}]`;
  convert the upstream array to a map and compare (upstream duplicates —
  never observed — would collapse; the subsequent PATCH re-normalizes).
- Up-to-date ⇔ both sets equal. Diff triggers Update's metadata PATCH,
  which sends the FULL declared sets (set-replace, matching the panel's
  observed behavior) — idempotent by construction.

## R-6 — CRD validation design (DECIDED)

**Decision** (per FR-001..004, FR-011 and the CEL cost-budget lesson):
- `taints` — `+optional`, `+kubebuilder:validation:MaxItems=12`.
- `NodepoolTaint.Key` — required, `MinLength=1`, `MaxLength=253`, pattern:
  optional DNS-subdomain prefix + `/` + label-name segment
  (`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`).
- `NodepoolTaint.Value` — optional, `MaxLength=63`, label-value pattern
  (`^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$`).
- `NodepoolTaint.Effect` — required,
  `+kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute`.
- Duplicate guard — type-level CEL on the nodepool:
  `!has(...taints) || taints.all(t, taints.filter(u, u.key == t.key && u.effect == t.effect).size() == 1)`
  — O(n²) over a MaxItems=12 list is well inside the apiserver CEL cost
  budget (the feature-007 lesson: bound the array, then quadratic rules
  are fine). Verified with server-side dry-run during implementation.
- No immutability CEL (mutable by design); existing nodepool XValidations
  untouched.

## R-7 — Update-path ordering & autoscaling interplay (DECIDED)

**Decision**: in `Update`, converge metadata (labels/taints PATCH) **before**
the autoscaling early-return, then keep the existing count-delta logic
untouched. Rationale: FR-006/US2 require tainted autoscaled pools; today's
early-return would otherwise make autoscaled pools permanently
drift-uncorrectable. `isNodepoolUpToDate` gains the metadata comparison so
the runtime actually routes drift into Update (Observe stays authority).

## R-8 — e2e & validation strategy (DECIDED)

**Decision**: author kuttl bundle `22-nodepool-taints` following bundle 13's
shape (cluster + nodepool by ref), asserting taints/labels convergence via
`status`-independent field asserts plus condition-TYPE waits (the
feature-007 kuttl lesson). The live gate for THIS feature runs the lighter
path: `make e2e.up` + `make e2e.deploy` (k3d + side-loaded build), then a
custom manifest attaching a minimal 1-node pool by flat `clusterID` to a
pre-existing Ready cluster — full FR walk (create-with-taints →
node-propagation kubeconfig read → day-2 edit → empty-set clear →
out-of-band drift revert → delete) without provisioning a cluster. The
bundle remains runnable in the standard harness unchanged.

**Node-propagation check — RESOLVED at the gate (2026-07-10, group 117111
on cluster 1096397)**: Timeweb DOES propagate group-level taint/label
changes to already-running nodes — the node transitions through an
`updating` state and comes back with the new sets applied in place (no
recreation; observed for add, modify, and clear). One quirk found:
a **value-less taint** (`value: ""`) is persisted on the group object but
is NOT applied to the node object (a *valued* `PreferNoSchedule` taint
propagates fine, so it is the empty value, not the effect). Documented in
quickstart/docs; upstream ticket candidate per project convention. Also
confirmed at the gate: public-host PATCH applies taints verbatim; the
`value` key is REQUIRED in taint entries (omitting it → 400
`taints.N.value must be a string`) — the provider always sends `""` for
nil, which is accepted.
