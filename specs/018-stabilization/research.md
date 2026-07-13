# Research: Stabilization round 2 — v0.9.0 slice (018)

Phase-0 decisions. Code facts gathered 2026-07-13 against the current tree.

## R-1 — Shared rate budget (FR-001/002/003)

**Fact**: `timeweb.New` is called in 9 `*/connector.go` (one per controller group), inside
`Connect` — i.e. once PER RECONCILE. `New` builds its own `rate.NewLimiter(2,3)` and its own
`http.Client`/transport. So N concurrent reconciles = N independent 2 r/s budgets and N
connection pools → aggregate egress far exceeds the intended host budget → Timeweb 429 /
Qrator egress ban (the reproduced CDN status-freeze).

**Decision**: introduce a process-global, per-host limiter + shared `http.Transport` in the
`timeweb` package (package-level, lazily initialised, keyed by base host). `New` takes the
shared limiter + transport instead of constructing its own; the per-request `authTransport`
(bearer token) still wraps the shared base transport per client, so tokens stay isolated.
Public `New` signature unchanged for callers (the sharing is internal); a `Config` knob may
expose the limiter for tests.

**Alternatives considered**: build ONE `timeweb.Client` in `main` and inject it into every
controller (rejected — the token is per-ProviderConfig/per-reconcile, so a single shared
client can't carry auth; sharing only the limiter+transport is the correct seam). Per-host
map vs single global (there is effectively one host — `api.timeweb.cloud` — so a simple
global limiter suffices; keep a host key for correctness + future multi-host).

## R-2 — S3User credential integrity (FR-004/005/006)

**Fact**: `buildConnection(user, grants)` is returned from Observe (L121), Create (L149), and
Update (L188). `user.SecretKey` comes from `GetStorageUserV2` on Observe/Update — which does
not return the secret key — so steady-state republishes `secret_key: ""`. `dataEndpoint` is a
hardcoded `https://s3.twcstorage.ru`.

**Decision (create-only, spec Q1=B)**: return connection details ONLY from Create; Observe and
Update return empty `ConnectionDetails{}` (a no-op for the runtime — VP-1). The Create response
authoritatively contains the secret key. Region: derive the singular `endpoint`/`region` from
the PRIMARY granted bucket's region (resolved from the referenced S3Bucket), removing the
hardcoded default; endpoint host stays the shared `s3.twcstorage.ru` (region is metadata).
Adopted user whose key is unobtainable → clear condition (`ReasonSecretMissing`-family), never
a blank Secret.

**VP-1 RESOLVED (empirically, 2026-07-13)**: a regression test against the REAL
`managed.NewAPILocalSecretPublisher` (crossplane-runtime v2.3.1) confirms that publishing
EMPTY `ConnectionDetails` from Observe does NOT wipe the secret — the applicator is
APIPatchingApplicator (merge patch) and `corev1.Secret.Data` is `json:"data,omitempty"`, so
nil Data is omitted from the patch and the Create-published keys survive. Strict create-only
is therefore SAFE. Test kept as `internal/controller/s3user/vp1_runtime_test.go`.

## R-3 — Uniform capped requeue (FR-007)

**Fact**: `SetupNetwork` (network/controller.go:48), `SetupFloatingIP` (:77), and
`SetupCluster` (kubernetes/controller.go:58) build their controller WITHOUT
`WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()})`; every other Setup
func has it (Router, Firewall, Nodepool, Addon, S3*, Compute, Project, SSHKey, CR×2, CDN).

**Decision**: add the same `WithOptions(...NewController())` to those three. Behaviour-only
(backoff cap), no logic change.

## R-4 — Dedup (FR-011)

**Fact**: `deriveAdminKeys` is duplicated verbatim in `s3bucket/external.go:171` and
`s3user/connector.go:101` (both derive the account super-user S3 keys at runtime, never
cached).

**Decision**: hoist one `shared.DeriveAdminKeys(ctx, tw)` preserving the never-cache contract;
both callers use it. Scan for the other 014-named patterns (Observe skeleton, ref-resolution
sentinels, condition-record helper, number formatting) and consolidate the ones that are truly
byte-duplicated; behaviour-preserving, and any copy found missing a limiter/not-found handling
is fixed explicitly (surfaced in release notes if behaviour changes).

## R-5 — Docs & examples (FR-008/009/010)

**Decision**: `make validate-examples` server-side dry-run of all `examples/*` → fix any
schema rejections and comments naming nonexistent fields; verify `k8sVersion` example strings
resolve to the live `vX.Y.Z+k0s.N` format; regenerate the printcolumns reference from the
generated CRDs and diff; author `docs/conditions.md` cataloguing every Ready/Synced reason the
controllers set (grep `shared.Reason*` + inline conditions), including the runtime gotcha where
a returned error overrides a set terminal reason with `ReconcileError`.

## R-6 — Record hygiene (FR-012)

**Decision**: tick 009 tasks; mark 011/012/013 specs complete; retire superseded
`specs/_next-*.preface.md` seeds whose work shipped; backfill the plural `buckets` connection-
Secret key into the 012 S3User spec; refresh `CLAUDE.md`.

## R-7 — Pre-release multi-agent review (2026-07-13)

Three specialized reviews (CISO / DevOps / Go) run on the diff before release:
- **CISO**: clean, ship. No Critical/High/Medium. Confirmed no token/secret in
  logs/status/events; shared transport is auth-isolated (per-request clone);
  create-only cannot lose a credential; admin-key never-cache holds.
- **DevOps**: rate-limiter fix sound + complete; capped-backoff gaps were exactly
  the 3 Setups fixed; VP-1 independently confirmed. Follow-up: the `rgwiam` IAM
  client (`panel.s3.twcstorage.ru`) is NOT under the shared limiter (different
  host) — seeded in 014 as FR-005b.
- **Go review**: found ONE real bug — the adopted-no-key `SecretMissing` condition
  set in Create was CLOBBERED by the runtime's post-Create `Synced=True/
  ReconcileSuccess` (same condition Type) on a nil return, leaving the resource
  looking healthy with no usable Secret. FIXED: Create now returns an error so the
  failure is sticky (Warning event carries the reason; surfaces as ReconcileError
  per docs/conditions.md). Also fixed (finalizer-wedge class,
  project_ref_gate_must_not_block_delete): DeriveAdminKeys failure is tolerated on
  the delete path. Config.RateLimit/Burst first-caller-wins caveat documented.
  Deferred (LOW): a test-isolation escape hatch for the shared limiter.
