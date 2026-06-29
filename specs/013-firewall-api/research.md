# Research — Firewall (feature 013)

Phase 0 research. The Timeweb firewall surface is **already documented** in
`docs/openapi-timeweb.json` (paths + schemas under the `Firewall` tag), so most facts are read
directly from the spec; the gaps were **live-verified against the production API on
2026-06-28** with the account Bearer token (read-only probes). Items below record decisions,
rationale, and what was confirmed vs. left for implementation-time re-observation.

## R-1 — API surface: documented `/api/v1/firewall/*` (CONFIRMED)

**Decision**: Use the documented firewall REST surface; no new protocol, no AWS SDK.

| Endpoint | Method | Purpose | Used by |
|---|---|---|---|
| `/api/v1/firewall/groups` | GET | list groups (`{response_id, meta.total, groups[]}`) | adoption guard |
| `/api/v1/firewall/groups` | POST `?policy=DROP\|ACCEPT` body `{name, description}` | create group → `{group}` | Create |
| `/api/v1/firewall/groups/{id}` | GET | group (`firewall-group`: id, name, description, policy, timestamps) | Observe |
| `/api/v1/firewall/groups/{id}` | PATCH body `{name, description}` | rename / re-describe (**no policy**) | Update |
| `/api/v1/firewall/groups/{id}` | DELETE | delete group | Delete |
| `/api/v1/firewall/groups/{id}/rules` | GET | list rules | Observe |
| `/api/v1/firewall/groups/{id}/rules` | POST body `{direction, protocol, port, cidr, description}` | add rule → `{rule}` | Create/Update |
| `/api/v1/firewall/groups/{id}/rules/{rule_id}` | PATCH / DELETE | edit / remove rule | Update |
| `/api/v1/firewall/groups/{id}/resources` | GET | list attached resources (`{id, type}[]`) | Observe |
| `/api/v1/firewall/groups/{id}/resources/{resource_id}` | POST `?resource_type=…` | attach a service | Create/Update |
| `/api/v1/firewall/groups/{id}/resources/{resource_id}` | DELETE `?resource_type=…` | detach a service | Update/Delete |
| `/api/v1/firewall/service/{resource_type}/{resource_id}` | GET | groups a resource belongs to (reverse lookup) | exclusivity check |

**Rationale**: Confirmed `GET /firewall/groups` returns the documented envelope live (this
account: `meta.total=0`). Group/rule/resource shapes read from the spec. The group is
**account-global** (no project field in the create body).

## R-2 — Rule shape (CONFIRMED from spec)

**Decision**: A rule is `{direction, protocol, port, cidr, description}`.

| Field | Type | Notes |
|---|---|---|
| `direction` | enum `ingress` \| `egress` | required (maps to spec's inbound/outbound) |
| `protocol` | enum `tcp` \| `udp` \| `icmp` | required |
| `port` | string | "port or range, for tcp/udp"; **omit for icmp** |
| `cidr` | string (IPv4 or IPv6) | address/subnet; `0.0.0.0/0` = "all addresses" |
| `description` | string | optional per-rule comment |
| `id`, `group_id` | string (out only) | upstream identity, not operator-set |

**Quirk / implementation note**: `port` is a **string** ("22"); the **range delimiter** (likely
`"8000-9000"`) is not pinned in the spec — verify the exact spelling on first live create.
Rules have **no stable natural key** beyond the `{direction, protocol, port, cidr}` tuple, so the
controller matches rules by that tuple (see R-6), not by upstream `id`.

## R-3 — Service attachment: opaque `{id, type}`, exclusive (CONFIRMED + enum CORRECTED)

**Decision**: Attach by `resource_id` (path, **string**) + `resource_type` (query). v1 uses
`resource_type=balancer`. The attachment is **opaque** — no Crossplane cross-MR reference.

**Live correction**: the published `ResourceType` enum lists only `server`, but the API actually
accepts **`server | dbaas | balancer | app`** — proven by forcing the enum-validation error:

```
GET /api/v1/firewall/service/__invalid__/x  →  400 validation_error
  "resource_type value is not a valid enumeration member; permitted: 'server','dbaas','balancer','app'"
GET /api/v1/firewall/service/balancer/test-id → 200   (load balancer = "balancer")
GET /api/v1/firewall/service/load_balancer/…  → 400   (NOT "load_balancer")
```

**Rationale**: matches the dashboard, whose service picker listed only `k8s-lb_*` load balancers
(the environment has no cloud servers — feature owner, 2026-06-28). The path `resource_id` is a
**string**, accommodating both integer server ids and the `k8s-lb_<uuid>` balancer ids.

**Quirk**: the `firewall-group-resource` out-schema types `id` as **integer**, but balancer ids
are UUID-ish strings — a published-spec inconsistency. The controller treats the attachment id as
an **opaque string** on both read and write; verify the `GET …/resources` id rendering for
balancers at implementation time.

**Alternatives considered**: typed `serverRef`/`balancerRef` cross-MR references — rejected for
v1: the provider has no `LoadBalancer` kind to reference (the `k8s-lb_*` objects are created as a
side effect of Kubernetes clusters), and the environment uses no servers. Opaque `{id, type}` is
additive-compatible with typed refs later.

## R-4 — 1:1 exclusivity (CONFIRMED from UI; API behaviour to verify)

**Decision**: Model attachment as exclusive — a service belongs to **at most one** group. The
controller never silently moves a service; if attach fails because the service is bound
elsewhere, surface a terminal `ServiceConflict` condition.

**Rationale**: the dashboard greys out already-bound services ("привязаны к другой группе
правил"). The `GET /firewall/service/{type}/{id}` reverse lookup lets the controller see a
service's current group(s).

**Probe-at-implementation**: the exact failure mode of `POST …/resources/{id}` when the service
is already attached elsewhere (HTTP 409 vs 400 vs silent move) is not documented — confirm on
first live attach and map to `ServiceConflict` (terminal) vs transient accordingly.

## R-5 — Policy: DROP (default-deny) vs ACCEPT, create-only (CONFIRMED)

**Decision**: Expose `policy` as an enum `DROP` (default) | `ACCEPT`. Set at create via the
`?policy=` query param; **immutable** thereafter.

**Rationale**: `POST /firewall/groups` takes `?policy=DROP|ACCEPT`; the **group PATCH body is
`{name, description}` only** — no policy — so policy cannot be changed in place. `DROP` is the
default-deny allow-list (the dashboard's "Разрешающий" / "remaining traffic blocked"), matching
the spec. `ACCEPT` (default-allow) is exposed for completeness at no extra cost. Enforce
immutability in `Update` via `shared.FirstImmutableDiff` / `RejectImmutableChange` (the `name`
pattern), not CEL.

## R-6 — Convergence: Observe-authority + set-diff (DECIDED, mirrors Router)

**Decision**: `Observe` is the sole convergence authority. Up-to-date ⇔ group `name`/`description`
match **and** the rule **set** matches **and** the attachment **set** matches.

- **Rules** diffed as a set of canonical `{direction, protocol, normalizedPort, cidr, description}`
  tuples (order-insensitive). `Update` adds missing rules (POST) and deletes extras (DELETE by
  upstream `rule_id`); a description-only change is delete+create (or PATCH as an optional
  optimization). Duplicate declared tuples → terminal `InvalidConfiguration` (FR-013).
- **Attachments** diffed as a set of `{id, type}`. `Update` attaches missing (POST) and detaches
  extras (DELETE), **paced** (`maxFirewallMutationsPerReconcile`, Router's pattern) to respect the
  rate limiter.
- `Update` returns **without** claiming convergence; the next `Observe` re-verifies (the
  provider's "2xx ≠ converged" rule).

**Rationale**: identical to Router's paced one-pass Update + Observe re-verification; rules and
attachments are unordered server-side, so set semantics avoid needless churn.

## R-7 — Deletion safety (DECIDED)

**Decision**: `Delete` issues `DELETE /firewall/groups/{id}` and treats 404 as success. Connect
does **no** Kubernetes work on the delete path (there are no cross-MR refs to resolve — opaque
attachments are literals), so a Firewall can never wedge its finalizer on a missing dependency.

**Rationale**: this is the Router/S3User lesson (`project_ref_gate_must_not_block_delete`) applied
pre-emptively — though here it's structurally free because attachments aren't references.
**Probe-at-implementation**: whether `DELETE group` auto-detaches its resources or requires
explicit pre-detach — Router's `DELETE` cascades; assume firewall does too and verify (if not,
detach-then-delete).

## R-8 — Client strategy: hand-written, no regen (DECIDED)

**Decision**: Implement the endpoints by hand in `internal/clients/timeweb/firewall.go` using the
established `doV2(ctx, method, path, body)` helper (see `storages_users_v2.go`), returning
`*http.Response` for `timeweb.Classify` + `DecodeBody`. **No oapi-codegen regen.**

**Rationale**: regenerating would require adding the `Firewall` tag to the Makefile
`-include-tags` list **and** re-applying the superset patch for the stale `ResourceType` enum
(`project_openapi_handpatched_superset`); hand-writing ~10 small methods is lower-risk and matches
how the Router and S3User-v2 surfaces were built. The controller sends `resource_type=balancer` as
a plain query string, so the generated enum is irrelevant. Optionally patch the openapi
`ResourceType` enum to `server|dbaas|balancer|app` for documentation fidelity — not required for
the build.

**Alternatives considered**: full oapi-codegen of the `Firewall` tag — viable but drags the
stale-enum patch and tag-list churn for no functional gain.

## Carry-forward conventions (from the Router study)

- **Setup**: `managed.NewReconciler` + `WithManagementPolicies()` + `ratelimiter.NewController()`;
  registered as `networkctrl.SetupFirewall(mgr, log, pollInterval)` in `cmd/provider/main.go`.
  **No `Watches`** (no selector/cross-MR dependency).
- **external-name** = group UUID, set verbatim via `meta.SetExternalName` (not numeric-encoded).
- **Errors**: `timeweb.Classify` (404→`ErrNotFound`; 408/409/425/429/5xx→`TransientError`; other
  4xx→`APIError`) + `ClassifyNetworkError`. Add a `ServiceConflict` reason for exclusivity.
- **Conditions**: `shared.RecordConditionChange`, `SyncedFalse`/`ReadyFalse`, reason constants;
  immutable `policy`/`name` via `shared.FirstImmutableDiff` + `RejectImmutableChange`.
- **Tests**: four-case with the `timeweb` fake (`internal/clients/timeweb/fake.go`).
