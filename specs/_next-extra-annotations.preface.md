# Preface — Future feature: `extra-annotations` (opt-in dataplane safety guards)

**Status:** vague seed / parking lot — NOT ready for `/speckit-specify`. Deferred out
of 007 (2026-06-17, owner decision). Discuss + sharpen before specifying.

## Idea (one line)

Opt-in, annotation-driven safety guards for **dataplane-bearing** managed resources —
protect operators from accidentally destroying data when deleting an MR.

## Why deferred from 007

007 (maintenance round) deliberately ships **no** provider-side delete guard. The
guard design touches deletion semantics, Crossplane `managementPolicies`, and
cross-kind "is this object holding data?" definitions — too much to settle inside a
small maintenance round. Parked here until it can be its own scoped feature.

## Rough shape (all TBD — do not treat as decided)

- **Mechanism:** an opt-in annotation on the MR (e.g. a "require explicit
  confirmation before destructive delete" flag), block-by-default vs warn-only — open.
- **Candidate kinds (dataplane objects):** S3 buckets (object count), databases,
  servers (disks), KubernetesCluster (running workloads — "emptiness" is fuzzy),
  ContainerRegistry (images). The owner flagged **cluster** as the prime case, also
  deferred.
- **Confirmation UX:** an explicit override annotation to proceed; how it interacts
  with `managementPolicies`/orphan — open.
- Possibly broader than delete-guards — "extra annotations" as a general opt-in
  behavior surface (hence the working name).

## Open questions to resolve before specifying

- Which kinds, and how is "has data / is non-empty" determined per kind?
- Block-by-default or warn-only? Per-kind default?
- Annotation vocabulary + how it composes with Crossplane deletion semantics.
- Is this only delete-guards, or a wider opt-in annotation framework?

*Seeded from the 007 `/speckit-specify` clarification on US5 (destructive ops).*
