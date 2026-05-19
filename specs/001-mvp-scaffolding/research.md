# Phase 0 Research: MVP Scaffolding

**Feature**: 001-mvp-scaffolding | **Plan**: [plan.md](./plan.md)

This document resolves every technical unknown flagged during planning. Each item is a
**Decision → Rationale → Alternatives considered** triple, prefixed with an `R-N`
identifier referenced from `plan.md`, `data-model.md`, and `contracts/*`.

---

## R-1: Container Registry credential model

**Decision**: The `ContainerRegistry` controller will obtain registry pull/push
credentials from the Timeweb storage-users endpoint (`/api/v1/storages/users`),
matching credentials to the registry by name/owner via the storage-user listing. The
controller publishes a `kubernetes.io/dockerconfigjson` Secret containing
`.dockerconfigjson`, `endpoint`, `username`, and `password` keys. If the live API
investigation during implementation reveals a registry-specific credential endpoint
that is not documented in the vendored `openapi.json`, the implementation will use
that endpoint instead; the spec's FR-009 contract is preserved either way.

**Rationale**: The vendored OpenAPI surface for Container Registry
(`/api/v1/container-registry[/{id}[/repositories]]`, `/api/v1/container-registry/presets`)
exposes no registry-specific auth endpoint. The closest documented credentials are the
storage-user records under `/api/v1/storages/users`, which appears (from the dashboard's
behavior) to be the shared credential pool for S3 and the registry. Confirming this
requires live API exploration during implementation — but the spec's contract
(SC-005: operator references the produced Secret as `imagePullSecrets`) is implementable
regardless of which Timeweb endpoint actually yields the credentials.

**Alternatives considered**:
- *Account-token-as-registry-password*: Some registries accept the platform API token as
  the docker password. Rejected as default — would couple registry access to provider
  credentials and force every pull workload to share the provider's token; also fragile
  if Timeweb introduces scoped registry tokens later.
- *Block on Timeweb support clarification*: Rejected. The unknown is bounded, the worst
  case (use storage-user credentials) ships a working SC-005 path, and the design
  document under FR-010 will record the final answer for future maintainers.

**Open follow-up**: implementation PR for ContainerRegistry MUST include a
`docs/resources/containerregistry.md` section "How credentials are obtained" that
documents the actual mechanism used, with a link to the Timeweb dashboard's behaviour
or to support correspondence if applicable.

---

## R-2: External-name format per MVP resource

**Decision**: Use the stringified numeric Timeweb resource ID as the external-name
annotation value for every MVP managed resource. For `ContainerRegistryRepository` (a
child of `ContainerRegistry`), use a composite of the form
`{parentRegistryName}/{repositoryName}` to encode parent identity since repositories
are addressed by name within a registry, not by a standalone numeric ID.

For `Project`, `SshKey`, `S3Bucket`, `ContainerRegistry`, `ContainerRegistryPreset`:
`metadata.annotations["crossplane.io/external-name"] = "<integer-id>"` (the value of the
JSON `id` field returned by Timeweb on create).

For `ContainerRegistryRepository`:
`metadata.annotations["crossplane.io/external-name"] = "<registry-name>/<repository-name>"`.

**Rationale**: Crossplane's external-name convention treats the value as opaque; the
controller is responsible for encoding and decoding it consistently. Numeric IDs are
the natural identity in Timeweb's API and round-trip cleanly. Composite encoding for the
repository case follows the established Crossplane pattern (cf. `bucket/object` in the
AWS provider).

**Alternatives considered**:
- *Use resource name (`spec.name`) instead of ID*: Rejected. Names are user-mutable in
  some resources (Project description; S3Bucket comment) and might be re-used after
  deletion. ID is durable.
- *Encode the parent as a Kubernetes object reference*: Rejected for repositories;
  inflates the annotation, makes import harder.

---

## R-3: HTTP error → transient/terminal classification

**Decision**: A single mapper at `internal/clients/timeweb/errors.go` applies this
table to every Timeweb response:

| HTTP Status | Classification | Behavior |
| ----------- | -------------- | -------- |
| 200, 201, 204 | success | proceed |
| 404 | `ResourceNotFound` (sentinel error) | `Observe` returns `ResourceExists=false`; Create/Update/Delete on 404 → transient (the upstream may not be ready yet) |
| 408 (Request Timeout) | transient | requeue with backoff |
| 409 (Conflict) | transient | requeue — usually means a concurrent operation; one retry typically clears it |
| 425 (Too Early) | transient | requeue |
| 429 (Too Many Requests) | rate-limit transient | requeue with `Retry-After` header if present, else exponential backoff capped at 32s, do NOT set `Synced=False` |
| 5xx | transient | requeue with exponential backoff |
| Other 4xx (400, 401, 403, 422, …) | terminal | `Synced=False`, reason = HTTP status + first paragraph of JSON `message` field, never include token text |
| Network errors (DNS, connection refused, TLS handshake failure, context deadline) | transient | requeue with backoff |

**Rationale**: Crossplane's documented contract is that transient errors requeue and
terminal errors flip the CR's `Synced` condition with a reason. The exact mapping has to
be picked per provider; this table is conservative (most errors are transient → requeue)
and explicit (terminal errors require a 4xx that is NOT 429/408/425/409). It also avoids
the common bug of treating 5xx as terminal (which would mark resources as failed during
a Timeweb-side outage).

**Alternatives considered**:
- *Treat 401/403 as transient (token may be rotating)*: Rejected. A persistent 401/403
  indicates a misconfigured ProviderConfig, which operators must see immediately. The
  separate FR-015 (Secret rotation) covers the rotation case at a different layer
  (Secret-watch invalidates the cached client).

---

## R-4: Immutable field inventory per MVP resource

**Decision**: The following fields are immutable per Timeweb's API behaviour
(confirmed by inspecting `openapi.json` create vs. update schemas and consulting the
dashboard for fields the API documents as create-only). FR-017 reject-and-surface
applies to these:

| Resource | Immutable fields |
| -------- | ---------------- |
| `Project` | (none — `name`, `description`, `avatar_id` are all PATCHable per `update-project` schema) |
| `SshKey` | `body` (the public key material), `name` (no PATCH endpoint on `/api/v1/ssh-keys/{id}` for renames) |
| `S3Bucket` | `name`, `location`, `type`, `storage_class`, `preset_id` once `configurator_id` is set (the create schema is mutually exclusive, but post-create the chosen sizing axis can't be swapped) |
| `ContainerRegistry` | `name`, the `preset_id`/`configuration` axis once chosen (PATCH allows changing the value within the same axis; cannot switch axis) |
| `ContainerRegistryRepository` | the entire identity — repositories are immutable; updates are no-ops |
| `ContainerRegistryPreset` | all fields — this is observe-only |

Each controller's `Update` method MUST diff the live state against the desired state and,
if any field in the immutable column changed, refuse to call the upstream API; instead
it sets `Synced=False` with reason `ImmutableFieldChange` and emits an Event naming the
field(s).

**Rationale**: The list is derived directly from `openapi.json` (presence vs. absence of
fields on update schemas like `RegistryEdit` and `update-project`) and from the Constitution
II classification of errors (which would otherwise cause silent recreation).

**Alternatives considered**:
- *Generate the immutable inventory from a Go struct tag*: Considered for forward
  consistency; rejected for v0.1 because the per-resource tables are short enough that a
  hand-maintained list keeps the implementation readable and the contract document
  authoritative.

---

## R-5: Connection Secret content per MVP resource

**Decision**:

| Resource | Secret type | Keys |
| -------- | ----------- | ---- |
| `ProviderConfig` | — | does not publish a connection Secret |
| `Project` | — | does not publish a connection Secret (no consumable connection info) |
| `SshKey` | — | does not publish a connection Secret (the public key is in `spec`/`status`, not a credential) |
| `S3Bucket` | `Opaque` | `endpoint` (the S3 host URL), `bucket` (bucket name), `region` (location string), `access_key`, `secret_key` |
| `ContainerRegistry` | `kubernetes.io/dockerconfigjson` | `.dockerconfigjson` (the marshaled docker config blob), plus the extracted `endpoint`, `username`, `password` keys for non-`imagePullSecrets` consumers |
| `ContainerRegistryRepository` | — | does not publish a connection Secret (the parent registry's Secret already serves the auth needs) |
| `ContainerRegistryPreset` | — | observe-only, does not publish |

S3 credentials are read from the `bucket` schema fields (`access_key`, `secret_key`,
`hostname`); they are returned inline by the bucket-create response.

ContainerRegistry credentials follow R-1 (storage-users endpoint until live verification
proves otherwise).

**Rationale**: Direct application of FR-009 (typed-where-natural). The four resources
that have nothing consumable (ProviderConfig, Project, SshKey, ContainerRegistryRepository,
ContainerRegistryPreset) do not emit Secrets to avoid manufacturing meaningless objects
in the operator's namespace.

**Alternatives considered**:
- *Always publish a Secret (even empty) for consistency*: Rejected. Empty Secrets are
  noise; operators see them via `kubectl get secrets` and wonder what they are for.

---

## R-6: ContainerRegistryPreset reconcile model

**Decision**: A single non-CR-triggered reconciler runs every 30 minutes (configurable
via `--preset-sync-interval`) and performs a full GET of
`/api/v1/container-registry/presets`. It upserts a Kubernetes
`ContainerRegistryPreset` CR per upstream preset (named after the preset's slugified
name) in the namespace the provider was installed into (default: the provider's own
namespace; configurable via `--preset-target-namespace`). Presets that disappear
upstream are deleted from the cluster. Operator edits to `spec` are no-ops (the
reconciler always overwrites from upstream); a Kubernetes
ValidatingAdmissionPolicy ships with the package to reject `spec` edits with a clear
message.

**Rationale**:
- The catalog is small (handful of presets) and rarely changes — a 30-minute poll is
  inexpensive and keeps the cluster eventually consistent with upstream.
- Putting the CRs in the provider's namespace lets cluster-wide RBAC grant read access
  cheaply; operators reference them by name across namespaces (since the data is
  effectively read-only catalog data).
- A ValidatingAdmissionPolicy is the v2-native way to enforce read-only `spec`; no
  webhook server required.

**Alternatives considered**:
- *Per-CR observation (the preset CR exists only when an operator creates one)*: Rejected
  — defeats the UX goal of "list presets and pick one".
- *Cluster-scoped Preset CRD*: Rejected — Clarifications pinned namespaced MRs only.
  Acceptable trade-off: presets live in the provider namespace, are referenced
  cross-namespace by name.

---

## R-7: `oapi-codegen` configuration

**Decision**: Use `oapi-codegen/v2` in `client` mode (`-generate types,client`) with a
project-local config file (`internal/clients/timeweb/generated/cfg.yaml`) pinning:
- Package name: `generated`
- Import path: `github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated`
- `output-options.skip-prune`: false (let the generator drop unused schemas to keep
  the output small)
- `output-options.skip-fmt`: false (run `gofmt`)
- `compatibility.always-prefix-enum-values`: true (avoid collisions)
- `output-options.exclude-tags`: `Аккаунт`, `Оплата`, `Базы знаний`, `Балансировщики`,
  `Выделенные серверы`, `Почта`, `AI Агенты`, `Apps`, `Cloud-AI` — i.e. only generate
  for the API surfaces the MVP and its known post-MVP candidates need. Reduces generated
  file size by ~60%.

Re-generation: `make generate-client` invokes oapi-codegen against
`docs/openapi-timeweb.json`. The Makefile's top-level `generate` target runs
`generate-client` then `controller-gen`.

**Rationale**: oapi-codegen is the standard Go OpenAPI client generator and supports
the granularity needed (tag-based exclusion) to keep the generated surface scoped. The
Russian description strings will appear in generated Go comments; since these are
internal package docs (not user-facing), they don't violate FR-018 (which governs CRD
descriptions, not internal Go code).

**Alternatives considered**:
- *`swagger-codegen`*: Rejected. Java-based, awkward in a Go project, generates less
  idiomatic Go.
- *`go-swagger`*: Rejected. Heavyweight; produces both client and server scaffolding;
  the project only needs a client.
- *Hand-write the API client*: Rejected per Clarifications Q2; chose oapi-codegen.

---

## R-8: Crossplane v2 namespaced MR conventions

**Decision**: Follow the conventions documented in Crossplane v2 release notes:
- `ProviderConfig` is cluster-scoped; managed resources reference it by name via
  `spec.providerConfigRef.name` (the v1 convention preserved in v2).
- Managed resources are namespace-scoped. The provider's controller-runtime manager
  uses an "all namespaces" cache; reconcilers are not scoped to a single namespace.
- Connection Secrets are written by default to the MR's own namespace. Operators may
  override via `spec.writeConnectionSecretToRef.namespace`.
- Finalizer ownership: each MR gets a unique finalizer
  (`<groupname>.timeweb.crossplane.io/finalizer`) that is removed only after upstream
  deletion confirms (`ResourceNotFound` on subsequent `Observe`).
- `ProviderConfigUsage` (cluster-scoped, named after the MR) tracks which MRs use which
  ProviderConfig — Crossplane-runtime's standard pattern; prevents deletion of a
  ProviderConfig that has live MRs.

**Rationale**: Crossplane v2's namespaced MR model is opinionated. Following
crossplane-runtime's conventions exactly avoids surprising operators familiar with other
v2 providers and removes the need for project-specific tooling around RBAC and
multi-tenancy.

**Alternatives considered**:
- *Per-namespace ProviderConfig*: Rejected. Crossplane v2 keeps ProviderConfig
  cluster-scoped to allow shared credentials across namespaces; per-namespace would
  conflict with that model.

---

## R-9: Build pipeline (Makefile + CI specifics)

**Decision**:
- **Makefile** (top-level): Targets `generate`, `build`, `test`, `cover`, `image`,
  `xpkg.build`, `reviewable`. `reviewable` is `lint` + `generate` (with dirty-tree
  failure) + `test`. CI runs `make reviewable`.
- **Tool pinning**: `hack/tools.go` imports `oapi-codegen`, `controller-gen`,
  `counterfeiter`, and the `golangci-lint` binary so `go.mod` pins their versions.
- **Multi-arch image**: `Dockerfile` is a multi-stage build (scratch final stage). The
  release workflow runs `docker buildx build --platform linux/amd64,linux/arm64` and
  pushes to `ghcr.io/lebedevdsl/provider-timeweb`.
- **Signing**: cosign keyless signature on every published tag, using the GitHub
  workflow's OIDC identity.
- **xpkg**: `crossplane xpkg build` packages the binary + CRD manifests + crossplane.yaml
  into the `.xpkg` file consumed by `crossplane xpkg push`.

**Rationale**: Hewing to the Crossplane native-provider Makefile shape minimizes the
"first contributor must learn this from scratch" cost. SC-003 (a contributor can produce
a buildable binary + regenerated CRDs in under 30 minutes) depends on this familiarity.

**Alternatives considered**:
- *Bazel*: Rejected. Overkill for the project size; Go modules + Makefile is the
  ecosystem default.
- *GPG-signed releases*: Rejected. cosign keyless is the modern standard, no key
  management overhead.

---

## R-10: Disposition of original `PLAN.md`

**Decision**: Move `PLAN.md` → `docs/archive/PLAN.md` and prepend a header pointing at
`spec.md` as the authoritative document. The archive note reads:

> **Status: superseded.** This is the 2026-04-30 draft of the provider plan, which
> recommended Upjet code generation from the Timeweb Terraform provider. Per the
> Clarifications session in `specs/001-mvp-scaffolding/spec.md` (2026-05-18), the
> project does not use Upjet and is fully native. The resource list in §3.1 of this
> document remains an accurate inventory of Timeweb's TF-modeled resources, but its
> recommendations and roadmap are not the project's current direction.

The move is performed as part of the implementation task `T010` (Setup phase).

**Rationale**: Deleting the draft would erase useful context (the resource list, the
TF-mapping table, the rate-limit notes). Keeping it at the repo root would mislead new
contributors into thinking it is the plan. Moving to `docs/archive/` with a clear
superseded marker resolves both.

**Alternatives considered**:
- *Git-rm and rely on history*: Rejected. The resource-list table is referenced by
  `spec.md` FR-001's enumeration and the design doc under FR-010 cites it as
  reference; keeping it accessible is convenient.
