# Phase 1 Data Model: MVP Scaffolding

**Feature**: 001-mvp-scaffolding | **Plan**: [plan.md](./plan.md) | **Research**: [research.md](./research.md)

This document enumerates every entity the v0.1 provider exposes, with the fields,
validation rules, relationships, and state transitions each one carries. It is the
authoritative reference for `apis/` Go types, CRD schemas under `contracts/`, and
controller reconcile logic.

For the precise CRD schema (kubebuilder annotations, OpenAPI fragments, validation
markers) see the per-resource specifications under [`contracts/`](./contracts/).

## Conventions

- **Kind** — the Kubernetes Kind name.
- **Scope** — `Namespaced` or `Cluster` (per Clarifications: only `ProviderConfig` is
  cluster-scoped).
- **Group/Version** — `<group>.timeweb.crossplane.io/v1alpha1` per service group.
- **Mutable / Immutable** — applies to `spec` fields only. `status` is always controller-owned.
- **External-name** — value of `metadata.annotations["crossplane.io/external-name"]`,
  formula per R-2.

---

## 1. `ProviderConfig`

**Kind**: `ProviderConfig` | **Group**: `timeweb.crossplane.io/v1alpha1` | **Scope**: Cluster

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `credentials.source` | enum (`Secret`) | yes | yes | Only `Secret` is supported in MVP (FR-003). Future sources are explicitly out of scope. |
| `credentials.secretRef.name` | string | yes | yes | Name of the Kubernetes Secret holding the Timeweb token. |
| `credentials.secretRef.namespace` | string | yes | yes | Namespace of the Secret. ProviderConfig is cluster-scoped, so the namespace must be explicit. |
| `credentials.secretRef.key` | string | yes | yes | Key inside the Secret data map. Defaults to `token`. |

### Status fields

| Field | Type | Owner | Notes |
| ----- | ---- | ----- | ----- |
| `conditions[]` | Crossplane conditions | controller | `Synced` only — ProviderConfig has no upstream representation, so `Ready` is not meaningful. |
| `users` | integer | controller | Count of managed resources currently bound via `ProviderConfigUsage`. |

### Validation rules

- `credentials.source` MUST equal `Secret`. CRD schema enforces with `enum: ["Secret"]`.
- `credentials.secretRef` MUST be fully qualified (`name` + `namespace` + `key`).

### Relationships

- Referenced by every namespaced MR via `spec.providerConfigRef.name`.
- Crossplane-runtime maintains a `ProviderConfigUsage` (cluster-scoped) per (MR, ProviderConfig) pair;
  blocks deletion of a ProviderConfig that has live usages.

### State transitions

- `apply` → ProviderConfig exists; controller verifies the Secret exists and the key is
  populated; if not, `Synced=False, reason=SecretMissing`. The provider does NOT
  validate the token against Timeweb at this stage — token validity is observed when
  an MR first reconciles.
- `Secret content changes` → next MR reconciliation picks up the new value (FR-015).
- `delete` while users exist → blocked by `ProviderConfigUsage` finalizer.

---

## 2. `Project`

**Kind**: `Project` | **Group**: `project.m.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider.name` | string (≤255) | yes | yes | Display name. Upstream PATCHable per `update-project`. |
| `forProvider.description` | string (≤255, nullable) | no | yes | Free-form description. PATCHable. |
| `forProvider.avatarID` | string (≤255, nullable) | no | yes | Reserved; carried for parity with the Timeweb API. |
| `providerConfigRef.name` | string | yes | yes | Names a `ProviderConfig`. |
| `deletionPolicy` | enum (`Delete`, `Orphan`) | no | yes | Crossplane standard; defaults to `Delete`. |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.id` | integer | Timeweb project ID (also encoded as external-name). |
| `atProvider.accountID` | string | Returned by API (`cp78562`-style). |
| `atProvider.isDefault` | boolean | Read-only; true if Timeweb marks this as the account's default project. |
| `conditions[]` | Crossplane | `Synced`, `Ready`. |

### External-name

Stringified `atProvider.id`. Set on first successful Create.

### Validation

- `forProvider.name` MUST be 1–255 characters.
- `forProvider.description` MAY be empty; CRD allows `null` but kubebuilder marker
  `+kubebuilder:validation:MaxLength=255`.

### Relationships

- Other Timeweb resources (S3Bucket, ContainerRegistry — both in MVP) carry an optional
  `forProvider.projectID` that may reference a Project by external-name. The MR does
  NOT establish a Kubernetes ownerReference; it references by upstream ID via the
  standard Crossplane reference resolver pattern.

### State transitions

```
                  +--- Observe → 404 ---> Create → 201 ---+
                  |                                       |
   Apply ───→ Observe ──→ 200 ──→ Diff ──→ no diff ──→ Ready
                                                |
                                                v
                                          Update (PATCH) → 200 → Ready
                                                |
                                                v
                          immutable field changed → reject + Synced=False
```

---

## 3. `SshKey`

**Kind**: `SshKey` | **Group**: `sshkey.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider.name` | string | yes | **no** | Display name; cannot be renamed (R-4). |
| `forProvider.body` | string | yes | **no** | Public key (OpenSSH format). Immutable per R-4. |
| `forProvider.isDefault` | boolean | no | yes | Mark as default for new servers. PATCHable via Timeweb API. |
| `providerConfigRef.name` | string | yes | yes | |
| `deletionPolicy` | enum | no | yes | |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.id` | integer | |
| `atProvider.createdAt` | string (RFC3339) | |
| `atProvider.usedBy[]` | array of `{id, name}` | Servers referencing this key — informational only. |
| `conditions[]` | Crossplane | |

### External-name

Stringified `atProvider.id`.

### Validation

- `forProvider.body` MUST start with `ssh-rsa`, `ssh-ed25519`, `ssh-dss`, or
  `ecdsa-sha2-*` (kubebuilder pattern marker).
- `forProvider.name` MUST be 1–255 characters.

### Connection Secret

None (R-5). The public key body is in `spec`; there is no consumable connection
information.

### State transitions

Same diagram as Project, except: any attempt to change `forProvider.body` or
`forProvider.name` triggers FR-017 reject-and-surface — `Synced=False,
reason=ImmutableFieldChange, message=body|name is immutable`.

---

## 4. `S3Bucket`

**Kind**: `S3Bucket` | **Group**: `objectstorage.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider.name` | string | yes | **no** | Globally unique bucket name. |
| `forProvider.location` | string | yes | **no** | Region code (catalog lookup; FR-010 design doc covers post-MVP catalog CRD). |
| `forProvider.type` | enum (`private`, `public`) | yes | yes | Access policy; can be flipped post-create. |
| `forProvider.storageClass` | enum (`cold`, `standard`) | yes | **no** | Storage tier. |
| `forProvider.presetID` | integer | one of `presetID`/`configuration` | **no** | Tariff plan ID. |
| `forProvider.configuration` | object (`id`, `disk`) | one of `presetID`/`configuration` | **no** | Custom configurator alternative. |
| `forProvider.description` | string (nullable) | no | yes | Comment. |
| `forProvider.isAllowAutoUpgrade` | boolean | no | yes | Auto-upgrade tariff if quota exceeded. |
| `forProvider.projectID` | integer | no | yes | Optionally bind to a Project (by external-name resolution). |
| `providerConfigRef.name` | string | yes | yes | |
| `writeConnectionSecretToRef.{name,namespace}` | string | no | yes | Override target Secret. Defaults to the MR's own namespace, name = MR name + `-conn`. |
| `deletionPolicy` | enum | no | yes | |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.id` | integer | |
| `atProvider.hostname` | string | S3 endpoint URL. |
| `atProvider.status` | string | Upstream status (`active`, etc). |
| `atProvider.diskStats` | object | size/used/isUnlimited. |
| `atProvider.objectAmount` | integer | |
| `atProvider.movedInQuarantineAt` | string (RFC3339, nullable) | If non-null, the bucket has been quarantined; reflected in `Ready` condition. |
| `conditions[]` | Crossplane | |

### Connection Secret (type: `Opaque`)

Per R-5:

| Key | Source |
| --- | ------ |
| `endpoint` | `atProvider.hostname` |
| `bucket` | `forProvider.name` (or resolved name from `atProvider`) |
| `region` | `forProvider.location` |
| `access_key` | `bucket.access_key` from upstream response |
| `secret_key` | `bucket.secret_key` from upstream response |

### Validation

- `name` MUST match `^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`.
- Exactly one of `presetID` or `configuration` MUST be set (CEL validation rule).
- `type` ∈ {`private`, `public`}; `storageClass` ∈ {`cold`, `standard`}.

### State transitions

Same Observe/Create/Update/Delete flow as Project. Changes to `type`, `description`,
`isAllowAutoUpgrade`, `projectID` PATCH the upstream resource; any change to `name`,
`location`, `storageClass`, `presetID`/`configuration` is rejected (FR-017).

---

## 5. `ContainerRegistry`

**Kind**: `ContainerRegistry` | **Group**: `containerregistry.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider.name` | string | yes | **no** | 3–48 chars, `^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`. |
| `forProvider.description` | string | no | yes | PATCHable. |
| `forProvider.presetRef.name` | string | one of `presetRef`/`configuration` | **no** | Reference to a `ContainerRegistryPreset` by Kubernetes name. The controller resolves to `preset_id` at create time. |
| `forProvider.configuration` | object (`id`, `disk`) | one of `presetRef`/`configuration` | **no** | Custom configurator alternative. |
| `forProvider.projectID` | integer | no | yes | Optionally bind to a Project. |
| `providerConfigRef.name` | string | yes | yes | |
| `writeConnectionSecretToRef.{name,namespace}` | string | no | yes | Defaults as above; Secret type is `kubernetes.io/dockerconfigjson`. |
| `deletionPolicy` | enum | no | yes | |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.id` | integer | |
| `atProvider.presetID` | integer | Resolved value from `presetRef`; carries the historical ID even if the catalog renames the preset. |
| `atProvider.diskStats` | object | size/used. |
| `atProvider.createdAt`, `updatedAt` | string (RFC3339) | |
| `conditions[]` | Crossplane | |

### Connection Secret (type: `kubernetes.io/dockerconfigjson`)

Per R-5:

| Key | Source |
| --- | ------ |
| `.dockerconfigjson` | Marshaled docker config: `{"auths":{"<endpoint>":{"username":"...","password":"...","auth":"<base64>"}}}` |
| `endpoint` | Registry URL (derived from upstream `id` + Timeweb registry hostname pattern; see R-1 for credential source) |
| `username` | From storage-users lookup (R-1) |
| `password` | From storage-users lookup (R-1) |

### Validation

- `name` MUST match the registry name regex above.
- Exactly one of `presetRef` or `configuration` MUST be set.

### State transitions

Same flow as S3Bucket. Update via PATCH supports `description` and `preset_id` /
`configuration` within the same axis. Switching `presetRef` from `presetA` to `presetB`
is mutable (Timeweb supports tariff changes); switching `presetRef` to `configuration`
(or vice versa) is rejected as an immutable-axis change.

---

## 6. `ContainerRegistryRepository`

**Kind**: `ContainerRegistryRepository` | **Group**: `containerregistry.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

Represents a repository under a `ContainerRegistry`. Created implicitly by `docker push`
upstream; this CRD enables declarative deletion and (in principle) future operations
like immutable-tag policies. The MVP supports only lifecycle (Observe + Delete).

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider.registryRef.name` | string | yes | **no** | Kubernetes name of the parent `ContainerRegistry`. |
| `forProvider.name` | string | yes | **no** | Repository name within the parent registry. |
| `providerConfigRef.name` | string | yes | yes | (Must equal the parent's.) |
| `deletionPolicy` | enum | no | yes | |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.tagCount` | integer | Number of image tags under the repository. |
| `atProvider.size` | integer | Bytes consumed (upstream metric). |
| `conditions[]` | Crossplane | `Ready=True` only if the repository exists upstream. If the upstream returns 404 on `Observe`, the CR is `Ready=False, reason=RepositoryNotPushed` to signal the operator must `docker push` first. |

### External-name

`<parent-registry-name>/<repository-name>` per R-2.

### Validation

- `forProvider.name` MUST be a valid Docker repository name segment.
- `forProvider.registryRef.name` MUST resolve to an existing `ContainerRegistry` in the
  same namespace.

### State transitions

```
Apply ──→ Observe → 404 ──→ Ready=False, reason=RepositoryNotPushed
                              ↑
                              | (operator runs `docker push registry/repo`)
                              ↓
        Observe → 200 ──→ Ready=True
        ↓
        Delete by operator → DELETE /api/v1/container-registry/{registry_id}/repositories/{name}
```

`Update` is a no-op in MVP (repositories carry no mutable fields). Reserved for v0.2+.

---

## 7. `ContainerRegistryPreset` (observe-only)

**Kind**: `ContainerRegistryPreset` | **Group**: `containerregistry.timeweb.crossplane.io/v1alpha1` | **Scope**: Namespaced

Catalog data surfaced as Kubernetes resources. Spec edits are rejected by a
ValidatingAdmissionPolicy.

### Spec fields

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `forProvider` | object | no | **no** | Effectively empty — the provider populates everything from upstream. Operators MUST NOT edit. |

### Status fields

| Field | Type | Notes |
| ----- | ---- | ----- |
| `atProvider.presetID` | integer | Catalog ID — referenced by `ContainerRegistry.spec.forProvider.presetRef`. |
| `atProvider.displayName` | string | Russian or English depending on upstream `x-title-i18n.eng` availability; controller prefers English. |
| `atProvider.disk` | integer | Disk capacity (GB). |
| `atProvider.price` | object (`amount`, `currency`) | Periodic price. |
| `atProvider.location` | string | Region the preset applies to. |
| `atProvider.cpu`, `atProvider.ram` | integer | Where exposed by upstream. |
| `atProvider.lastObservedAt` | string (RFC3339) | Stamp of the last successful catalog GET. |
| `conditions[]` | Crossplane | `Synced=True` means the CR matches the last upstream poll. |

### State transitions

- Catalog poll: every `--preset-sync-interval` (default 30 min), the provider fetches
  the preset list and:
  - **Upserts** a CR per upstream preset, namespaced under
    `--preset-target-namespace` (default: the provider's namespace).
  - **Deletes** CRs whose preset no longer appears upstream.
  - On API error (transient), retains the last-known CRs and surfaces
    `Synced=False, reason=CatalogPollFailed` on each, with the next poll attempt.

### External-name

Stringified `atProvider.presetID`. Set by the controller, not by the operator.

---

## Cross-resource relationships

```
┌────────────────────────┐
│   ProviderConfig       │ (cluster-scoped)
│   spec.credentials     │
└────────────────────────┘
            ▲
            │ providerConfigRef.name
            │
   ┌────────┼────────────────────────────┐
   │        │                            │
┌──┴──┐  ┌──┴────┐  ┌─────────────┐  ┌──┴──────────────┐
│Proj │  │SshKey │  │  S3Bucket   │  │ContainerRegistry│
└─────┘  └───────┘  └──────┬──────┘  └────────┬────────┘
                           │                  │ presetRef.name
                           │ projectID        ▼
                           │           ┌──────────────────────┐
                           ▼           │ContainerRegistryPreset│
                    (optional ref)     │  (observe-only)       │
                                       └──────────────────────┘
                                                ▲
                                                │
                                       ┌────────┴──────────────────┐
                                       │ContainerRegistryRepository│
                                       │  registryRef.name         │
                                       └───────────────────────────┘
```

- `ProviderConfig` is referenced by every namespaced MR.
- `ContainerRegistryPreset` is referenced by `ContainerRegistry` by Kubernetes name.
- `ContainerRegistryRepository` references its parent `ContainerRegistry` by Kubernetes
  name (must be in the same namespace).
- `Project` may be referenced by `S3Bucket` and `ContainerRegistry` via upstream
  `projectID` (numeric ID, resolved through Crossplane's standard reference resolver).

## Volume / scale assumptions

| Resource | Expected count per cluster |
| -------- | -------------------------- |
| `ProviderConfig` | 1–3 (typically one per Timeweb tenant) |
| `Project` | 1–5 |
| `SshKey` | 1–20 |
| `S3Bucket` | 5–30 |
| `ContainerRegistry` | 1–5 |
| `ContainerRegistryRepository` | 5–50 |
| `ContainerRegistryPreset` | 5–15 (catalog size) |

Total: well under 200 CRs per cluster in v0.1. controller-runtime's default workqueue
concurrency (1 per controller) is sufficient.
