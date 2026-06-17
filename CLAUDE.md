<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/006-router-private-cluster/plan.md`.

Companion artifacts in the same directory:
- `spec.md` — feature spec (clarified; 3 Q&A + live-probe findings baked into
  a Clarifications session). One new MR kind: **`Router`** in
  `network.m.timeweb.crossplane.io` — Timeweb's NAT/DHCP router appliance —
  with inline network attachments (per-attachment `nat`/`dhcp`, minItems=1),
  public addresses by **referencing the existing `FloatingIP` kind** (the
  Router never orders IPs; NAT-without-IP rejected at admission), tier/preset
  sizing (tier carries 1-node vs 2-node HA and the region), in-place resize
  in scope (capture pending, immutable-reject fallback), and the
  **private-cluster arrangement** (US3): cluster network behind a NAT'd
  router ⇒ worker nodes with NO public IPs. Public-by-default for nodes is
  unchanged (FR-008/SC-006). CRaaS pull-secrets explicitly out of scope.
- `research.md` — Phase 0: R-1 probe-verified upstream surface (undocumented
  `/api/v1/routers*`, `/presets/routers` — create/rename/attach/detach/DHCP/
  delete all verified live; silent-no-op quirks; new-VPC settle delay),
  R-2 FloatingIP equivalence (confirmed), R-3 NAT-toggle capture pending,
  R-4 resize capture pending, R-5 K8s-binding experiment (derived
  `parent_services` hypothesis + FR-012 delete-while-bound test), R-6
  `DimRouterPreset`, R-7 inline attachments, R-8 network group placement,
  R-9 error classification, R-10 e2e bundles 18/19.
- `data-model.md` — Router spec/status (attachment struct, FloatingIP
  selectors, status mirror incl. `parentServices`), `DimRouterPreset`
  promotion, no-schema-change expectation for `KubernetesCluster`,
  relationships diagram.
- `contracts/` — `router-v1alpha1.md` (CRD contract + conditions table),
  `timeweb-router-endpoints.md` (probe-verified endpoint inventory, create
  body, behavioral quirks incl. the reproduced error-yet-created cluster
  create on router-attached networks → adoption guard required).
- `quickstart.md` — operator walkthrough: router + NAT'd network, the private
  cluster, day-2 ops, troubleshooting matrix.

Feature 005 (custom configurator sizing + ContainerRegistry group move +
tech-debt pass) is complete on its branch — T028 live canary passed all four
bundles. Key carried-forward lessons baked into 006's plan: location-first
catalog resolution (`azLocation`: spb-3↔ru-1, msk-1↔ru-3, ams-1↔nl-1,
fra-1↔de-1), master/worker tag-partitioned `DimKubernetes*Configurator` dims,
nodepool Ready gated on actual per-node state (`status.atProvider.nodes`),
parent-cluster `Watches` + 60s-capped rate limiter for dependent kinds,
`UpstreamFailed` condition vocabulary, and "2xx ≠ converged — verify by
re-observation" for the Timeweb API. See
`specs/005-custom-sizing-configurators/` for its artifacts.

Features 001/002/003/004 merged into main — shared `ProviderConfigSpec`, the
in-controller catalog resolver primitive, the cross-MR `client.Get` ref idiom,
the `Network`/`FloatingIP`/`Server` kinds (003), the K8s kinds (004), and the
kuttl/k3d e2e harness all carry forward unchanged. The MVP foundation at
`specs/001-mvp-scaffolding/` remains authoritative for the `Project` /
`SshKey` kinds and the cross-cutting decisions (error classification,
external-name, tooling).

The constitution governing principles for this provider lives at
`.specify/memory/constitution.md`.
<!-- SPECKIT END -->
