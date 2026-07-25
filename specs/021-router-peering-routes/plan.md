# Implementation Plan: Router peering — static routes, multi-router networks, NAT release

**Branch**: `021-router-peering-routes` | **Date**: 2026-07-25 | **Spec**: [spec.md](./spec.md)

## Summary

v0.10.0, NON-BREAKING (additive spec fields only). Five slices: (1) Router
`staticRoutes` with literal and by-reference nexthops; (2) multi-router
network attachment hardening (scoping already structurally per-router — pin
with tests + live e2e); (3) NAT-IP declarative release on the disable/move
transition (DNAT-guarded, default-on, never-steal untouched); (4)
KubernetesCluster `clusterNetworkCIDR` create-only fields; (5) hygiene —
`x/text` ≥ v0.39.0 (GO-2026-5970) and a pinning test for the
already-implemented valid-versions error message (US5 discovered satisfied:
`DimensionValueNotFoundError` formats `valid: …` ≤20 values).

## Technical Context

Go / crossplane-runtime v2, no new deps (one dep BUMP). Generated client
already carries the whole needed surface (no regen, no hand-patch this
feature): `GetStaticRoutes` / `PostStaticRoute` / `DeleteStaticRoute`
(+`StaticRouteIn/Out`, `{static_routes: […]}` envelopes), `GetDnatRules`
(`DnatRuleOut.PublicIp`), and `CreateClusterJSONRequestBody.ClusterNetworkCidr`
(anonymous pointer struct, `pods_network`/`services_network`). The cluster GET
does NOT echo `cluster_network_cidr` → no status mirror; immutability is
admission-only. `GET …/static-routes/available` is known-unreliable — never
referenced.

## Constitution Check

- **I** — additive CRD fields on Router (staticRoutes + status mirror) and
  KubernetesCluster (clusterNetworkCIDR); `make generate` same PR. PASS.
- **II** — Observe read-only (routes GET added); route convergence is
  subnet-keyed diff, idempotent create/delete; NAT unbind only inside the
  transition the provider executes, guarded by declared-set + DNAT read;
  errors classified; blocked states surfaced. PASS.
- **III** — fake-client tests for every new path (routes CRUD/drift/pacing,
  via-resolution gates, NAT release matrix, CIDR body/immutability, version
  message pin). PASS.

## Design decisions

- **D-1 route model**: `spec.forProvider.staticRoutes[]{subnet, nexthop | via{routerRef, networkRef}}`;
  CEL exactly-one-of + MaxItems=32 + bounded unique-subnet rule; status mirror
  `atProvider.staticRoutes[]{id, subnet, nexthop}`. Convergence identity =
  subnet; changed nexthop = delete + create (no upstream update op). Same
  `ops` pacing budget as attachments.
- **D-2 via-resolution**: Connect-time (refs.go pattern, skipped on deletion):
  resolve NetworkRef → upstream network id; get neighbor Router CR (same
  namespace), require Ready + its `status.atProvider.networks[]` entry for
  that network with non-empty gateway → nexthop. Unresolvable → typed
  not-ready gate (ErrTargetNotReady flow), converges via watch/poll.
- **D-3 NAT release**: in Update's NAT-disable AND NAT-change branches, after
  the successful NAT write: skip if the old address is declared on another
  attachment; read `GetDnatRules` — address present ⇒ skip + Normal event
  (retained); else resolve FIP by address (existing `natIPBindability`) and
  `UnbindFloatingIp` only when bound to THIS router. Unbind/DNAT-read failures
  ⇒ Warning event, never a reconcile error (best-effort per spec); unbind
  consumes `ops`.
- **D-4 multi-router**: controller logic is already router-scoped (GET
  `/routers/{id}/networks` only) — no code change expected; pin with a unit
  test (shared net attached upstream to our router + declared ⇒ zero ops) and
  the live two-router e2e incl. survivor deletion. Leaked reservations:
  Network controller ignores busy addresses already (no reserved-IP drift
  logic) — verify by inspection, note in research.
- **D-5 clusterNetworkCIDR**: `ClusterNetworkCIDR *{podsNetwork, servicesNetwork}`
  (both required when block present; CIDR-pattern validated), CEL transition
  immutability (block + fields), wired into `buildCreateClusterBody`. No
  observe/update surface (not echoed).
- **D-6 hygiene**: `go get golang.org/x/text@latest` (≥0.39.0) + tidy; unit
  test pinning the versions-in-error message via the resolver error type.

## Touch points

```text
apis/network/v1alpha1/router_types.go                 # staticRoutes spec + status types
apis/kubernetes/v1alpha1/kubernetescluster_types.go   # clusterNetworkCIDR
internal/controller/network/refs.go                   # via-resolution (+resolvedRoutes)
internal/controller/network/router_external.go        # routes observe/diff/converge; NAT release
internal/controller/network/router_external_test.go
internal/controller/kubernetes/cluster_external.go    # CIDR body wiring
internal/controller/kubernetes/cluster_external_test.go
go.mod / go.sum                                       # x/text bump
package/crds + zz_generated deepcopy                  # make generate
docs/routers.md, docs/kubernetes.md, docs/conditions.md, examples/
```

## Validation

Unit + lint + generate-clean + validate-examples; dev-tag → inyan-staging;
related kuttl bundles **18-router-lifecycle** + **20-router-selector** on
staging, plus scripted live checks: two-router common network + static routes
(both forms) + panel-deletion drift repair + survivor deletion + NAT release
end-to-end; cluster create with declared CIDRs (create/verify/delete). Then
release v0.10.0 (notes → merge → tag → CI publish; vuln gate green).
