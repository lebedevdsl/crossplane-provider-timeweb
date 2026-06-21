# Feature 008 — consolidated findings & follow-ups

Single source of findings from all 008 (packaging + live-e2e) work, for
`/speckit-specify` → `/speckit-clarify`. Supersedes the per-topic prefaces
(the `009-server-ssh-keys` preface is folded in below and deleted; the
`account-gate` preface was wrong and already removed). Two **pre-008** prefaces
remain separate and are referenced at the end.

Legend: **[FIXED-dev]** = already fixed in the dev build, needs only the final
release semver build. **[OPEN]** = to be scoped/implemented. **[VERIFY]** =
needs a live re-observation to confirm. **[TICKET]** = Timeweb support material.

---

## 0. Context — already fixed during 008 (no action beyond the release build)

These were surfaced and fixed live on `twc-staging`; listed so the spec doesn't
re-open them:

- **[FIXED-dev]** S3Bucket Ready gating: ready bucket reports `created`, not
  `active` (was stuck at Creating). (v0.1.1)
- **[FIXED-dev]** Qrator egress-ban mitigation: client rate-limit 15→2 r/s /
  burst 30→3, 15s dial + 10s TLS timeout, `ratelimiter.NewController()` on all
  controllers. (v0.1.2)
- **[FIXED-dev]** 404 error-body surfacing in `timeweb.Classify` (was a bare
  sentinel; now carries upstream message + `response_id`). (v0.1.3)
- **[FIXED-dev]** Custom **server** sizing: `configuration.gpu` must be sent
  explicitly (0 for non-GPU) — omitting it → API falls back to preset 0
  ("Preset with id: 0 not found"). (v0.1.6, live-provision confirmed)
- **[FIXED-dev]** Removed the redundant nodepool `Reconciling: N/M nodes`
  Event (kept the condition message).
- **[FIXED-dev]** e2e: `09` asserts → `kubectl wait` (order-independent, kuttl
  positional-slice limitation #76); `11`→`spb-3`, `13`→`+location: ru-3`,
  `14`/`16`→`ru-3/msk-1`; `16/17` un-gated.
- **[FIXED-dev]** Printcolumn unification: every reconciled MR now
  `READY · SYNCED · <domain> · [STATE] · ID · AGE` with `ID` (=external-name,
  `priority=1`). Fixed `sshkey`/`project` (`EXTERNAL-NAME`→`ID`), added `ID` to
  `addon`/`repository`, added `STATE` to `s3bucket`.

---

## 1. Status / observability gaps  **[OPEN]**

1.1 **Nodepool `CLUSTER` column is empty.** `status.atProvider.clusterID` is set
   only in Create (`nodepool_external.go:231`); Observe's `populateNodepoolStatus`
   doesn't re-set it, so it's blank in steady state. Fix: populate `clusterID`
   from the resolved parent on every Observe.

1.2 **Nodepool `PUBLIC-IP` column is misnamed.** It's a *boolean* flag
   (`JSONPath=.spec.forProvider.publicIP`, true/false/unset), not an address — a
   nodepool has N nodes, can't show IPs. Rename to **`PUBLIC`** (or similar). The
   server's `PUBLIC-IP` column is fine (one address).

1.3 **Node status shows only the private IP.** `groupNodeBody` parses only
   `node_ip` (the `192.168.0.x` VPC address). The upstream `NodeOut` also has a
   `network` field we don't parse. **[VERIFY]** whether the (public-by-default)
   nodes actually *have* a public IP there — if yes, surface it; if no, that's a
   larger finding (the "public default" isn't materializing as a routable IP).
   Motivating observation: every node we've inspected showed only `192.168.0.x`.

1.4 **Server `availabilityZone` not mirrored into `atProvider`.** Bug-11 showed a
   preset silently overrides the requested AZ (server landed `spb-3` vs requested
   `spb-1`), and we couldn't *observe* it because the resolved AZ isn't in status.
   Fix: mirror the resolved/observed AZ into `status.atProvider`. Optional:
   warn/condition when a preset's zone ≠ requested `availabilityZone`.

---

## 2. Custom sizing / configurator selection  **[OPEN]**

2.1 **k8s WORKER custom sizing — gpu unknown.** The worker config block has
   `Gpu *int omitempty` (omitted when nil) — same shape as the server bug. The
   k8s *nodegroup* endpoint may or may not require `gpu` like `/servers` did.
   **[VERIFY]** via bundle 17 (in-flight) or a probe. Masters correctly carry no
   gpu (by design — masters never take GPU). If the worker endpoint needs it,
   apply the same always-send-gpu fix to the worker body only.

2.2 **ru-1 exposes only non-orderable promo configurators.** ru-1's
   `/configurator/servers` are all promo/legacy (`discount35`, `ssd_2022`,
   `spb_gpu`, `spb3_dedicated_cpu`) and the create endpoint refuses them
   (manifests as preset-0). The resolver currently picks one and fails. Consider:
   detect/skip promo-tagged (non-orderable) configurators, or surface a clear
   "no orderable configurator in <location>" instead of the upstream's misleading
   error.

2.3 **Configurator selection isn't cost-aware.** Tightest-fit-by-max picked
   ru-3 id 131 (`msk_high_cpu`) over the cheaper id 31 (`msk_nvme`) for a tiny
   1/1/15 node. Both provision, but a general-purpose request probably wants the
   cheapest adequate configurator. Consider a price/standard-family tiebreak.

---

## 3. e2e harness & bundle robustness  **[OPEN]**

3.1 **Transient "context does not exist" precheck flake.** The kubeconfig
   context-existence check in `kuttl.sh` flaked twice (killed bundles 18 and 11
   before they ran anything). Add a short retry/backoff to the check.

3.2 **Bundle location hardcoding.** Several bundles hardcode `ru-1`/`spb-3` that
   don't match the account's seeded presets/configurators (which are ru-3/msk-1),
   causing preset-not-found / promo-catalog failures. Parameterize location + AZ
   (`TWE_LOCATION` / `TWE_AZ`) and thread through all bundles, or standardize on
   one account-valid region. (Relates to the location/AZ preface — §6.)

3.3 **Parallel bundle execution is now safe.** The provider's single global
   rate-limiter (2 r/s) caps API pressure regardless of how many bundles run, so
   the `kuttl-test.yaml` `parallel: 1` rationale ("concurrent reconciles trip the
   limiter") is outdated. Enable opt-in parallelism (esp. the slow k8s tier) via
   separate `KUTTL_TEST` jobs or `parallel: N`. **Account quotas** (concurrent
   servers/clusters/vCPU), not Qrator, are the real ceiling — document/guard it.

---

## 4. Orphans / cleanup  **[OPEN / VERIFY]**

4.1 **Network-less k8s clusters auto-create a `192.168.0.0/24` VPC that
   orphans.** Observed an orphan VPC ("Reasonable Corvus", not-connected) whose
   subnet matches the worker nodes' `192.168.0.x`. **[VERIFY]** whether deleting
   a network-less cluster cleans up its auto-VPC; if not, adopt+delete it (or
   document the cleanup), since it's a recurring orphan source. (Known quirk:
   network-less clusters auto-create an orphan VPC.)

4.2 **Server creates may auto-create a private VPC that orphans on delete.**
   Suspected from the debug servers; **[VERIFY]** and handle if real.

4.3 **Post-run orphan sweep should include VPCs.** `TWE_NO_API_SWEEP=1` (set
   because the dev laptop is WAF-blocked) skips the live sweep entirely; the
   sweep, when run from inside Timeweb, should also enumerate `/vpcs`.

---

## 5. Upstream quirks — Timeweb support / `_next` ticket material  **[TICKET]**

6.1 Custom-create returns a misleading `404 "Preset with id: 0 not found"` when
   `configuration.gpu` is omitted (should honor the configuration or say
   "gpu required").
6.2 ru-1 `/configurator/servers` lists promo configurators that the create
   endpoint refuses (should not be offered, or should error clearly).
6.3 (Already ticketed) Qrator DDoS protection silently bans the egress IP on
   request bursts — TCP SYN timeout, no published limit.

---

## 6. Release hygiene  **[OPEN]**

7.1 `--debug` is currently on the live `deploymentruntimeconfig` (the file is
   already reverted); ensure it's **off** before/at release.
7.2 Cut the clean release **semver** from the final tree (dev tags were used for
   the debug iterations, per the dev-tag convention).
7.3 Validate the **private cluster (bundle 19)** — env-gated (`TIMEWEB_E2E_PRIVATE=1`,
   Router + NAT + `publicIP:false`), not yet run on the current build.

---

## 7. Pending validations (to close T018 / SC-007)  **[VERIFY]**

Re-verify suite (`dev-1781988940`, 2026-06-21) — **5/5 bundles that actually ran
PASSED**, zero ban/timeout signatures:
- ✅ `09` server · ✅ `16` server-custom-sizing (**gpu:0 fix live-validated**) ·
  ✅ `18` router · ✅ `12` k8s-cluster · ✅ `13` nodepool-scaling
- ⚠️ `11`, `17` — **never ran**, killed by the §3.1 context-flake (now 3 hits
  across 2 runs incl. last sweep's `18`). Re-run needed; `17` leaves the
  **k8s-worker gpu question still UNVERIFIED**.
- ❌ `14` — failed on the **pre-fix** bundle copy (ru-1 preset; the suite
  snapshotted the bundle before the ru-3 retarget landed). Fix is in source;
  re-run needed.

Still to verify: **k8s-worker gpu** (§2.1, re-run `17`), **node public-IP**
(§1.3, GET a live node's `network`), **auto-VPC orphan cleanup** (§4.1, post-run
VPC sweep). The §3.1 context-flake is the top blocker — fixing it unblocks the
re-runs that close the rest.

---

## 8. Scope notes

**IN scope — fold into this effort:**
- `specs/_next-location-az-presets.preface.md` — location/AZ↔preset unification.
  Directly underlies §1.4, §2.2, §3.2 (the ru-1/ru-3 location mess); pull it in.

**OUT of scope — separate new features, do NOT include here:**
- `specs/_next-server-ssh-keys.preface.md` — Server SSH-key runtime management +
  inline keys. A **big new feature**, not stabilization/bugfixing.
- `specs/_next-extra-annotations.preface.md` — dataplane delete-guard
  annotations. Also a **genuinely new feature**.

Both are listed only so the spec author doesn't pull them in by accident.
