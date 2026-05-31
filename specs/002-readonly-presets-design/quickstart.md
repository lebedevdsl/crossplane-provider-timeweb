# Quickstart — Internal Catalog Resolution & ProviderConfig Scoping

**Feature**: `002-readonly-presets-design` | **Plan**: [plan.md](./plan.md)

**Audience**: Platform operators upgrading from the MVP (`v0.1`, `001-mvp-scaffolding`) and operators starting fresh on this release.

## What changes for the operator

| Change | What you do |
|--------|-------------|
| The cluster-scoped `ProviderConfig` (MVP) is renamed to `ClusterProviderConfig`. A new namespaced `ProviderConfig` is added. | Re-apply existing PC manifests with `kind: ClusterProviderConfig`, or move credentials into a namespaced `ProviderConfig` co-located with your MRs. |
| `ContainerRegistry` and `S3Bucket` `forProvider` shape changed. Raw `preset_id` / `configuration` fields are gone. | Re-apply manifests using either `forProvider.resources: { … }` or `forProvider.presetName: <slug>`. |
| The `ContainerRegistryPreset` CRD is removed. | Nothing to do — any existing CRs are GC'd when the CRD disappears. |
| Catalog data is no longer in-cluster. | Use the Timeweb dashboard (or any preset slugs the controller surfaces in `PresetNotFound` error messages) to pick a preset name. |

## Upgrade path

### 1. Migrate `ProviderConfig` to `ClusterProviderConfig` (or to a namespaced PC)

If your MVP cluster has a single shared `ProviderConfig`, the simplest upgrade is to rename the kind:

```bash
# Snapshot the existing PC
kubectl get providerconfig/default -o yaml > pc-backup.yaml

# Edit pc-backup.yaml: change `kind: ProviderConfig` to `kind: ClusterProviderConfig`.
# Add `spec.credentials.secretRef.namespace` if it isn't already set.
# Delete metadata.uid and metadata.resourceVersion.

# Install the new provider release (its CRDs replace the MVP ones)
# … your usual Crossplane package install …

# Apply the renamed PC
kubectl apply -f pc-backup.yaml
```

Existing `Project` and `SshKey` MRs in the cluster keep their `spec.providerConfigRef.name` and resolve against the renamed `ClusterProviderConfig` via the runtime's dual-reference fallback — no per-MR edit needed.

For team-isolated credentials, create a namespaced `ProviderConfig` per team instead:

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: team-a
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-token
      key: token
```

The Secret must exist in `team-a`. MRs in `team-a` whose `spec.providerConfigRef.name` is `default` resolve to this PC; MRs in other namespaces continue to resolve to the cluster-scoped `ClusterProviderConfig`.

### 2. Re-apply `ContainerRegistry` and `S3Bucket` manifests

The MVP shape (`preset_id`, `configuration{configurator_id, …}`) is rejected by the new CRD validation. Rewrite each manifest in one of the two new shapes:

**Variant A — by raw `resources` (controller picks an upstream configurator)**:

```yaml
apiVersion: containerregistry.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: my-registry
  namespace: team-a
spec:
  providerConfigRef:
    name: default
  forProvider:
    name: my-registry
    resources:
      location: ru-1
      diskGB: 5
```

The controller picks a configurator deterministically from `(location, diskGB)` and records `status.atProvider.lockedConfiguratorID`.

**Variant B — by `presetName` (named tariff)**:

```yaml
spec:
  forProvider:
    presetName: start-ru-1
```

The controller resolves `start-ru-1` against the live Timeweb catalog and records `status.atProvider.lockedPresetID`.

If the slug doesn't exist, the MR shows `Synced=False, reason=PresetNotFound` with a message listing currently-valid slugs.

The same XOR shape applies to `S3Bucket`:

```yaml
spec:
  forProvider:
    resources:
      location: ru-1
      diskGB: 30
      storageClass: hot
```

### 3. Switching sizing variants after create

Once an MR is `Ready=True`, the chosen sizing variant (`presetName` or `resources`) is locked. Operations:

- **Within-variant change** — patch `presetName` to a different slug, or patch `resources.diskGB` to a new value. Controller PATCHes upstream.
- **Cross-variant change** — replacing `resources` with `presetName` (or vice versa) is rejected: `Synced=False, reason=SizingSwitchRequiresRecreate`. The upstream resource is unchanged. To switch variants, `kubectl delete` and reapply with the new shape.

## Verifying the install

```bash
# 1. The new PC kinds are registered:
kubectl api-resources | grep timeweb.crossplane.io
# Expected: providerconfigs (Namespaced), clusterproviderconfigs (Cluster),
#           providerconfigusages, clusterproviderconfigusages.

# 2. The MVP ContainerRegistryPreset kind is gone:
kubectl api-resources | grep containerregistrypresets
# Expected: no output.

# 3. The refactored MR CRDs accept the new shape:
kubectl explain containerregistry.spec.forProvider --recursive | grep -E 'presetName|resources'
# Expected: both fields visible, with XOR validation.
```

## Discovering preset slugs

There is no `kubectl get presets` — catalog data is not exposed as a Kubernetes object. Sources for valid `presetName` values:

1. **Timeweb dashboard** — the canonical source of named tariffs (Start, Standard, Pro, …).
2. **Error message on PresetNotFound** — if you type a slug the controller can't resolve, the resulting condition message lists up to 20 currently-valid slugs visible to your `ProviderConfig`.
3. **`docs/presets.md`** in the provider release notes — documents the slug rule (`<short>-<location>`) so you can hand-construct a slug from the dashboard's "Tariff" + "Location" columns.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `Synced=False, reason=PresetNotFound` on a fresh MR | Typed slug doesn't exist (typo, or the tariff was renamed upstream). | Read the condition message — it lists valid slugs. Re-apply. |
| `Synced=False, reason=NoConfiguratorAvailable` | `resources` fields outside any configurator's bounds (e.g. `diskGB: 9999`). | Read the message — it names the bound that was exceeded. Adjust and re-apply. |
| `Synced=False, reason=SizingSwitchRequiresRecreate` | Operator changed which side of the XOR is set on a locked MR. | Delete the MR and reapply with the new sizing variant. |
| `Synced=False, reason=CatalogUnauthorized` | The PC's token doesn't have permission on a catalog endpoint. | Issue a token with the right scopes; update the Secret. |
| MR is `Ready=Unknown` for minutes after apply | First reconcile is fetching the catalog (cold cache miss + slow upstream). | Wait one TTL window (default 5 min). |

## What's coming next

The next feature (`KubernetesCluster` + `KubernetesNodeGroup`) reuses everything described above: the same `presetName XOR resources` shape per MR, the same dual-scope `ProviderConfig` pair, the same internal resolver. The K8s feature adds new dimension registrations (master/worker presets, k8s versions, network drivers, availability zones) but no new resolver primitives.
