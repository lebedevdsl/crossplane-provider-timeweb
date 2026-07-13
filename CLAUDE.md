<!-- SPECKIT START -->
Current feature: **018-stabilization** (release target **v0.9.0**, NON-BREAKING) — read the
plan at `specs/018-stabilization/plan.md`. A hardening slice of the 014 review round:
(1) **shared rate budget** — `timeweb.New` is per-Connect in all 9 connectors so N reconciles
get N independent 2 r/s limiters (the multiplication that trips Timeweb 429 / Qrator ban — the
reproduced CDN status-freeze); fix = process-global per-host limiter + shared transport reused
by every client, per-request bearer auth kept isolated. (2) **S3User credential integrity** —
`buildConnection` runs on Observe/Create/Update but the GET omits the secret key, so
steady-state republishes a BLANK `secret_key` (data-loss); fix = create-only publish
(Observe/Update return empty ConnectionDetails), derive singular `endpoint`/`region` from the
primary granted bucket (kill hardcoded `s3.twcstorage.ru` default), adopted-no-key → condition.
(3) **capped requeue** on SetupNetwork/SetupFloatingIP/SetupCluster (only ones missing it).
(4) hygiene — examples dry-run clean, k8sVersion explain format, `docs/conditions.md` reference,
hoist duplicated `deriveAdminKeys` (s3bucket+s3user) to shared, record hygiene (009/011/012/013
complete, retire shipped `_next` seeds, backfill `buckets` into 012). Plan-phase verify VP-1
(runtime empty-ConnectionDetails is a no-op) / VP-2 (GET omits key) / VP-3 (primary-bucket
region source). Artifacts in `specs/018-stabilization/`. The BREAKING 014 items (secret-key
renames incl. the per-bucket S3User structure, selector rejection, project-ref unification,
printcolumn/JSON casing, immutability CEL) + P2 resilience stay in 014 for a later round.

Feature **017-cdn-added-features** is COMPLETE — **released as v0.8.0** (live-gated end-to-end on inyan-staging incl. full Let's Encrypt automation + mode:none cleanup). Read the plan at
`specs/017-cdn-added-features/plan.md`. Extends `Cdn` with the remaining panel surface, ALL
wire-captured in-session 2026-07-13 (spec.md "Wire facts" is authoritative): `domains
[]string` ≤2 (aliases = {tech}∪declared; tech domain untouchable), ONE cert per resource —
`ssl {mode: none|letsEncrypt|custom, certificateSecretRef}` → `POST /cdn/certificates`
{certificate, private_key} / `POST /cdn/certificates/issue` (202 async; 422
cert_issue_incorrect_dns; tasks accumulate — key on max(id), NO failure reason) / bind =
`config.security.certificate_id` (409 certificate_in_use guards delete order);
delete-if-ours via `status.managedCertificateID`; rotation = parse Secret PEM vs readback
{cn,domains,expires_at}; LE budget: ≥15min spacing, max 4, reset via spec change or
self-clearing `cdn.timeweb.crossplane.io/retry-ssl` annotation; success = MATERIALIZATION
(never task-state strings). **LE path UNVERIFIED upstream (fails w/ correct CNAME, no
reason, ticket filed) — docs+release notes MUST carry the warning.** Also: secure token
(`security.secureToken {secretRef, restrictByIP}` → `secure_token {secret_key,
restrict_by_ip}|null`; security writes are per-key partial), `trafficLimitGBPerMonth` →
`traffic_limit_bytes` (GiB), `origin.awsAuthSecretRef` (forbidden w/ bucketRef — those
auto-wire upstream). Artifacts in `specs/017-cdn-added-features/`.

Feature **016-cdn-resource** is COMPLETE — **released as v0.7.0**
(`ghcr.io/lebedevdsl/provider-timeweb:v0.7.0`, 2026-07-12; live-gated on `inyan-staging`
against the real API; kuttl bundle 23 + 28 unit tests). Follow-ups (query-string
allowed/forbidden cache-key modes, custom domains/SSL/secure-token/traffic limits) seeded in
`specs/_next-cdn-followups.preface.md`. Read the plan at
`specs/016-cdn-resource/plan.md`. New namespaced kind **`Cdn`**
(`cdn.m.timeweb.crossplane.io/v1alpha1`, NEW group dir `apis/cdn/`) managing a Timeweb Cloud
CDN resource on the **undocumented** `/api/v1/cdn/http-resources` surface (devtools-captured
2026-07-12: POST create w/ required `preset_id` + `server{host,port}`|`storage_id` origin;
GET resource (`cdn_domain`, `status: processing`); **PATCH same path** partial updates w/
nested `config`; **GET `/{id}/configuration`** full settings — SECRET-BEARING (`origin.aws`
plaintext S3 keys: never log/mirror); POST `/{id}/clear-cache` `{purge_type: full|partial,
paths}`). Spec surface: origin exactly-one of typed `bucketRef` (→`storage_id` = S3Bucket
upstream id)/`domain`/`ip` + `https`/`port`; settings blocks cache (edge/browser TTL,
always-online, query-string key) / security (`forceHTTPS`) / performance (http3, gzip,
slicing 1–1024MB, contentOptimization off|video|images, robots deny|proxy|custom) / cors /
requestHeaders — nil block ⇒ not owned, mirrored only; single-writer drift reversion,
converge by configuration-readback diff (upstream `status` sticks at `processing` for hours
while serving — NEVER gate Ready/updates/purge on it; suspended family only). Self-clearing
purge annotation
`cdn.timeweb.crossplane.io/purge` = `all` | comma-sep `/`-rooted paths → Event +
`lastPurgedAt` + removal-on-success. AWS auth for bucket origins expected upstream-automatic
(R-3; fallback `deriveAdminKeys` hoisted shared). Client **HAND-WRITTEN**
`internal/clients/timeweb/cdn.go` (`firewall.go`/`doV2` pattern, NO openapi regen).
Controller mirrors Firewall + `Watches(S3Bucket→Cdn)` (nodepool idiom) + ref gate skipped on
deletion. Touch points: `apis/cdn/v1alpha1/*` (new), `apis/apis.go`,
`internal/controller/cdn/*` (new), `cmd/provider/main.go`; regen CRDs+DeepCopy same PR.
Validation: kuttl bundle 23 (admission) + live gate (fresh 10GB bucket + bucketRef Cdn,
content via `technicalDomain`, drift revert, purge, delete). Open probes P-1..P-6 in
`contracts/timeweb-cdn-endpoints.md` (DELETE semantics, presets, aws auto-wire, terminal
status; all have fallbacks). Companion artifacts in `specs/016-cdn-resource/`: spec.md
(US1–US4, FR-001..017, 4 clarify decisions + live probe findings), research.md (R-1..R-10),
data-model.md, contracts/ (cdn-v1alpha1 / timeweb-cdn-endpoints), quickstart.md.

Feature **015-nodepool-taints** is COMPLETE (shipped in **v0.6.0**) — read the plan at
`specs/015-nodepool-taints/plan.md`. Additive on **`KubernetesClusterNodepool`**: optional
**`taints []{key,value?,effect}`** (enum `NoSchedule|PreferNoSchedule|NoExecute`, MaxItems=12,
label-syntax patterns, CEL unique-(key,effect)) AND **day-2 mutability for taints + the existing
`labels`** (create-only contract lifted) with single-writer drift reversion. Upstream surface
**live-verified 2026-07-10**: node-group POST accepts `taints` (panel probe, echoed), public GET
returns `labels`+`taints` arrays, and the same `/k8s/clusters/{cid}/groups/{gid}` path takes an
**undocumented PATCH** (panel-verified; public-host PATCH exercised via the provider at the live
gate). Hand-patch `docs/openapi-timeweb.json` (Taint schema, NodeGroupIn.taints, PATCH op →
`UpdateClusterNodeGroup`) + regen client. Controller: Observe set-diffs declared vs upstream
taints (identity key+effect) & labels (map⇄array) from the EXISTING GET; Update PATCHes **owned
fields only** (`name`,`labels`,`taints`, full-set replace) BEFORE the autoscaling early-return.
Touch points: `apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go`,
`internal/controller/kubernetes/nodepool_external{,_test}.go`, regen CRDs+DeepCopy same PR.
Validation: kuttl bundle 22 authored; live gate = `e2e.up`+`e2e.deploy` + custom manifest
attaching a minimal 1-node pool by flat `clusterID` to a pre-existing Ready cluster (no cluster
provisioning; `inyan-infra`/`cloud-infra` untouched). Companion artifacts in
`specs/015-nodepool-taints/`: spec.md (US1–US4, FR-001..014, 2 clarify sessions), research.md
(R-1..R-8), data-model.md, contracts/ (nodepool-taints-v1alpha1 / timeweb-nodegroup-patch),
quickstart.md.

Feature **013-firewall-api** is COMPLETE/merged — read the plan at
`specs/013-firewall-api/plan.md`. New namespaced kind **`Firewall`**
(`network.m.timeweb.crossplane.io/v1alpha1`) managing a Timeweb Cloud **firewall rule
group** declaratively: identity (`name`/`description`/create-only `policy` `DROP`(default-deny)
|`ACCEPT`), inline **rules** (`direction` ingress|egress / `protocol` tcp|udp|icmp / `port`
string / `cidr`), and inline **service attachments** by **opaque `{serviceID, serviceType}`**
(not typed refs) — v1 targets **load balancers** (`resource_type=balancer`; env runs k8s LBs,
no servers). Single-writer, **1:1 exclusivity** (a service is in ≤1 group → `ServiceConflict`).
API is the documented `/api/v1/firewall/*` surface (already in `docs/openapi-timeweb.json`);
**live-verified 2026-06-28** the real `ResourceType` enum is `server|dbaas|balancer|app` (the
published `server`-only is stale). **Client is HAND-WRITTEN** `internal/clients/timeweb/firewall.go`
(the `doV2`/`storages_users_v2.go` pattern), NOT regenerated — so the stale enum/tag don't matter.
Controller mirrors **Router** (Observe-as-sole-authority + paced one-pass Update set-diff) but
**simpler**: opaque attachments ⇒ NO ref resolution, NO selector, NO catalog resolver, NO
`Watches`; nothing to skip on delete (no refs ⇒ finalizer can't wedge). Touch points:
`apis/network/v1alpha1/{firewall_types.go,groupversion_info.go}`,
`internal/clients/timeweb/firewall.go` (new), `internal/controller/network/firewall_*` (new),
`cmd/provider/main.go`; regen CRDs + DeepCopy same PR. Companion artifacts in
`specs/013-firewall-api/`: spec.md (US1–US4, FR-001..015 +FR-010a), research.md (R-1..R-8), data-model.md,
contracts/ (firewall-v1alpha1 / timeweb-firewall-endpoints), quickstart.md. e2e: group+rules
self-contained; service-attachment e2e needs a pre-existing balancer id (no `LoadBalancer` kind).

Feature **012-s3user-iam** is COMPLETE (committed `c5a2802`; shipped in v0.4.0 notes) — read the
plan at
`specs/012-s3user-iam/plan.md`. New namespaced kind **`S3User`**
(`objectstorage.m.timeweb.crossplane.io/v1alpha1`) provisioning scoped, least-privilege
object-storage IAM users to replace the account-admin keys `S3Bucket` hands out.
Two protocols: identity via Timeweb proprietary `/api/v2/storages/users` REST
(hand-patch `docs/openapi-timeweb.json` + regen), grants via AWS **IAM Query**
(`PutUserPolicy`/`GetUserPolicy`/`ListUserPolicies`/`DeleteUserPolicy`) SigV4-signed at
`https://panel.s3.twcstorage.ru/` (region `ru-1`, service `iam`) with the account
super-user's S3 keys derived at runtime from `GET /api/v1/storages/users` (never cached).
**Live-verified 2026-06-28**: RGW supports N inline policies/user but the panel persists
all grants as ONE merged `iam-user-policy` — controller MUST match it (single-writer,
user-centric `S3User.bucketAccess[]` with typed `bucketRef`); Observe diffs statements
**semantically** (panel reuses Sids, unordered). AWS-SDK confined to a new
`internal/clients/rgwiam` package (signer-only, `aws/signer/v4`) — controller stays
AWS-free. Also **redesigns `S3Bucket`** (breaking, alpha): connection Secret drops
`access_key`/`secret_key` (keeps `endpoint`/`bucket`/`region`); adds read-only
`status.attachedUsers` mirror. Touch points: `apis/objectstorage/v1alpha1/{types.go,
groupversion_info.go}`, `internal/controller/s3user/*` (new), `internal/clients/rgwiam/*`
(new), `internal/controller/s3bucket/external.go`, `cmd/provider/main.go`; regenerate CRDs
+ DeepCopy same PR. Companion artifacts in `specs/012-s3user-iam/`: spec.md (US1–US4,
FR-001..018), research.md (R-1..R-7), data-model.md, contracts/ (s3user-v1alpha1 /
s3bucket-redesign-v1alpha1 / timeweb-s3user-endpoints), quickstart.md.

Feature **011-nodepool-flavor** — read the plan at
`specs/011-nodepool-flavor/plan.md`. Additive: an optional `flavor` enum
(`standard` | `dedicated-cpu`, default `standard`) on `KubernetesClusterNodepool`
worker `resources`, selecting the worker configurator **family**. Fixes the resolver
silently picking `dedicated-cpu` (hidden ~4 GB/cpu floor) over `general` via the
tightest-fit sort (which rejected small pools like 2cpu/2GB with
`invalid_configuration_ram`). Mapping: `standard`→`k8s_configurator_general` (panel
default, low ratio), `dedicated-cpu`→`k8s_configurator_dedicated_cpu`. Mechanism:
`RequireTags []string` on `ConfiguratorInput` + a tag-filter step in
`SelectConfigurator` (after capability filter, before standard/promo partition and the
fit sort); `resolveK8sConfigurator` maps flavor→tag for the **worker** dim only (master
unchanged — single family). CRD enum + `+kubebuilder:default=standard`, no CEL;
regenerate CRD YAML + DeepCopy in the same PR. Backward compatible — existing pools keep
their locked configurator (Create-time resolution only). Touch points:
`apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go`,
`internal/controller/shared/resolver/{resolver.go,select_configurator.go}`,
`internal/controller/kubernetes/{configurator.go,nodepool_external.go}`. Companion
artifacts in `specs/011-nodepool-flavor/`: spec.md (US1–US3, FR-001..008), research.md
(R-1..R-6), data-model.md, contracts/ (nodepool-flavor-v1alpha1 /
configurator-flavor-selection), quickstart.md.

Feature **010-router-network-selectors** is COMPLETE/merged (shipped in v0.3.0): the
`Router` kind gained a per-attachment `networkSelector` (`metav1.LabelSelector`)
alongside `networkRef`/`networkID`, expanding to-many over `Ready` labelled Networks
with a never-detach-last guard, paced attach/detach, and a `Network→Router` `Watches`.
Artifacts in `specs/010-router-network-selectors/`.

Feature **009-stabilization-bugfixes** — read the plan at
`specs/009-stabilization-bugfixes/plan.md`. Stabilization/bugfix round from the
008 live-e2e findings: observability (populate nodepool `CLUSTER`, rename
`PUBLIC-IP`→`PUBLIC`, surface node public addr [VERIFY], server resolved AZ),
e2e reliability (context-flake retry, `TWE_LOCATION`/`TWE_AZ` parameterization,
opt-in parallel), custom sizing (verify k8s-worker `gpu` [VERIFY], prefer
non-promo standard configurators, clear no-orderable error), auto-network
traceability (record auto-VPC id on owner — no delete/no sweep), release hygiene
(`--debug` off, clean semver, validate bundle 19). All changes additive. OUT of
scope: server SSH-key runtime mgmt (`_next-server-ssh-keys`), dataplane
delete-guard annotations (`_next-extra-annotations`). Companion artifacts in
`specs/009-stabilization-bugfixes/`: spec.md, research.md (R-1..R-7), data-model.md,
contracts/ (observability/resolver-selection/e2e-harness), quickstart.md. Source
findings: `specs/_next-008-followups.md`.

Feature **008-packaging** is COMPLETE (uncommitted on the `008-packaging` branch
at this writing) — read its plan at `specs/008-packaging/plan.md`. Packaging +
delivery (no MR/CRD change): publish
the provider as a standard Crossplane OCI package (`.xpkg`) + multi-arch
controller image to a private **Timeweb CRaaS** registry (closes the missing
`xpkg push`), and generalize the e2e harness to install the *published* package
by pull against an operator-set context (`twc-staging`) — dropping the
`k3d-`-only guard but keeping an explicit-context requirement — so live e2e runs
from inside Timeweb (the dev network is WAF-blocked from `api.timeweb.cloud`).
Companion artifacts in `specs/008-packaging/`: spec.md (US1–US3 + FR-001..011),
research.md (R-1..R-6, Crossplane packaging + CRaaS), data-model.md, contracts/
(publish-command / provider-install / e2e-env), quickstart.md. Marketplace
listing is deferred (not yet).

Feature 007 (maintenance round — placement/AZ unification, preset-slug
simplification, printcolumn rationalization, observability, ~25 review findings,
+ code-quality tooling: bodyclose/gosec/govulncheck/`crossplane beta validate`)
is COMPLETE/merged. The deferred `extra-annotations` feature (dataplane
delete-guards) is seeded in `specs/_next-extra-annotations.preface.md`.

Feature 006 (Router + private cluster + automatic NAT convergence) is
COMPLETE/merged — its companion context below remains useful reference:

Companion artifacts in the same directory:
- `spec.md` — feature spec (clarified; 3 Q&A + live-probe findings baked into
  a Clarifications session). One new MR kind: **`Router`** in
  `network.m.timeweb.crossplane.io` — Timeweb's NAT/DHCP router appliance —
  with inline network attachments (per-attachment `nat`/`dhcp`, minItems=1),
  public addresses by **referencing the existing `FloatingIP` kind** (the
  Router never orders IPs; NAT-without-IP rejected at admission), tier/preset
  sizing (tier carries 1-node vs 2-node HA and the region), in-place resize
  in scope (capture pending, immutable-reject fallback), and the
  **private-cluster arrangement** (US3): cluster network behind a NAT'd
  router ⇒ worker nodes with NO public IPs. Public-by-default for nodes is
  unchanged (FR-008/SC-006). CRaaS pull-secrets explicitly out of scope.
- `research.md` — Phase 0: R-1 probe-verified upstream surface (undocumented
  `/api/v1/routers*`, `/presets/routers` — create/rename/attach/detach/DHCP/
  delete all verified live; silent-no-op quirks; new-VPC settle delay),
  R-2 FloatingIP equivalence (confirmed), R-3 NAT-toggle capture pending,
  R-4 resize capture pending, R-5 K8s-binding experiment (derived
  `parent_services` hypothesis + FR-012 delete-while-bound test), R-6
  `DimRouterPreset`, R-7 inline attachments, R-8 network group placement,
  R-9 error classification, R-10 e2e bundles 18/19.
- `data-model.md` — Router spec/status (attachment struct, FloatingIP
  selectors, status mirror incl. `parentServices`), `DimRouterPreset`
  promotion, no-schema-change expectation for `KubernetesCluster`,
  relationships diagram.
- `contracts/` — `router-v1alpha1.md` (CRD contract + conditions table),
  `timeweb-router-endpoints.md` (probe-verified endpoint inventory, create
  body, behavioral quirks incl. the reproduced error-yet-created cluster
  create on router-attached networks → adoption guard required).
- `quickstart.md` — operator walkthrough: router + NAT'd network, the private
  cluster, day-2 ops, troubleshooting matrix.

Feature 005 (custom configurator sizing + ContainerRegistry group move +
tech-debt pass) is complete on its branch — T028 live canary passed all four
bundles. Key carried-forward lessons baked into 006's plan: location-first
catalog resolution (`azLocation`: spb-3↔ru-1, msk-1↔ru-3, ams-1↔nl-1,
fra-1↔de-1), master/worker tag-partitioned `DimKubernetes*Configurator` dims,
nodepool Ready gated on actual per-node state (`status.atProvider.nodes`),
parent-cluster `Watches` + 60s-capped rate limiter for dependent kinds,
`UpstreamFailed` condition vocabulary, and "2xx ≠ converged — verify by
re-observation" for the Timeweb API. See
`specs/005-custom-sizing-configurators/` for its artifacts.

Features 001/002/003/004 merged into main — shared `ProviderConfigSpec`, the
in-controller catalog resolver primitive, the cross-MR `client.Get` ref idiom,
the `Network`/`FloatingIP`/`Server` kinds (003), the K8s kinds (004), and the
kuttl/k3d e2e harness all carry forward unchanged. The MVP foundation at
`specs/001-mvp-scaffolding/` remains authoritative for the `Project` /
`SshKey` kinds and the cross-cutting decisions (error classification,
external-name, tooling).

The constitution governing principles for this provider lives at
`.specify/memory/constitution.md`.
<!-- SPECKIT END -->
