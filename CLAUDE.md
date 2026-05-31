<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/002-readonly-presets-design/plan.md`.

Companion artifacts in the same directory:
- `spec.md` — feature specification with binding clarifications. The 2026-05-31
  respec moved catalog data out of Kubernetes objects: no `Catalog` /
  `*Preset` / `*Configurator` CRDs. Operator-facing shape on every consuming
  MR is `presetName XOR resources` (per sizing block); IDs are derived by the
  controller. ProviderConfig is split into namespaced + cluster-scoped pair.
  The spec also locks in K8s-readiness: a third "enum" resolver dimension
  kind, per-block axis-locking semantics, and the split-MR design for the
  upcoming `KubernetesCluster` + `KubernetesNodeGroup` feature.
- `research.md` — Phase 0 decisions (resolver primitive design, dual-PC
  mechanics, `ContainerRegistryPreset` removal path, CEL `oneOf` enforcement,
  K8s-readiness dimension table, test strategy).
- `data-model.md` — entities: the dual-scope ProviderConfig pair, refactored
  `ContainerRegistry` / `S3Bucket` shapes, the internal resolver cache, and
  the dimension registry (initial + K8s forward-compat registrations).
- `contracts/` — per-kind contracts: `providerconfig-namespaced-v1alpha1.md`,
  `clusterproviderconfig-v1alpha1.md`, `containerregistry-refactor-v1alpha1.md`,
  `s3bucket-refactor-v1alpha1.md`, and `resolver-internal.md` (the internal
  resolver primitive).
- `quickstart.md` — MVP-to-this-feature migration walkthrough.

The MVP foundation is at `specs/001-mvp-scaffolding/` — its `spec.md`,
`research.md`, `data-model.md`, `contracts/`, and `quickstart.md` remain
authoritative for the `Project` / `SshKey` kinds and the cross-cutting
decisions (error classification, external-name, tooling) carried forward
unchanged.

The constitution governing principles for this provider lives at
`.specify/memory/constitution.md`.
<!-- SPECKIT END -->
