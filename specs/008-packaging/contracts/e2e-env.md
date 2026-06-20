# Contract: e2e Environment & Harness (remote-context, pull-install)

**Feature**: `008-packaging` | Covers FR-004, FR-005, FR-006 | US2

The environment contract for running the existing kuttl bundles against an
**arbitrary, explicitly-provided** cluster context (`twc-staging`) using the
**published** CRaaS package — replacing the k3d-only, side-load harness. Bundles
are unchanged (FR-010); only context + install + cleanup plumbing changes.

> Planning contract — exact script edits land in `/speckit-tasks` + impl. Do NOT
> edit `test/e2e/` files in this phase.

## Env contract

| Var | Required | Old behavior | New behavior |
|---|---|---|---|
| `E2E_KUBECONTEXT` | **yes, explicit** | derived `k3d-$(E2E_CLUSTER_NAME)` in `Makefile.test:24`; hard-rejected unless `k3d-*` (`kuttl.sh:49`) | operator-supplied, e.g. `twc-staging`; **no ambient default**, **no k3d prefix requirement** (FR-004) |
| `E2E_NAMESPACE` | yes | `timeweb-e2e` | unchanged (workload isolation, FR-006) |
| `E2E_PACKAGE` | new, **defaulted** | n/a (side-loaded local image) | published `.xpkg` ref; **default = the author's Timeweb CRaaS** (single-maintainer), overridable so a build-from-repo user points at their own registry (FR-005/FR-008c) |
| `E2E_VERSION` | yes | `e2e` (local tag) | published tag/digest to install (FR-005) |
| `E2E_PULL_SECRET` | yes (new) | n/a | name of the `crossplane-system` dockerconfigjson Secret (`craas-pull`) |
| `TIMEWEB_CLOUD_TOKEN` | yes | live API token | unchanged |
| `TIMEWEB_E2E_TOKEN` | optional | second-account token (multi-PC bundle) | unchanged |
| `E2E_CROSSPLANE_VER` | n/a on remote | pinned for k3d install | **ignored** on `twc-staging` (Crossplane owner-installed: 2.3.2) |

## Guard changes (the safety swap)

**Remove** (`kuttl.sh`):
- `[[ "$E2E_KUBECONTEXT" != k3d-* ]]` hard reject (lines ~49–59).
- The local-API-server-URL assertion (`https://127.0.0.1|0.0.0.0|localhost…`,
  lines ~507–516) — k3d-specific; would reject a remote `twc-staging` API server.

**Keep / add** (the preserved wrong-cluster safety, `feedback_pin_kubectl_context_for_e2e`):
1. `E2E_KUBECONTEXT` MUST be set and explicitly provided — no derivation, no
   ambient current-context fallback. Refuse to run if empty (FR-004).
2. Context-exists check (`kubectl config get-contexts`, `kuttl.sh:64`) — keep.
3. Minified single-context kubeconfig (`kubectl config view --raw --minify
   --context=…`, `kuttl.sh:491`) — keep: the rendered kubeconfig can reach **only**
   that one cluster.
4. `current-context == E2E_KUBECONTEXT` assertion (`kuttl.sh:497`) — keep.

Net: the same blast-radius protection as the k3d lock, but portable to any cluster.

## Install change (side-load → pull)

Replace the `deploy.sh` build→push-local→`--embed-runtime-image`→install flow with:
1. Ensure the `crossplane-system` `dockerconfigjson` pull Secret (`E2E_PULL_SECRET`).
2. Apply the `Provider` CR with `spec.package=$(E2E_PACKAGE):$(E2E_VERSION)` (or
   `@sha256:…`) + `packagePullSecrets:[{name: $(E2E_PULL_SECRET)}]` — see
   `provider-install.md`.
3. (mutable tag) annotation-bump to force re-resolution
   (`project_crossplane_provider_repull_annotation_bump`).
4. `kubectl wait --for=condition=Healthy provider/provider-timeweb --timeout=5m`.

No `docker build`, no local registry, no `--embed-runtime-image` on the remote path
(FR-005). On `twc-staging`, `e2e.up` (k3d + Crossplane install) is **skipped** —
the cluster + Crossplane pre-exist; only `e2e.deploy` (pull-install) + `e2e.test`
run, with `make e2e.down` a no-op against a non-k3d cluster (guard it).

## Isolation + cleanup (shared cluster)

- **Namespace isolation** — keep `timeweb-e2e` (FR-006); MRs are namespaced.
- **Interrupt-safe teardown** — the existing SIGHUP/SIGINT/SIGTERM traps +
  `cleanup_mrs` + minified-kubeconfig carry over unchanged (cluster-agnostic).
- **Live-API orphan sweep (MANDATORY on shared `twc-staging`)** — extend
  `report_orphans` from in-cluster-MR listing to a **live Timeweb API** diff
  (`GET /api/v1/routers`, `/api/v2/vpcs`, `/api/v1/k8s/clusters`,
  `/api/v1/floating-ips`) pre vs post run; flag `e2e-*`/probe-named unattached
  resources for confirm-then-delete (`feedback_always_check_live_api_orphans`).
- The out-of-band import VPC (`kuttl.sh` §3b) cleanup is unchanged.

## Acceptance

- Suite runs green against `twc-staging` (non-k3d) using only the published
  package (SC-002).
- Refuses to run with no explicit context (FR-004).
- Post-run: zero run-created cluster objects AND zero live-Timeweb orphans (SC-004).
- Reconciliation succeeds from inside Timeweb — no WAF block (US2 scenario 4).
</content>
