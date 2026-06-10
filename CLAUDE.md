<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/005-custom-sizing-configurators/plan.md`.

Companion artifacts in the same directory:
- `spec.md` — feature spec (clarified). Three bundled workstreams:
  (1) **custom configurator sizing** — a `forProvider.resources`
  (`cpu`/`ramGB`/`diskGB` + optional axes) block, CEL-`exactly-one-of` with
  `presetName`, resolving to an upstream `configurator_id` via the existing
  resolver `Configurator` dimension — on `Server` first, then
  `KubernetesCluster`/`KubernetesClusterNodepool` (removes the ambiguous-preset
  pain from feat 004; presets stay first-class); (2) **ContainerRegistry hard
  move** from `containerregistry.m.…` → `kubernetes.m.timeweb.crossplane.io`
  (mirrors the dashboard's Kubernetes→registries tab; breaking, pre-1.0);
  (3) **tech-debt pass** — fix the `Server.resolveRefs` spec-mutation/CEL
  latent bug (same as the feat-004 cluster fix), e2e harness fixes
  (`e2e.down`, kuttl multi-`--test`, condition-order asserts), and
  Connect-error `reason=Reconciling` alignment.
- `research.md` — Phase 0 (R-1 configurator reuse, R-2 axis/unit normalization,
  R-3 Server resources mapping, R-4 sizing-switch, R-5 K8s `configuration`,
  R-6 CR group-move strategy, R-7 Server-CEL fix, R-8 e2e fixes, R-9 reason
  alignment).
- `data-model.md` — the shared `resources` block + per-kind changes +
  `lockedConfiguratorID` + the `DimServerConfigurator` stub→real promotion +
  the relocated ContainerRegistry kinds.
- `contracts/` — `server-resources-v1alpha1.md`,
  `kubernetes-resources-v1alpha1.md`, `containerregistry-group-move.md`,
  `timeweb-configurator-endpoints.md`.
- `quickstart.md` — custom-sizing walkthrough (Server + K8s) + the CR
  group-move re-apply.

Earlier features' companion artifacts (for reference):
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
