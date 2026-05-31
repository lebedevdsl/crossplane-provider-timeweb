# Phase 0 — Research

**Feature**: `003-server-mr-and-network` | **Date**: 2026-06-01 | **Plan**: [./plan.md](./plan.md)

This document records the technical decisions for the three new MR kinds + their dependencies. Every NEEDS CLARIFICATION from `plan.md`'s Technical Context is resolved here. Each entry follows the **Decision / Rationale / Alternatives considered** format.

## R-1 — `Server` CRD sizing shape (fixed-preset only in v0.1)

**Question**: The dashboard exposes both "Фиксированная" (fixed preset) and "Произвольная" (custom configurator) sizing tabs. How does v0.1 model this?

**Decision**: **Fixed preset only.** `Server.spec.forProvider.presetName` (string slug) is required. The custom-configurator path (`resources.{cpu, ramMB, diskGB, …}` resolving to `configurator_id` via `/api/v1/configurator/servers`) is NOT exposed on the v0.1 CRD. The forward-compat `ServerConfigurator` dimension registered in feature 002 (`internal/controller/shared/resolver/dimensions.go`) stays at its `fetchUnwired` stub — it'll come online with the follow-up feature that surfaces the custom-resources path.

**Rationale**:
- Spec clarification (Q from initial /speckit-specify session): "Fixed preset only — justified by the user's 'simplify first not all details are required' directive; matches the most-common dashboard flow."
- The slug-resolver primitive already exists (feature 002, `internal/controller/shared/resolver/slug.go` — `Slugify`, `MatchPresetSlug`). Reusing it for `ServerPreset` is a pure registration + new fetcher, no new primitive.
- Constitution §I — additive evolution within `v1alpha1` permitted, so adding `forProvider.resources` later doesn't break existing manifests.

**Alternatives considered**:
- **Both paths now.** Rejected per spec: doubles the surface area + the test matrix; the custom path also requires the more complex configurator-selection algorithm (filter + capability-rank + tightest-fit + tiebreak) that hasn't been exercised in production yet.
- **Custom only.** Rejected: the dashboard defaults to fixed presets, that's the operator-friendly path. Custom is power-user.

## R-2 — `Server.forProvider.os` shape (two-field object)

**Question**: How does the operator type the OS image + version? Single slug, two-field object, or flat ID?

**Decision**: **Two-field object** — `forProvider.os: { image: <lowercase family slug>, version: <upstream version string> }`. The resolver maps `(image, version)` to upstream `os_id` via `/api/v1/os/servers`, using a new `ServerOSImage` dimension of kind `Enum`. No combined-slug shape. No flat `osID` escape hatch in v0.1.

Examples: `{ image: ubuntu, version: "24.04" }`, `{ image: debian, version: "13" }`, `{ image: windows, version: "2022" }`.

**Rationale**:
- Spec clarification (Q from /speckit-clarify session): locked to Option A.
- Matches the upstream catalog's actual shape — `/api/v1/os/servers` returns objects with `name` + `version` fields. 1:1 mapping minimizes resolver complexity.
- Validation per-field independently is cleaner CEL than parsing a `/`-delimited slug.
- The resolver primitive's `Enum` dimension kind already exists (feature 002, `internal/controller/shared/resolver/resolve.go::resolveEnum`). New work is: define `ServerOSImage` constant + fetcher in `dimensions.go`; case-fold the operator's `image` to lowercase + match against the upstream entry's lowercased `name`.

**Alternatives considered**:
- **Combined slug** (`os: "ubuntu/24.04"`). Rejected per clarification — compact but less validate-able; resolver would need to split on `/` and the split rule overloads the slug normalizer from feature 002 (which is for `<short>-<location>` pairs, NOT for `<family>/<version>` pairs).
- **Flat `osID: int64`.** Rejected per clarification — opaque-ID anti-pattern feature 002 explicitly worked to eliminate. We don't even ship the escape hatch in v0.1; an operator who genuinely needs to pin a specific OS revision can do so by typing the exact `name`/`version` the upstream catalog returns.

## R-3 — Cross-resource references (`networkRef` / `projectRef` / `sshKeyRefs` / `floatingIPRefs` / FloatingIP `serverRef`)

**Question**: How are cross-MR references modeled and resolved? Do we hand-roll the resolver or use crossplane-runtime helpers?

**Decision**: **Use crossplane-runtime v2's standard reference resolver** (`reference.ResolutionRequest` + `reference.ResolveOne` for singular refs; `reference.ResolveMultiple` for list refs). Each ref pair on a managed resource is:

```go
// On Server.spec.forProvider:
NetworkRef       *xpv2.Reference          `json:"networkRef,omitempty"`
NetworkSelector  *xpv2.Selector           `json:"networkSelector,omitempty"`
NetworkID        *string                  `json:"networkID,omitempty"`

ProjectRef       *xpv2.Reference          `json:"projectRef,omitempty"`
ProjectSelector  *xpv2.Selector           `json:"projectSelector,omitempty"`
ProjectID        *int64                   `json:"projectID,omitempty"`

SshKeyRefs       []xpv2.Reference         `json:"sshKeyRefs,omitempty"`
SshKeySelector   *xpv2.Selector           `json:"sshKeySelector,omitempty"`
SshKeyIDs        []int64                  `json:"sshKeyIDs,omitempty"`

FloatingIPRefs   []xpv2.Reference         `json:"floatingIPRefs,omitempty"`   // observe-only (FR-017)

// On FloatingIP.spec.forProvider:
ServerRef        *xpv2.Reference          `json:"serverRef,omitempty"`
ServerSelector   *xpv2.Selector           `json:"serverSelector,omitempty"`
ServerID         *int64                   `json:"serverID,omitempty"`
```

Each managed-resource Go type implements `ResolveReferences(ctx, client.Client) error` that calls `reference.ResolveOne`/`ResolveMultiple` for every ref/selector pair, populating the flat `*ID` fields after resolution. Mutual exclusivity between `Ref`/`Selector`/`ID` is enforced by CEL at admission time.

**Rationale**:
- Same pattern feature 002 verified for `KubernetesNodeGroup → KubernetesCluster` (forward-compat decision, not yet implemented but already in the data-model).
- The `reference.Resolve*` helpers handle: target-Ready check (block resolution until referenced MR is `Ready=True`), label selector matching, and the standard Kubernetes-style "selector → resolves to one object" semantics. Hand-rolling this for 5 ref pairs invites bugs.
- The `From: <MR-kind>, Extract: status.atProvider.upstreamID` extractor pattern is exactly what we need — the resolver reads the upstream ID off the target's status and writes it to the flat `*ID` field.

**Alternatives considered**:
- **Hand-rolled walker** matching the pre-feature-002 `ResolveToken` fallback. Rejected: removed by feature 002 for exactly the same reasons. The crossplane-runtime helper is the canonical path.
- **Direct `Get` calls in the connector.** Rejected: doesn't handle selectors, doesn't surface a typed "target not ready" condition. Reinvents the wheel.

## R-4 — `FloatingIP` ownership of bind/unbind side-effects

**Question**: When a `FloatingIP` MR sets `forProvider.serverRef`, who issues the upstream `POST /floating-ips/{id}/bind` call — the `FloatingIP` controller or the `Server` controller?

**Decision**: **The `FloatingIP` controller owns bind/unbind.** The `Server` controller does NOT issue bind/unbind calls even when `Server.forProvider.floatingIPRefs` is populated. `floatingIPRefs` on Server is observe-only (per FR-017) — purely diagnostic for `kubectl describe server`.

Concretely: the `FloatingIP` controller's `Observe`/`Create`/`Update` flow is:
1. **Observe**: GET `/api/v1/floating-ips/{id}` → read `bound_to: { resource_type, resource_id }`. If `bound_to.resource_id == status.atProvider.resolvedServerID` (or both nil) → no drift. If they differ → bind drift (operator changed `serverRef` or upstream changed bind out-of-band).
2. **Create**: POST `/api/v1/floating-ips` → record `upstreamID` + `ip`. If `serverRef` resolves to a `Ready=True` Server → immediately POST `/floating-ips/{id}/bind {resource_type: "server", resource_id: <id>}`. Record `resolvedServerID` + `boundAt` in status.
3. **Update** covers two drift cases: (a) operator changed `serverRef` to a different Server → unbind from old + bind to new (sequential, not transactional — Crossplane runtime tolerates the intermediate state). (b) operator cleared `serverRef` → unbind only.
4. **Delete**: if currently bound, unbind first; then DELETE `/api/v1/floating-ips/{id}`.

**Rationale**:
- Constitution §II — single-owner for upstream side-effects. Splitting bind/unbind across two controllers (Server creates the FIP, FloatingIP doesn't know about it; OR vice versa) creates a window where two controllers race to mutate the same upstream entity.
- The `FloatingIP` MR is the natural owner because: its identity (the IPv4 address) outlives any individual binding; the bind is more of an *attribute* of the FloatingIP than of the Server.
- This matches the AWS upjet pattern for `EipAssociation` (the EIP owns the bind), and the GCP pattern for forwarding rules pointing at instances.

**Alternatives considered**:
- **Separate `FloatingIPBinding` MR.** Rejected: triples the number of objects an operator needs to manage for what's conceptually one resource. Crossplane's `aws.ec2.Eip` + `aws.ec2.EipAssociation` split is widely regarded as awkward.
- **Server controller owns the bind via `floatingIPRefs`.** Rejected: makes the Server controller mutate a sibling MR's upstream resource, violating Constitution §II's single-owner rule.

## R-5 — `Server.Update` field mutability

**Question**: Which `Server.spec.forProvider` fields can be PATCHed on a live server (FR-009)?

**Decision**: Treat the upstream `PATCH /api/v1/servers/{id}` body as the source of truth. Per the openapi probe at planning time, that body accepts `name`, `comment`, `hostname`, `bandwidth`, and `cloud_init`; NOT `preset_id`, `os_id`, `image_id`, `configuration`, `availability_zone`, `project_id`, `network`, or `ssh_keys_ids`. Therefore:

| Field | Mutable on live server? | Reason mapping |
|---|---|---|
| `name` | YES | Direct PATCH |
| `hostname` | YES | Direct PATCH |
| `comment` | YES | Direct PATCH |
| `cloud_init` | YES (no-op past first boot) | Direct PATCH; reapply requires reboot which we do NOT trigger |
| `presetName` | NO → `ImmutableFieldChange` | Resize lands in a separate feature |
| `os` | NO → `ImmutableFieldChange` | Re-image lands in a separate feature |
| `location` | NO → `ImmutableFieldChange` | Region moves require recreate |
| `availabilityZone` | NO → `ImmutableFieldChange` | AZ moves require recreate |
| `sshKeyRefs` / `sshKeyIDs` | NO → `ImmutableFieldChange` | SSH key list is set at create only on Timeweb cloud servers |
| `networkRef` / `networkSelector` / `networkID` | NO → `ImmutableFieldChange` | Network attachment is set at create only |
| `projectRef` / `projectSelector` / `projectID` | NO → `ImmutableFieldChange` | Project assignment is set at create only |
| `floatingIPRefs` | n/a | Observe-only per FR-017; not a source of mutation |

**Rationale**:
- Matches what the Timeweb API actually accepts on PATCH. Forwarding an unsupported field generates a 4xx that surfaces as `Synced=False, reason=APIError` — confusing for the operator. Pre-flight CEL on the MR (`+kubebuilder:validation:XValidation` rule with `oldSelf` comparison) lets us surface `ImmutableFieldChange` cleanly.
- Constitution §II — fail fast with a typed condition rather than a transient API error.
- `cloud_init` is treated as mutable in the spec but "no-op past first boot" — accurately captures Timeweb's behavior (the script runs once at first boot, subsequent edits are stored but inert until the server is recreated).

**Alternatives considered**:
- **Mark ALL `forProvider` fields immutable.** Rejected: makes `name` / `comment` / `hostname` edits require a server recreate, which is operator-hostile. Timeweb supports renaming live; we should too.
- **Trust the upstream API for validation.** Rejected: lets the operator submit a manifest that fails after 4–10s instead of at admission. CEL pre-flight is a better operator experience.

## R-6 — `Network` (VPC) lifecycle and the `/v1` vs `/v2` path split

**Question**: Why does the openapi spec list `POST /api/v2/vpcs` for create but `DELETE /api/v1/vpcs/{id}` for delete? Which path should the client wrap?

**Decision**: Wrap **both** paths. The `network.Create` external method posts to `/api/v2/vpcs`; the `network.Delete` external method calls `DELETE /api/v1/vpcs/{id}`. The `network.Observe`/`Update` paths likewise use `/api/v2/vpcs/{id}`. This split is a Timeweb-side migration in-progress (v2 introduced new fields like `availability_zone`); the v1 delete endpoint stayed for compatibility. The provider abstracts this away — operators see a single `Network` MR contract.

**Rationale**:
- The openapi spec is authoritative; this split is real and documented.
- We follow the `project_timeweb_underscore_envelopes` memory: curl-probe the live API at implementation time to confirm response shapes are what the spec claims.
- Wrapping both paths in one Go client method (`(*Client).DeleteVPC(id)` internally calls `DELETE /api/v1/vpcs/{id}`) is the simplest hide-the-split approach.

**Alternatives considered**:
- **v2 paths only, hope delete works.** Rejected: the openapi doesn't list `DELETE /api/v2/vpcs/{id}`, so calling it is undefined.
- **Hand-write the client, skip oapi-codegen for VPC.** Rejected: cuts against the established generator-allowlist pattern from feature 001/002.

## R-7 — Cloud-init validation

**Question**: Should the `cloud_init` field be validated by the provider (schema check, size limit, base64 encoding decision)?

**Decision**: **Pass-through.** `Server.forProvider.cloudInit` is a raw string field. CEL validation limits it to ≤16 KiB (Timeweb's documented limit per the openapi `description`). No schema validation (cloud-init YAML is content-addressable and the operator may legitimately use the `#!/bin/bash` shebang form). Encoding/decoding is not the provider's job — operators paste cloud-init as-is.

**Rationale**:
- Pass-through matches the upstream contract: Timeweb takes whatever the operator sends and serves it to cloud-init at first boot.
- Validating cloud-init YAML is a rabbit hole (cloud-init has its own dialect, multiple top-level sections, optional shebang form). Not the provider's responsibility.
- The 16 KiB CEL ceiling is cheap to enforce and matches the API.

**Alternatives considered**:
- **Validate as YAML.** Rejected — overreach; cloud-init scripts in the wild are routinely `#!/bin/bash` shebang scripts that aren't YAML at all.
- **No size limit.** Rejected — the upstream limit is real; we should fail at admission with a clear message rather than at the upstream call.

## R-9 — API-group taxonomy: `compute.m.…` vs `network.m.…`

**Question**: Where do future Timeweb network-class kinds (`Router`, `Balancer`, `FirewallRule`, `SecurityGroup`) live? Same Go package as `Network` + `FloatingIP`, or fragmented across more API groups?

**Decision**: **Single `network.m.timeweb.crossplane.io` API group + single `apis/network/v1alpha1` Go package for every network-class kind.** v0.3 ships `Network` + `FloatingIP`; follow-up features add `Router`, `Balancer` (dashboard image #3 — "Создать балансировщик", own tariff with node count + bandwidth tiers), `FirewallRule`, `SecurityGroup` to the SAME group + SAME Go package. Splitting into `loadbalancer.m.…` / `firewall.m.…` / `router.m.…` is explicitly rejected.

Same idiom applies to `compute.m.timeweb.crossplane.io`: v0.3 ships `Server`; future `Disk`, `Backup`, `Snapshot` join the same group.

**Rationale**:
- Spec clarification (2026-06-01 network-group-commitment session): operator-facing reasoning — the dashboard groups these kinds together under "Сети" (Networks); the Crossplane API surface should mirror that grouping.
- Go-package overhead per new API group: a new `groupversion_info.go`, a new `AddToScheme` (and a matching call in `cmd/provider/main.go`), a new Crossplane `provider.AddToScheme` registration, a separate `package/crds/<group>_*.yaml` directory, separate xpkg metadata entries. Each one of these is a paper cut; ~5 cuts per kind multiplied by 4+ future network kinds is a lot of accidental boilerplate.
- Cross-resource references work uniformly within the same Go package (no import-cycle risk; one DeepCopy generator pass covers all kinds; one `managed.go` per-kind accessor file with shared helpers).
- Matches AWS's `ec2` API group containing both `Instance` (VM-class) and `Vpc`/`Subnet`/`SecurityGroup` (network-class)? No — AWS actually splits compute from networking in upjet-provider-aws. But the cross-ref granularity here is finer: every `Server` touches a `Network` and (optionally) a `FloatingIP` AND a `FirewallRule` AND a `Router`. Co-locating saves cross-package boilerplate.
- Matches `crossplane-contrib/provider-helm` and `crossplane-contrib/provider-kubernetes` (from feature 002 R-2 audit): both put related kinds in one group (`helm.crossplane.io` covers `Release` + `Repository`; `kubernetes.crossplane.io` covers `Object` + `ObservedObjectCollection`). Fragmentation by sub-domain is NOT the established v2 idiom.

**Alternatives considered**:
- **One group per concept** (`loadbalancer.m.…`, `firewall.m.…`, `router.m.…`, …). Rejected per rationale above — 4-5× the boilerplate, no operator-visible benefit, cross-resource refs become more painful.
- **One group per Timeweb dashboard section** (compute, network, storage, registry, etc.). Already what we have — `network.m.…` IS this. R-9 just locks it down so a future contributor doesn't unilaterally split it.
- **Single `m.timeweb.crossplane.io` group for everything**. Rejected: scales poorly (the existing `containerregistry.m.…` + `objectstorage.m.…` groups are already established) and obscures the dashboard-section mapping.

**Forward implications for tasks.md** (no plan-phase change to current bundles):
- The future `Balancer` MR will be authored in `apis/network/v1alpha1/balancer_types.go` + `internal/controller/network/balancer_external.go`, alongside the existing `network_types.go` and `floatingip_types.go`.
- The future `FloatingIP.spec.forProvider.serverRef` trio extends to a sibling `balancerRef` / `databaseRef` / `networkRef` trio when those kinds land — the upstream `bind` endpoint already accepts `resource_type ∈ {server, balancer, database, network}` per the openapi probe (feat 003 R-4 / contracts/timeweb-endpoints.md). v0.3 hardcodes `"server"`; future expansion is a `switch`-statement addition, not a redesign.

## R-8 — E2E coverage strategy for the 4 new bundles

**Question**: What's the minimum kuttl test set that proves all 7 success criteria from spec.md?

**Decision**: Four new bundles, each gated solely on `TIMEWEB_CLOUD_TOKEN` (no separate test-only env vars).

| Bundle | Covers SCs | Smallest-tier configuration |
|---|---|---|
| `08-network-lifecycle/` | SC-003 (partial) | Create `Network` with `subnetCIDR: 10.30.0.0/24, location: msk-1`; assert `[Ready=True, Synced=True]`. Delete; assert removal. |
| `09-server-lifecycle/` | SC-001, SC-002, SC-006 | Create the smallest premium server in `msk-1` with Ubuntu 24.04, no network. Assert Ready within 10 min. PATCH `comment`; assert reconciliation. Apply with a bogus `presetName`; assert `Synced=False, reason=ReconcileError` carrying valid-slug list. |
| `10-server-with-network/` | SC-003 (full) | Create Network + 2 Servers attached to it. Assert both `Server.status.atProvider.privateIP` fall in the CIDR. (No actual SSH-between-servers test in the bundle — that requires shelling out from the test runner; it's covered by the spec's manual-validation step.) |
| `11-floating-ip-bind/` | SC-005, SC-007 | Create FloatingIP with no `serverRef` → assert Ready, no `resolvedServerID`. Patch with `serverRef` pointing at the `09-server-lifecycle` server (or its replica). Assert bind. Re-patch with a different server. Assert exactly one unbind + one bind. Clear `serverRef`. Assert unbind. |

SC-004 (Server delete removes upstream within 5 min) is asserted as the cleanup step of bundle `09-server-lifecycle/`.

**Rationale**:
- The 4-bundle split lets `09` run independently (cheapest cost — single server, no VPC, no FloatingIP).
- The smallest premium preset (~800 ₽/мес, prorated to hourly ≈ 1.09 ₽/hour per the dashboard) keeps the cost of one full e2e run under €0.05.
- `kuttl.sh` already discovers the smallest available preset at runtime (extended for `getServersPresets`); no per-bundle hardcoded slug.

**Alternatives considered**:
- **One mega-bundle.** Rejected: an early-step failure blocks all subsequent assertions; kuttl's per-bundle isolation is much easier to debug.
- **Network + Server in one bundle.** Rejected: SC-003 needs the network case to work independently of Server-attached cases (we want to catch a Network-only regression that doesn't show up when a Server is attached).
