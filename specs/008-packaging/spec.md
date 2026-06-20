# Feature Specification: Provider Packaging & Remote-Cluster e2e Delivery

**Feature Branch**: `008-packaging`

**Created**: 2026-06-17

**Status**: Draft

**Input**: User description: "008-packaging. as we triggered waf on timeweb side and i have a working staging cluster on tw, we have to run e2e on that cluster from inside the timeweb hehe. for e2e bundle it will be just different context, but delivery of the image should be ready and pushed, consult with crossplane doc on how to package provider if it's not clear"

## Context & Motivation

External egress to the Timeweb API (`api.timeweb.cloud`) from the developer's
network is currently blocked by Timeweb's Qrator WAF, so the live e2e suite can
no longer run from the local k3d harness. The owner has a working Timeweb
**staging cluster**; running the provider *inside* Timeweb removes the WAF block
for managed-resource reconciliation. To do that, the provider must be **published
as an installable package** (the current tooling only builds a local `.xpkg`
file — nothing pushes it to a registry), and the **e2e harness must be able to
target an arbitrary cluster context** (today it hard-rejects any non-`k3d-`
context). This feature is packaging + delivery only — no managed-resource
behavior changes.

## Clarifications

### Session 2026-06-18

- Q: What does the "GitHub release target" consist of? → A: Public `ghcr.io` package (signed, multi-arch) + a GitHub Release (git tag + notes) with the built `.xpkg` attached as a release asset. CRaaS is test/e2e-only, NOT a public publish target.
- Q: What maturity does the first public release claim? → A: Gate on the full billable e2e — the Server/K8s/Nodepool/Router bundles MUST run green on a live cluster before the public release is cut; all MR kinds are validated (no "experimental" kinds shipped).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Publish an installable provider package (Priority: P1)

A maintainer builds and **pushes** a versioned provider package (and its
controller runtime image) to a container registry, so that any Crossplane
cluster can install the provider by referencing the published package — without
building from source or side-loading a local image.

**Why this priority**: Nothing downstream works without a published, pullable
package. The remote-cluster e2e (US2) and any real operator install both depend
on it. Today the package never leaves the build host.

**Independent Test**: From a clean checkout, run the publish flow; then on a
fresh cluster with Crossplane installed, reference the published package in a
Provider install and confirm the provider reaches Installed + Healthy and can
reconcile a trivial managed resource.

**Acceptance Scenarios**:

1. **Given** a clean working tree at a release version, **When** the maintainer runs the publish flow, **Then** a versioned provider package and a multi-arch controller image are pushed to the registry and are pullable by digest and by tag.
2. **Given** the published package reference, **When** an operator installs the provider on a fresh cluster, **Then** the provider becomes Installed + Healthy with no manual image loading.
3. **Given** a published version, **When** the same version is re-published, **Then** the behavior is controlled (no silent overwrite of an already-released tag, or an explicit, intentional re-push).

---

### User Story 2 - Run the e2e suite against a real cluster (Priority: P1)

The e2e suite can target an arbitrary Kubernetes context — specifically the
Timeweb **staging cluster** — installing the **published** provider (pulled, not
side-loaded), running the existing bundles, isolating its resources, and cleaning
up afterward. This lets the suite run from inside Timeweb where the WAF does not
block the Timeweb API.

**Why this priority**: This is the immediate unblock — the only way to run live
e2e while the WAF blocks external egress. It is the validation path for every
prior feature's managed resources.

**Independent Test**: Point the harness at the operator-provided staging cluster
context (e.g. `twc-staging`), run the suite using the published package, and
confirm the bundles reach green and the live Timeweb account shows zero leftover
resources afterward.

**Acceptance Scenarios**:

1. **Given** a reachable non-k3d cluster context that is set explicitly, **When** the suite is launched, **Then** it runs against that cluster (the k3d-only restriction no longer applies) and refuses to run if no context is explicitly provided.
2. **Given** the published provider package, **When** the suite sets up, **Then** it installs the provider by pulling the package (no local image side-load) at a configurable version/reference.
3. **Given** a completed run on the shared staging cluster, **When** teardown finishes, **Then** the run's resources are isolated to a dedicated namespace and both cluster objects and the live Timeweb API show no orphans.
4. **Given** the suite runs from inside Timeweb, **When** the provider reconciles managed resources, **Then** Timeweb API calls succeed (no WAF block).

---

### User Story 3 - Reproducible, verifiable delivery (Priority: P2)

Published artifacts are versioned and integrity-verifiable (digest-pinnable and
signed), with documented install steps and a repeatable publish path (a single
command and/or CI), so the staging cluster — and any operator — always installs a
known-good build.

**Why this priority**: Makes the delivery trustworthy and repeatable, but US1+US2
already deliver a working unblock; this hardens it.

**Independent Test**: Verify a published version's signature/digest before
install; follow the documented steps on a clean cluster without reading source.

**Acceptance Scenarios**:

1. **Given** a published version, **When** a consumer verifies it, **Then** the artifact's digest and signature confirm provenance before install.
2. **Given** the install documentation, **When** an operator follows it on their own cluster, **Then** they install the provider and apply a first managed resource without consulting source code.
3. **Given** the publish path, **When** it runs in CI or via one command, **Then** it produces the same artifacts deterministically for a given version.

---

### Edge Cases

- Target cluster's CPU architecture differs from the build host (must still run → multi-arch image).
- The staging cluster lacks Crossplane, or has an incompatible Crossplane version.
- Registry requires pull credentials on the target cluster (private CRaaS registry → image pull secret).
- **CRaaS bootstrap (chicken-and-egg)**: the registry hosting the provider package cannot be provisioned *by the very provider being published*; it must pre-exist or be created out-of-band as a one-time step.
- **Publish origin reachability**: the developer's machine may be unable to reach the CRaaS push endpoint (e.g. egress restrictions); publishing may need to run from CI or from inside Timeweb.
- A run is interrupted mid-suite on the shared staging cluster (cleanup must still remove partial resources and Timeweb orphans).
- An accidental run against a non-staging cluster (explicit-context guard must prevent ambient-context surprises).
- Re-publishing an already-released version.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The provider package MUST be built and **pushed** to a container registry as a versioned, pullable artifact (closing the current local-file-only gap).
- **FR-002**: Installing the published package on a fresh Crossplane cluster MUST yield a provider that reaches Installed + Healthy and can reconcile managed resources, with no manual image loading.
- **FR-003**: The controller runtime image MUST be multi-architecture (at least amd64 and arm64) so it runs on the staging cluster's architecture.
- **FR-004**: The e2e harness MUST accept an arbitrary, **operator-provided** cluster context (e.g. `twc-staging`), removing the k3d-only restriction, and MUST refuse to run when no context is explicitly provided (no ambient-context default) — preserving the wrong-cluster safety guard.
- **FR-005**: The e2e harness MUST install the provider by pulling the published package, instead of side-loading a locally-built image. The package reference (registry host + repo + version) MUST be a **user-defined parameter** (env/Make variable), with a **default pointing to the author's Timeweb CRaaS** — this is a single-maintainer setup, so the default is the author's registry, but it stays overridable (no hardcoding) so a build-from-repo user can point it at their own registry (see FR-008c).
- **FR-006**: An e2e run on a shared/real cluster MUST isolate its resources to a dedicated namespace and, on teardown (including interruption), remove its cluster objects and sweep live Timeweb API orphans.
- **FR-007**: Published artifacts MUST be integrity-verifiable (content digest and signature) so a consumer can confirm provenance before install.
- **FR-008**: Documentation MUST cover (a) installing the published provider on any cluster, (b) running the e2e suite against a remote/staging cluster, and (c) the **build-from-repo path** — how someone who clones the repo builds the provider package + image themselves, pushes it to their own registry, and installs their own Provider from that self-built image (not only the pre-published CRaaS package).
- **FR-009**: The publish path MUST be repeatable (a single command and/or CI) and MUST NOT silently overwrite an already-released version.
- **FR-010**: This feature MUST NOT change managed-resource behavior, CRD schemas, or the conditions/printcolumns shipped by feature 007 (packaging/delivery only).

- **FR-011**: The provider package and controller image MUST be published to an **OCI registry the staging cluster can pull from**, selected via the same user-defined registry parameter as FR-005. **Any generic registry works** — a Timeweb CRaaS is optional, not required: image pulls are not behind the Qrator WAF (which only fronts `api.timeweb.cloud`), so the cluster can pull from an external registry while the provider's API calls stay in-network. The **maintainer provides push auth (`docker login`)** and **creates the cluster pull secret** (per the Crossplane docs) as setup steps; the feature references these — it does NOT provision the registry or mint credentials. **The CRaaS/private registry is for tests/e2e only — it is NOT a public publish target** (see FR-012).

- **FR-012**: The provider MUST ALSO be published as a **public release on GitHub**: the package + multi-arch controller image pushed to **public `ghcr.io`** (signed — FR-007), and a **GitHub Release** cut at the version git tag (release notes + the built `.xpkg` attached as a release asset). This is the public distribution target — pullable without credentials, installable by anyone via a `Provider` referencing the `ghcr.io` package. The `IMAGE_REPO` parameter (FR-005) selects the target, so the same flow serves both the public `ghcr.io` release and the private CRaaS test publish. **The public release is GATED on validation: it MUST NOT be cut until the full e2e suite — every MR kind's bundle, including the billable Server/KubernetesCluster/Nodepool/Router — has run green against a live cluster. No kind is released as "experimental"; all are validated first.**

### Key Entities

- **Provider package**: the installable OCI artifact (package metadata + CRDs) that a cluster references to install the provider; identified by a registry reference + version + digest.
- **Controller runtime image**: the multi-arch image the provider runs as once installed.
- **Registry**: the publish/pull target hosting the package and image.
- **Release version**: an immutable, verifiable version identity (tag + digest + signature) for a published build.
- **Target cluster**: an arbitrary Kubernetes cluster (the Timeweb staging cluster) identified by an explicit context/kubeconfig, where the published provider is installed and the e2e runs.

## Success Criteria *(mandatory)*

- **SC-001**: A fresh cluster installs the provider directly from the registry and the provider reaches Healthy within 5 minutes, with no manual image loading.
- **SC-002**: The full e2e suite runs to green against the Timeweb staging cluster (a non-k3d context) using only the published package.
- **SC-003**: The provider runs successfully on the staging cluster's CPU architecture without a host-specific rebuild.
- **SC-004**: After an e2e run, zero run-created resources remain on the cluster and zero orphans remain on the live Timeweb account (verified against the API).
- **SC-005**: A published version's provenance is verifiable (digest + signature) before install.
- **SC-006**: An operator installs the provider on their own cluster and applies a first managed resource following the documentation alone, without reading source.
- **SC-007**: A public **GitHub Release** exists at a version tag with a signed `ghcr.io` package + the `.xpkg` attached, and it was cut **only after** the full e2e suite (all MR kinds, incl. Server/K8s/Nodepool/Router) ran green on a live cluster (FR-012).

## Assumptions

- Pulls from the Timeweb Container Registry happen **in-network** from the staging cluster (the CRaaS registry host is distinct from `api.timeweb.cloud` and is not the WAF-blocked endpoint), so both the image pull AND the provider's managed-resource API calls stay inside Timeweb — fully sidestepping the external-egress WAF.
- Running the provider inside Timeweb removes the WAF block for managed-resource reconciliation; no application-level change is needed to work around the WAF.
- The e2e suite reuses the existing bundles unchanged except for context/install plumbing; resources stay namespace-isolated (the existing `timeweb-e2e` namespace) and are orphan-swept after each run.
- Image signing already exists for the controller image; integrity (FR-007) extends the same mechanism to the pushed package.
- **The owner installs Crossplane on the staging cluster** (out of scope here) and has working kube access to it via the `twc-staging` context.
- The target CRaaS registry either pre-exists or is provisioned as a one-time bootstrap (it cannot be created by the very provider being published — see Edge Cases); publishing originates from a network that can reach the CRaaS registry endpoint (the developer's machine, CI, or in-Timeweb).

## Dependencies

- A Timeweb Container Registry (CRaaS) with push credentials (publish) and an in-cluster pull credential for the staging cluster.
- A working Timeweb staging cluster reachable via the `twc-staging` context, with Crossplane installed by the owner (Crossplane 2.3.2 on k8s v1.35.4+k0s, installed 2026-06-17) and a valid Timeweb API token configured via ProviderConfig.
- The existing build/package tooling (image build, package build) as the starting point; this feature adds the publish (push) + remote-context capabilities.

## Out of Scope

- New managed-resource kinds or any change to existing MR behavior/CRDs.
- **Upbound Marketplace** / curated-catalog listing — deferred (not yet). 008 DOES publish a public release to `ghcr.io` + a GitHub Release (FR-012); a curated marketplace listing is a later step. No Helm/OLM bundle is involved (the `.xpkg` already IS an OCI artifact).
- Provisioning the staging cluster itself, or installing Crossplane onto it as a managed step (assumed available).
- Resolving the WAF block for the developer's external network (out of our control; the in-Timeweb run is the workaround).
