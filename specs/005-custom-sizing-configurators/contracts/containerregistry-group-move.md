# Contract — ContainerRegistry group move

**Hard move** of two kinds from `containerregistry.m.timeweb.crossplane.io` → `kubernetes.m.timeweb.crossplane.io` (old group removed). Mirrors the Timeweb dashboard (registries are the "Реестры контейнеров" tab inside the Kubernetes section).

## Before → After

| | Before | After |
|---|---|---|
| `ContainerRegistry` | `containerregistry.m.timeweb.crossplane.io/v1alpha1` | `kubernetes.m.timeweb.crossplane.io/v1alpha1` |
| `ContainerRegistryRepository` | `containerregistry.m.timeweb.crossplane.io/v1alpha1` | `kubernetes.m.timeweb.crossplane.io/v1alpha1` |

## Invariants

- **No field, status, behavior, or resolver change** — only `apiVersion`/group. The `ContainerRegistryPreset` / `S3BucketPreset` resolver dimensions and `initialSizeGB` sizing are untouched.
- Types relocate into `apis/kubernetes/v1alpha1`; registered in that package's SchemeBuilder + `managed.go`. `apis/containerregistry/` deleted; its `AddToScheme` removed from `apis/apis.go`.
- CRD files renamed: `package/crds/kubernetes.m.timeweb.crossplane.io_containerregistries.yaml` + `…_containerregistryrepositories.yaml`; old CRD files deleted.
- Controllers stay in `internal/controller/containerregistry/` repointed to the relocated GVKs; `cmd/provider/main.go` setup repointed.
- `package/crossplane.yaml`, README, docs, and the `05-containerregistry` e2e bundle updated to the new apiVersion.

## Breaking-change note

Operators MUST re-apply ContainerRegistry/Repository manifests under the new `apiVersion`. Acceptable: all kinds are `v1alpha1` (pre-`v1beta1`, freely revisable per Constitution §I) and there are no external consumers. No conversion webhook (no in-cluster instances to migrate in dev).

## Acceptance

- `kubectl get containerregistries.kubernetes.m.timeweb.crossplane.io` works; a manifest under the new group reconciles `[Ready=True, Synced=True]` identically to before; the old group's CRDs no longer exist.
