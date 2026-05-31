# Quickstart — Internal Catalog Resolution & ProviderConfig Scoping

**Feature**: `002-readonly-presets-design` | **Plan**: [plan.md](./plan.md)

**Audience**: Platform operators upgrading from the MVP (`v0.1`,
`001-mvp-scaffolding`) and operators starting fresh on this release.

## What changes for the operator

| Change | What you do |
|--------|-------------|
| The cluster-scoped `ProviderConfig` (MVP) is renamed to `ClusterProviderConfig`. A new namespaced `ProviderConfig` is added. | Re-apply existing PC manifests with `kind: ClusterProviderConfig`, or move credentials into a namespaced `ProviderConfig` co-located with your MRs. |
| `ContainerRegistry` and `S3Bucket` `forProvider` shape changed. The MVP's raw `preset_id` / `configuration` fields are gone, replaced by an integer enum `initialSizeGB` (+ optional `location` / `storageClass`). | Re-apply manifests with `initialSizeGB` set to one of the published tariff tiers. The controller derives `preset_id` from the live catalog. |
| Catalog data is no longer exposed as Kubernetes objects. There is no `ContainerRegistryPreset` / `*Preset` / `*Configurator` CRD. | Nothing to do — any CRs of the removed kinds are GC'd when the CRDs disappear. |
| `ContainerRegistry` now publishes a connection Secret with the docker login (username = registry name, password = the same Timeweb API token, endpoint = `<name>.registry.twcstorage.ru`). | If you previously published your own Secret, drop it — the controller writes one via `writeConnectionSecretToRef`. |

## Upgrade path

### 1. Migrate `ProviderConfig` to `ClusterProviderConfig` (or to a namespaced PC)

If your MVP cluster has a single shared `ProviderConfig`, the simplest
upgrade is to rename the kind:

```bash
# Snapshot the existing PC
kubectl get providerconfig/default -o yaml > pc-backup.yaml

# Edit pc-backup.yaml: change `kind: ProviderConfig` to
# `kind: ClusterProviderConfig`. Add `spec.credentials.secretRef.namespace`
# if it isn't already set (cluster-scoped PCs require it).
# Delete metadata.uid and metadata.resourceVersion.

# Install the new provider release (its CRDs replace the MVP ones).

# Apply the renamed PC
kubectl apply -f pc-backup.yaml
```

Existing `Project` and `SshKey` MRs keep their `spec.providerConfigRef.name`
and resolve against the renamed `ClusterProviderConfig` via the
crossplane-runtime v2 default for `spec.providerConfigRef.kind`
(`ClusterProviderConfig`). Setting `kind: ProviderConfig` is required
to point at a namespaced PC; there is **no** silent fallback in either
direction, so a mistyped or missing `(kind, name)` pair surfaces as
`Synced=False, reason=InvalidProviderConfigRef`. No per-MR edit needed
for the MVP→rename upgrade if `kind` was already omitted.

For team-isolated credentials, create a namespaced `ProviderConfig` per
team instead:

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: team-a
spec:
  credentials:
    source: Secret
    secretRef:        # `namespace:` may be omitted — controller defaults to the PC's namespace (`team-a`)
      name: timeweb-token
      key: token
```

### 2. Re-apply `ContainerRegistry` and `S3Bucket` manifests

The MVP shape (`preset_id`, `configuration{configurator_id, …}`) is
rejected by the new CRD validation. The final design exposes a single
integer enum constrained to the tariff tiers Timeweb publishes:

**`S3Bucket`** (allowed `initialSizeGB`: `1`, `10`, `100`, `250`):

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3Bucket
metadata:
  name: my-bucket
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig                  # or ClusterProviderConfig
    name: default
  forProvider:
    name: team-a-data
    initialSizeGB: 1
    storageClass: hot                     # "hot" | "cold"; mutable
    location: ru-1                        # optional
```

**`ContainerRegistry`** (allowed `initialSizeGB`: `5`, `10`, `25`, `50`,
`75`, `100`):

```yaml
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: my-registry
  namespace: team-a
spec:
  providerConfigRef:
    kind: ProviderConfig
    name: default
  writeConnectionSecretToRef:
    name: my-registry-creds
    namespace: team-a
  forProvider:
    name: my-registry
    initialSizeGB: 5
    location: ru-1                        # optional
```

The controller resolves `(initialSizeGB, storageClass?, location?)` to
the matching `preset_id` and records the result in
`status.atProvider.lockedPresetID`. The locked ID is consulted on every
subsequent reconcile to detect drift if the operator edits the size
field after create.

If `initialSizeGB` doesn't match any upstream preset visible to the PC's
token, the MR shows `Synced=False, reason=PresetNotFound` with a message
listing the currently-valid `(size, class, location)` triples.

### 3. Resizing after create

`initialSizeGB` is mutable on both kinds. Patch the value to one of the
allowed enum members; the controller picks the new `preset_id` and
PATCHes the upstream resource. `storageClass` on `S3Bucket` is mutable
too (bucket policy switch). `location` is immutable — change attempts
are rejected at admission.

## Verifying the install

Two paths: the e2e bundle if you want a one-command sanity check, or
manual `kubectl explain` if you just installed the package into an
existing cluster.

### Option A — `make e2e` (k3d, isolated cluster, real Timeweb API)

```bash
export TIMEWEB_CLOUD_TOKEN=<your-token>
make e2e          # k3d up → install Crossplane → build+install provider → kuttl
make e2e.cleanup  # investigate first, then wipe leftover MRs (never auto-deletes)
make e2e.down     # delete the k3d cluster + local registry
```

The bundle covers Project import, SshKey lifecycle, S3Bucket
(`initialSizeGB=1`), ContainerRegistry (`initialSizeGB=5`), and the
`PresetNotFound` negative path. See [`test/e2e/README.md`](../../test/e2e/README.md)
for the full pipeline and prerequisites. The e2e suite uses **k3d**
(with a built-in local registry); a `kind` cluster is not required.

### Option B — manual checks against your existing cluster

```bash
# 1. The new PC kinds are registered:
kubectl api-resources | grep timeweb.crossplane.io
# Expected: providerconfigs (Namespaced), clusterproviderconfigs (Cluster),
#           providerconfigusages, clusterproviderconfigusages,
#           plus the .m.timeweb.crossplane.io MR kinds.

# 2. No catalog-as-CRD kinds remain:
kubectl api-resources | grep -E 'preset|configurator'
# Expected: no output. Catalog lives in the controller's resolver cache.

# 3. The refactored MR CRDs expose initialSizeGB with the enum constraint:
kubectl explain s3bucket.spec.forProvider.initialSizeGB
kubectl explain containerregistry.spec.forProvider.initialSizeGB
# Expected: integer field; description references the published tier list.
```

## Discovering valid sizes

There is no `kubectl get presets` — catalog data is not exposed as a
Kubernetes object. Sources for valid `initialSizeGB` values:

1. **CRD enum constraint** — `kubectl explain
   <kind>.spec.forProvider.initialSizeGB` lists the enum members the CRD
   accepts. These match the dashboard's tier picker by construction.
2. **`docs/presets.md`** — operator-facing reference for both kinds,
   including the unsupported "Произвольная" (custom) path.
3. **Error message on PresetNotFound** — if `(size, class, location)`
   doesn't resolve, the condition message lists the up-to-20 triples
   currently visible to your PC's token.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `Synced=False, reason=PresetNotFound` on a fresh MR | The `(initialSizeGB, storageClass, location)` triple doesn't match any preset visible to your token. Common: `location` typo, or the tier was renamed upstream. | Read the condition message — it lists valid triples. Re-apply. |
| `Synced=False, reason=CatalogUnauthorized` | The PC's token doesn't have permission on the catalog endpoint (`/api/v1/container-registry/presets`, `/api/v1/presets/storages`). | Issue a token with the right scopes; update the Secret. The resolver caches the 401 — bouncing the controller after fixing the Secret is the fastest way to invalidate. |
| MR is `Ready=Unknown` for ~60s after apply | The default poll interval is 60s; the first reconcile after create has to wait one tick for the upstream resource to come up. | Wait one poll cycle. The controller does not currently auto-requeue on creation. |
| `ContainerRegistry` Ready=False with `reason=CredentialsPending` for >1 minute | Should not happen — the docker login is derived from registry name + the same API token. If you see it, the registry name is empty in observation. | Check `status.atProvider.upstreamID`. If unset, the upstream create failed; check provider logs. |
| `S3Bucket` Synced=False with upstream `configurator.id should not be null` | You're on a stale build that still tries the `resources`-based path. | Rebuild from this feature's branch (or later); the final build uses presets only. |

## What's coming next

The next feature (`KubernetesCluster` + `KubernetesNodeGroup`) reuses
everything above: the same dual-scope `ProviderConfig` pair and the same
internal resolver primitive. The K8s feature wires up the six
forward-compat dimensions already registered in
`internal/controller/shared/resolver/dimensions.go`
(`KubernetesMasterPreset`, `KubernetesWorkerPreset`, `KubernetesVersion`,
`KubernetesNetworkDriver`, `AvailabilityZone`, `ServerConfigurator`) —
no new resolver primitives required. The field→dimension mapping
comment at the top of that file documents the K8s create-body contract
the feature will fulfil.
