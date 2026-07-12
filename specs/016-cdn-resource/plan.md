# Implementation Plan: CDN Resource — declarative Timeweb Cloud CDN

**Branch**: `016-cdn-resource` | **Date**: 2026-07-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/016-cdn-resource/spec.md`

**Release target**: **v0.7.0**

## Summary

Add a namespaced managed-resource kind **`Cdn`** (`cdn.m.timeweb.crossplane.io/v1alpha1`)
managing one Timeweb Cloud CDN resource on the **undocumented** `/api/v1/cdn/http-resources`
surface (devtools-captured 2026-07-12; absent from the published OpenAPI spec). Scope per
clarified spec: origin as exactly one of typed `bucketRef` (→ upstream `storage_id`) / plain
`domain` / plain `ip` with an `https` scheme toggle; the full day-2 settings surface (cache
TTLs + query-string key, HTTPS redirect, HTTP/3, gzip, large-file slicing, content
optimization, robots.txt, CORS, request headers) with single-writer drift reversion; a
self-clearing `cdn.timeweb.crossplane.io/purge` annotation (`all` | comma-separated `/`-rooted
paths) driving `POST .../clear-cache`; AWS auth for bucket origins auto-wired with runtime-
derived account S3 keys (feature-012 mechanism) — expected to be upstream-automatic when
`storage_id` is set (R-3). Custom delivery domains, SSL certificates, secure token, and traffic
limits stay out of v1. Client is **hand-written** (`internal/clients/timeweb/cdn.go`, the
`firewall.go`/`doV2` pattern) — no openapi hand-patch/regen for an undocumented API. Controller
mirrors **Firewall** (Observe-as-sole-authority, paced one-pass Update) plus a light
`S3Bucket → Cdn` `Watches` for origin readiness (nodepool idiom).

## Technical Context

**Language/Version**: Go (latest stable, tracked by `go.mod` per `project_go_tooling_policy`);
Crossplane v2 namespaced MR model (`.m.` group, `managed.WithManagementPolicies()`).

**Primary Dependencies**: `crossplane-runtime/v2`, `sigs.k8s.io/controller-runtime`,
`internal/clients/timeweb` (hand-written wrapper + shared limiter). **No new third-party
dependency** — CDN is plain Timeweb REST with the account Bearer token; AWS-auth keys (if the
controller must send them, R-3) are plain strings in the PATCH body, no AWS SDK involvement.

**Storage**: N/A — external state is Timeweb's API; MR status mirrors it. Stateless reconciler.

**Testing**: `go test` four-case pattern per Constitution §III (success / not-found / transient
/ terminal) against the fake `timeweb` client, plus purge-annotation grammar tests and
config-diff tests. kuttl bundle 23 (admission + local lifecycle, no live API); live gate =
`e2e.up`/`e2e.deploy` + custom manifest (bucket + Cdn with `bucketRef`) on the operator-pinned
context.

**Target Platform**: Linux (Crossplane provider pod, amd64, distroless/static:nonroot); k3d /
Timeweb for e2e.

**Project Type**: Crossplane provider (single Go module).

**Performance/Constraints**: Shared per-host rate limiter (Qrator —
`project_timeweb_qrator_ddos_egress_block`). `Observe` issues 2 reads (GET resource + GET
configuration). `Update` sends at most one PATCH per reconcile (paced, Router idiom); purge is
one extra POST only when the annotation is present. Settings apply is **asynchronous** upstream
("Применяются настройки") — convergence is judged only by re-observation (R-5), never by 2xx.

**Scale/Scope**: 1 new kind (`Cdn`) in a NEW API group dir `apis/cdn/v1alpha1/`; new client
file `internal/clients/timeweb/cdn.go`; new controller package `internal/controller/cdn/`;
register in `cmd/provider/main.go`; regenerate CRDs + DeepCopy; hoist `deriveAdminKeys` to a
shared spot (third caller). Secret-hygiene constraint: the configuration GET response is
secret-bearing (`origin.aws`) — never logged verbatim, never mirrored to status.

## Open clarifications (resolved)

- **Scope tier** (spec Clarifications 2026-07-12): core + full settings; domains/SSL/secure
  token/traffic limit deferred.
- **Origin model**: typed `bucketRef` + plain `domain`/`ip`, CEL exactly-one; `https` toggle;
  optional `port`.
- **Purge**: self-clearing `cdn.timeweb.crossplane.io/purge` annotation, `all` | `/`-rooted
  comma-separated paths; Event + `status.atProvider.lastPurgedAt` + annotation removal on
  success; retry-with-annotation-intact on failure.
- **AWS auth**: auto-wire for `bucketRef` origins with runtime-derived account keys (012
  mechanism); no operator-facing credential surface; `domain`/`ip` origins never touch the
  upstream `aws` block.

## Constitution Check

*GATE: evaluated against `.specify/memory/constitution.md` v1.0.0.*

- **§I CRD Contract Stability — PASS.** `Cdn` is a new `v1alpha1` CRD (additive; new group).
  DeepCopy + CRD YAML regenerated and committed in the same PR (`make generate`). No change to
  existing kinds (`S3Bucket` is only read by the new controller).
- **§II Idempotent, Side-Effect-Aware Reconciliation — PASS.** `Observe` is read-only (GET
  resource + GET configuration; the purge POST is triggered by an explicit operator annotation,
  executed in the Update/Observe flow with removal-after-success semantics — one purge per
  annotation set, safe under repeated reconciles). `Create` is idempotent via external-name =
  upstream id + by-name adoption guard (Router idiom). `Update` PATCHes owned fields only and
  converges by re-observation (async apply, R-5). `Delete` tolerates already-gone. Errors
  classified via the existing `timeweb.Classify` taxonomy.
- **§III Controller Test Discipline — PASS.** Four-case unit tests for Observe/Create/Update/
  Delete + purge-grammar + config-diff tests against the fake client; no live HTTP.
- **Provider Constraints — PASS.** Account token from `ProviderConfig.spec.credentials` only.
  New secret-hygiene surface acknowledged: `origin.aws` in the configuration response and the
  runtime-derived admin keys are never cached, logged, evented, or mirrored into spec/status
  (spec FR-011/FR-017; 012 precedent).
- **Observability — PASS.** Standard `Synced`/`Ready` + structured logger + existing reason
  vocabulary (`UpstreamFailed`; suspended/limit states map to distinguishing Ready=False
  reasons); purge emits normal/warning Events.

No violations → Complexity Tracking intentionally empty.

## Project Structure

### Documentation (this feature)

```text
specs/016-cdn-resource/
├── plan.md              # This file
├── research.md          # Phase 0 — R-1..R-10 (probe inventory, config mapping, aws auto-wire,
│                        #   presets, async apply, purge mechanics, client choice, bucketRef,
│                        #   aliases quirk, e2e design)
├── data-model.md        # Phase 1 — Cdn spec/status, CEL rules, wire mapping, state machine
├── quickstart.md        # Phase 1 — operator walkthrough + troubleshooting matrix
├── contracts/           # Phase 1
│   ├── cdn-v1alpha1.md            # CRD contract + conditions + purge annotation contract
│   └── timeweb-cdn-endpoints.md   # captured endpoint inventory, bodies, quirks, open probes
└── tasks.md             # Phase 2 (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
apis/cdn/v1alpha1/                      # NEW API group
├── cdn_types.go                        # CdnParameters/Origin/Cache/Security/Performance/
│                                       #   Cors/RequestHeader/Observation/Spec/Status + CEL
├── groupversion_info.go                # cdn.m.timeweb.crossplane.io/v1alpha1 SchemeBuilder
└── zz_generated.deepcopy.go            # regenerated

apis/apis.go                            # + cdn v1alpha1 AddToScheme

internal/clients/timeweb/
└── cdn.go                              # hand-written: GetHTTPResource, GetHTTPResourceConfig,
                                        #   CreateHTTPResource, PatchHTTPResource, DeleteHTTPResource,
                                        #   ClearCache, ListCDNPresets, ListHTTPResources (adoption)

internal/controller/cdn/                # NEW controller package
├── controller.go                       # SetupCdn(mgr, log, pollInterval) + Watches(S3Bucket→Cdn)
├── connector.go                        # Connect: ResolveToken, timeweb client, kube client
├── external.go                         # Observe/Create/Update/Delete + purge handling +
│                                       #   config set-diff + bucketRef→storage_id resolution
└── external_test.go                    # four-case + purge grammar + diff tests

internal/controller/shared/             # deriveAdminKeys hoisted here (3rd caller) if R-3
                                        #   shows the controller must send keys; else untouched

cmd/provider/main.go                    # + cdnctrl.SetupCdn(...)
package/crds/...cdns.yaml               # regenerated CRD
examples/cdn.yaml                       # operator example (bucketRef + settings + purge note)
tests/e2e/23-cdn/                       # kuttl bundle 23
docs/cdn.md                             # kind doc (project docs/ convention)
```

**Structure Decision**: New **cdn** API group + controller package (the product is its own
panel section; no existing group fits — `network` is VPC/router-centric, `objectstorage` is
S3). Controller mirrors **Firewall** (no catalog resolver, no selectors) with two additions:
(a) a `Watches(S3Bucket → Cdn-by-bucketRef)` mapping so bucket readiness promptly unblocks
origin resolution (nodepool parent-watch idiom, 60s-capped rate limiter), and (b) purge
annotation handling with removal-after-success via the kube client. Hand-written client keeps
the undocumented surface out of the generated-code path (013 precedent).

## Complexity Tracking

No constitution violations — section intentionally empty.
