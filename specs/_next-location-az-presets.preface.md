# Preface — Next spec (007): Location/AZ model unification + preset-slug simplification

**Status:** seed for `/speckit-specify` (will auto-number to **007**). Not a spec yet.
**Created:** 2026-06-17, during 006 e2e. **Author direction:** make placement consistent
"like other providers"; verify the AZ-vs-location assumption against the live API
before committing a model.

> **How to use:** run `/speckit-specify` and pass the **Feature description** block
> below as the argument (or `/speckit-specify "$(sed -n '/^## Feature description/,/^## /p' specs/_next-location-az-presets.preface.md)"`).
> The Findings + Evidence sections are the clarifications the planner should bake in.

---

## Feature description (paste into /speckit-specify)

Unify the placement model across all Timeweb managed resources and simplify how
operators name presets. Today some MRs require `location` (Server, Network,
FloatingIP) while others require `availabilityZone` (Router, KubernetesCluster),
and every preset slug redundantly re-encodes the location it already carries
(e.g. `ssd-15-ru-1` on an MR that also sets `location: ru-1`). Operators want:
(1) one consistent, required placement field on every MR — `location` — mirroring
`region` in the AWS/GCP providers, with `availabilityZone` as an optional finer
selector; (2) preset slugs that drop the redundant location suffix (`ssd-15`),
while still accepting the long form for backward compatibility; (3) a precise
"wrong preset" error that lists the presets **available for that location** in the
simplified form. The resolver must filter the live catalog by the operator's
placement and reject unknown slugs with the scoped list. Region↔zone data must
come from the authoritative `/api/v2/locations` endpoint rather than a hardcoded
table, so all eight regions and their zones are supported.

---

## Findings (live-API verified — these are clarifications, not open questions)

**F-1 — `availability_zone` and `location` are NOT the same thing.** They are a
strict two-tier hierarchy (region → zones), confirmed via `GET /api/v2/locations`:

| location | location_code | availability_zones |
|---|---|---|
| ru-1 | RU | **spb-1, spb-2, spb-3, spb-4, spb-5** (five!) |
| ru-2 | RU | nsk-1 |
| ru-3 | RU | msk-1 |
| nl-1 | NL | ams-1 |
| de-1 | DE | fra-1 |
| pl-1 | PL | gdn-1 |
| kz-1 | KZ | ala-1 |
| us-4 | US | buf-2 |

`ru-1` having five zones disproves "same thing, different name." The relationship
is **1 location → many zones**; the reverse (zone → location) is a clean function,
which is all the existing `azLocation` map needs — but see F-3.

**F-2 — The two field names are not sloppiness; they track catalog granularity.**
- Server / VPC catalogs are keyed by **`location`** only (no `availability_zone`
  in the preset/configurator payloads).
- K8s-preset and router-tier catalogs are **zone-affine**: each entry carries an
  `availability_zone`, and a zone mismatch makes the upstream *silently mis-place*
  the resource (the 006 finding). That's why those kinds currently require the
  zone. In today's catalog each region offers exactly ONE zone for these products
  (ru-3→msk-1, nl-1→ams-1, de-1→fra-1, ru-1→spb-3), so `location` alone is
  currently sufficient to pick the zone — but that can change (ru-1 already has 5
  zones physically; the product is just not sold in spb-1/2/4/5 yet).

**F-3 — Latent bug: AZ coverage is 4/12.** `internal/controller/shared/azlocation.go`
maps only `spb-3, msk-1, ams-1, fra-1`. The `availabilityZone` CRD enums
(`router_types.go:96`, `kubernetescluster_types.go:61`) likewise allow only those
four. So Router/K8s can be placed in only 4 of 8 regions today; `nsk-1`, `gdn-1`,
`ala-1`, `buf-2`, `spb-1/2/4/5` would fail `AZToLocation`. The file's own comment
admits "CRD enum and azLocation table out of sync," and `LocationZones` already
returns a slice "so a multi-AZ region stays an additive change." Sourcing
region↔zone from `/api/v2/locations` (200, authoritative) fixes this for good.

**F-4 — The "list available presets" API already exists.** `PresetNotFoundError`
(`resolver/errors.go`) already carries `ValidSlugs` (capped 20) and renders them.
The catalog fetch is the same `/api/v1/presets/{servers,k8s,routers}` the resolver
already calls. The gap is only that the list is **global and long-form**, not
location-scoped and simplified.

**F-10 — Rationalize printcolumns across all kinds (uniform-ish, operator-first).**
NOTE (verified 2026-06-17): this repo generates CRDs with **`controller-gen`
(kubebuilder)**, NOT upbound/angryjet — so `READY`/`SYNCED`/`AGE` are **NOT
auto-injected**; they come from the kubebuilder markers and removing them would
DROP the columns. (The "double AGE" once seen was a duplicate `printcolumn:AGE`
marker, since fixed — there is no auto-default to collide with here.) So KEEP
declaring `READY`/`SYNCED`/`AGE`, but standardize them identically across all
kinds. The rationalization below is about the OTHER columns; use kubebuilder
`priority=1` (= `-o wide` only) to declutter. Current columns drifted per-kind (enumerated 2026-06-17): `LOCATION`
vs `AZ` inconsistency (ties to F-1..F-4), `PRESET` vs `TIER` for the same concept,
internal ids (`UPSTREAM-ID`/`EXTERNAL-NAME`) cluttering default output, FloatingIP
showing BOTH `BOUND-TO` (int, empty for routers) and `BOUND-UUID` (redundant — a
side-effect of the F-5 add), nodepool lacking a `PUBLIC-IP` column despite the kept
flag. **Proposal — a FIXED column ORDER on every kind:**
`READY, SYNCED, LOCATION, ID, <per-kind extras…>, AGE` — `AGE` is ALWAYS last; the
`READY SYNCED LOCATION ID` prefix is identical across all kinds (omit `LOCATION`/`ID`
only where a kind genuinely has none); `ID` = the unified upstream resource id
(rename `UPSTREAM-ID`→`ID`, fold `EXTERNAL-NAME` into it). The middle
`<per-kind extras>` (≤2–3, operator-useful, between `ID` and `AGE`): `STATE`
(lifecycle kinds — server/cluster/router/nodepool), server `PRESET`/`PUBLIC-IP`,
cluster `K8S-VERSION`, nodepool `DESIRED`/`OBSERVED`+`PUBLIC-IP`, router
`PRESET`(rename TIER)+`NAT`, network `CIDR`, floatingip `IP`/`BOUND-RES`/`BOUND-TO`,
dependent kinds keep their parent (`CLUSTER`/`REGISTRY`). **Wide-only**
(`priority=1`): `AZ` (when distinct from `LOCATION`), secondary bound-id. **Specific fixes:** standardize the
`READY`/`SYNCED`/`AGE` markers identically across kinds (keep them — they're
marker-driven here); collapse FloatingIP `BOUND-TO`+`BOUND-UUID` → one string `BOUND-TO`
rendering whichever id is set (single display field); add `PUBLIC-IP` to nodepool;
unify `LOCATION`/`AZ` and `PRESET`/`TIER` naming with F-1..F-4.

**F-9 — FloatingIP create intermittently exceeds the 30s client timeout (slow
IP allocation, not a hung endpoint) — self-heals on retry.** Observed live in the
bundle-18 run: `POST /api/v1/floating-ips` repeatedly failed with `context
deadline exceeded (Client.Timeout exceeded while awaiting headers)` for ~3 min,
then succeeded (IP `5.129.223.138`). Diagnostic that isolates the cause WITHOUT
creating a resource: an **empty-body POST** returns `400` in ~0.19s (endpoint is
alive and validates instantly) and a GET is ~0.24s — so the endpoint is NOT hung;
the slow part is the **public-IP allocation step in ru-3**, which intermittently
exceeds the `DefaultTimeout = 30s` (`client.go:52`). The provider classifies it
transient and retries correctly, and NO orphan IPs leak (timeout = genuinely
not-created here; verified the live count stayed at baseline). **Optional fix:**
raise the per-request timeout for FloatingIP create (e.g. 30s→60s) to cut retry
churn; keep the transient-retry. **Reusable technique:** to tell "endpoint hung"
from "operation slow," fire an invalid/empty-body POST — a fast 4xx means the
endpoint is responsive and the latency is in the resource-allocation path.

**F-8 — Router Delete: `DeleteRouter` cascades; do NOT detach networks first.
[RESOLVED in 006, 2026-06-17 — the original hypothesis was INVERTED]** The first
guess (detach-then-delete) was WRONG and actively broke teardown: a router
requires ≥1 network, so `DeleteRouterNetwork` on the LAST attachment returns
**`400 Bad Request`**, deadlocking the delete (live: ×115 retries). Live-verified
correct behavior: a plain `DELETE /api/v1/routers/{id}` returns **`200`** and
cascades the network detach itself; the formerly-attached VPCs become deletable
**immediately after** (`204`) — NO `type:bgp` stranding in the normal MR flow.
006's Delete now just calls `DeleteRouter` (the detach loop was removed). The one
genuinely-stuck `e2e-plan-probe-net2` (409 "Network cannot be deleted") was an
**out-of-band manual-probe artifact**, not the controller's flow — it still needs
a Timeweb support ticket, but it is NOT representative.

**F-7 — Router `no_paid` (billing) surfaces as `status: "error"`, mis-classified
as a provisioning failure.** Timeweb pre-charges a month in advance before
creating a resource; if the projected monthly burn would exceed the balance it
refuses with a no-pay state. For **routers** this surfaces in the API as
`status: "error"` (dashboard localizes it "Не оплачен") — there is NO `no_paid`
status string for routers (verified: dumped the full router object, only field is
`status`, value `error`; no `paid`/`pay` key anywhere; no `/orders` endpoint —
404). So `setRouterReadyCondition`'s `case s == "no_paid"` (`router_external.go:649`)
is **dead code for routers**; the `strings.Contains(s,"error")` branch fires
instead → `Ready=False UpstreamFailed: "provisioning failed … delete and
recreate"`, a misleading message for what is actually a billing/headroom issue.
Observed live: account healthy (23.5k RUB, 58-day runway) yet a 450-RUB/mo router
(already the cheapest tier, `router-1x1-1gb`) hit no_paid because the **full e2e
suite runs bundles concurrently**, momentarily pushing total projected monthly
above the balance; the router created during that peak was rejected and is now
terminal `error`. **Fix:** routers can't be distinguished no_paid-vs-genuine-error
from the router object alone — either treat `error` with a message that names the
billing possibility, or cross-check via a billing endpoint. **e2e lesson:** the
full suite stacks month-in-advance reservations; the canary needs either more
balance headroom or serialized/scoped bundle runs.

**F-6 — Router Observe lacks the `starting` short-circuit that Update has →
harmless but noisy reconcile loop.** While a router is `status: starting`, the
requested NAT bind + DHCP can't apply (upstream drops writes), so
`isRouterUpToDate` permanently reports drift and `Observe` returns
`ResourceUpToDate: false` every poll. Crossplane then calls `Update` every ~54s;
`Update` *does* short-circuit on `starting` (`router_external.go:220`, returns
nil with ZERO API writes) — so the router is NOT disturbed — but the managed
reconciler still emits `UpdatedExternalResource` each cycle (observed x14/12m in
the 006 e2e). Cosmetic + one wasted GET per poll. **Fix:** mirror the Update guard
in Observe — when `router.Status == "starting"` (still `Creating`), skip
`isRouterUpToDate` and return `ResourceUpToDate: true`. Also seen alongside a
transient `preset not found (valid: <none>)` x77 before create succeeded — smells
like the shared resolver cache briefly storing an empty/failed `/presets/routers`
fetch; verify the cache never memoizes empty/error results (ties to the resolver
work in this spec).

**F-5 — FloatingIP `bound_to` can't show a router (UUID-keyed binding).** When a
FloatingIP is attached to a Router, the upstream returns `resource_type: "router"`
+ `resource_id: "<uuid>"` (e.g. `e392e073-…`), with `is_bound`/`bound_to` null.
Timeweb models `resource_id` as a union (int64 OR string-UUID). The controller
(`floatingip_external.go:245`) only decodes the **int64** variant
(`AsFloatingIpResourceId0`); routers use the **UUID** variant
(`AsFloatingIpResourceId1`), so `ObservedBoundTo.ResourceID` (`*int64`) stays nil
and the `BOUND-TO` printcolumn renders blank (`BOUND-RES` still shows `router`).
Diagnostic-only — the binding itself works (the comment at line 231 says
observedBoundTo is "purely diagnostic"). Also: the `ResourceType` doc comment
(`floatingip_types.go:84`) lists only `server/balancer/database/network` — "router"
must be added. Verified live 2026-06-17 on the 006 private-cluster egress IP.

## Proposed scope (for /speckit-plan to refine)

1. **`PresetInput` gains `Location`** (parallel to existing `Zone`); `resolve.go`
   drops entries whose location ≠ input before slug matching (copy of the Zone
   filter at `resolve.go:77`).
2. **Slug simplification (`slug.go`)** — match on the bare `<short>` after
   location-filtering; keep accepting `<short>-<location>` and the `-<id>`
   disambiguator → backward compatible, no manifest breakage.
3. **Location-scoped not-found** — build `ValidSlugs` from the location-filtered
   entries in simplified form: `preset "x" not found in location "ru-1" —
   available: ssd-15, ssd-30, …`.
4. **Uniform placement shape** — required `location` + optional `availabilityZone`
   on every MR. Router/K8s switch their required field to `location`; when only
   `location` is given and the catalog offers >1 zone for that product/region,
   **error asking for `availabilityZone`** (never silently mis-place). Server/
   Network already match this shape.
5. **Live region↔zone** — replace the hardcoded `azLocation` table with a
   `/api/v2/locations`-backed lookup (cached like other catalog reads); widen/derive
   the `availabilityZone` enums or move validation into the resolver.
6. **Cosmetic** — simplify `examples/`, `docs/`, and `test/e2e/scripts/kuttl.sh`
   slug discovery (drop the `-$location` concat).
8. **Router Observe `starting` guard (F-6)** — short-circuit `isRouterUpToDate`
   when `router.Status == "starting"` (return `ResourceUpToDate: true`), mirroring
   the existing Update guard, to kill the no-op reconcile/event loop. Audit the
   shared resolver cache to ensure empty/error catalog fetches are never memoized.
7. **FloatingIP router-binding diagnostic (F-5)** — add a string identifier to
   `FloatingIPBindingObservation` (e.g. `resourceUUID *string`), decode the
   `AsFloatingIpResourceId1()` (UUID) variant in `populateFloatingIPStatus`, and
   make the `BOUND-TO` printcolumn a `string` that renders whichever id variant is
   set. Add `router` to the `ResourceType` doc enum (`floatingip_types.go:84`).

Blast radius (preset call sites): `server_external.go` (×2), `router_external.go`,
`cluster_external.go`, `nodepool_external.go`, plus `resolver/` and the enums/CEL.

## Open decisions (for /speckit-clarify)

- **D-1:** Required `location` + optional `availabilityZone` everywhere (recommended),
  vs. keep the per-kind split and only fix F-3 + do the slug simplification.
- **D-2:** Hard-require explicit `availabilityZone` for zone-affine kinds when a
  region exposes >1 *offered* zone, vs. always derive + default.
- **D-3:** Backward-compat window for long-form slugs — accept forever, or accept
  with a deprecation note.

## Non-goals

- Merging `location` and `availabilityZone` into one field (F-1 forbids it).
- Multi-AZ scheduling / spreading. This is about *naming + validation*, not placement
  strategy.
- Changing the upstream catalog or how zone-affinity mis-placement is detected
  (006 already handles the zone-echo check).

## Evidence appendix (commands, account `gorodvkarmane13`, 2026-06-17)

- `GET /api/v2/locations` → the F-1 table (8 regions; ru-1 has 5 zones).
- `GET /api/v1/presets/servers` → locations ru-1/ru-2/ru-3/nl-1/de-1/kz-1/us-4,
  **no** `availability_zone` field.
- `GET /api/v1/presets/k8s` → both `location` + `availability_zone`, 1:1 over the
  4 offered zones (spb-3/msk-1/ams-1/fra-1).
- `GET /api/v2/vpcs` → every VPC carries `location` + `availability_zone` in lockstep.
- `azlocation.go` map = {spb-3,msk-1,ams-1,fra-1} (4/12). Enums at
  `router_types.go:96`, `kubernetescluster_types.go:61` = same 4.
