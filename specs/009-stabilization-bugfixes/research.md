# Phase 0 Research — Stabilization & Bugfixes

Most context was established live during the 008 e2e work; this records the
decisions and the two items that still need a live probe (with a defined
acceptance signal).

> **RESOLVED 2026-06-21 (live probe, bundle 17 on twc-staging):**
> - **R-1**: the k8s **worker** nodegroup created cleanly **without** `gpu`
>   (`wupstream=113135, Synced=True`) — the `/k8s/clusters/{id}/groups` endpoint
>   does NOT enforce `gpu` like `/servers` did. **No worker gpu fix needed** (T016
>   is a no-op).
> - **R-2**: the upstream `NodeOut` schema exposes only `node_ip` (string, the
>   cluster-network/private address) and `network` (an **integer = bandwidth**,
>   not a network object). **There is no per-node public IPv4 field** — even
>   though the group is `public_ip_enabled: true`. So nothing extra to surface;
>   T009 becomes documentation (the shown `node_ip` is the only address the API
>   gives; public reachability is governed by the nodepool `publicIP` flag).

## R-1 [VERIFY] — k8s worker custom-create `gpu` requirement

**Question**: The server custom-create needed an explicit `configuration.gpu: 0`
(omitting it made the API fall back to preset 0 — "Preset with id: 0 not found").
The k8s **worker** body has the same `Gpu *int omitempty` shape
(`nodepool_external.go:389`, omitted when nil). Does
`POST /api/v1/k8s/clusters/{id}/groups` enforce `gpu` the same way?

**Decision**: Treat as a **live probe**, not an assumption. Acceptance signal:
re-run bundle `17-k8s-custom-sizing` (after R-4) in an orderable region.
- If the worker pool reaches Ready → no change needed; the k8s endpoint tolerates
  the omitted gpu. Record the result.
- If it 404s with a preset-fallback error → apply the **same** fix as the server
  (always emit `gpu`, default 0) to the **worker body only**.

**Settled**: Masters never take accelerators (user-confirmed) — the master
config block correctly has no gpu field; do NOT add one. Rationale: a
panel-created master/cluster carries no gpu; adding it risks the inverse
"property should not exist" rejection seen on the server when gpu was misplaced.

**Alternatives considered**: Pre-emptively add gpu to the worker now (rejected —
the k8s endpoint may reject an unexpected field, as the server did when gpu was
put inside `configuration`; verify first).

## R-2 [VERIFY] — node public address surfacing

**Question**: Public-by-default k8s worker nodes only ever showed a private
`192.168.0.x` address in status. The controller parses only `node_ip`
(`groupNodeBody.NodeIP`); the upstream `NodeOut` also has a `network` field we
don't parse. Do these nodes actually have a public address there?

**Decision**: **Live probe** — GET a live cluster's node groups
(`/api/v1/k8s/clusters/{id}/groups`) and inspect a node's `network` field.
- If a public address exists → surface it in node status (FR-003), in addition to
  the private `node_ip`; add a `node_public_ip` (or equivalent) to `groupNodeBody`.
- If no public address exists → record the finding; reconcile the
  "public-by-default" expectation (the column/flag semantics may need a doc note,
  and FR-003 becomes "document that default nodes are reachable via the public
  network attachment, not a per-node public IPv4").

**Why it matters**: `feature 006` was built because "Timeweb K8s nodes get public
IPs by default"; if the public reachability is via a shared public network
attachment rather than a per-node IPv4, the operator-facing wording must match
reality.

## R-3 — Configurator family classification (no price math)

**Decision**: Classify configurators by their catalog `tags`. From the live ru-1
vs ru-3 catalogs:
- **Standard family** (orderable, preferred): `msk_nvme`, `msk_dedicated_cpu`,
  `msk_high_cpu`, plain `ssd`/`nvme` families.
- **Promo / legacy / special** (deprioritized, often non-orderable):
  `discount35`, `ssd_2022`, `spb_gpu`, `spb3_dedicated_cpu`, `*promo*`.

**Selection rule (FR-010)**: within the configurators that satisfy the requested
size (existing capability filter), **rank standard-family entries ahead of
promo/legacy** before the existing tightest-fit / lowest-id tiebreak. No price
lookup. If *only* promo/legacy (non-orderable) entries remain, surface FR-009's
clear error rather than picking one that will fail.

**Open detail for design**: maintain the classification as a small allow/deny
tag-prefix list in the resolver, not hardcoded ids (ids drift). Rationale:
location-portable; survives catalog churn.

**Alternatives**: real price ranking (rejected by clarification — out of scope);
hardcode preferred ids (rejected — ids are account/region-specific and drift).

## R-4 — Context-flake retry (FR-005)

**Root cause**: `kuttl.sh` aborts a bundle when
`kubectl config get-contexts -o name | grep -qxF "$E2E_KUBECONTEXT"` momentarily
returns empty (a transient kubeconfig read race). Observed 3× across 2 runs
(killed bundles 18, 11, 17) despite the context plainly existing.

**Decision**: Wrap the context-existence check in a bounded retry (e.g. 3
attempts, short backoff). Keep the explicit-context safety (never default to a
wrong cluster) — only the *existence read* is retried. Fail only after the
retries are exhausted, with the existing guidance message.

**Alternatives**: replace the check with `kubectl config use-context` probe
(rejected — heavier, mutates current-context); drop the check (rejected — the
explicit-context guard is a safety requirement, `pin_kubectl_context_for_e2e`).

## R-5 — Region parameterization (FR-006)

**Decision**: Introduce `TWE_LOCATION` (default `ru-3`) and `TWE_AZ` (default
`msk-1`), seeded in `presets.local.env`, and replace every hardcoded
`location:`/`availabilityZone:` in the bundle manifests with
`${TWE_LOCATION}`/`${TWE_AZ}` (the manifests are already `envsubst`-templated for
other `TWE_*` values). The seeded presets/configurators
(`TWE_K8S_MASTER_PRESET`, etc.) already correspond to that region, so they stay
consistent.

**Interaction**: the `azLocation` map (msk-1↔ru-3, spb-3↔ru-1, …) still governs
resolver location resolution; parameterization just stops bundles from pinning a
region the seeded catalog can't fulfill. The FloatingIP-bind bundle's same-zone
requirement is preserved by both FIP and server using the same `${TWE_AZ}`.

**Alternatives**: hardcode ru-3/msk-1 everywhere (rejected by clarification —
parameterize for account portability).

## R-6 — Opt-in parallelism (FR-007)

**Decision**: The provider's single global rate limiter (`rate.Limiter(2,3)`)
caps total API pressure regardless of how many bundles reconcile concurrently, so
parallelism is Qrator-safe. Mechanism: support running independent bundles as
**separate `make e2e.test KUTTL_TEST=<x>` jobs** (each its own kuttl process), the
already-proven pattern; serial remains the default. Update the `kuttl-test.yaml`
`parallel: 1` comment (its rationale is obsolete) and document that **account
resource quotas** (max concurrent servers/clusters/vCPU) — not request rate — are
the parallelism ceiling, with guidance to split the slow k8s tier from the fast
server/router tier.

**Alternatives**: kuttl `parallel: N` within one process (viable but our
per-bundle env-discovery + secret setup is per-invocation; separate jobs are
cleaner); parallel-by-default (rejected — quota-risky, complicates triage).

## R-7 — Auto-network id capture (FR-011)

**Decision**: A network-less cluster causes Timeweb to auto-create a
`192.168.0.0/24` VPC; the cluster's nodes report it (the node `network`/`node_ip`
is on that subnet). Capture the auto-created network id during Observe (it is
derivable from the cluster's observed network association / the nodes' network)
and mirror it into the cluster's `status.atProvider` (e.g.
`autoCreatedNetworkID`). **No delete, no sweep** (clarified). This is read-only
(Constitution II): record what Observe already sees.

**Open detail for design**: confirm which upstream field carries the auto-VPC id
for a network-less cluster (the cluster GET's network section vs the node group's
`network.id`); the data-model documents the chosen source.

**Alternatives**: auto-delete on owner delete (rejected by clarification — never
blind-delete); add to the orphan sweep (rejected by clarification).

## Cross-cutting confirmations (from 008, no further research)

- Client rate limiting (2 r/s) + conservative timeouts already in place — parallel
  e2e won't trip Qrator.
- 404 error bodies now surfaced (`timeweb.Classify`) — the misleading
  "preset 0" became diagnosable; FR-009's clearer error builds on this.
- Printcolumn convention already unified to `… ID AGE` (008); the `PUBLIC`
  rename + `clusterID` population complete it.
