---
description: "Task list for 008-packaging — embed-model OCI publish, remote-cluster e2e, live validation, and a GATED public ghcr/GitHub release (FR-012). US1 + US2-harness done; free e2e subset green on v0.1.1."
---

# Tasks: Provider Packaging, Remote-Cluster e2e & Public Release

**Input**: `specs/008-packaging/` (plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md).

**Scope**: Packaging + delivery + public release. **No `apis/`/CRD/condition change** (FR-010) — EXCEPT controller bugfixes the live e2e surfaces (e.g. the S3 Ready-gating), which are in scope as validation findings.

**As-built**: **embed model** (one multi-arch `.xpkg` bakes the controller in; `buildx --load`+`docker save`+`--embed-runtime-image-tarball`). Registry param `IMAGE_REPO` selects targets: **public `ghcr.io`** (release, FR-012) vs **private CRaaS** (test/e2e only). **Verification = re-observation on `twc-staging`.**

## Format: `[ID] [P?] [Story?] Description`

---

## Phase 1: Setup

- [X] T001 Toolchain + clean baseline verified (crossplane v2.1.3, docker buildx 0.29, cosign; build/lint/generate green).

## Phase 2: Foundational — registry + parameter + embed meta

- [X] T002 Registry (owner step): generic Timeweb registry `inyan-images.registry.twcstorage.ru` + `docker login`; pull secret `registry-creds` in `crossplane-system`. (CRaaS = test-only.)
- [X] T003 `Makefile` — `IMAGE_REPO` user-defined param (default = test CRaaS), overridable to public `ghcr.io` (FR-005/011/012).
- [X] T004 `Makefile` `xpkg.build` — **embed**: strip `spec.controller` from a staged `crossplane.yaml` copy, bake runtime via `--embed-runtime-image-tarball`; keep `spec.crossplane.version`.
- [X] T005 Publish-origin reachability confirmed (dev pushes to registry; cluster pulls in-network).

## Phase 3: User Story 1 — Publish an installable package (P1) ✅ LIVE-VERIFIED

- [X] T006 [US1] `Makefile` `xpkg.push` — `crossplane xpkg push --package-files=… $(IMAGE_REPO):$(VERSION)`. Pushed v0.1.0 then **v0.1.1**.
- [X] T007 [US1] Per-platform embed via `buildx --load`+`docker save`+embed loop (default docker driver; `linux/amd64` for staging). True multi-arch deferred to T027 (needs container driver).
- [X] T008 [US1] `make release` — xpkg.push + cosign (cosign sign wired but not yet run → T016).
- [X] T009 [US1] [VERIFY-LIVE] Installed on twc-staging via `deploy/provider.yaml` → Installed+Healthy, pod runs the **embedded** image; SSHKey smoke reconciled real key + cleaned up.

## Phase 4: User Story 2 — Remote-cluster e2e (P1) ✅ harness + free subset done

- [X] T010 [US2] `kuttl.sh` — drop the k3d-only guard; context-mode-aware (`E2E_REMOTE`); keep explicit-context + minified-kubeconfig safety; local-API guard k3d-only.
- [X] T011 [US2] `Makefile.test` — `E2E_KUBECONTEXT` overridable; `E2E_PACKAGE`/`E2E_PULL_SECRET` added.
- [X] T012 [US2] `kuttl.sh` — remote provider-Installed+Healthy precheck (fail-fast w/ guidance).
- [X] T013 [US2] FQ resource names already used in `TWE_KINDS` (external-secrets `sshkey` collision is a non-issue at the script level).
- [X] T014 [US2] `kuttl.sh` — live-API orphan-sweep helper (report-only; gated by `TWE_NO_API_SWEEP`).
- [X] T015 [US2] **Lazy + env-overridable discovery** in `kuttl.sh` (each catalog/sizing curl runs only if a selected bundle needs it AND no env override) + `test/e2e/presets.local.env` (gitignored, pre-seeded values) → **zero host→API calls** from a WAF-blocked laptop.
- [X] T016 [US2] `deploy/` install set: `provider.yaml` (+ `runtimeConfigRef`), `providerconfig.yaml`, **`deploymentruntimeconfig.yaml`** (leader-election off — fixes CrashLoopBackOff on the flaky apiserver).
- [X] T017 [US2] [VERIFY-LIVE] **Free subset GREEN** on twc-staging (sshkey/s3/registry/network/preset-not-found) on v0.1.1 — zero API calls, zero orphans.
- [ ] T018 [US2] [VERIFY-LIVE] **Billable bundles green** on twc-staging — Server, FloatingIP, KubernetesCluster, Nodepool, Router, custom-sizing (16/17), router (18) (+ env-gated 10b/15/19/07 as desired). **← THE SC-007 GATE for the public release.**
  - [X] **12-k8s-cluster-lifecycle GREEN** on v0.1.2 (913s; cluster+nodepool both Ready=True, clean teardown, no orphans) — **validated the Qrator throttle fix**: zero `i/o timeout`, no egress ban (vs. immediate ban last run). `make` exited 1 on a harness artifact (the `TIMEWEB_E2E_TOKEN` dual-PC path), not the test.
  - [ ] Sweep of `09/11/16/18/13/17` in flight (`TIMEWEB_E2E_TOKEN` unset for clean exit codes).

## Phase 5: Live-validation findings (007 regressions caught by the 008 e2e)

- [X] T019 `s3bucket/external.go` — Ready-gating: a ready bucket reports **`created`** (verified live; generated enum is `created`/`no_paid`/`transfer`), not `active`. Fixed `setBucketReadyCondition` (`created,active`→Available) + the backwards unit test (`new`→Creating); rebuilt **v0.1.1**, provider rolled Healthy.
- [ ] T020 As more billable bundles run (T018), fix any further gating/observability regressions they surface; rebuild + bump VERSION; re-run. (Same loop as T019.)
  - [X] **Qrator egress-ban fix** (`v0.1.2`): the e2e billable burst got our cluster egress silently DDoS-banned (support-confirmed 2026-06-19; TCP SYN timeout, not 4xx). Crushed the client rate limit `15→2 r/s` / burst `30→3`, added a 15s dial + 10s TLS timeout (`newDefaultTransport`), and applied `ratelimiter.NewController()` (1s→60s backoff) to the 5 controllers that still used controller-runtime's 5ms→1000s default. Captured in the `project_timeweb_qrator_ddos_egress_block` memory.
  - [X] **Redundant nodepool Event cleanup** (shipped v0.1.3): dropped the in-progress `Reconciling: N/M worker nodes provisioned` Events (`setNodepoolReadyCondition`) — redundant with `status.atProvider.nodes`; kept the condition message + the `Available`/`UpstreamFailed` Events.
  - [X] **404 error-body surfacing** (shipped v0.1.3): `timeweb.Classify` discarded the response body on 404 (bare sentinel). Now enriches `ErrNotFound` with the upstream message + `response_id` — this is what made bug 16 diagnosable (`Preset with id: 0 not found`).
  - [X] **Bug 11 (FloatingIP bind `different_zones_exception`)**: root-caused live — preset `ssd-15` (preset_id 2573) forces the server into `spb-3` while the FIP honored the requested `spb-1`. NOT a provider bug. Fixed bundle 11 to pin `spb-3` across allocate/bind/unbind. Provider follow-ups noted (observe `availabilityZone` into `atProvider`; warn on preset/AZ conflict) → `_next` candidate.
  - [X] **Bug 16 (custom configurator create `Preset 0 not found`) — REAL provider bug, FIXED (v0.1.6)**: the custom-create `configuration` block **omitted `gpu`** (`omitempty`, only set when operator specified one). The API treats a configuration without `gpu` as malformed → discards the block → falls back to preset 0. Ground truth from a panel-created server (the UI always sends `gpu: 0`). Fix: always emit `gpu` (0 default); reverted the bandwidth red-herring detour (v0.1.4/v0.1.5). Confirmed by a **live provision** (`upstreamID=8423483`, ru-3/msk-1). Regression test added (`TestServerCustomSizing` asserts `gpu==0`). Memory: `project_timeweb_custom_configurator_gpu_and_ru1_catalog`.
  - [X] **Bug 16 secondary — ru-1 catalog**: ru-1 exposes only promo/legacy configurators (discount35/ssd_2022/spb_gpu/spb3_dedicated_cpu) that the create endpoint refuses; ru-3/msk-1 has standard orderable ones. **Bundle 16 retargeted ru-1 → ru-3/msk-1**; **16/17 un-gated** (removed the `TWE_CUSTOM_SIZING` skip; deleted the wrong account-limit preface).
  - [X] **09-server-lifecycle asserts** → `kubectl wait --for=condition=<type>=<status>` (order-independent) — fixes the recurring positional-array `[Synced,Ready]`/`[Ready,Synced]` false failures (kuttl#76); reference pattern for future bundles.
  - [X] **Printcolumn unification (finishing 007's rationalization)**: every reconciled MR kind now follows `READY · SYNCED · <domain> · [STATE] · ID · AGE`, with `ID` = `crossplane.io/external-name` at `priority=1` (wide-only) — consistent across all kinds. Fixed `sshkey`/`project` (were `EXTERNAL-NAME`, no priority → renamed `ID` priority=1); added `ID` to `addon`/`repository` (were missing it); added `STATE` to `s3bucket`. CRDs regenerated.

## Phase 6: User Story 3 — Reproducible, verifiable delivery + docs (P2)

- [ ] T021 [US3] cosign **sign + verify the pushed PACKAGE**; digest-pin `deploy/provider.yaml`; wire into `make release`. (Public uses keyless — see T024.)
- [ ] T022 [US3] `package/crossplane.yaml` — confirm declared `spec.crossplane.version` (`>=v2.0.0-0`) vs staging 2.3.2; keep.
- [ ] T023 [P] [US3] `docs/` — install-from-ghcr guide + remote-e2e runbook + build-from-repo path (FR-008 a/b/c); align with README + quickstart.

## Phase 7: User Story 4 — Public GitHub release (FR-012) — GATED on T018 (SC-007)

**Goal**: a public, signed, multi-arch release. **Gate**: do NOT cut until T018 (full billable e2e) is green — all kinds validated, none "experimental".

- [ ] T024 [US4] Set up a `docker-container` buildx builder (`docker buildx create --driver docker-container --use --bootstrap`); build true **multi-arch** `linux/amd64,linux/arm64` embedded `.xpkg`(s) + push as one index (R-8).
- [ ] T025 [US4] Retarget to **public `ghcr.io/lebedevdsl/provider-timeweb`** (IMAGE_REPO override; `docker login ghcr.io`); push signed multi-arch package (R-7).
- [ ] T026 [US4] **cosign keyless (OIDC)** signing for the public artifact + verify; document provenance (R-7).
- [ ] T027 [P] [US4] `.github/workflows/release.yml` — on tag: build → keyless-sign → push ghcr → `gh release create <tag>` with the `.xpkg` attached + notes (R-7; `id-token: write`).
- [ ] T028 [P] [US4] `README.md` + release notes — **alpha** labeling, `v1alpha1` CRD-stability caveat (§I), per-kind status (all validated post-gate), install-from-ghcr (public, no pull secret).
- [ ] T029 [US4] [VERIFY] Tag `v0.1.0` + cut the GitHub Release — **only after T018 green**; verify a fresh cluster installs from public ghcr → Healthy (SC-001/SC-007).

## Phase 8: Polish & cross-cutting

- [ ] T030 [P] Fix the example **leading comment-only-doc headers** (`examples/providerconfig.yaml`, `examples/kubernetescluster.yaml`, …) so xpkg's parser accepts them; **re-enable `--examples-root=examples`** (currently an empty dir).
- [ ] T031 [P] `package/crossplane.yaml` — committed `spec.controller.image` default off ghcr (cosmetic — embed strip makes it irrelevant) or add a one-line note.
- [ ] T032 Final gate: `make lint` + `go test ./...` green; `make generate` clean; `make validate-examples` passes.
- [ ] T033 [P] Sync `specs/008-packaging/` + memory: embed model + `--load`/save; **S3 ready=`created`**; **leader-election-off DeploymentRuntimeConfig** for flaky apiservers; `twc-staging` facts (in-Timeweb API OK, Crossplane 2.3.2, external-secrets `sshkey` collision); lazy-discovery + `presets.local.env`.

---

## Dependencies & execution order

- **Phases 1–4 (Setup/Foundational/US1/US2-harness+free-subset) are DONE.** Publish→install→reconcile + the free e2e are live-verified on v0.1.1.
- **T018 (billable e2e)** is the linchpin: it both finishes US2 validation AND **gates the public release (T029/SC-007)**. Fixes it surfaces (T020) loop back through rebuild + re-run.
- **US3 (T021–T023)** hardens the artifact — parallel-friendly with US2/docs.
- **US4 (T024–T029)** the public release — **blocked by T018**.
- **Polish (T030–T033)** — T030 re-enables bundled examples; T033 captures as-built memory.

## Parallel opportunities

- US3: T023 (docs) ∥ T021/T022. US4: T027 (workflow) ∥ T028 (README). Polish: T030 ∥ T031 ∥ T033.

## Implementation strategy (MVP-first)

1. **Done:** publish/install (US1) + remote e2e harness + free-subset green (US2), incl. the S3 fix (v0.1.1).
2. **Next critical path:** **T018** — run the billable bundles green (fixing regressions via the T019/T020 loop). This is the SC-007 gate.
3. **Then:** US3 hardening + **US4 public release** (multi-arch, ghcr, keyless sign, GitHub Release, alpha) → tag **v0.1.0**.
4. **`[VERIFY-LIVE]` gates truth** ([[feedback_verify_by_reobservation]], [[feedback_always_check_live_api_orphans]], [[feedback_pin_kubectl_context_for_e2e]]).
