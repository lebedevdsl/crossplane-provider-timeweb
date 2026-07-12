# Tasks: CDN Resource (Cdn kind)

**Input**: Design documents from `/specs/016-cdn-resource/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Unit tests are MANDATORY (constitution §III) and included per story.
kuttl bundle 23 is a durable artifact; the live gate runs the custom-manifest
walk (plan.md / research.md R-10). Release target: **v0.7.0**.

**Organization**: grouped by user story; US1 (provision) is the MVP slice.

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup

- [X] T001 [P] Run open probes P-1..P-6 (curl commands in
      `contracts/timeweb-cdn-endpoints.md`; token-authenticated, operator-assisted
      where needed) and fold results into `specs/016-cdn-resource/research.md` +
      the endpoints contract. Non-blocking: every probe has a specified fallback;
      P-2 (DELETE) doubles as cleanup of probe resource 22209.
      DONE 2026-07-12 session 2 (throwaway resource 22219): P-1/P-2/P-3/P-5/P-6
      resolved, wire shapes corrected in client+differ; P-4 (bucket aws
      auto-wire) deferred to the bucketRef live check; panel probe resource
      22209 ("Ambitious Jackdaw", primary account) still needs manual cleanup.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: new API group + hand-written client + controller scaffolding that
every story builds on.

- [X] T002 Create `apis/cdn/v1alpha1/groupversion_info.go` — group
      `cdn.m.timeweb.crossplane.io`, version `v1alpha1`, SchemeBuilder (013
      `groupversion_info.go` as template)
- [X] T003 Create `apis/cdn/v1alpha1/cdn_types.go` — `Cdn`/`CdnList`,
      `CdnParameters` (name, description, project idiom, `CdnOrigin` with
      bucketRef/domain/ip + https + port, `CdnCache`, `CdnSecurity`,
      `CdnPerformance` + `CdnRobots`, `CdnCors`, `CdnRequestHeader`),
      `CdnObservation` (id, technicalDomain, state, source, lockedPresetID,
      lastPurgedAt, domains, observedSettings, trafficUsage), CEL rules +
      bounds + printcolumns per data-model.md and contracts/cdn-v1alpha1.md
- [X] T004 Register the group in `apis/apis.go` (AddToScheme)
- [X] T005 Run `make generate` — DeepCopy (`apis/cdn/v1alpha1/zz_generated.deepcopy.go`)
      + CRD YAML (`package/crds/cdn.m.timeweb.crossplane.io_cdns.yaml`); verify
      CEL rules survive `crossplane beta validate` AND a server dry-run apply
      (`project_cel_cost_budget_crd`)
- [X] T006 Create `internal/clients/timeweb/cdn.go` — hand-written wire structs
      (underscore envelopes `http_resource`/`http_resource_configuration`,
      pointer fields + omitempty for PATCH partials) and methods:
      `ListHTTPResources`, `GetHTTPResource`, `GetHTTPResourceConfiguration`,
      `CreateHTTPResource`, `PatchHTTPResource`, `DeleteHTTPResource`,
      `ClearCDNCache`, `ListCDNPresets` (the `firewall.go`/`doV2` pattern;
      NEVER log the configuration response — secret-bearing `origin.aws`)
- [X] T007 Extend `internal/clients/timeweb/fake.go` with the CDN method hooks
      (same pattern as existing fakes)
- [X] T008 Create `internal/controller/cdn/controller.go` (SetupCdn with
      `managed.WithManagementPolicies()`, `Watches(S3Bucket → Cdn-by-bucketRef)`
      mapping + 60s-capped rate limiter per research.md R-8) and
      `internal/controller/cdn/connector.go` (ResolveToken → timeweb client +
      kube client); register `SetupCdn` in `cmd/provider/main.go`

**Checkpoint**: `go build ./...` green; CRD installs; no story logic yet.

---

## Phase 3: User Story 1 — Provision a CDN in front of an origin (P1) 🎯 MVP

**Goal**: apply Cdn (bucketRef | domain | ip) → upstream created, technical
domain in status, Ready when serving.

**Independent test**: domain-origin manifest → upstream appears, status carries
`technicalDomain`, Synced+Ready True; bucketRef manifest gates on bucket Ready.

- [X] T009 [US1] Implement `Observe` existence path in
      `internal/controller/cdn/external.go`: GET by decoded external-name,
      404 → ResourceExists=false; by-name adoption guard via list when
      external-name empty (Router idiom); mirror `http_resource` into
      `status.atProvider`; Ready gating on serving state (`processing` →
      Ready=False reason=Provisioning; suspended family per data-model state
      machine)
- [X] T010 [US1] Implement origin resolution in
      `internal/controller/cdn/external.go`: bucketRef → same-ns `client.Get`
      S3Bucket, require Ready + `status.atProvider.id`, map to `storage_id`;
      not-ready → no create + `OriginNotReady` condition; gate SKIPPED when
      `GetDeletionTimestamp() != nil` (`project_ref_gate_must_not_block_delete`);
      domain/ip → `server{host, port-by-scheme}` + `use_https`
- [X] T011 [US1] Implement `Create`: resolve `preset_id` via `ListCDNPresets`
      (lowest-price pick, research R-4), POST create body, set external-name
      from response id, seed `lockedPresetID` + `technicalDomain`; verify by
      re-observation (no Ready on 2xx)
- [X] T012 [US1] Unit tests in `internal/controller/cdn/external_test.go`:
      Observe/Create success, not-found, transient, terminal (constitution
      four-case) + adoption + bucketRef gate (missing / not-Ready / deleting)
      + preset resolution fallback

**Checkpoint**: US1 deliverable — provision/observe works end-to-end against
the fake; MVP demoable on live account.

---

## Phase 4: User Story 2 — Day-2 settings with drift reversion (P2)

**Goal**: declared settings pushed and kept; panel drift reverted; omitted
blocks untouched + mirrored.

**Independent test**: change `cache.edgeTTLSeconds` → upstream follows; flip a
declared toggle in panel → reverted next reconcile; omitted block never
PATCHed.

- [X] T013 [US2] Implement the config differ in
      `internal/controller/cdn/external.go` (or `diff.go` alongside): declared
      non-nil blocks vs `GetHTTPResourceConfiguration` read, owned-fields-only
      per data-model diff table (never `domains`/`aws`/`certificate_id`/
      `secure_token`/`allowed_methods`); wire mappings incl.
      `contentOptimization` → `image_optimization`+`packaging.mp4`, header
      list⇄map, TTL-pair semantics; produce the minimal dirty `config` subset
- [X] T014 [US2] Implement `Update`: identity fields (name/description) +
      origin change + ONE paced PATCH with the dirty subset per reconcile;
      skip push while upstream reports apply-in-flight (research R-5 / P-6
      refinement); `ResourceUpToDate` only on diff-clean Observe
- [X] T015 [US2] Mirror observed settings into
      `status.atProvider.observedSettings` + `domains` (read-only, aws
      excluded) in the Observe path
- [X] T016 [US2] Unit tests: diff-table cases (each block owned/omitted/drifted),
      update success/transient/terminal, async-apply skip, no-PATCH-when-clean

**Checkpoint**: single-writer drift reversion demonstrable per-block.

---

## Phase 5: User Story 3 — Annotation-triggered purge (P3)

**Goal**: `cdn.timeweb.crossplane.io/purge` = `all` | `/`-rooted CSV → one
purge, Event, `lastPurgedAt`, annotation removed.

**Independent test**: annotate Ready Cdn with `all` → exactly one clear-cache
POST, Event, annotation gone; invalid value → warning Event, no POST.

- [X] T017 [US3] Implement purge handling in
      `internal/controller/cdn/external.go`: grammar parse (`all` → full;
      else CSV, every entry must start with `/` → partial), execute on
      serving resource: `ClearCDNCache` → `CachePurged` Event +
      `status.atProvider.lastPurgedAt` + annotation removal via kube Update
      (order POST→remove, research R-6); invalid → `PurgeInvalid` warning
      Event + removal without POST; not-serving → retain + one `PurgeDeferred`
      Event; upstream failure → `PurgeFailed` warning, annotation retained
- [X] T018 [US3] Unit tests: grammar table (all / paths / `/`-less entry /
      empty), one-shot semantics across repeated reconciles, failure-retains-
      annotation, deferred-until-serving

**Checkpoint**: purge flow complete and event-observable.

---

## Phase 6: User Story 4 — Deletion & lifecycle states (P4)

**Goal**: MR delete removes upstream; out-of-band states surfaced/recreated.

**Independent test**: delete MR → upstream gone, finalizer released with
origin bucket pre-deleted; panel-delete upstream → recreated.

- [X] T019 [US4] Implement `Delete` in `internal/controller/cdn/external.go`:
      DELETE by external-name, 404/already-gone → success; zero ref resolution
      on the delete path; align with probe P-2 findings
- [X] T020 [US4] Map upstream suspended/paused/limit states → Ready=False
      reason=Suspended (distinguish from Provisioning) in Observe; out-of-band
      deletion → ResourceExists=false → runtime re-Create (verify path)
- [X] T021 [US4] Unit tests: delete success / already-gone / transient /
      terminal; suspended mapping; recreate-after-vanish observe sequence

**Checkpoint**: full lifecycle covered; all constitution test gates green.

---

## Phase 7: Polish, e2e & release

- [X] T022 [P] Author `examples/cdn.yaml` (canonical manifest from
      contracts/cdn-v1alpha1.md) + `docs/cdn.md` (kind doc: fields, purge
      annotation contract, troubleshooting matrix from quickstart.md) +
      printcolumns entry in `docs/printcolumns.md`
- [X] T023 [P] Author kuttl bundle `tests/e2e/23-cdn/` — admission cases
      (origin oneof 0/2, port-with-bucketRef, slicing range, robots
      custom-iff-mode, requestHeaders unique-name) + lifecycle asserts using
      `kubectl wait --for=condition=...` only
      (`feedback_kuttl_wait_for_condition`)
- [X] T024 Run `make reviewable` (lint via `go run`, bodyclose/gosec,
      govulncheck, generate-diff clean) + `crossplane beta validate` on the
      package; fix findings
- [X] T025 Live gate — COMPLETED 2026-07-12 (see research.md live-gate closure), interrupted twice by Qrator
      IP bans (laptop egress, then inyan-staging egress; see the Qrator
      project memory). Environment: Crossplane 2.2.1 + provider
      `ghcr.io/lebedevdsl/provider-timeweb:dev-1783876993` on context
      `inyan-staging` (currently PAUSED via DeploymentRuntimeConfig
      `paused`, replicas=0). PROVEN live (run 4): create w/ domain origin,
      technicalDomain in status, Ready=True under the ignore-processing
      model, Synced=True, settings PATCH path. REMAINING: (a) unpause
      (`kubectl --context=inyan-staging patch provider.pkg.crossplane.io/provider-timeweb
      --type=merge -p '{"spec":{"runtimeConfigRef":{"apiVersion":"pkg.crossplane.io/v1beta1","kind":"DeploymentRuntimeConfig","name":"default"}}}'`),
      (b) one kuttl re-run (`E2E_KUBECONTEXT=inyan-staging TWE_NO_API_SWEEP=1
      KUTTL_TEST=23-cdn make e2e.test` with the working token as
      TIMEWEB_CLOUD_TOKEN), (c) bucketRef leg (staged manifest: 10GB bucket +
      Cdn bucketRef → Ready + `storage_id` + configuration `origin.aws`
      auto-wire check = P-4/SC-002), (d) delete both + verify upstream empty,
      (e) upstream cleanup: ids 22225?/22227 + panel probe 22209, (f) fold
      results into research.md
- [X] T026 Release **v0.7.0**: notes at `docs/release-notes/v0.7.0.md`;
      pushed `ghcr.io/lebedevdsl/provider-timeweb:v0.7.0` (2026-07-12); the
      identical code ran as `dev-1783876993` through the inyan-staging live
      gate. Query-string list modes seeded in
      `specs/_next-cdn-followups.preface.md`. Git commit/tag = operator
      (manual batching convention).

---

## Dependencies

```
T001 (probes) ──informs──> T006, T011, T014, T019 (fallbacks exist; never blocks)
Phase 2: T002 → T003 → T004 → T005 ; T006 → T007 ; T008 needs T003+T006
US1 (T009–T012) needs Phase 2      ── MVP checkpoint
US2 (T013–T016) needs US1 Observe (T009)
US3 (T017–T018) needs US1 Observe (T009); independent of US2
US4 (T019–T021) needs US1 (T009–T011); independent of US2/US3
Phase 7: T022/T023 [P] anytime after Phase 2; T024 after all code;
         T025 after T024; T026 after T025 (live gate gates the release)
```

## Parallel opportunities

- T001 (operator probes) alongside all of Phase 2.
- T006+T007 (client+fake) in parallel with T002–T005 (types) — different files.
- After T009: US2 (T013+), US3 (T017+), US4 (T019+) touch disjoint logic and
  can be developed in parallel branches of work (same files `external.go`/
  `external_test.go`, so sequence commits, not agents —
  `feedback_no_stash_concurrent_agents`).
- T022, T023 fully parallel with story phases once the CRD exists.

## Implementation strategy

MVP = Phase 2 + US1 (T002–T012): provision + observe + Ready gating,
demoable live. Then US2 (drift), US3 (purge), US4 (lifecycle) as independent
increments, polish + kuttl, live gate, release v0.7.0 (T026) only after the
live gate passes (`feedback_verify_by_reobservation`).
