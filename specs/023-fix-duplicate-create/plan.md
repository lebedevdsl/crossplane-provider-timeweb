# Implementation Plan: Duplicate-create defenses

**Branch**: `023-fix-duplicate-create` | **Date**: 2026-07-25 | **Spec**: [spec.md](./spec.md)

## Summary

v0.11.1 PATCH (behavior + conditions only; `status.atProvider.upstreamID`
already exists — no schema change). Two nodepool guards + audit + docs.

## Constitution Check

I: no CRD change (PASS). II: this feature ENFORCES the no-duplicates clause;
Observe stays read-only (guards read only); the stomp condition parks the
resource without mutating upstream (PASS). III: full unit matrix (PASS).

## Design decisions

- **D-1 stomp defense (Observe)**: two trigger points — (a) `DecodeID` fails
  (empty/garbage external-name) with `status.atProvider.upstreamID` non-empty;
  (b) group GET canonically 404s while the recorded `upstreamID` ≠ the
  external-name id. Both ⇒ `Ready=False ExternalNameConflict` condition naming
  both identities + remedies, return exists:true/upToDate:true (zone-echo
  parking idiom — no create, no churn, self-clears when the external-name is
  restored). Same-id 404 ⇒ unchanged recreate.
- **D-2 adoption guard (Create)**: on `ExternalCreateIncomplete` OR
  `external-create-failed`, after sizing resolution: `GetClusterNodeGroups`
  (`{node_groups: […]}`), match name == declared AND sizing matches (preset_id
  when preset-declared; configurator_id when resources-declared — local group
  struct gains `name`/`configurator_id` fields). 1 ⇒ `SetExternalName`,
  return; 0 ⇒ POST; >1 ⇒ `Synced=False AdoptionAmbiguous` + error naming ids.
- **D-3 audit**: table in research.md. cluster/router: guarded (006).
  nodepool: this feature. Server/Network/FloatingIP/S3*/CDN/Firewall/Project/
  SSHKey/Addon: risk+surface assessed; any flagged remainder is SCHEDULED
  (spec FR-004 amended accordingly) — patch scope stays the incident kind.
- **D-4 docs**: getting-started GitOps-hygiene section (external-name is
  provider-owned; ArgoCD `ignoreDifferences` example), conditions rows for
  both reasons, create-wedge runbook note.

## Touch points

```text
internal/controller/shared/conditions.go        # ReasonExternalNameConflict, ReasonAdoptionAmbiguous
internal/controller/kubernetes/nodepool_external.go(+_test)  # both guards; group struct name/configurator_id
specs/023-fix-duplicate-create/research.md      # audit table
docs/getting-started.md, docs/conditions.md, docs/kubernetes.md
```

## Validation

Unit matrix per FR-006. Live staging replay: minimal cluster + PUBLIC pool
(cheapest); (1) stomp replay — pin stale id → condition, restore → converge;
(2) SC-003 out-of-band group delete → recreate; (3) ambiguity-marker adoption
smoke via unit only (kill-mid-create not reliably reproducible live). Then
release v0.11.1.
