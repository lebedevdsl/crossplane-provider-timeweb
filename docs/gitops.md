# GitOps with this provider

Read this before wiring the provider's resources into ArgoCD or Flux.

## `crossplane.io/external-name` is provider-owned — never render it from git

That annotation records the identity of the upstream object. The provider
writes it; git must not.

A GitOps tool that keeps re-applying a pinned (or stale) value fights the
provider: a stale id observes as "resource missing", so the provider creates a
replacement, writes the new id, and the tool reverts it — every sync mints
another cloud object. This is not hypothetical: on 2026-07-25 a pinned
annotation left over from a recreated cluster produced **three billable node
groups from one manifest** in three sync cycles.

### ArgoCD

Exclude the annotation from diffing and self-heal:

```yaml
# Application spec
  ignoreDifferences:
    - group: "*"
      kind: "*"
      jsonPointers:
        - /metadata/annotations/crossplane.io~1external-name
```

A complete Application is in
[`examples/argocd/application.yaml`](../examples/argocd/application.yaml).

The same rule applies to anything else the provider writes: `status`, and the
`crossplane.io/external-create-*` annotations. Sync only what you author.

### Flux

Flux does not revert fields it does not manage, so no special configuration is
needed — but the same rule holds: do not template the annotation.

## What the provider does to defend itself

**Scope: `KubernetesClusterNodepool` only** (v0.11.1). Other kinds —
`Network`, `Router`, `Server`, `FloatingIP`, `KubernetesCluster` — do **not**
have this guard yet, so the `ignoreDifferences` stanza above is your primary
defence, not a belt-and-braces extra.

Where the guard exists, a stomped identity is parked rather than duplicated:

- `Ready=False, reason=ExternalNameConflict` — the external-name points at a
  missing object while the resource's status remembers a different live one.
  Nothing is created. Restore the annotation to the remembered id (and stop
  rendering it in git), or clear `status.atProvider.upstreamID` to
  deliberately create anew.
- `Synced=False, reason=AdoptionAmbiguous` — a retried create found several
  upstream candidates matching the declaration. Adopt one explicitly via the
  annotation and remove the extras; the provider refuses to guess.

## Runbook: "cannot determine creation result"

If the provider is restarted mid-create, crossplane-runtime wedges the
resource:

```
cannot determine creation result - remove the crossplane.io/external-create-pending
annotation if it is safe to proceed
```

Recovery:

1. Check upstream (panel or API) whether the object was actually created.
2. If it was: set `crossplane.io/external-name` to its id.
3. Remove the `crossplane.io/external-create-pending` annotation.

Since v0.11.1 a retried nodepool create first looks for an existing match and
adopts it (or refuses on ambiguity), so clearing the marker cannot mint
duplicates for that kind. For other kinds, verify upstream first.

## Sync-wave ordering

References gate themselves — a resource whose target isn't Ready waits with a
clear condition rather than failing — so strict ordering is optional. Two
places where ordering still saves time:

- **Private clusters**: network → router (with NAT) → cluster (`routerRef`) →
  private nodepool. See [`examples/private-cluster.yaml`](../examples/private-cluster.yaml).
- **Anything referencing a Project**: create the Project first, or expect a
  few "not yet ready" cycles.
