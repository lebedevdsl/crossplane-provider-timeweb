<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan at
`specs/003-server-mr-and-network/plan.md`.

Companion artifacts in the same directory:
- `spec.md` — feature specification with locked clarifications. Adds three
  new MR kinds: `Server` (cloud VM, `compute.m.timeweb.crossplane.io`),
  `Network` (VPC, `network.m.timeweb.crossplane.io`), and `FloatingIP`
  (floating IPv4, `network.m.timeweb.crossplane.io`). Scope is the
  dashboard's "Create Server" flow simplified to the fixed-preset path
  (custom configurator deferred). Cloud servers only — dedicated servers
  out. Cross-MR refs use the standard crossplane-runtime resolver:
  `Server` references `Network`, `Project`, `SshKey`, and (observe-only)
  `FloatingIP`; `FloatingIP` owns bind/unbind to a `Server`.
- `research.md` — Phase 0 decisions (R-1 sizing shape, R-2 OS object shape,
  R-3 cross-resource references via crossplane-runtime, R-4 FloatingIP
  bind ownership, R-5 server field mutability, R-6 the VPC v1/v2 path
  split, R-7 cloud-init pass-through, R-8 e2e bundle strategy).
- `data-model.md` — entities: `Server`, `Network`, `FloatingIP` (with full
  Go-style spec/status shapes + CEL rule list + lifecycle), the two new
  resolver dimension registrations (`ServerPreset`, `ServerOSImage`), and
  the relationships diagram.
- `contracts/` — per-kind operator-facing contracts: `server-v1alpha1.md`,
  `network-v1alpha1.md`, `floatingip-v1alpha1.md`, plus the upstream
  endpoint inventory in `timeweb-endpoints.md`.
- `quickstart.md` — operator walkthrough from a minimum Server up through
  network attachment and floating-IP pinning; troubleshooting matrix; what's
  NOT in v0.3.

Feature 002 (`specs/002-readonly-presets-design/`) merged into main —
shared `ProviderConfigSpec`, the in-controller catalog resolver primitive,
the K8s forward-compat dimension registry, and the kuttl/k3d e2e harness
all carry forward unchanged. The MVP foundation at `specs/001-mvp-scaffolding/`
remains authoritative for the `Project` / `SshKey` kinds and the cross-cutting
decisions (error classification, external-name, tooling).

The constitution governing principles for this provider lives at
`.specify/memory/constitution.md`.
<!-- SPECKIT END -->
