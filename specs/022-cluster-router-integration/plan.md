# Implementation Plan: Declarative cluster↔router integration (routerRef)

**Branch**: `022-cluster-router-integration` | **Date**: 2026-07-25 | **Spec**: [spec.md](./spec.md)

## Summary

v0.11.0 (additive, NON-BREAKING): `KubernetesCluster.spec.forProvider.routerRef`
(+ flat `routerID`) → integration op `PATCH /k8s/clusters/{id}
{virtual_router_id: <router uuid> | null}` (undocumented field; detach =
explicit JSON null — upstream-confirmed). Readback = the router's
`parent_services` (cluster GET echoes nothing). Nodepool `router_required_…`
family classified into the fixable `routerRef` remedy. Refless clusters:
byte-identical behavior.

## Constitution Check

- **I**: additive spec fields + status mirror; `make generate` same PR. PASS.
- **II**: Observe read-only (one routers-list read only when a declaration or
  a recorded integration exists); integration PATCH idempotent (re-PATCH of
  the same uuid is a no-op upstream; verified at gate); detach only on
  explicit removal (recorded declaration), never on transient ref failure;
  errors classified. PASS.
- **III**: fake-client tests for every path (below). PASS.

## Design decisions

- **D-1 declaration**: `RouterRef *xpv2.Reference` + `RouterID *string`
  (at-most-one, CEL), resolved at Connect (Ready-gated router → its
  external-name uuid; skip on deletion). Unresolvable ⇒ Connect gate error
  (established idiom) ⇒ Update never sees a half-state.
- **D-2 recorded declaration**: `status.atProvider.integratedRouterID`
  records the uuid the provider last integrated. Detach fires ONLY when the
  spec declaration is gone AND this record exists — panel-integrated refless
  clusters are never touched, and refless clusters with no record get ZERO
  new API reads (FR-006).
- **D-3 readback**: one `GetRouters` list → `integratedWith` = uuid of the
  router whose `parent_services` contains the cluster id (type k8s). Executed
  in Observe only when a declaration or record exists; failures keep the last
  mirrored value and never fail Observe. `routerIntegrated` =
  (integratedWith == declared) when declared; false after detach.
- **D-4 the op**: hand-written helper on the cluster external using
  `UpdateClusterWithBody` + raw JSON — `{"virtual_router_id":"<uuid>"}` /
  `{"virtual_router_id":null}` (typed omitempty cannot express null).
  `docs/openapi-timeweb.json` gains the nullable field on the updateCluster
  body (record/convention; regen picks it up whenever it next runs).
- **D-5 convergence rows** (Observe drift → Update one-pass):
  declared && integratedWith != declared → PATCH uuid (move = same single
  PATCH; gate-verified); !declared && record → PATCH null + clear record +
  mirror false. NAT-wait: declared router observed NOT NATing the cluster's
  network → typed wait condition (reuse `RouterNATRequired` vocabulary),
  no PATCH until observed — gate re-verifies whether the desync trap still
  bites integrated clusters and the nodepool text finalizes on that evidence.
- **D-6 nodepool classification**: `*timeweb.APIError` code family
  (`router_required_for_worker_groups_without_public_ip`,
  `router_must_have_nat_ip…`, `router_must_have_dhcp…`) → condition:
  "cluster is not router-integrated — set spec.forProvider.routerRef (or the
  router attachment lacks NAT/DHCP); integration is a day-2 op, recreation is
  NOT required." Final wording pending gate evidence (D-5).

## Touch points

```text
apis/kubernetes/v1alpha1/kubernetescluster_types.go     # routerRef/routerID + CEL, status fields
internal/controller/kubernetes/refs.go                  # resolveRouterRef (uuid, Ready-gated)
internal/controller/kubernetes/connector.go             # wire resolution (skip on delete)
internal/controller/kubernetes/cluster_external.go      # observe/converge integration; WithBody helper
internal/controller/kubernetes/nodepool_external.go     # error-family classification
internal/controller/kubernetes/*_test.go                # unit matrix
docs/openapi-timeweb.json                               # virtual_router_id on updateCluster (nullable)
docs/kubernetes.md, docs/conditions.md, examples/       # routerRef docs; trap text rewrite
package/crds + deepcopy                                 # make generate
```

## Validation

Unit matrix: integrate-on-create; drift re-integrate; move; detach-on-removal
(+record cleared); no-detach-on-transient-ref; refless zero-reads; wait-on-
NAT-less; classification family + passthrough; idempotent over existing
linkage. Gate on inyan-staging: network → router+NAT → cluster+routerRef →
private nodepool Ready (SC-001, the definitive end-to-end); panel-detach
repair; move to second router; detach; desync re-verification. Release
v0.11.0: notes → merge → tag → CI → staging → GitLab #135/#132.
