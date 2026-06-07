<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/004-k8s-cluster-nodepool/plan.md`.

Companion artifacts in the same directory:
- `spec.md` — feature specification with locked clarifications. Adds three
  new MR kinds in API group `kubernetes.m.timeweb.crossplane.io`:
  `KubernetesCluster` (managed control plane), `KubernetesClusterNodepool`
  (worker group, `clusterRef`), and `KubernetesClusterAddon` (one installed
  addon, `clusterRef`). Scope is the dashboard's "Create Kubernetes cluster"
  flow, fixed-preset path (custom configurator deferred). Day-2 ops in scope:
  nodepool scaling, autoscaling/autohealing, in-place version upgrade,
  kubeconfig connection Secret. Reuses feat-003 `Network` (VPC attach) +
  feat-001 `Project` as ref targets.
- `research.md` — Phase 0 decisions (R-1 group/package layout, R-2 master/
  worker preset dimension split, R-3 exact-match k8sVersion, R-4 driver/AZ as
  CRD enums, R-5 Nodepool-MR-only / zero-worker create, R-6 relative-delta
  scaling, R-7 in-place upgrade, R-8 kubeconfig secret, R-9 addon shape,
  R-10 external-name/clusterID persistence, R-11 ref resolution, R-12 client
  tag + e2e).
- `data-model.md` — entities: `KubernetesCluster`, `KubernetesClusterNodepool`,
  `KubernetesClusterAddon` (Go-style spec/status + CEL + lifecycle), the three
  promoted resolver dimensions (`DimKubernetesMasterPreset`/`WorkerPreset`/
  `Version` stub→real), and the relationships diagram.
- `contracts/` — per-kind contracts: `kubernetescluster-v1alpha1.md`,
  `kubernetesclusternodepool-v1alpha1.md`, `kubernetesclusteraddon-v1alpha1.md`,
  plus the upstream endpoint inventory in `timeweb-k8s-endpoints.md`.
- `quickstart.md` — operator walkthrough: minimum cluster+pool, scaling,
  network/project attach, version upgrade, addons; troubleshooting matrix;
  what's NOT in v0.x.

Features 001/002/003 merged into main — shared `ProviderConfigSpec`, the
in-controller catalog resolver primitive (incl. the K8s forward-compat
dimensions this feature promotes to real fetchers), the cross-MR `client.Get`
ref idiom, the feat-003 `Network`/`Server` kinds, and the kuttl/k3d e2e harness
all carry forward unchanged. The MVP foundation at `specs/001-mvp-scaffolding/`
remains authoritative for the `Project` / `SshKey` kinds and the cross-cutting
decisions (error classification, external-name, tooling).

The constitution governing principles for this provider lives at
`.specify/memory/constitution.md`.
<!-- SPECKIT END -->
